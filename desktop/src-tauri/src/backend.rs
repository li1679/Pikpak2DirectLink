use crate::native::{hidden_command, ProcessJob};
use serde::Deserialize;
use std::{
    fs,
    io::{BufRead, BufReader, Write},
    path::{Path, PathBuf},
    process::{Child, ChildStdin, Stdio},
    sync::mpsc,
    thread,
    time::{Duration, Instant},
};

#[derive(Deserialize)]
struct Startup {
    event: String,
    url: Option<String>,
    error: Option<String>,
}
pub struct Backend {
    pub child: Child,
    input: Option<ChildStdin>,
    _job: ProcessJob,
}
impl Backend {
    pub fn start(exe: &Path, root: &Path) -> Result<(Self, tauri::Url), String> {
        fs::create_dir_all(root.join("logs")).map_err(|e| e.to_string())?;
        let log = fs::OpenOptions::new()
            .create(true)
            .append(true)
            .open(root.join("logs/native-service.log"))
            .map_err(|e| e.to_string())?;
        let mut child = hidden_command(exe)
            .args(["--data-dir"])
            .arg(root)
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::from(log))
            .spawn()
            .map_err(|e| format!("无法启动后台服务：{e}"))?;
        let job = match ProcessJob::attach(&child) {
            Ok(job) => job,
            Err(e) => {
                let _ = child.kill();
                let _ = child.wait();
                return Err(format!("无法管理后台进程：{e}"));
            }
        };
        let input = child.stdin.take();
        let stdout = child.stdout.take().ok_or("服务输出管道不可用")?;
        let (tx, rx) = mpsc::channel();
        thread::spawn(move || {
            let mut line = String::new();
            let result = BufReader::new(stdout).read_line(&mut line).map(|_| line);
            let _ = tx.send(result);
        });
        let mut backend = Self {
            child,
            input,
            _job: job,
        };
        let result = (|| {
            let line = rx
                .recv_timeout(Duration::from_secs(60))
                .map_err(|_| "服务启动超时；请查看数据目录中的 logs".to_string())?
                .map_err(|e| e.to_string())?;
            parse_ready(&line)
        })();
        match result {
            Ok(url) => Ok((backend, url)),
            Err(e) => {
                backend.stop();
                Err(e)
            }
        }
    }
    pub fn stop(&mut self) {
        if let Some(mut input) = self.input.take() {
            let _ = input.write_all(b"shutdown\n");
        }
        let deadline = Instant::now() + Duration::from_secs(15);
        loop {
            match self.child.try_wait() {
                Ok(Some(_)) => return,
                Err(_) => break,
                Ok(None) => {
                    if Instant::now() >= deadline {
                        break;
                    }
                    thread::sleep(Duration::from_millis(50))
                }
            }
        }
        let _ = self.child.kill();
        let _ = self.child.wait();
    }
}
impl Drop for Backend {
    fn drop(&mut self) {
        self.stop()
    }
}

fn parse_ready(line: &str) -> Result<tauri::Url, String> {
    let ready: Startup = serde_json::from_str(line)
        .map_err(|_| "服务没有返回有效启动状态；请查看 logs".to_string())?;
    if ready.event != "ready" {
        return Err(ready.error.unwrap_or("后台服务启动失败".into()));
    }
    let url: tauri::Url = ready
        .url
        .ok_or("服务缺少本机地址")?
        .parse()
        .map_err(|_| "无效的本机服务地址")?;
    if url.scheme() != "http"
        || url.host_str() != Some("127.0.0.1")
        || url.port() != Some(51873)
        || !url.username().is_empty()
        || url.password().is_some()
        || url.path() != "/"
        || url.query().is_some()
        || url.fragment().is_some()
    {
        return Err("后台服务返回了非预期本机地址".into());
    }
    Ok(url)
}
pub fn sidecar_path() -> Result<PathBuf, String> {
    let exe = std::env::current_exe().map_err(|e| e.to_string())?;
    let dir = exe.parent().ok_or("无法定位程序目录")?;
    for name in [
        "pikpak-service.exe",
        "pikpak-service-x86_64-pc-windows-msvc.exe",
    ] {
        let path = dir.join(name);
        if path.is_file() {
            return Ok(path);
        }
    }
    Err("缺少 pikpak-service.exe；请重新安装，便携版须保留完整文件夹".into())
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn validates_local_handshake() {
        assert!(parse_ready(r#"{"event":"ready","url":"http://127.0.0.1:51873"}"#).is_ok());
        for url in [
            "http://evil.example:51873",
            "https://127.0.0.1:51873",
            "http://127.0.0.1:1",
            "http://user@127.0.0.1:51873",
            "http://127.0.0.1:51873/path",
        ] {
            assert!(parse_ready(&format!(r#"{{"event":"ready","url":"{url}"}}"#)).is_err())
        }
        assert!(parse_ready(r#"{"event":"error","error":"occupied"}"#)
            .unwrap_err()
            .contains("occupied"));
    }
}
