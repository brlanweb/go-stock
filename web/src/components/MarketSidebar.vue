<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { api, fmtPct, type Quote, type Recommendation, type SyncStatus } from '../api'

const props = withDefaults(defineProps<{
  market?: string
  groupBy?: string
  metric?: string
  period?: string
  controls?: boolean
  securityCount?: number
}>(), {
  market: 'all', groupBy: 'industry', metric: 'change_pct', period: '1d', controls: true, securityCount: 0
})

const emit = defineEmits<{
  change: [options: { market: string; groupBy: string; metric: string; period: string }]
}>()

const router = useRouter()
const selectedMarket = ref(props.market)
const selectedGroup = ref(props.groupBy)
const selectedMetric = ref(props.metric)
const selectedPeriod = ref(props.period)
const keyword = ref('')
const searchResults = ref<any[]>([])
const recommendations = ref<Recommendation[]>([])
const recommendationDates = ref<string[]>([])
const recommendationDate = ref('')
const recommendationRunning = ref(false)
const recommendationMessage = ref('')
const watchlist = ref<Quote[]>([])
const syncStatus = ref<SyncStatus | null>(null)
let searchTimer: number | undefined
let syncTimer: number | undefined

const syncProgress = computed(() => {
  const backfill = syncStatus.value?.backfill
  if (!backfill?.total) return 0
  return Math.min(100, Math.max(0, backfill.complete / backfill.total * 100))
})

const marketOptions = [['all', 'A股'], ['gem', '创业板'], ['star', '科创板'], ['bse', '北交所']]
const groupOptions = [['industry', '行业'], ['concept', '概念']]
const metricOptions = [['change_pct', '涨跌幅'], ['pe_ttm', '市盈率 TTM'], ['main_net_inflow', '主力资金']]
const periodOptions = [['1d', '今日'], ['3d', '三日'], ['5d', '五日']]

watch(() => [props.market, props.groupBy, props.metric, props.period], ([market, groupBy, metric, period]) => {
  selectedMarket.value = market
  selectedGroup.value = groupBy
  selectedMetric.value = metric
  selectedPeriod.value = period
})

function setOption() {
  const options = { market: selectedMarket.value, groupBy: selectedGroup.value, metric: selectedMetric.value, period: selectedPeriod.value }
  if (props.controls) emit('change', options)
  else router.push({ path: '/', query: options })
}

function onSearch() {
  window.clearTimeout(searchTimer)
  if (!keyword.value.trim()) { searchResults.value = []; return }
  searchTimer = window.setTimeout(async () => {
    try { searchResults.value = await api.search(keyword.value.trim()) } catch { searchResults.value = [] }
  }, 250)
}

function openStock(symbol: string) {
  keyword.value = ''
  searchResults.value = []
  router.push(`/stock/${symbol}`)
}

async function loadRecommendations(date = '') {
  try { recommendations.value = await api.recommendations(date) } catch { recommendations.value = [] }
}

async function selectRecommendationDate() {
  await loadRecommendations(recommendationDate.value)
}

async function runRecommendations() {
  recommendationRunning.value = true
  recommendationMessage.value = ''
  try {
    await api.runRecommendations()
    recommendationMessage.value = '分析已启动，请稍后刷新'
    window.setTimeout(async () => {
      const dates = await api.recommendationHistory().catch(() => [] as string[])
      recommendationDates.value = dates
      recommendationDate.value = dates[0] || ''
      await loadRecommendations(recommendationDate.value)
      recommendationRunning.value = false
    }, 8000)
  } catch (e: any) {
    recommendationMessage.value = e?.message || '分析启动失败'
    recommendationRunning.value = false
  }
}

async function loadSyncStatus() {
  try { syncStatus.value = await api.syncStatus() } catch { /* 保留上次成功状态 */ }
}

onMounted(async () => {
  const [datesResult, watchResult, syncResult] = await Promise.allSettled([
    api.recommendationHistory(), api.watchlist(), api.syncStatus()
  ])
  if (datesResult.status === 'fulfilled') {
    recommendationDates.value = datesResult.value
    recommendationDate.value = datesResult.value[0] || ''
  }
  await loadRecommendations(recommendationDate.value)
  if (watchResult.status === 'fulfilled') watchlist.value = watchResult.value.filter((item): item is Quote => typeof item !== 'string')
  if (syncResult.status === 'fulfilled') syncStatus.value = syncResult.value
  syncTimer = window.setInterval(loadSyncStatus, 5000)
})

onUnmounted(() => {
  window.clearTimeout(searchTimer)
  window.clearInterval(syncTimer)
})
</script>

<template>
  <aside class="market-sidebar">
    <router-link to="/" class="brand">go-stock</router-link>
    <label class="side-field"><span>范围</span><select v-model="selectedMarket" @change="setOption"><option v-for="[value, label] in marketOptions" :key="value" :value="value">{{ label }}</option></select></label>
    <label class="side-field"><span>划分</span><select v-model="selectedGroup" @change="setOption"><option v-for="[value, label] in groupOptions" :key="value" :value="value">{{ label }}</option></select></label>
    <label class="side-field"><span>指标</span><select v-model="selectedMetric" @change="setOption"><option v-for="[value, label] in metricOptions" :key="value" :value="value">{{ label }}</option></select></label>
    <label class="side-field"><span>区间</span><select v-model="selectedPeriod" @change="setOption"><option v-for="[value, label] in periodOptions" :key="value" :value="value">{{ label }}</option></select></label>

    <div class="side-search"><span>快速定位</span><input v-model="keyword" autocomplete="off" placeholder="输入代码/简称" @input="onSearch" @keydown.enter="searchResults[0] && openStock(searchResults[0].symbol)" />
      <div v-if="searchResults.length" class="search-results"><button v-for="result in searchResults" :key="result.symbol" @click="openStock(result.symbol)"><b>{{ result.name }}</b><span>{{ result.symbol }}</span></button></div>
    </div>
    <div v-if="securityCount" class="side-stats"><span>证券数量</span><strong>{{ securityCount }}</strong></div>
    <div v-if="syncStatus" class="coverage-status" :class="{ active: syncStatus.backfill_running }">
      <div class="coverage-title"><span>历史数据同步</span><strong>{{ syncStatus.backfill_running ? '进行中' : '已停止' }}</strong></div>
      <div class="coverage-progress"><i :style="{ width: `${syncProgress}%` }"></i></div>
      <span>完整 {{ syncStatus.backfill.complete }}/{{ syncStatus.backfill.total }} · {{ syncProgress.toFixed(1) }}%</span>
      <small>待处理 {{ syncStatus.backfill.pending }} · 处理中 {{ syncStatus.backfill.running }} · 失败 {{ syncStatus.backfill.failed }}</small>
      <small>部分 {{ syncStatus.backfill.partial }} · 空 {{ syncStatus.backfill.empty }}<template v-if="syncStatus.backfill.latest_date"> · 截至 {{ syncStatus.backfill.latest_date }}</template></small>
    </div>

    <section class="sidebar-section recommendation-panel">
      <header><strong>AI 趋势推荐</strong><div class="recommendation-tools"><select v-if="recommendationDates.length" v-model="recommendationDate" title="查看历史推荐" @change="selectRecommendationDate"><option v-for="date in recommendationDates" :key="date" :value="date" v-text="date"></option></select><button class="run-analysis" :disabled="recommendationRunning" title="手动生成今日趋势推荐" @click="runRecommendations">{{ recommendationRunning ? '分析中' : '生成' }}</button></div></header>
      <small v-if="recommendationMessage" class="recommendation-message">{{ recommendationMessage }}</small>
      <button v-for="item in recommendations" :key="item.symbol" :title="item.reason" @click="openStock(item.symbol)"><span><b>{{ item.name }}</b><small>{{ item.sector }}</small></span><em>{{ item.probability.toFixed(0) }}%</em></button>
      <p v-if="!recommendations.length">等待每日 05:00 分析结果</p>
    </section>
    <section class="sidebar-section watch-panel"><header><strong>自选股</strong><small>{{ watchlist.length }}/10</small></header><button v-for="item in watchlist" :key="item.symbol" @click="openStock(item.symbol)"><span><b>{{ item.name }}</b><small>{{ item.code }}</small></span><em :class="item.change_pct && item.change_pct > 0 ? 'positive' : 'negative'">{{ fmtPct(item.change_pct) }}</em></button><p v-if="!watchlist.length">在详情页加入自选</p></section>
    <div class="side-help"><strong>数据说明</strong><span>页面查询来自本地 MySQL</span><span>每日 05:00 生成趋势分析</span><span>推荐仅供研究，不构成投资建议</span></div>
  </aside>
</template>

<style scoped>
.market-sidebar { position:relative; z-index:3; display:flex; min-height:0; flex-direction:column; gap:12px; padding:14px 8px; border-right:1px solid #354157; background:#1c2639; color:#edf1f7; overflow-y:auto; }
.brand { padding:0 7px 7px; border-bottom:1px solid #354157; color:#f0f3f8; font-size:16px; font-weight:720; }
.side-field { display:grid; grid-template-columns:48px 1fr; align-items:center; gap:8px; color:#adb7c7; font-size:13px; }.side-field select,.side-search input { width:100%; min-width:0; height:29px; border:1px solid #3c475c; border-radius:0; outline:none; background:#354055; color:#f1f4f8; font-size:13px; }.side-field select { padding:0 8px; }
.side-search { position:relative; display:grid; gap:8px; margin-top:6px; padding-top:12px; border-top:1px solid #354157; color:#c3cbd7; font-size:13px; }.side-search input { padding:6px 8px; }
.search-results { position:absolute; z-index:20; top:calc(100% + 3px); width:100%; max-height:260px; overflow:auto; border:1px solid #445067; background:#1d2739; box-shadow:0 12px 30px rgba(0,0,0,.4); }.search-results button { display:flex; width:100%; justify-content:space-between; gap:8px; padding:8px; border:0; border-bottom:1px solid #344056; border-radius:0; background:transparent; color:#edf2f9; text-align:left; }.search-results span { color:#9ba8bd; font-size:11px; }
.side-stats { display:flex; align-items:center; justify-content:space-between; padding:8px 7px; border-top:1px solid #354157; border-bottom:1px solid #354157; color:#9da9bb; font-size:12px; }.side-stats strong { color:#e7ecf4; font-size:13px; }.coverage-status { display:grid; gap:4px; padding:7px; border-left:2px solid #68758a; background:#252f41; color:#d9e0e9; font-size:10px; }.coverage-status.active { border-left-color:#d6a12c; background:#2b2c32; }.coverage-title { display:flex; align-items:center; justify-content:space-between; font-size:11px; }.coverage-title strong { color:#aeb9ca; font-size:10px; }.coverage-status.active .coverage-title strong { color:#e9c16c; }.coverage-progress { height:4px; overflow:hidden; background:#111a28; }.coverage-progress i { display:block; height:100%; background:#4fbc91; transition:width .25s; }.coverage-status small { color:#9ba8bd; font-size:9px; }
.sidebar-section { display:grid; gap:2px; }.sidebar-section header { display:flex; align-items:center; justify-content:space-between; padding:2px 7px 5px; color:#e2e7ef; font-size:12px; }.recommendation-tools { display:flex; align-items:center; gap:4px; }.sidebar-section header select { max-width:84px; border:0; background:#2b364a; color:#aeb9ca; font-size:10px; }.run-analysis { padding:3px 6px!important; border:1px solid #4a5870!important; background:#2b364a!important; color:#e8edf5!important; font-size:10px!important; }.run-analysis:disabled { cursor:wait; opacity:.6; }.recommendation-message { padding:2px 7px 5px; color:#d8b967; font-size:10px; }.sidebar-section header small { color:#8390a4; font-size:10px; }.sidebar-section button { display:flex; min-width:0; align-items:center; justify-content:space-between; gap:5px; padding:5px 7px; border:0; border-bottom:1px solid #303b50; border-radius:0; background:#222d41; color:#ecf0f6; text-align:left; }.sidebar-section button:hover { background:#2b374c; }.sidebar-section button span { min-width:0; }.sidebar-section button b,.sidebar-section button small { display:block; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }.sidebar-section button b { font-size:11px; }.sidebar-section button small { margin-top:1px; color:#8996aa; font-size:9px; }.sidebar-section button em { flex:0 0 auto; color:#e9c16c; font-style:normal; font-size:11px; font-weight:700; }.sidebar-section button em.positive { color:#ef6a72; }.sidebar-section button em.negative { color:#28bd8b; }.sidebar-section p { padding:6px 7px; color:#738197; font-size:10px; }.side-help { display:grid; gap:7px; margin-top:auto; padding:10px 7px 4px; border-top:1px solid #354157; color:#9ba8bd; font-size:11px; line-height:1.4; }.side-help strong { color:#e2e7ef; font-size:12px; }
@media (max-width:900px) { .market-sidebar { display:grid; min-height:auto; grid-template-columns:repeat(2,minmax(0,1fr)); gap:9px; border-right:0; border-bottom:1px solid #354157; overflow:visible; }.brand,.side-help,.side-stats,.sidebar-section { display:none; }.coverage-status { grid-column:1/-1; }.side-search { margin:0; padding:0; border:0; } }
@media (max-width:600px) { .market-sidebar { grid-template-columns:1fr 1fr; }.side-search { grid-column:1/-1; } }
</style>
