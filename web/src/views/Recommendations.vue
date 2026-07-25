<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { api, type Recommendation } from '../api'
import MarketSidebar from '../components/MarketSidebar.vue'

const router = useRouter()
const dates = ref<string[]>([])
const activeDate = ref('')
const items = ref<Recommendation[]>([])
const loading = ref(false)
const message = ref('')
const running = ref(false)

async function loadDate(date: string) {
  activeDate.value = date
  loading.value = true
  try {
    items.value = await api.recommendations(date)
  } catch (e: any) {
    items.value = []
    message.value = e?.message || '加载失败'
  } finally {
    loading.value = false
  }
}

async function refreshDates() {
  dates.value = await api.recommendationHistory(365).catch(() => [] as string[])
  if (dates.value.length) await loadDate(dates.value[0])
}

async function runAnalysis() {
  running.value = true
  message.value = ''
  try {
    await api.runRecommendations()
    message.value = '分析已启动，约 10 秒后自动刷新'
    window.setTimeout(refreshDates, 10000)
  } catch (e: any) {
    message.value = e?.message || '分析启动失败'
  } finally {
    running.value = false
  }
}

function openStock(symbol: string) {
  router.push(`/stock/${symbol}`)
}

onMounted(refreshDates)
</script>

<template>
  <div class="reco-shell">
    <MarketSidebar :controls="false" />
    <main class="reco-content">
      <header class="reco-header">
        <div class="reco-title"><strong>AI 趋势推荐历史</strong><small>每日分析结果永久保留，可回溯</small></div>
        <div class="reco-tools">
          <button class="run-btn" :disabled="running" @click="runAnalysis">{{ running ? '分析中…' : '立即生成' }}</button>
        </div>
      </header>
      <p v-if="message" class="reco-message">{{ message }}</p>

      <div class="reco-body">
        <aside class="date-list">
          <button v-for="date in dates" :key="date" :class="{ active: date === activeDate }" @click="loadDate(date)">{{ date }}</button>
          <p v-if="!dates.length" class="empty">暂无历史记录，请配置模型后生成</p>
        </aside>

        <section class="reco-table">
          <div v-if="loading" class="empty">加载中…</div>
          <template v-else-if="items.length">
            <div class="reco-row head"><span>排名</span><span>股票</span><span>动量分</span><span>核心依据</span><span>板块</span></div>
            <button v-for="item in items" :key="item.symbol" class="reco-row" @click="openStock(item.symbol)">
              <span class="rank">{{ item.rank }}</span>
              <span class="stock"><b>{{ item.name }}</b><small>{{ item.code }}</small></span>
              <span class="score">{{ item.probability.toFixed(1) }}</span>
              <span class="reason">{{ item.reason }}</span>
              <span class="sector">{{ item.sector }}</span>
            </button>
            <p class="disclaimer">说明：动量分仅为基于历史价格动量的相对排序，非真实统计概率；历史表现不代表未来收益。模型：{{ items[0].model || '—' }}</p>
          </template>
          <div v-else class="empty">该日期暂无推荐数据</div>
        </section>
      </div>
    </main>
  </div>
</template>

<style scoped>
.reco-shell { display:grid; grid-template-columns:212px minmax(0,1fr); width:100vw; height:100vh; overflow:hidden; background:#0f1826; color:#e7ecf4; }
.reco-content { display:flex; min-width:0; min-height:0; flex-direction:column; padding:0 14px 14px; overflow:hidden; }
.reco-header { display:flex; align-items:center; justify-content:space-between; padding:12px 2px; border-bottom:1px solid #26324a; }
.reco-title strong { font-size:16px; }.reco-title small { margin-left:10px; color:#8895ab; font-size:12px; }
.run-btn { padding:6px 14px; border:1px solid #3a496a; border-radius:0; background:#233150; color:#e7ecf4; font-size:13px; cursor:pointer; }.run-btn:disabled { cursor:wait; opacity:.6; }
.reco-message { margin:8px 2px 0; color:#d8b967; font-size:12px; }
.reco-body { display:grid; min-height:0; grid-template-columns:132px minmax(0,1fr); gap:14px; margin-top:12px; overflow:hidden; }
.date-list { display:flex; min-height:0; flex-direction:column; gap:3px; overflow-y:auto; padding-right:4px; }
.date-list button { padding:7px 9px; border:0; border-left:2px solid transparent; border-radius:0; background:#182338; color:#c4cddc; font-size:12px; text-align:left; cursor:pointer; }
.date-list button.active { border-left-color:#e9c16c; background:#22314e; color:#fff; }
.date-list .empty { padding:8px; color:#6f7c92; font-size:11px; }
.reco-table { display:flex; min-height:0; flex-direction:column; overflow-y:auto; }
.reco-row { display:grid; grid-template-columns:48px 150px 74px minmax(0,1fr) 96px; gap:10px; align-items:center; padding:11px 10px; border:0; border-bottom:1px solid #1e2a40; background:transparent; color:#e7ecf4; text-align:left; cursor:pointer; }
.reco-row.head { position:sticky; top:0; background:#101a2b; color:#8895ab; font-size:12px; cursor:default; }
.reco-row:not(.head):hover { background:#1a2540; }
.reco-row .rank { display:inline-flex; width:26px; height:26px; align-items:center; justify-content:center; background:#2a3a5c; color:#e9c16c; font-weight:700; }
.reco-row .stock b { font-size:14px; }.reco-row .stock small { display:block; margin-top:2px; color:#8895ab; font-size:11px; }
.reco-row .score { color:#ef6a72; font-size:16px; font-weight:700; }
.reco-row .reason { color:#c4cddc; font-size:12px; line-height:1.4; }
.reco-row .sector { color:#93a0b6; font-size:12px; }
.disclaimer { padding:10px 4px; color:#6f7c92; font-size:11px; line-height:1.5; }
.empty { padding:20px; color:#6f7c92; font-size:13px; }
@media (max-width:900px) {
  .reco-shell { grid-template-columns:1fr; height:auto; min-height:100vh; overflow:visible; }
  .reco-content { height:auto; overflow:visible; }
  .reco-body { grid-template-columns:1fr; }
  .date-list { flex-direction:row; flex-wrap:wrap; max-height:none; }
  .reco-row { grid-template-columns:36px 1fr 60px; }
  .reco-row .reason, .reco-row .sector, .reco-row.head span:nth-child(4), .reco-row.head span:nth-child(5) { display:none; }
}
</style>
