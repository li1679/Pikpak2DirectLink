<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'
import { DatabaseBackup, Gauge, RefreshCw, Save } from 'lucide-vue-next'
import GlassCard from '../GlassCard.vue'
import PrimaryButton from '../PrimaryButton.vue'
import { api } from '../../lib/api'
import { toast } from '../../composables/useToast'
import { formatBytes, formatDateTime, formatRelative } from '../../lib/format'
import type { SettingsResponse, StorageStatusResponse } from '../../lib/types'

const emit = defineEmits<{ (e: 'changed'): void }>()

const settings = ref<SettingsResponse | null>(null)
const settingsLoading = ref(false)
const settingsErr = ref('')
const concInput = ref(1)
const timeoutInput = ref(60)
const concErr = ref('')
const concSaving = ref(false)

const storage = ref<StorageStatusResponse | null>(null)
const storageLoading = ref(false)
const storageErr = ref('')
const backupRunning = ref(false)
let storageRefreshTimer: number | undefined
let storageMounted = true

async function loadSettings() {
  settingsLoading.value = true
  settingsErr.value = ''
  try {
    settings.value = await api.settings.get()
    concInput.value = settings.value.concurrency
    timeoutInput.value = Math.max(1, Math.round(settings.value.task_timeout_seconds / 60))
  } catch (e: any) {
    settingsErr.value = e?.message || '加载设置失败'
  } finally {
    settingsLoading.value = false
  }
}

async function loadStorage() {
	clearStorageRefresh()
	storageErr.value = ''
	storageLoading.value = true
	try {
		const payload = await api.settings.storage.get()
		if (!storageMounted) return
		storage.value = payload
		if (storage.value.running) {
			storageRefreshTimer = window.setTimeout(loadStorage, 1500)
		}
	} catch (e: any) {
		storageErr.value = e?.message || '加载存储状态失败'
	} finally {
		storageLoading.value = false
	}
}

function clearStorageRefresh() {
	if (storageRefreshTimer) {
		window.clearTimeout(storageRefreshTimer)
		storageRefreshTimer = undefined
	}
}

async function runBackup() {
  storageErr.value = ''
  backupRunning.value = true
  try {
    await api.settings.storage.backup()
    toast('数据库备份已创建并校验', 'success')
    await loadStorage()
  } catch (e: any) {
    storageErr.value = e?.message || '创建备份失败'
  } finally {
    backupRunning.value = false
  }
}

function backupStatusLabel(status?: string) {
  if (status === 'success') return '校验通过'
  if (status === 'failed') return '失败'
  if (status === 'running') return '进行中'
  return '尚未执行'
}

function backupIntervalLabel(seconds: number) {
  if (!Number.isFinite(seconds) || seconds <= 0) return '-'
  const hours = seconds / 3600
  return Number.isInteger(hours) ? `${hours} 小时` : `${Math.round(hours * 10) / 10} 小时`
}

async function saveSettings() {
  concErr.value = ''
  if (!Number.isFinite(concInput.value) || concInput.value < 1 || concInput.value > 32) { concErr.value = '并发数需在 1–32 之间'; return }
  if (!Number.isFinite(timeoutInput.value) || timeoutInput.value < 1) { concErr.value = '超时至少 1 分钟'; return }
  concSaving.value = true
  try {
    settings.value = await api.settings.update({ concurrency: concInput.value, task_timeout_minutes: timeoutInput.value })
    toast('设置已保存', 'success')
    emit('changed')
  } catch (e: any) {
    concErr.value = e?.message || '保存失败'
  } finally {
    concSaving.value = false
  }
}

onMounted(async () => {
	await Promise.all([loadSettings(), loadStorage()])
})

onUnmounted(() => {
	storageMounted = false
	clearStorageRefresh()
})
</script>

<template>
  <div class="page">
    <GlassCard seam>
      <div class="sec-head mb">
        <div class="sec-title">
          <span class="sec-glyph"><Gauge /></span>
          <div><span class="eyebrow">concurrency</span><h2>并行解析</h2><p>同时解析的任务数与新任务超时。串行（1）逐个处理；并行（&gt;1）可同时处理多个链接</p></div>
        </div>
      </div>
      <p v-if="settingsErr" class="error-block" role="alert">{{ settingsErr }} <button type="button" class="btn btn-line btn-sm" :disabled="settingsLoading" @click="loadSettings"><RefreshCw />重试</button></p>
      <form class="form" @submit.prevent="saveSettings">
        <label class="field"><span class="field-label">并发解析数（1 为串行）</span><input v-model.number="concInput" class="input input-mono" type="number" min="1" max="32" step="1" inputmode="numeric" /></label>
        <label class="field"><span class="field-label">任务超时（分钟）</span><input v-model.number="timeoutInput" class="input input-mono" type="number" min="1" max="720" step="1" inputmode="numeric" /></label>
        <PrimaryButton type="submit" :loading="concSaving || settingsLoading" :disabled="!settings || !!settingsErr"><template #icon><Save /></template>保存设置</PrimaryButton>
      </form>
      <p class="state">
        当前 <b class="mono">并发 {{ settings?.concurrency ?? '-' }}</b> ·
        <b class="mono">超时 {{ settings ? Math.round(settings.task_timeout_seconds / 60) : '-' }} 分钟</b> ·
        队列 <b class="mono">{{ settings?.waiting ?? 0 }} 等待 / {{ settings?.running ?? 0 }} 运行</b>
      </p>
      <Transition name="v-fade"><p v-if="concErr" class="error-block">{{ concErr }}</p></Transition>
      <p class="state">网盘临时资源保留 6 小时，到期自动删除；应用关闭期间无法清理，重新打开后补删。只清理本程序创建或转存的资源。</p>
    </GlassCard>

    <GlassCard seam>
      <div class="sec-head mb">
        <div class="sec-title">
          <span class="sec-glyph ok"><DatabaseBackup /></span>
          <div><span class="eyebrow">storage</span><h2>数据库备份</h2><p>查看自动备份状态，或立即创建一份经过完整性校验的 SQLite 快照</p></div>
        </div>
        <PrimaryButton size="sm" variant="soft" :loading="backupRunning || storage?.running" :disabled="storageLoading || !storage || !!storageErr" @click="runBackup">
          <template #icon><DatabaseBackup /></template>立即备份
        </PrimaryButton>
      </div>

      <div class="storage-grid">
        <div class="storage-row"><span>备份目录</span><b class="mono path-value">{{ storage?.backup_dir || '-' }}</b></div>
        <div class="storage-row"><span>自动备份</span><b>{{ backupIntervalLabel(storage?.backup_interval_seconds ?? 0) }} · 保留 {{ storage?.backup_retention ?? '-' }} 份</b></div>
        <div class="storage-row"><span>最近一次</span><b :class="{ 'danger-text': storage?.last_run?.status === 'failed' }">{{ backupStatusLabel(storage?.last_run?.status) }} · {{ formatDateTime(storage?.last_run?.completed_at || storage?.last_run?.started_at || '') }}</b></div>
        <div class="storage-row"><span>最近成功</span><b>{{ storage?.last_success ? `${formatBytes(storage.last_success.size_bytes)} · ${formatDateTime(storage.last_success.completed_at || storage.last_success.started_at)}` : '暂无' }}</b></div>
        <div class="storage-row"><span>下次执行</span><b>{{ storage?.next_run_at ? formatRelative(storage.next_run_at) : '-' }}</b></div>
      </div>

      <Transition name="v-fade"><p v-if="storageErr" class="error-block" role="alert">{{ storageErr }} <button type="button" class="btn btn-line btn-sm" :disabled="storageLoading" @click="loadStorage"><RefreshCw />重试</button></p></Transition>
    </GlassCard>
  </div>
</template>

<style scoped>
.page { display: flex; flex-direction: column; gap: 14px; }
.sec-head.mb { margin-bottom: 14px; }
.eyebrow { display: block; margin-bottom: 2px; }
.form { display: grid; grid-template-columns: 1fr 1fr auto; gap: 10px; align-items: end; }
.form .btn { height: 34px; }
.state { font-size: var(--fs-sm); color: var(--ink-2); margin-top: 12px; }
.state b { color: var(--ink); font-weight: var(--fw-semi); }
.storage-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); border-top: 1px solid var(--line); }
.storage-row { min-width: 0; display: flex; align-items: baseline; justify-content: space-between; gap: 12px; padding: 10px 0; border-bottom: 1px solid var(--line); font-size: var(--fs-sm); }
.storage-row:nth-child(odd) { padding-right: 18px; }
.storage-row:nth-child(even) { padding-left: 18px; border-left: 1px solid var(--line); }
.storage-row span { flex: none; color: var(--ink-3); }
.storage-row b { min-width: 0; color: var(--ink); font-weight: var(--fw-semi); text-align: right; overflow-wrap: anywhere; }
.storage-row .path-value { font-size: var(--fs-xs); }
.storage-row .danger-text { color: var(--danger-ink); }
@media (max-width: 820px) {
  .form, .storage-grid { grid-template-columns: 1fr; }
  .storage-row:nth-child(odd), .storage-row:nth-child(even) { padding-left: 0; padding-right: 0; border-left: 0; }
}
</style>
