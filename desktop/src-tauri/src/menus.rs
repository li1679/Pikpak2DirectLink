use crate::{native, shutdown, DesktopState};
use std::{sync::atomic::Ordering, thread};
use tauri::{
    menu::{MenuBuilder, MenuItemBuilder, SubmenuBuilder},
    Manager,
};
use tauri_plugin_dialog::{DialogExt, MessageDialogButtons, MessageDialogKind};

pub fn install(app: &tauri::AppHandle) -> tauri::Result<()> {
    let data = MenuItemBuilder::with_id("data", "打开数据目录").build(app)?;
    let logs = MenuItemBuilder::with_id("logs", "打开日志目录").build(app)?;
    let export = MenuItemBuilder::with_id("export", "导出恢复密钥…").build(app)?;
    let restore = MenuItemBuilder::with_id("restore", "恢复数据库备份…").build(app)?;
    let quit = MenuItemBuilder::with_id("quit", "退出")
        .accelerator("Alt+F4")
        .build(app)?;
    let file = SubmenuBuilder::new(app, "文件")
        .item(&data)
        .item(&logs)
        .separator()
        .item(&export)
        .item(&restore)
        .separator()
        .item(&quit)
        .build()?;
    let help = MenuItemBuilder::with_id("help", "使用与备份说明").build(app)?;
    let help_menu = SubmenuBuilder::new(app, "帮助").item(&help).build()?;
    app.set_menu(MenuBuilder::new(app).item(&file).item(&help_menu).build()?)?;
    Ok(())
}

pub fn handle(app: &tauri::AppHandle, id: &str) {
    let state = app.state::<DesktopState>();
    match id {
  "quit"=>shutdown(app),
  "data"=>{if let Err(e)=native::open_target(&state.root){native::show_error(&e)}},
  "logs"=>{if let Err(e)=native::open_target(state.root.join("logs")){native::show_error(&e)}},
  "help"=>app.dialog().message("关闭窗口会停止解析与本机代理下载。\n\n数据、账号和设置保存在本机数据目录，卸载不会自动删除。\n\n请在设置中创建数据库备份，并通过“文件 → 导出恢复密钥”单独保存密钥。DPAPI 密钥绑定当前 Windows 账户，换电脑时需要恢复密钥。\n\n通过“文件 → 恢复数据库备份”恢复当前个人版快照。应用没有在线更新或开机启动。").title("使用与备份").show(|_|{}),
  "export"|"restore"=>{
   if state.maintenance.swap(true,Ordering::SeqCst){return}
   let app=app.clone();let restore=id=="restore";
   thread::spawn(move||{
    if restore{restore_backup(&app)}else{export_key(&app)}
    app.state::<DesktopState>().maintenance.store(false,Ordering::SeqCst);
   });
  },_=>{}
 }
}

fn export_key(app: &tauri::AppHandle) {
    if !app.dialog().message("恢复密钥能够解密数据库中的账号和资源。请单独保管，不要放进数据库备份目录，也不要发送给他人。\n\n继续选择保存位置？").title("导出恢复密钥").kind(MessageDialogKind::Warning).buttons(MessageDialogButtons::OkCancel).blocking_show(){return}
    let Some(file) = app
        .dialog()
        .file()
        .set_title("保存恢复密钥（新文件）")
        .set_file_name("PikPak-Personal-recovery-key.txt")
        .blocking_save_file()
    else {
        return;
    };
    let path = match file.into_path() {
        Ok(path) => path,
        Err(e) => {
            native::show_error(&e.to_string());
            return;
        }
    };
    let state = app.state::<DesktopState>();
    match native::hidden_command(&state.service)
        .arg("--data-dir")
        .arg(&state.root)
        .arg("--export-key")
        .arg(path)
        .output()
    {
        Ok(output) if output.status.success() => {
            app.dialog()
                .message("恢复密钥已导出。请与数据库备份分开保管。")
                .title("导出完成")
                .blocking_show();
        }
        Ok(output) => native::show_error(&format!(
            "导出失败：{}",
            String::from_utf8_lossy(&output.stdout)
        )),
        Err(e) => native::show_error(&e.to_string()),
    }
}

fn restore_backup(app: &tauri::AppHandle) {
    let Some(file) = app
        .dialog()
        .file()
        .set_title("选择个人版 SQLite 备份")
        .add_filter("SQLite 备份", &["db"])
        .blocking_pick_file()
    else {
        return;
    };
    let path = match file.into_path() {
        Ok(path) => path,
        Err(e) => {
            native::show_error(&e.to_string());
            return;
        }
    };
    if !app.dialog().message("恢复会停止当前解析和代理下载，并用快照替换当前数据。程序会先保存当前数据的安全副本。\n\n仅支持本个人版且密钥匹配的备份。完成后应用退出，请重新打开。\n\n确定继续？").title("恢复数据库备份").kind(MessageDialogKind::Warning).buttons(MessageDialogButtons::OkCancel).blocking_show(){return}
    let state = app.state::<DesktopState>();
    if state.quitting.swap(true, Ordering::SeqCst) {
        return;
    }
    if let Some(window) = app.get_webview_window("main") {
        let _ = window.hide();
    }
    if let Ok(mut backend) = state.backend.lock() {
        backend.stop()
    }
    match native::hidden_command(&state.service)
        .arg("--data-dir")
        .arg(&state.root)
        .arg("--restore-db")
        .arg(path)
        .output()
    {
        Ok(output) if output.status.success() => {
            app.dialog()
                .message(
                    "备份已恢复。请重新打开应用。恢复前的数据保留在数据目录的 restore-safety 中。",
                )
                .title("恢复完成")
                .blocking_show();
        }
        Ok(output) => native::show_error(&format!(
            "恢复未完成，请重新打开应用并检查提示：{}",
            String::from_utf8_lossy(&output.stdout)
        )),
        Err(e) => native::show_error(&e.to_string()),
    }
    app.exit(0);
}
