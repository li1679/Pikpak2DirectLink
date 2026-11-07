# Changelog

## Desktop v1.0.2 — 2026-11-07

- 添加账号先加密保存，不再等待 PikPak 登录验证；错误密码、风控或网络错误不再阻止账号入库。
- 新账号显示“已保存 · 待验证”；首次解析自动登录，也可手动验证；失败后保留账号并显示检查结果。
- 同名账号重新保存时保留使用量和创建时间；只有密码改变才清除原会话，数据库失败时整体回滚。
- 历史报错账号仍自动参与解析与并发轮转，无需手动重置或指定账号；尝试失败后继续换号，仍遵守账号本月流量上限。
- 网盘临时资源按首次创建记录最多保留 6 小时，任务完成、直链有效期和重启不再延长清理时间。
- 保留到期清理台账、失败重试和启动补清理，不扫描用户原有网盘文件；健康检查残留资源也纳入台账。
- 任务详情保留窗口同步为 6 小时，原数据目录与密钥不变。

## Desktop v1.0.1 — 2026-11-07

- 图标改为黑底白字的小写 `pikpak`，同步页面图标、Windows EXE 与安装包资源。
- 添加统一图标生成脚本，桌面构建时自动同步各尺寸资源。
- 功能、数据目录和密钥保持不变。

## Desktop v1.0.0 — 2026-11-07

- 新增 Windows x64 Tauri/WebView2 窗口与当前用户 NSIS 安装包。
- 自动管理 Go 后台生命周期；单实例，正常退出和父进程异常退出均回收后台。
- 桌面数据独立保存在本机用户目录；自动生成并通过 DPAPI 保护密钥。
- 新增原生数据/日志目录、恢复密钥导出和个人版数据库备份恢复菜单。
- 保留原命令行入口；不添加开机启动、自更新或旧数据导入。

## Personal fork — 当前版本

- 改为仅本机使用、免项目注册登录；保留 PikPak 账号登录与账号池。
- 删除管理员密码、OAuth、订阅和用户配额、CDK、在线更新及相关前后端和数据库结构。
- 仅使用全新个人版数据库；删除旧库迁移、JSON 导入和旧格式兼容读取。
- 保留解析、批次和多选、文件库、直链 / 本机代理、aria2、日志、设置、备份恢复。
- 该阶段的桌面化已在后续 Desktop v1.0.0 中完成。

以下为上游历史记录，不代表当前个人版功能或升级路径。

## v3.4.0 - 2026-11-05 - Configurable community OAuth login

- Add administrator-managed OAuth providers with custom names, endpoints, scopes, field mappings, ordering, registration controls, and encrypted client secrets.
- Show enabled, configured login methods dynamically in the user portal and record the method used by each session.
- Preserve existing LinuxDO users, configuration, and callback URLs while isolating identities by provider and stable subject ID.
- Bind callbacks to their browser, provider, configuration revision, and optional PKCE verifier; reject stale, replayed, or cross-provider callbacks.

## v3.3.1 - 2026-10-27 - Admin file library

- Add a persistent admin file library for magnet and PikPak share links, with file counts, folders, and context-menu move, rename, retry, and delete operations.
- Generate revocable public file links that resolve on demand and redirect to a fresh direct or proxy download address; support HEAD, Range through redirects, and optional JSON address responses.
- Encrypt resource inputs, extraction codes, selectors, link tokens, and failure details; preserve links across restarts and key rotation.
- Reuse concurrent resolutions, refresh expired addresses, and reject changed or ambiguous source files.
- Build with Go 1.26.10 to include standard-library security fixes required by the release vulnerability scan.

## v3.1.0 - 2026-07-10 - Secure Durable Storage

### Breaking upgrade requirements

- `DATA_ENCRYPTION_KEY` is now mandatory. It must be the canonical standard-Base64 encoding of exactly 32 random bytes; startup fails when it is missing, malformed, or cannot decrypt the existing database.
- Before the first upgrade from v3.0.x, stop accepting work and let every old in-memory queued/running job finish. Those jobs were never stored on disk and cannot be migrated.
- Public deployments must terminate TLS at a trusted reverse proxy and set `PUBLIC_BASE_URL` to the external `https://` URL.
- Keep the encryption key outside the database backup directory and back it up separately. Losing it makes encrypted credentials, sessions, and job details unrecoverable.

### SQLite storage and encryption

- SQLite is now the only active server persistence source for administrators, users, sessions, PikPak accounts, CDKs, settings, jobs, cleanup state, migrations, and backup status.
- PikPak passwords and sessions, application secrets, and complete job payloads use AES-256-GCM field encryption with purpose/record-bound authenticated data.
- Admin and user cookies are represented in SQLite only by SHA-256 token digests; raw session tokens are not persisted.
- Key rotation is supported with comma-separated `DATA_ENCRYPTION_PREVIOUS_KEYS`: configure the new current key together with every old key still needed to read retained records or backups.
- SQLite disk connections now enforce WAL mode, foreign keys, a busy timeout, full synchronous durability, and immediate write transactions.

### Migration and durable jobs

- The first v3.1.0 start snapshots the legacy database, `auth.json`, account JSON, and session files into a checksummed migration backup before importing them into encrypted SQLite.
- The plaintext migration backup remains pending until an administrator verifies the new database and explicitly confirms deletion in Settings.
- Parent and child resolve jobs persist across process restarts. Nonterminal jobs are never auto-resumed; startup records them as `failed/service_restart`.
- Complete job details remain available for 3 hours, then sensitive payloads are scrubbed while audit metadata remains for up to 30 days.
- The user portal remembers the current job ID in `sessionStorage` and shows explicit restart-interrupted and expired-detail states without exposing backend errors.

### Verified backups and recovery

- A verified SQLite snapshot runs every 24 hours by default, with seven successful backups retained. Each snapshot is integrity-checked, hashed, synced, and atomically published.
- Administrators can inspect backup status, trigger a snapshot, and confirm deletion of the legacy migration backup from Settings.
- Added offline recovery commands:
  - `storage restore-db --backup <file> --yes`
  - `storage restore-migration --backup <dir> --yes`
- Restore commands require the service to be stopped and save a safety copy of current data before replacement.
- Restoring either snapshot is point-in-time recovery: all users, quota usage, tasks, account state, and configuration changes made after that snapshot are lost.

## 2026-06-17 - Multi-Link Parallel Resolution

**Resolve many links in one submission**
- The resolve box now accepts multiple links separated by newlines; each line is detected independently (magnet **or** PikPak share link) and resolved on its own
- Each link becomes its own job enqueued in the global resolve queue, so the lines fan out in parallel under the admin's concurrency limit (`maxResolveConcurrency`)
- A multi-link submission creates a parent batch job that is not itself queued; it merges its children's results once they finish. Single-link submissions keep the existing flow (multi-file links still pause for a manual file selection)
- Each link's files auto-resolve to download links (no per-file selection); a share with multiple roots restores them all
- Partial failures are tolerated: only successful links are returned, and the UI shows **解析成功 x/x 条**
- Results render as a folder tree where **each link is a sibling top-level folder**, on both the admin page and the CDK user portal
- Proxy links for batch results point at the parent job and carry the resolving account so they keep working across accounts and after a child job is evicted
- Available on both the admin resolve page and the CDK user portal

## 2026-06-17 - Monthly Traffic Limits (accounts) + Traffic-based CDKs

**Per-account monthly downstream traffic budget**
- Each PikPak account now has a monthly downstream-traffic budget (default 700G; 1G = 1024³ bytes)
- On a successful direct-link return (admin and CDK users alike), the resource size is counted against the resolving account's budget
- An account that reaches its budget is marked **到达限行流量** and is excluded from new resolves; the counter (and the state) auto-reset at the start of each calendar month — no scheduler needed
- Admins can set the budget when adding an account, and change it later per account on the account page (`PATCH /api/accounts/{id}`)
- Available-account count excludes capped accounts

**CDK metering switched from count to traffic**
- CDKs now carry a downstream-traffic allowance (GB) instead of a parse count; traffic is charged on successful resolve (a small overage is possible if parallel jobs race, then the CDK is blocked)
- Admin CDK create/edit and the user portal now show traffic (GB) instead of counts
- **One-time DB migration**: existing CDKs are converted at `1 credit = 2G`. The `cdks` table is rebuilt automatically on first start after upgrade. **Back up `data/pikpak.db` before upgrading** if you want a rollback point.

## 2026-06-17 - CDK User Error Disclosure Fix

**Stop leaking PikPak account info to CDK users**
- A failed parse previously surfaced the raw "all PikPak accounts failed: `<email>`: ..." error (and the "starting with `<email>`" progress message) straight to CDK end users, exposing platform account usernames
- `toUserJobView` now collapses any failure into a single generic message and reconstructs the progress message from the job's status/stage only — the raw `Error`/`Message` are never forwarded to CDK users
- Admin views are unchanged and still show full error detail for debugging

## 2026-06-16 - Parallel Resolution

**Admin-Controllable Parallel Resolution**
- Link resolution can now run multiple jobs in parallel instead of strictly serial
- New Settings panel control for the parallel concurrency (1 = serial, up to 32)
- The value is persisted in the database and restored on restart; `RESOLVE_CONCURRENCY` only seeds the initial default
- New admin endpoints: `GET /api/settings` and `PUT /api/settings`
- In parallel mode, resolution round-robins across available PikPak accounts (via a rotating cursor) instead of always starting on the first account; failed accounts are tried last
- Per-job timeout switches automatically with the mode: `QUEUE_TIMEOUT` (45s) while serial, `PARALLEL_QUEUE_TIMEOUT` (2m) while parallel, so parallel jobs that share PikPak's attention get a larger budget
- Per-job data isolation (results, CDK refunds, temp folders) is preserved under concurrency

## 2026-06-13 - Security & UX Improvements

### 🔒 Security Enhancements

**Optional Access Password Protection**
- Added `ACCESS_PASSWORD` environment variable for optional authentication
- When set, UI and API endpoints require session-based login
- Cookie-based HttpOnly sessions with 30-day expiration
- New endpoints: `POST /api/auth/login` and `POST /api/auth/logout`
- `/api/config` reports `auth_required` and `authenticated` status
- Proxy links remain accessible without login (for external downloaders)
- Startup warning logged when `ACCESS_PASSWORD` is not set
- Removed confusing unused `POST /api/login` alias

**Per-Job Proxy Tokens**
- Each proxy link now includes a unique random token (`?token=...`)
- Proxy handler validates token before serving files
- Closes the open-proxy vulnerability while keeping downloads accessible

**Memory Leak Prevention**
- Job store now bounded to 200 entries (configurable)
- Oldest jobs automatically evicted when limit reached
- Prevents unbounded memory growth from never-cleaned job history

### 🎨 Frontend Improvements

**Dark Mode Support**
- Full dark theme with optimized color palette
- Theme toggle button in sidebar (auto/light/dark)
- Persisted to localStorage
- Honors `prefers-color-scheme` in auto mode
- All components adapted with CSS variables

**Authentication UI**
- Login overlay shown when authentication is required
- Logout button in sidebar when auth is enabled
- Session state tracked and displayed
- Auth errors handled gracefully

**Link Detection**
- Real-time link type detection as user types
- Badge shows "磁力链接 / PikPak 分享链接 / 无法识别"
- Visual feedback (green for valid, red for invalid)
- Improves input validation UX

**Toast Notifications**
- Non-intrusive toast messages for actions
- Success/error/info variants with color coding
- Auto-dismiss after 3 seconds
- Replaces modal alerts for copy feedback

**Delete Confirmation**
- Account deletion now requires confirmation dialog
- Prevents accidental data loss
- Shows account username in confirmation prompt

**Layout Polish**
- Metric bar hidden on Logs page (more console space)
- Theme toggle and logout controls in sidebar
- Improved spacing and visual hierarchy
- Better mobile responsiveness

### 📝 Documentation

**README Updates**
- Documented new `ACCESS_PASSWORD` configuration
- Added security recommendations section
- Updated usage instructions with new features
- Systemd service example includes ACCESS_PASSWORD
- Explained proxy token protection

**.env.example Updates**
- Added `ACCESS_PASSWORD` with explanatory comment
- Clarified proxy link behavior with tokens

### 🔧 Technical Changes

**Backend (Go)**
- `internal/app/auth.go`: New auth session store with token generation
- `internal/app/config.go`: Added `AccessPassword` field and `AuthEnabled()` method
- `internal/app/server.go`: Auth middleware, login/logout handlers, token validation
- `internal/app/jobs.go`: Bounded job store with capacity management, proxy tokens
- `cmd/server/main.go`: Security warning when auth is disabled

**Frontend (JavaScript)**
- Theme management with localStorage persistence
- Auth state tracking and overlay management
- Real-time link detection with regex validation
- Toast notification system
- Delete confirmation dialogs
- Enhanced error handling

**Frontend (HTML/CSS)**
- Auth overlay modal structure
- Theme toggle and logout buttons
- Link type indicator badge
- Toast container and animations
- CSS variables for theme support
- Dark mode color palette

### ✅ Testing

- All Go tests pass
- Build successful on Windows/Go 1.26.2
- No breaking changes to existing deployments
- Backward compatible (auth is opt-in)
