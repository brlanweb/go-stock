<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'
import { api, type SyncStatus } from './api'
import MarketSidebar from './components/MarketSidebar.vue'

const sync = ref<SyncStatus | null>(null)
let timer: number | undefined

// 页面访问密码
const authChecked = ref(false)
const authRequired = ref(false)
const authed = ref(false)
const password = ref('')
const authError = ref('')
const authLoading = ref(false)

async function loadSync() {
  try { sync.value = await api.syncStatus() } catch { /* ignore */ }
}

async function checkAuth() {
  try {
    const status = await api.authStatus()
    authRequired.value = status.required
    authed.value = status.authenticated
  } catch {
    // 状态接口本身放行；失败按不需要密码处理，避免误锁
    authRequired.value = false
    authed.value = true
  } finally {
    authChecked.value = true
  }
}

async function submitPassword() {
  if (!password.value) { authError.value = '请输入密码'; return }
  authLoading.value = true
  authError.value = ''
  try {
    await api.authLogin(password.value)
    authed.value = true
    password.value = ''
    loadSync()
  } catch (e: any) {
    authError.value = e?.message || '密码错误'
  } finally {
    authLoading.value = false
  }
}

onMounted(async () => {
  await checkAuth()
  if (authed.value) loadSync()
  timer = window.setInterval(() => { if (authed.value) loadSync() }, 30000)
})
onUnmounted(() => clearInterval(timer))
</script>

<template>
  <div v-if="authChecked && authRequired && !authed" class="auth-gate">
    <form class="auth-card" @submit.prevent="submitPassword">
      <h1>go-stock</h1>
      <p>该站点已启用访问密码，请输入后查看内容。</p>
      <input v-model="password" type="password" placeholder="访问密码" autofocus autocomplete="current-password" />
      <button type="submit" :disabled="authLoading">{{ authLoading ? '验证中…' : '进入' }}</button>
      <span v-if="authError" class="auth-error">{{ authError }}</span>
    </form>
  </div>
  <template v-else-if="authChecked">
    <RouterView v-slot="{ Component, route }">
      <component v-if="!route.path.startsWith('/stock/')" :is="Component" />
      <div v-else class="detail-shell">
        <MarketSidebar :controls="false" />
        <main class="detail-content">
          <header class="detail-header">
            <span v-if="sync" class="dim sync-info">
              <template v-if="sync.backfill_running">完整 {{ sync.backfill.complete }}/{{ sync.backfill.total }} · 部分 {{ sync.backfill.partial }} · 空 {{ sync.backfill.empty }}</template>
              <template v-else-if="sync.backfill.latest_date">数据截至 {{ sync.backfill.latest_date }}</template>
            </span>
          </header>
          <component :is="Component" />
        </main>
      </div>
    </RouterView>
  </template>
</template>

<style scoped>
.auth-gate { display:flex; align-items:center; justify-content:center; width:100vw; height:100vh; background:#0f1826; }
.auth-card { display:flex; flex-direction:column; gap:12px; width:300px; max-width:88vw; padding:28px 26px; border:1px solid #26324a; background:#182338; color:#e7ecf4; box-shadow:0 18px 48px rgba(0,0,0,.45); }
.auth-card h1 { margin:0; font-size:22px; letter-spacing:1px; }
.auth-card p { margin:0; color:#8895ab; font-size:13px; line-height:1.5; }
.auth-card input { height:38px; padding:0 12px; border:1px solid #3a496a; border-radius:0; outline:none; background:#101a2b; color:#e7ecf4; font-size:14px; }
.auth-card input:focus { border-color:#e9c16c; }
.auth-card button { height:38px; border:0; border-radius:0; background:#e9c16c; color:#1a1206; font-size:14px; font-weight:700; cursor:pointer; }
.auth-card button:disabled { cursor:wait; opacity:.6; }
.auth-error { color:#ef6a72; font-size:12px; }
.detail-shell { display:grid; grid-template-columns:212px minmax(0,1fr); width:100vw; height:100vh; overflow:hidden; background:#eef1f4; }
.detail-content { display:grid; min-width:0; min-height:0; grid-template-rows:32px minmax(0,1fr); padding:0 8px; overflow:auto; color:#121820; }
.detail-header { display:flex; min-height:32px; align-items:center; justify-content:flex-end; border-bottom:1px solid #cdd3db; }
.sync-info { color:#687280; font-size:12px; }
@media (max-width:900px) { .detail-shell { width:100%; height:auto; min-height:100vh; grid-template-columns:minmax(0,1fr); overflow:visible; }.detail-content { display:block; width:100%; padding:0 8px 12px; overflow:hidden; }.detail-header { height:32px; } }
</style>
