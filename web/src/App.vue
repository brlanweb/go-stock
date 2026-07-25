<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'
import { api, type SyncStatus } from './api'

const sync = ref<SyncStatus | null>(null)
let timer: number | undefined

async function loadSync() {
  try {
    sync.value = await api.syncStatus()
  } catch { /* 忽略 */ }
}

onMounted(() => {
  loadSync()
  timer = window.setInterval(loadSync, 30000)
})
onUnmounted(() => clearInterval(timer))
</script>

<template>
  <div class="container">
    <header class="header">
      <router-link to="/" class="logo">go-stock</router-link>
      <span v-if="sync" class="dim sync-info">
        <template v-if="sync.backfill_running">
          回填中 {{ sync.backfill.done }}/{{ sync.backfill.total }}
        </template>
        <template v-else-if="sync.backfill.latest_date">
          数据截至 {{ sync.backfill.latest_date }}
        </template>
      </span>
    </header>
    <router-view />
  </div>
</template>

<style scoped>
.header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 4px 0 8px;
}
.logo { font-size: 18px; font-weight: 700; }
.sync-info { font-size: 12px; }
</style>
