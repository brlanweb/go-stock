<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { api, type SectorListItem, type SectorConstituentItem } from '../api'
import MarketSidebar from '../components/MarketSidebar.vue'

const props = defineProps<{ code: string }>()
const router = useRouter()
const sectors = ref<{ type: 'industry' | 'concept'; name: string; code: string } | null>(null)
const constituents = ref<SectorConstituentItem[]>([])
const loading = ref(false)
const message = ref('')
const groupBy = ref<'industry' | 'concept'>('industry')

async function load() {
  // code 既可能是 sector_code，也可能是 "industry:银行" 形式（来自个股详情页的占位）。
  if (props.code.startsWith('industry:')) {
    sectors.value = { type: 'industry', name: decodeURIComponent(props.code.slice('industry:'.length)), code: props.code }
    loadConstituents()
    return
  }
  sectors.value = { type: 'concept', name: props.code, code: props.code }
  loadConstituents()
}

async function loadConstituents() {
  if (!sectors.value) return
  loading.value = true
  message.value = ''
  try {
    const res = await api.sectorConstituents(sectors.value.code, 200)
    constituents.value = res.constituents
    groupBy.value = sectors.value.type
  } catch (e: any) {
    message.value = e?.message || '成分股加载失败'
    constituents.value = []
  } finally {
    loading.value = false
  }
}

function switchGroup(type: 'industry' | 'concept') {
  router.replace({ path: '/' })
}

function openStock(symbol: string) {
  router.push(`/stock/${symbol}`)
}

onMounted(load)
</script>

<template>
  <div class="sector-shell">
    <MarketSidebar :controls="false" />
    <main class="sector-content">
      <header class="sector-header">
        <div>
          <strong>{{ sectors?.name || code }}</strong>
          <small>· 涨跌幅由高到低排序</small>
        </div>
        <router-link to="/" class="back-link">← 返回市场云图</router-link>
      </header>
      <p v-if="message" class="sector-message">{{ message }}</p>
      <p v-if="loading" class="empty">加载中…</p>
      <div v-else-if="!constituents.length" class="empty">暂无成分股数据</div>
      <table v-else class="sector-table">
        <thead>
          <tr>
            <th>排名</th><th>股票</th><th>行业</th>
            <th v-if="groupBy==='industry'">实时股价</th>
            <th>涨跌幅</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="(item, idx) in constituents" :key="item.symbol" @click="openStock(item.symbol)">
            <td>{{ idx + 1 }}</td>
            <td><b>{{ item.name }}</b><small>{{ item.code }}</small></td>
            <td>{{ item.industry || '-' }}</td>
            <td v-if="groupBy==='industry'">
              <template v-if="item.is_trading">{{ item.price.toFixed(2) }}</template>
              <template v-else>-</template>
            </td>
            <td :class="item.change_pct > 0 ? 'up' : item.change_pct < 0 ? 'down' : ''">
              {{ (item.change_pct > 0 ? '+' : '') + item.change_pct.toFixed(2) }}%
            </td>
          </tr>
        </tbody>
      </table>
    </main>
  </div>
</template>

<style scoped>
.sector-shell { display:grid; grid-template-columns:212px minmax(0,1fr); width:100vw; height:100vh; overflow:hidden; background:#0f1826; color:#e7ecf4; }
.sector-content { display:flex; min-width:0; min-height:0; flex-direction:column; padding:0 14px 14px; overflow:hidden; }
.sector-header { display:flex; align-items:center; justify-content:space-between; padding:12px 2px; border-bottom:1px solid #26324a; }
.sector-header strong { font-size:16px; }.sector-header small { margin-left:8px; color:#8895ab; font-size:12px; }
.back-link { color:#c4cddc; font-size:12px; }.sector-message { padding:10px; color:#d8b967; font-size:12px; }
.empty { padding:24px; color:#6f7c92; font-size:13px; }
.sector-table { width:100%; border-collapse:collapse; font-size:13px; }
.sector-table th, .sector-table td { padding:8px 6px; border-bottom:1px solid #1e2a40; text-align:left; }
.sector-table th { position:sticky; top:0; background:#101a2b; color:#8895ab; font-weight:600; }
.sector-table tbody tr { cursor:pointer; }
.sector-table tbody tr:hover { background:#1a2540; }
.sector-table b { display:block; font-size:14px; }
.sector-table small { display:block; color:#8895ab; font-size:11px; }
.sector-table .up { color:#ef6a72; }.sector-table .down { color:#28bd8b; }
@media (max-width:900px) { .sector-shell { grid-template-columns:1fr; height:auto; min-height:100vh; overflow:visible; } .sector-content { height:auto; overflow:visible; } }
</style>