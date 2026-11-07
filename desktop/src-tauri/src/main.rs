#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

mod backend;
mod menus;
mod native;

use std::{
    path::PathBuf,
    sync::{
        atomic::{AtomicBool, Ordering},
        Mutex,
    },
    thread,
    time::Duration,
};
use tauri::{Manager, WebviewUrl, WebviewWindowBuilder, WindowEvent};
use tauri_plugin_dialog::{DialogExt, MessageDialogKind};

struct DesktopState {
    backend: Mutex<backend::Backend>,
    quitting: AtomicBool,
    maintenance: AtomicBool,
    root: PathBuf,
    service: PathBuf,
}

fn data_directory(app: &tauri::AppHandle) -> Result<PathBuf, String> {
    let mut args = std::env::args_os().skip(1);
    while let Some(arg) = args.next() {
        if arg == "--data-dir" {
            let path = PathBuf::from(args.next().ok_or("--data-dir 需要绝对路径")?);
            if !path.is_absolute() {
                return Err("--data-dir 必须是绝对路径".into());
            }
            return Ok(path);
        }
    }
    app.path().app_local_data_dir().map_err(|e| e.to_string())
}

fn shutdown(app: &tauri::AppHandle) {
    let state = app.state::<DesktopState>();
    if state.quitting.swap(true, Ordering::SeqCst) {
        return;
    }
    if let Some(window) = app.get_webview_window("main") {
        let _ = window.hide();
    }
    let handle = app.clone();
    thread::spawn(move || {
        if let Ok(mut backend) = handle.state::<DesktopState>().backend.lock() {
            backend.stop()
        }
        handle.exit(0);
    });
}

fn setup(app: &mut tauri::App) -> Result<(), Box<dyn std::error::Error>> {
    let root = data_directory(app.handle()).map_err(std::io::Error::other)?;
    let service = backend::sidecar_path().map_err(std::io::Error::other)?;
    let (backend, url) = backend::Backend::start(&service, &root).map_err(std::io::Error::other)?;
    app.manage(DesktopState {
        backend: Mutex::new(backend),
        quitting: AtomicBool::new(false),
        maintenance: AtomicBool::new(false),
        root: root.clone(),
        service,
    });
    menus::install(app.handle())?;
    let local_origin = url.origin();
    WebviewWindowBuilder::new(&mut *app, "main", WebviewUrl::External(url))
        .title("PikPak Personal")
        .inner_size(1180.0, 820.0)
        .min_inner_size(800.0, 600.0)
        .center()
        .data_directory(root.join("webview"))
        .on_navigation(move |url| {
            if url.origin() == local_origin {
                return true;
            }
            if matches!(url.scheme(), "http" | "https") {
                let _ = native::open_target(url.as_str());
            }
            false
        })
        .on_new_window(|url, _| {
            if matches!(url.scheme(), "http" | "https") {
                let _ = native::open_target(url.as_str());
            }
            tauri::webview::NewWindowResponse::Deny
        })
        .build()?;
    let monitor = app.handle().clone();
    thread::spawn(move || loop {
        thread::sleep(Duration::from_secs(1));
        let state = monitor.state::<DesktopState>();
        if state.quitting.load(Ordering::SeqCst) {
            break;
        }
        let exited = state
            .backend
            .lock()
            .map(|mut b| matches!(b.child.try_wait(), Ok(Some(_))))
            .unwrap_or(true);
        if exited {
            monitor
                .dialog()
                .message("后台服务已停止。请重新打开应用；可在数据目录的 logs 中查看原因。")
                .title("PikPak Personal")
                .kind(MessageDialogKind::Error)
                .blocking_show();
            shutdown(&monitor);
            break;
        }
    });
    Ok(())
}

fn main() {
    let app = tauri::Builder::default()
        .plugin(tauri_plugin_single_instance::init(|app, _, _| {
            if let Some(window) = app.get_webview_window("main") {
                let _ = window.unminimize();
                let _ = window.show();
                let _ = window.set_focus();
            }
        }))
        .plugin(tauri_plugin_dialog::init())
        .setup(setup)
        .on_window_event(|window, event| {
            if let WindowEvent::CloseRequested { api, .. } = event {
                api.prevent_close();
                shutdown(window.app_handle())
            }
        })
        .on_menu_event(|app, event| menus::handle(app, event.id().as_ref()))
        .build(tauri::generate_context!());
    match app {
        Ok(app) => app.run(|app, event| {
            if matches!(event, tauri::RunEvent::Exit) {
                if let Some(state) = app.try_state::<DesktopState>() {
                    state.quitting.store(true, Ordering::SeqCst);
                    if let Ok(mut backend) = state.backend.lock() {
                        backend.stop()
                    }
                }
            }
        }),
        Err(e) => native::show_error(&format!(
            "应用无法启动：{e}\n\n请检查本机端口是否被占用，以及数据目录的访问权限。"
        )),
    }
}
