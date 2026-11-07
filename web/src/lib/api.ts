// Typed HTTP client for the local PikPak tool.
import type {
  AccountSummary,
  ApiError,
  BackupRun,
  ConfigResponse,
  CreateJobRequest,
  CreateLibraryEntryRequest,
  Job,
  LibraryDownloadLink,
  LibraryEntry,
  LogEntry,
  OperationResult,
  SelectItemsRequest,
  SettingsResponse,
  StorageStatusResponse,
  UpdateSettingsRequest,
} from './types'

function makeError(message: string, status?: number): ApiError {
  const err = new Error(message) as ApiError
  err.status = status
  return err
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  let res: Response
  let text: string
  try {
    res = await fetch(path, {
      method,
      headers: body !== undefined ? { 'Content-Type': 'application/json' } : undefined,
      body: body !== undefined ? JSON.stringify(body) : undefined,
      signal: method === 'GET' ? AbortSignal.timeout(15000) : undefined,
    })
    text = await res.text()
  } catch (e) {
    if (e instanceof Error && e.name === 'TimeoutError') throw makeError('请求超时，请重试')
    throw makeError(e instanceof Error ? e.message : '网络请求失败')
  }
  if (res.status === 204) return undefined as T

  let payload: unknown = null
  if (text) {
    try { payload = JSON.parse(text) }
    catch { throw makeError(`服务器返回了无效响应 (${res.status})`, res.status) }
  }
  if (!res.ok) {
    const error = payload && typeof payload === 'object' && 'error' in payload ? payload.error : null
    throw makeError(typeof error === 'string' ? error : `请求失败 (${res.status})`, res.status)
  }
  if (payload === null) throw makeError('服务器返回了空响应', res.status)
  return payload as T
}

export const api = {
  config: () => request<ConfigResponse>('GET', '/api/config'),

  settings: {
    get: () => request<SettingsResponse>('GET', '/api/settings'),
    update: (body: UpdateSettingsRequest) => request<SettingsResponse>('PUT', '/api/settings', body),
    storage: {
      get: () => request<StorageStatusResponse>('GET', '/api/settings/storage'),
      backup: () => request<{ backup: BackupRun }>('POST', '/api/settings/storage/backups'),
    },
  },

  logs: {
    list: (after = 0) => request<{ logs: LogEntry[] }>('GET', `/api/logs?after=${after}`),
    clear: () => request<OperationResult>('DELETE', '/api/logs'),
  },

  accounts: {
    list: () => request<{ accounts: AccountSummary[] }>('GET', '/api/accounts'),
    add: (body: { username: string; password: string; traffic_limit_gb: number }) =>
      request<AccountSummary>('POST', '/api/accounts', body),
    update: (id: string, traffic_limit_gb: number) =>
      request<OperationResult>('PATCH', `/api/accounts/${id}`, { traffic_limit_gb }),
    remove: (id: string) => request<OperationResult>('DELETE', `/api/accounts/${id}`),
    reset: (id: string) => request<OperationResult>('POST', `/api/accounts/${id}/reset`),
    refreshLogin: (id: string) => request<AccountSummary>('POST', `/api/accounts/${id}/refresh-login`),
    deleteParseError: (id: string, index: number) =>
      request<OperationResult>('DELETE', `/api/accounts/${id}/parse-errors/${index}`),
  },

  jobs: {
    create: (body: CreateJobRequest) => request<Job>('POST', '/api/jobs', body),
    get: (id: string) => request<Job>('GET', `/api/jobs/${id}`),
    select: (id: string, body: SelectItemsRequest) => request<Job>('POST', `/api/jobs/${id}/select`, body),
  },

  library: {
    list: () => request<{ entries: LibraryEntry[] }>('GET', '/api/library'),
    create: (body: CreateLibraryEntryRequest) => request<LibraryEntry>('POST', '/api/library', body),
    update: (id: string, body: { name?: string; parent_id?: string }) =>
      request<LibraryEntry>('PATCH', `/api/library/${encodeURIComponent(id)}`, body),
    remove: (id: string) => request<void>('DELETE', `/api/library/${encodeURIComponent(id)}`),
    retry: (id: string) => request<LibraryEntry>('POST', `/api/library/${encodeURIComponent(id)}/retry`),
    link: (id: string, fileID: string, mode: 'direct' | 'proxy', reset = false) =>
      request<LibraryDownloadLink>('POST', `/api/library/${encodeURIComponent(id)}/files/${encodeURIComponent(fileID)}/link`, { mode, reset }),
    revokeLink: (id: string, fileID: string) =>
      request<void>('DELETE', `/api/library/${encodeURIComponent(id)}/files/${encodeURIComponent(fileID)}/link`),
  },
}

export { type ApiError }
