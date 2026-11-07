// Shared resolve state machine: submit → poll GET → handle selection_required
// (pause and expose items) → select → resume → terminal.
import { ref, computed, onUnmounted, type Ref } from 'vue'
import type {
  BatchSummary,
  CreateJobRequest,
  DownloadItem,
  JobMode,
  JobResult,
  JobStatus,
  SelectItemsRequest,
} from '../lib/types'

interface JobLike {
  id: string
  status: JobStatus
  message: string
  error: string
  items: DownloadItem[]
  result: JobResult
  results: JobResult[]
  batch: BatchSummary
  queue_ahead: number
}

export interface JobAdapter<T extends JobLike> {
  create: (body: CreateJobRequest) => Promise<T>
  get: (id: string) => Promise<T>
  select: (id: string, body: SelectItemsRequest) => Promise<T>
}

const POLL_MS = 1200
const TERMINAL: JobStatus[] = ['completed', 'failed']

export function useJob<T extends JobLike>(adapter: JobAdapter<T>) {
  const job = ref<T | null>(null) as Ref<T | null>
  const submitting = ref(false)
  const error = ref('')
  const pollingError = ref(false)
  let timer: number | undefined
  let alive = true
  let generation = 0

  function clearTimer() {
    if (timer) {
      window.clearTimeout(timer)
      timer = undefined
    }
  }

  function schedulePoll(id: string) {
    clearTimer()
    timer = window.setTimeout(() => refreshJob(id), POLL_MS)
  }

  async function refreshJob(id: string, version = generation) {
    try {
      const j = await adapter.get(id)
      if (!alive || version !== generation) return
      job.value = j
      error.value = ''
      pollingError.value = false
      handle(j)
    } catch (e: any) {
      if (!alive || version !== generation) return
      error.value = e?.message || '查询任务失败'
      pollingError.value = true
    }
  }

  async function retry() {
    if (!job.value || submitting.value) return
    submitting.value = true
    try { await refreshJob(job.value.id) }
    finally { submitting.value = false }
  }

  function handle(j: T) {
    if (j.status === 'selection_required' || TERMINAL.includes(j.status)) {
      clearTimer()
      return
    }
    if (!alive) return
    schedulePoll(j.id)
  }

	async function submit(input: string, passCode: string, mode: JobMode) {
    clearTimer()
    error.value = ''
    const version = ++generation
    pollingError.value = false
    submitting.value = true
    job.value = null
    try {
		const j = await adapter.create({ input, pass_code: passCode || undefined, mode })
      if (!alive || version !== generation) return
      job.value = j
      handle(j)
    } catch (e: any) {
      if (!alive || version !== generation) return
      error.value = e?.message || '提交解析失败'
    } finally {
      if (alive && version === generation) submitting.value = false
    }
  }

  async function selectItems(ids: string[]) {
    if (!job.value) return
    const id = job.value.id
    const version = ++generation
    clearTimer()
    error.value = ''
    submitting.value = true
    try {
      const j = await adapter.select(id, { item_ids: ids })
      if (!alive || version !== generation) return
      job.value = j
      handle(j)
    } catch (e: any) {
      if (!alive || version !== generation) return
      error.value = e?.message || '选择失败'
    } finally {
      if (alive && version === generation) submitting.value = false
    }
  }

  function reset() {
    clearTimer()
    job.value = null
    ++generation
    error.value = ''
    pollingError.value = false
    submitting.value = false
  }

  onUnmounted(() => { alive = false; ++generation; clearTimer() })

  const phase = computed(() => {
    if (submitting.value) return 'submitting'
    if (pollingError.value) return 'query_failed'
    if (!job.value) return 'idle'
    return job.value.status
  })

  return { job, phase, error, submitting, pollingError, submit, selectItems, reset, retry }
}
