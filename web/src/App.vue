<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'
import { api, type SyncStatus } from './api'
import MarketSidebar from './components/MarketSidebar.vue'

const sync = ref<SyncStatus | null>(null)
let timer: number | undefined

async function loadSync() {
  try { sync.value = await api.syncStatus() } catch { /* ignore */ }
}

onMounted(() => {
  loadSync()
  timer = window.setInterval(loadSync, 30000)
})
onUnmounted(() => clearInterval(timer))
</script>

<template>
  <router-view v-slot="{ Component, route }">
    <component v-if="route.path === '/'" :is="Component" />
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
  </router-view>
</template>

<style scoped>
.detail-shell { display:grid; grid-template-columns:212px minmax(0,1fr); width:100vw; height:100vh; overflow:hidden; background:#eef1f4; }
.detail-content { display:grid; min-width:0; min-height:0; grid-template-rows:32px minmax(0,1fr); padding:0 8px; overflow:auto; color:#121820; }
.detail-header { display:flex; min-height:32px; align-items:center; justify-content:flex-end; border-bottom:1px solid #cdd3db; }
.sync-info { color:#687280; font-size:12px; }
@media (max-width:900px) { .detail-shell { width:100%; height:auto; min-height:100vh; grid-template-columns:minmax(0,1fr); overflow:visible; }.detail-content { display:block; width:100%; padding:0 8px 12px; overflow:hidden; }.detail-header { height:32px; } }
</style>
