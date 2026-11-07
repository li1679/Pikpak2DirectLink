# PikPak Personal Windows 桌面版

## 打开应用

推荐运行 `../dist/PikPak Personal_1.0.2_x64-setup.exe` 安装，然后从开始菜单打开 **PikPak Personal**。也可双击 `../dist/PikPak-Personal/PikPak Personal.exe` 免安装运行，必须保留同目录的 `pikpak-service.exe`。

面板沿用原有五个页面。PikPak 账号先加密保存，不先验证密码；首次解析时自动登录，也可点击“重新登录”。验证失败账号仍保留，并自动参与后续解析和轮转，无需手动选账号或重置；账号月流量上限仍生效。同名账号重新保存可更新密码。项目本身不需要注册、登录或密码，无需手填环境变量或生成加密密钥。

重复打开会唤起原窗口。**关闭窗口即退出应用**，当前解析和本机代理下载也会停止；不会缩到托盘，也没有开机自启或在线更新。默认监听 `127.0.0.1:51873`，同端口运行命令行服务时会明确报错，不会连接到不明后台。

程序创建或转存到网盘的临时资源，按首次记录创建时间保留最多 **6 小时**，到期由应用自动删除，不随刷新直链或重启延长。后台每分钟检查，删除失败会重试，关闭期间过期的重新打开后补删；只处理程序自己的台账，不动网盘原有文件。解析失败或文件列表检查后的资源可能提前清理。删除需要应用运行且账号可访问，并非 PikPak 云端定时任务。

## 数据和密钥

默认目录：`%LOCALAPPDATA%\com.pikpak.personal.desktop\`。以“文件 → 打开数据目录”显示的路径为准。

- `personal.db`：加密字段存储的个人数据库。
- `data-key.dpapi`：由 Windows 当前账户保护的随机加密密钥。
- `backups/`：个人库快照。
- `logs/`：后台服务与启动错误日志。
- `webview/`：独立浏览器配置和 aria2 等浏览器侧设置。
- `restore-safety/`：执行恢复前保留的安全副本。

桌面版不自动读取当前目录的 `data/`，不导入旧项目数据库、JSON 或旧文件会话。原命令行版的环境变量不会覆盖桌面存储和启动账号。

卸载不自动删除个人数据。切勿随意删除或替换 `data-key.dpapi`；数据库存在但密钥缺失或损坏时，应用会停止启动，不生成新密钥覆盖。

## 备份与恢复

1. 在“设置 → 数据库备份”创建快照，或保留自动备份。
2. 在“文件 → 导出恢复密钥”中选择一个**新文件**保存恢复密钥，并单独保管。
3. 同账户恢复时使用“文件 → 恢复数据库备份”，选择本个人版的 `.db` 快照，确认后程序停止后台、校验密钥并恢复，随后退出。
4. 重新打开应用检查数据。恢复会丢失快照之后的变更；恢复前副本在 `restore-safety/` 中。

DPAPI 文件不能作为跨电脑通用密钥。换机或 Windows 账户变更时，需要单独导出的 Base64 恢复密钥。高级维护可在**应用已关闭且目标目录没有本机密钥文件**时，用安装目录中的服务程序显式导入密钥，再恢复快照：

```powershell
# 在完整免安装程序目录执行；安装版请使用其 pikpak-service.exe 的完整路径。
$data = Join-Path $env:LOCALAPPDATA 'com.pikpak.personal.desktop'
.\pikpak-service.exe --data-dir $data --import-key 'D:\安全位置\PikPak-Personal-recovery-key.txt'
.\pikpak-service.exe --data-dir $data --restore-db 'D:\备份\你的个人版快照.db'
```

如果目标目录已经有个人数据库，导入前会校验该密钥能否打开它。已有 `data-key.dpapi` 不会被覆盖；损坏文件需先安全另存，再人工处理。没有恢复密钥的旧数据库不会被自动重置或导入。

## 开发构建

环境：Windows x64、Go 1.26.10+、Node.js 20+、Rust stable MSVC、Visual Studio C++ Build Tools 和 WebView2。运行时不需要这些编译工具，只需要 WebView2。

在项目根目录：

```powershell
Push-Location web
npm.cmd ci
Pop-Location
Push-Location desktop
npm.cmd ci
.\build.ps1
Pop-Location
```

Go 不在 PATH 时：

```powershell
.\desktop\build.ps1 -GoExecutable 'C:\你的Go目录\bin\go.exe'
```

仅构建免安装文件夹：`./desktop/build.ps1 -NoBundle`。安装包由 Tauri 调用 NSIS 生成；第一次构建会下载构建依赖和 WebView2 引导程序。Cargo/npm 的锁文件应提交到版本库。

图标为黑底白字的小写 `pikpak`。`generate-icon.ps1` 统一生成 PNG、轮廓 SVG 和 Windows ICO；`build.ps1` 会自动调用它。

输出：

- `dist/PikPak Personal_1.0.2_x64-setup.exe`
- `dist/PikPak-Personal/PikPak Personal.exe`
- `dist/PikPak-Personal/pikpak-service.exe`

原命令行入口 `cmd/server` 不变。桌面入口是 `cmd/desktop-service`，窗口代码在 `desktop/src-tauri/src`。没有给网页配置文件系统、Shell 或通用命令执行权限；原生菜单直接调用固定维护操作。

## 检查

```powershell
go test ./...
Push-Location desktop\src-tauri
cargo test
cargo fmt --check
Pop-Location
```

桌面启动支持 `--data-dir <绝对路径>`，用于隔离测试目录；正常用户无需传参。WebView2 远程调试只在测试启动环境中临时配置，应用源码没有开启生产调试端口。

桌面化阶段（v1.0.0）已在 Windows 上完成实际 EXE 的五页导航、设置保存、备份、重启持久化、单实例、正常退出、强制结束父进程、原生密钥导出、原生数据库恢复，以及临时目录中的安装与卸载检查。

## 当前限制

- 安装包未进行代码签名，未增加自动更新。
- 原网页依赖先前记录的审计告警尚未在本次桌面化中升级处理。
- 安装器附带 WebView2 引导程序而不是完整离线运行时；缺少运行时时可能需要联网安装。
