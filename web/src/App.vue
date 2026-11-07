<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { AlertTriangle, Loader2, RefreshCw } from 'lucide-vue-next'
import AuroraBg from './components/AuroraBg.vue'
import ToastHost from './components/ToastHost.vue'
import Aria2ConfigModal from './components/Aria2ConfigModal.vue'
import Aria2PushOverlay from './components/Aria2PushOverlay.vue'
import Sidebar from './components/admin/Sidebar.vue'
import MetricGrid from './components/admin/MetricGrid.vue'
import ResolvePage from './components/admin/ResolvePage.vue'
import LibraryPage from './components/admin/LibraryPage.vue'
import AccountsPage from './components/admin/AccountsPage.vue'
import LogsPage from './components/admin/LogsPage.vue'
import SettingsPage from './components/admin/SettingsPage.vue'
import { api } from './lib/api'
import type { ConfigResponse, SettingsResponse } from './lib/types'

const booted = ref(false)
const syncing = ref(false)
const statusError = ref('')
const active = ref('resolve')
const config = ref<ConfigResponse | null>(null)
const settings = ref<SettingsResponse | null>(null)

const pageMap = {
  resolve: ResolvePage,
  library: LibraryPage,
  accounts: AccountsPage,
  logs: LogsPage,
  settings: SettingsPage,
} as const
const current = computed(() => pageMap[active.value as keyof typeof pageMap])
let pollTimer: number | undefined
let alive = true

async function refresh() {
  if (syncing.value || !alive) return
  window.clearTimeout(pollTimer)
  syncing.value = true
  try {
    const [nextConfig, nextSettings] = await Promise.all([api.config(), api.settings.get()])
    if (!alive) return
    config.value = nextConfig
    settings.value = nextSettings
    statusError.value = ''
    booted.value = true
  } catch (e: unknown) {
    if (alive) statusError.value = e instanceof Error ? e.message : '读取系统状态失败'
  } finally {
    syncing.value = false
    if (alive && booted.value) pollTimer = window.setTimeout(refresh, 5000)
  }
}

onMounted(refresh)
onUnmounted(() => { alive = false; window.clearTimeout(pollTimer) })
</script>

<template>
  <AuroraBg subtle />
  <ToastHost />
  <Aria2ConfigModal />
  <Aria2PushOverlay />

  <div v-if="!booted" class="boot">
    <div class="boot-card panel" :role="statusError ? 'alert' : 'status'">
      <AlertTriangle v-if="statusError" class="boot-ico" />
      <Loader2 v-else class="spin boot-ico" />
      <div><h2>{{ statusError ? '无法读取系统状态' : '正在同步' }}</h2><p>{{ statusError || '读取账号与系统状态…' }}</p></div>
      <button v-if="statusError" type="button" class="btn btn-soft" :disabled="syncing" @click="refresh"><RefreshCw :class="{ spin: syncing }" />重试</button>
    </div>
  </div>

  <div v-else class="shell">
    <Sidebar v-model:active="active" />
    <main class="workspace">
      <MetricGrid
        :total="config?.account_count ?? 0"
        :available="config?.available_account_count ?? 0"
        :failed="config?.failed_account_count ?? 0"
        :running="settings?.running ?? 0"
        :waiting="settings?.waiting ?? 0"
      />
      <div v-if="statusError" class="sync-error error-block" role="alert">
        <span>状态同步失败：{{ statusError }}</span>
        <button type="button" class="btn btn-line btn-sm" :disabled="syncing" @click="refresh"><RefreshCw :class="{ spin: syncing }" />重试</button>
      </div>
      <div class="region">
        <Transition name="v-swap" mode="out-in">
          <component :is="current" :key="active" @changed="refresh" />
        </Transition>
      </div>
    </main>
  </div>
</template>

<style scoped>
.boot { position: relative; z-index: 1; min-height: 100vh; display: grid; place-items: center; padding: 18px; }
.boot-card { display: flex; align-items: center; flex-wrap: wrap; gap: 13px; padding: 18px 22px; max-width: 100%; }
.boot-ico { width: 24px; height: 24px; color: var(--brand); flex: none; }
.boot-card h2 { font-size: var(--fs-md); font-weight: var(--fw-semi); }
.boot-card p { color: var(--ink-3); font-size: var(--fs-xs); margin-top: 1px; overflow-wrap: anywhere; }
.sync-error { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
.sync-error span { min-width: 0; overflow-wrap: anywhere; }
.sync-error .btn { flex: none; }
.shell { position: relative; z-index: 1; display: flex; height: 100vh; overflow: hidden; }
.workspace {
  flex: 1 1 auto; min-width: 0;
  height: 100vh;
  padding: 18px 24px;
  display: flex; flex-direction: column; gap: 14px;
  max-width: calc(100vw - var(--rail-w));
}
/* The page scrolls while the status strip stays pinned. */
.region { flex: 1 1 auto; min-height: 0; overflow-y: auto; padding-right: 3px; }
@media (max-width: 720px) {
  .shell { height: auto; overflow: visible; }
  .workspace { height: auto; padding: 16px 14px 96px; max-width: 100vw; }
  .region { overflow: visible; }
}
</style>
