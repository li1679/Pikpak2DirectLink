package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"pikpak2directlink/internal/app"
	"pikpak2directlink/internal/desktop"
	"pikpak2directlink/internal/version"
)

type startupMessage struct {
	Event string `json:"event"`
	URL   string `json:"url,omitempty"`
	Error string `json:"error,omitempty"`
}

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		_ = json.NewEncoder(os.Stdout).Encode(startupMessage{Event: "error", Error: err.Error()})
		log.Printf("desktop service stopped: %v", err)
		os.Exit(1)
	}
}

func run(args []string, control io.Reader, output io.Writer) error {
	flags := flag.NewFlagSet("desktop-service", flag.ContinueOnError)
	root := flags.String("data-dir", "", "absolute desktop data directory")
	addr := flags.String("listen", "127.0.0.1:51873", "loopback listen address")
	export := flags.String("export-key", "", "export existing recovery key to a new file")
	importKey := flags.String("import-key", "", "explicitly import a recovery key while stopped")
	restore := flags.String("restore-db", "", "restore a current personal snapshot while stopped")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected service arguments")
	}
	paths, err := desktop.StoragePaths(*root)
	if err != nil {
		return err
	}
	actions := 0
	for _, value := range []string{*export, *importKey, *restore} {
		if value != "" {
			actions++
		}
	}
	if actions > 1 {
		return errors.New("choose one maintenance action")
	}
	if *export != "" {
		return desktop.ExportRecoveryKey(paths, *export)
	}
	if *importKey != "" {
		return desktop.ImportRecoveryKey(paths, *importKey)
	}
	if *restore != "" {
		key, err := desktop.LoadOrCreateKey(paths)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := app.ValidatePersonalDatabaseKey(ctx, *restore, key); err != nil {
			return err
		}
		result, err := app.RestoreDatabase(ctx, paths.Database, *restore)
		if err != nil {
			return err
		}
		return json.NewEncoder(output).Encode(map[string]string{"event": "restored", "safety_backup": result.SafetyBackupPath})
	}
	logfile, err := openLog(paths.Logs)
	if err != nil {
		return err
	}
	defer logfile.Close()
	logger := log.New(logfile, "", log.LstdFlags)
	key, err := desktop.LoadOrCreateKey(paths)
	if err != nil {
		return err
	}
	cfg := desktop.Config(paths, key, *addr)
	if err := cfg.ValidateLocal(); err != nil {
		return err
	}
	// Bind before opening the DB. Never reuse an unknown process on this port.
	listener, err := net.Listen("tcp4", cfg.Addr)
	if err != nil {
		return fmt.Errorf("本机端口 %s 已被占用或无法监听；请关闭另一个 PikPak 服务后重试：%w", cfg.Addr, err)
	}
	defer listener.Close()
	srv, err := app.NewServer(cfg)
	if err != nil {
		return fmt.Errorf("启动个人数据库失败：%w", err)
	}
	defer srv.Close()
	httpServer := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 1 << 20, ErrorLog: logger}
	serveErr := make(chan error, 1)
	go func() { serveErr <- httpServer.Serve(listener) }()
	// The pipe is owned by the native window. EOF also stops orphaned services.
	stopped := make(chan struct{})
	go func() {
		scanner := bufio.NewScanner(control)
		for scanner.Scan() {
			if scanner.Text() == "shutdown" {
				break
			}
		}
		close(stopped)
	}()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	logger.Printf("desktop service %s listening on %s", version.Version, listener.Addr())
	if err := json.NewEncoder(output).Encode(startupMessage{Event: "ready", URL: "http://" + listener.Addr().String()}); err != nil {
		_ = httpServer.Close()
		return err
	}
	var failure error
	select {
	case <-stopped:
	case <-signals:
	case <-srv.RestartRequested():
		failure = errors.New("存储发生错误，已安全停止；请重新打开应用并检查日志")
	case err := <-serveErr:
		if !errors.Is(err, http.ErrServerClosed) {
			failure = err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	shutdownErr := httpServer.Shutdown(ctx)
	cancel()
	if shutdownErr != nil {
		_ = httpServer.Close()
	}
	closeErr := srv.Close()
	logger.Print("desktop service stopped")
	return errors.Join(failure, shutdownErr, closeErr)
}

func openLog(dir string) (*os.File, error) {
	path := filepath.Join(dir, "desktop-service.log")
	if info, err := os.Stat(path); err == nil && info.Size() > 2<<20 {
		_ = os.Remove(path + ".1")
		if err := os.Rename(path, path+".1"); err != nil {
			return nil, err
		}
	}
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
}
