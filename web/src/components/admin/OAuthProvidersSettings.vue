<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { Copy, Pencil, Plus, RefreshCw, Save, ShieldCheck, Trash2, X } from 'lucide-vue-next'
import GlassCard from '../GlassCard.vue'
import PrimaryButton from '../PrimaryButton.vue'
import { api } from '../../lib/api'
import { copyText } from '../../lib/clipboard'
import { toast } from '../../composables/useToast'
import type { OAuthProvider, OAuthProviderRequest } from '../../lib/types'

const providers = ref<OAuthProvider[]>([])
const loading = ref(false)
const saving = ref(false)
const error = ref('')
const editorOpen = ref(false)
const editing = ref<OAuthProvider | null>(null)
const deletingID = ref('')
const template = ref<'oidc' | 'linuxdo'>('oidc')

function emptyForm(): OAuthProviderRequest {
  return {
    name: '', authorization_url: '', token_url: '', userinfo_url: '', client_id: '',
    client_secret: '', clear_client_secret: false, scopes: 'openid profile email',
    token_auth_method: 'client_secret_post', id_field: 'sub', username_field: 'preferred_username',
    display_name_field: 'name', email_field: 'email', avatar_field: 'picture',
    login_enabled: true, registration_enabled: true, sort_order: 0, pkce_enabled: true,
  }
}
const form = ref<OAuthProviderRequest>(emptyForm())

function providerRequest(provider: OAuthProvider): OAuthProviderRequest {
  return {
    name: provider.name, authorization_url: provider.authorization_url, token_url: provider.token_url,
    userinfo_url: provider.userinfo_url, client_id: provider.client_id, scopes: provider.scopes,
    token_auth_method: provider.token_auth_method, id_field: provider.id_field,
    username_field: provider.username_field, display_name_field: provider.display_name_field,
    email_field: provider.email_field, avatar_field: provider.avatar_field,
    login_enabled: provider.login_enabled, registration_enabled: provider.registration_enabled,
    sort_order: provider.sort_order, pkce_enabled: provider.pkce_enabled,
    expected_revision: provider.revision,
  }
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    const result = await api.settings.oauthProviders.list()
    providers.value = result.providers ?? []
  } catch (e: any) {
    error.value = e?.message || '加载授权方式失败'
  } finally {
    loading.value = false
  }
}

function openCreate() {
  editing.value = null
  template.value = 'oidc'
  form.value = emptyForm()
  error.value = ''
  editorOpen.value = true
}

function applyTemplate() {
  const previous = form.value
  const next = emptyForm()
  if (template.value === 'linuxdo') {
    Object.assign(next, {
      name: 'Linux DO', authorization_url: 'https://connect.linux.do/oauth2/authorize',
      token_url: 'https://connect.linux.do/oauth2/token', userinfo_url: 'https://connect.linux.do/api/user',
      scopes: 'read', id_field: 'id', username_field: 'username', display_name_field: 'name',
      email_field: 'email', avatar_field: 'avatar_url', pkce_enabled: false,
    })
  }
  next.client_id = previous.client_id
  next.client_secret = previous.client_secret
  form.value = next
}

function openEdit(provider: OAuthProvider) {
  editing.value = provider
  form.value = { ...providerRequest(provider), client_secret: '', clear_client_secret: false }
  error.value = ''
  editorOpen.value = true
}

function closeEditor() {
  editorOpen.value = false
  editing.value = null
  form.value = emptyForm()
}

function replaceProvider(provider: OAuthProvider) {
  providers.value = [...providers.value.filter((p) => p.id !== provider.id), provider]
    .sort((a, b) => a.sort_order - b.sort_order || a.name.localeCompare(b.name))
}

async function save() {
  saving.value = true
  error.value = ''
  try {
    const body = { ...form.value, client_secret: form.value.client_secret?.trim() || undefined }
    const provider = editing.value
      ? await api.settings.oauthProviders.update(editing.value.id, body)
      : await api.settings.oauthProviders.create(body)
    replaceProvider(provider)
    closeEditor()
    toast('授权方式已保存', 'success')
  } catch (e: any) {
    error.value = e?.message || '保存授权方式失败'
  } finally {
    saving.value = false
  }
}

async function toggle(provider: OAuthProvider, field: 'login_enabled' | 'registration_enabled') {
  saving.value = true
  error.value = ''
  try {
    const updated = await api.settings.oauthProviders.update(provider.id, {
      ...providerRequest(provider), [field]: !provider[field],
    })
    replaceProvider(updated)
    toast('授权方式已更新', 'success')
  } catch (e: any) {
    error.value = e?.message || '更新失败，请刷新配置后重试'
  } finally {
    saving.value = false
  }
}

async function remove(provider: OAuthProvider) {
  saving.value = true
  error.value = ''
  try {
    await api.settings.oauthProviders.remove(provider.id, provider.revision)
    providers.value = providers.value.filter((p) => p.id !== provider.id)
    deletingID.value = ''
    toast('授权方式已删除', 'success')
  } catch (e: any) {
    error.value = e?.message || '删除授权方式失败'
  } finally {
    saving.value = false
  }
}

async function copyCallback(provider: OAuthProvider) {
  toast(await copyText(provider.callback_url) ? '回调地址已复制' : '复制失败', 'info')
}

onMounted(load)
</script>

<template>
  <GlassCard seam>
    <div class="sec-head mb provider-heading">
      <div class="sec-title">
        <span class="sec-glyph ok"><ShieldCheck /></span>
        <div><span class="eyebrow">oauth providers</span><h2>第三方授权登录</h2><p>添加多个授权站点，自定义登录名称、注册权限和显示顺序</p></div>
      </div>
      <div class="actions">
        <PrimaryButton variant="ghost" size="sm" :loading="loading" :disabled="saving || editorOpen" @click="load"><template #icon><RefreshCw /></template>刷新</PrimaryButton>
        <PrimaryButton variant="soft" size="sm" :disabled="saving || editorOpen || loading" @click="openCreate"><template #icon><Plus /></template>新增授权方式</PrimaryButton>
      </div>
    </div>

    <div v-if="!editorOpen" class="provider-list">
      <div v-if="loading && !providers.length" class="empty-note">正在加载授权方式…</div>
      <div v-else-if="!providers.length && !error" class="empty-note">尚未添加授权方式。添加并配置完成后，可在用户登录页显示。</div>
      <article v-for="provider in providers" :key="provider.id" class="provider-row">
        <div class="provider-info">
          <div class="provider-title"><strong>{{ provider.name }}</strong><span class="pill" :class="provider.configured ? 'pill-ok' : ''">{{ provider.configured ? '已配置' : '待配置' }}</span><span class="order">顺序 {{ provider.sort_order }}</span></div>
          <p class="provider-meta mono">{{ provider.id }}</p>
          <div class="callback-row"><code class="mono">{{ provider.callback_url }}</code><button class="btn btn-ghost btn-sm" type="button" aria-label="复制回调地址" @click="copyCallback(provider)"><Copy /></button></div>
        </div>
        <div class="provider-controls">
          <label class="check"><input type="checkbox" :checked="provider.login_enabled" :disabled="saving || loading" @change="toggle(provider, 'login_enabled')" />登录</label>
          <label class="check"><input type="checkbox" :checked="provider.registration_enabled" :disabled="saving || loading" @change="toggle(provider, 'registration_enabled')" />注册</label>
          <button class="btn btn-line btn-sm" type="button" :disabled="saving || loading" @click="openEdit(provider)"><Pencil />编辑</button>
          <button class="btn btn-ghost btn-sm delete-button" type="button" :disabled="saving || loading" :aria-label="`删除 ${provider.name}`" @click="deletingID = provider.id"><Trash2 /></button>
        </div>
        <div v-if="deletingID === provider.id" class="delete-confirm">
          <p>删除“{{ provider.name }}”后将关闭此登录入口，已有用户身份记录仍会保留。</p>
          <div class="actions"><button class="btn btn-ghost btn-sm" type="button" :disabled="saving" @click="deletingID = ''">取消</button><PrimaryButton variant="danger" size="sm" :loading="saving" @click="remove(provider)">确认删除</PrimaryButton></div>
        </div>
      </article>
    </div>

    <form v-else class="provider-form" @submit.prevent="save">
      <div class="editor-head"><h3>{{ editing ? `编辑 ${editing.name}` : '新增授权方式' }}</h3><button class="btn btn-ghost btn-sm" type="button" :disabled="saving" aria-label="关闭编辑" @click="closeEditor"><X /></button></div>
      <label v-if="!editing" class="field"><span class="field-label">配置模板</span><select v-model="template" class="input" :disabled="saving" @change="applyTemplate"><option value="oidc">通用 OAuth / OpenID Connect</option><option value="linuxdo">Linux DO</option></select></label>
      <p v-if="!editing" class="form-note full">通用模板需按授权站点提供的文档填写地址。保存后可复制专属回调地址。</p>
      <label class="field"><span class="field-label">登录显示名称</span><input v-model="form.name" class="input" maxlength="80" required :disabled="saving" placeholder="例如：我的社区" /></label>
      <label class="field"><span class="field-label">显示顺序（数值越小越靠前）</span><input v-model.number="form.sort_order" class="input input-mono" type="number" min="-10000" max="10000" required :disabled="saving" /></label>
      <div v-if="editing" class="field full"><span class="field-label">专属回调地址</span><div class="copy-row"><input class="input input-mono" :value="editing.callback_url" readonly /><button class="btn btn-line btn-sm" type="button" @click="copyCallback(editing)"><Copy />复制</button></div></div>
      <label class="field full"><span class="field-label">授权地址（Authorization URL）</span><input v-model="form.authorization_url" class="input input-mono" type="url" required :disabled="saving" placeholder="https://example.com/oauth/authorize" /></label>
      <label class="field"><span class="field-label">令牌地址（Token URL）</span><input v-model="form.token_url" class="input input-mono" type="url" required :disabled="saving" placeholder="https://example.com/oauth/token" /></label>
      <label class="field"><span class="field-label">用户信息地址（UserInfo URL）</span><input v-model="form.userinfo_url" class="input input-mono" type="url" required :disabled="saving" placeholder="https://example.com/userinfo" /></label>
      <label class="field"><span class="field-label">Client ID</span><input v-model="form.client_id" class="input input-mono" autocomplete="off" :disabled="saving" /></label>
      <label class="field"><span class="field-label">Client Secret{{ editing?.client_secret_configured ? '（已保存）' : '' }}</span><input v-model="form.client_secret" class="input input-mono" type="password" autocomplete="new-password" :disabled="saving || form.clear_client_secret" :placeholder="editing ? '留空保留已保存的 Secret' : '输入授权站点提供的 Secret'" /></label>
      <div class="check-grid full"><label class="check"><input v-model="form.login_enabled" type="checkbox" :disabled="saving" />开放登录</label><label class="check"><input v-model="form.registration_enabled" type="checkbox" :disabled="saving" />允许新用户注册</label><label v-if="editing" class="check danger"><input v-model="form.clear_client_secret" type="checkbox" :disabled="saving" />清空已保存的 Secret</label></div>
      <details class="advanced full" open>
        <summary>授权参数与用户字段映射</summary>
        <div class="advanced-grid">
          <label class="field"><span class="field-label">Scope（空格分隔）</span><input v-model="form.scopes" class="input input-mono" :disabled="saving" /></label>
          <label class="field"><span class="field-label">客户端认证方式</span><select v-model="form.token_auth_method" class="input" :disabled="saving"><option value="client_secret_post">client_secret_post</option><option value="client_secret_basic">client_secret_basic</option></select></label>
          <label class="check full"><input v-model="form.pkce_enabled" type="checkbox" :disabled="saving" />启用 PKCE（S256，需授权站点支持）</label>
          <p class="form-note full">填写用户信息响应中的字段名；嵌套字段使用点路径，例如 data.id。用户 ID 必须唯一且稳定。</p>
          <label class="field"><span class="field-label">用户 ID 字段</span><input v-model="form.id_field" class="input input-mono" required :disabled="saving" /></label>
          <label class="field"><span class="field-label">用户名字段</span><input v-model="form.username_field" class="input input-mono" :disabled="saving" /></label>
          <label class="field"><span class="field-label">显示名称字段</span><input v-model="form.display_name_field" class="input input-mono" :disabled="saving" /></label>
          <label class="field"><span class="field-label">邮箱字段</span><input v-model="form.email_field" class="input input-mono" :disabled="saving" /></label>
          <label class="field"><span class="field-label">头像字段</span><input v-model="form.avatar_field" class="input input-mono" :disabled="saving" /></label>
        </div>
      </details>
      <div class="form-actions full"><button class="btn btn-ghost" type="button" :disabled="saving" @click="closeEditor">取消</button><PrimaryButton type="submit" :loading="saving"><template #icon><Save /></template>保存授权方式</PrimaryButton></div>
    </form>
    <Transition name="v-fade"><p v-if="error" class="error-block">{{ error }}<button v-if="!editorOpen" class="link-btn" type="button" :disabled="loading || saving" @click="load">重新加载</button></p></Transition>
  </GlassCard>
</template>

<style scoped>
.mb { margin-bottom: 14px; }
.eyebrow { display: block; margin-bottom: 2px; }
.sec-glyph.ok { background: var(--ok-soft); color: var(--ok); }
.actions, .provider-controls, .provider-title { display: flex; align-items: center; flex-wrap: wrap; gap: 8px; }
.provider-heading { flex-wrap: wrap; }
.provider-list { display: grid; gap: 10px; }
.provider-row { display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 12px; border: 1px solid var(--line); border-radius: var(--r-md); padding: 13px; background: var(--panel-2); }
.provider-info { min-width: 0; flex: 1 1 320px; }
.provider-title strong { font-size: var(--fs-md); overflow-wrap: anywhere; }
.provider-meta { color: var(--ink-3); font-size: var(--fs-2xs); margin-top: 5px; overflow-wrap: anywhere; }
.order { color: var(--ink-3); font-size: var(--fs-xs); }
.callback-row { display: flex; align-items: center; gap: 6px; margin-top: 4px; }
.callback-row code { font-size: var(--fs-2xs); color: var(--ink-2); overflow-wrap: anywhere; }
.callback-row button { flex: none; }
.provider-controls { flex: none; gap: 12px; }
.delete-button, .danger { color: var(--danger-ink); }
.delete-confirm { flex-basis: 100%; display: flex; align-items: center; justify-content: space-between; gap: 12px; border-top: 1px solid var(--danger-line); padding-top: 12px; color: var(--danger-ink); font-size: var(--fs-sm); }
.empty-note, .form-note { font-size: var(--fs-sm); color: var(--ink-2); line-height: 1.6; }
.empty-note { padding: 14px 0; }
.provider-form, .advanced-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 12px; }
.full, .editor-head { grid-column: 1 / -1; }
.editor-head { display: flex; align-items: center; justify-content: space-between; gap: 12px; padding-bottom: 8px; border-bottom: 1px solid var(--line); }
.editor-head h3 { font-size: var(--fs-md); }
.field { min-width: 0; }
.copy-row { display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: 8px; }
.check-grid { display: flex; align-items: center; flex-wrap: wrap; gap: 12px 18px; padding: 3px 0; }
.advanced { border-top: 1px solid var(--line); padding-top: 12px; }
.advanced summary { font-size: var(--fs-sm); font-weight: var(--fw-semi); cursor: pointer; }
.advanced-grid { margin-top: 12px; }
.form-actions { display: flex; justify-content: flex-end; align-items: center; gap: 8px; }
.error-block .link-btn { margin-left: 12px; }
@media (max-width: 700px) {
  .provider-form, .advanced-grid { grid-template-columns: minmax(0, 1fr); }
  .provider-controls { width: 100%; }
  .delete-confirm { flex-direction: column; align-items: flex-start; }
  .provider-heading > .actions { width: 100%; }
}
</style>
