// Type definitions mirroring the Go JSON contract in internal/app.
// Field names match the server's JSON tags exactly.

export type ConfigResponse = {
  configured: boolean
  account_count: number
  failed_account_count: number
  available_account_count: number
  root_folder: string
}

export type SettingsResponse = {
  concurrency: number
  max_concurrency: number
  parallel: boolean
  running: number
  waiting: number
  serial_timeout_seconds: number
  parallel_timeout_seconds: number
  task_timeout_seconds: number
  min_task_timeout_seconds: number
  max_task_timeout_seconds: number
}

export type BackupRunStatus = 'running' | 'success' | 'failed'

export type BackupRun = {
  id: string
  kind: string
  status: BackupRunStatus
  path: string
  size_bytes: number
  sha256: string
  error: string
  started_at: string
  completed_at: string | null
}

export type StorageStatusResponse = {
  backup_dir: string
  backup_interval_seconds: number
  backup_retention: number
  running: boolean
  last_run?: BackupRun | null
  last_success?: BackupRun | null
  next_run_at: string
}

export type UpdateSettingsRequest = {
  concurrency?: number
  task_timeout_seconds?: number
  task_timeout_minutes?: number
}

export type AccountStatus = 'available' | 'failed'

export type ParseError = {
  time: string
  job_id?: string
  message: string
}

export type AccountSummary = {
  id: string
  username: string
  status: AccountStatus
  ready: boolean
  logged_in: boolean
  persisted: boolean
  premium: boolean
  premium_type: string
  premium_until: string
  premium_error: string
  premium_checked_at: string
  traffic_limit: number
  traffic_used: number
  traffic_limited: boolean
  last_error: string
  last_failed_at: string
  credential_checked_at: string
  credential_next_check_at: string
  credential_check_error: string
  parse_error_count: number
  parse_errors?: ParseError[]
  created_at: string
  updated_at: string
}

export type DownloadItem = {
  id: string
  name: string
  path: string
  kind: string
  mime_type: string
  size: string
}

export type LibraryFile = {
  id: string
  name: string
  path: string
  size: string
  mime_type?: string
  public_url?: string
  mode?: 'direct' | 'proxy'
}

export type LibraryEntry = {
  id: string
  parent_id: string
  kind: 'folder' | 'resource'
  name: string
  resource_kind: 'magnet' | 'share' | ''
  status: 'pending' | 'ready' | 'failed'
  file_count: number
  error?: string
  files: LibraryFile[]
  created_at: string
  updated_at: string
}

export type CreateLibraryEntryRequest = {
  kind: 'folder' | 'resource'
  name?: string
  parent_id: string
  input?: string
  pass_code?: string
}

export type LibraryDownloadLink = {
  url: string
  mode: 'direct' | 'proxy'
}

export type JobResult = {
  file: DownloadItem
  url: string
  direct_url: string
  proxy_url: string
  proxy_token: string
  expires_at: string
}

export type AccountAttempt = {
  account_id: string
  username: string
  status: string
  error: string
}

export type ShareState = {
  share_id: string
  tail_id: string
  pass_code_token: string
  selected_id: string
  selected_ids: string[]
}

export type BatchSummary = {
  total: number
  succeeded: number
  failed: number
  failures: { label: string; error: string }[]
}

export type JobKind = 'magnet' | 'share' | 'batch'
export type JobMode = 'direct' | 'proxy'
export type JobStatus = 'queued' | 'running' | 'selection_required' | 'completed' | 'failed'
export type JobStage = 'transfer' | 'source_selection' | 'result_selection' | 'complete' | 'failed'

export type Job = {
  id: string
  kind: JobKind
  mode: JobMode
  input: string
  pass_code: string
  status: JobStatus
  stage: JobStage
  message: string
  error: string
  account_id: string
  share: ShareState
  items: DownloadItem[]
  account_attempts: AccountAttempt[]
  result: JobResult
  results: JobResult[]
  warnings: string[]
  failure_code?: string
  details_available: boolean
  queue_ahead: number
  batch: BatchSummary
  created_at: string
  updated_at: string
}

export type CreateJobRequest = {
  input: string
  pass_code?: string
  mode?: JobMode
}

export type SelectItemsRequest = {
  item_ids?: string[]
  item_id?: string
}

export type LogLevel = 'info' | 'success' | 'warn' | 'error'

export type LogEntry = {
  id: number
  time: string
  level: LogLevel
  job_id: string
  message: string
  details: string[]
}

export type OperationResult = { status: string }

export type ApiError = Error & { status?: number }
