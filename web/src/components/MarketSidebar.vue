<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { api, fmtPct, type Quote, type SyncStatus, type WatchlistResponse } from '../api'

const props = withDefaults(defineProps<{
  market?: string
  groupBy?: string
  metric?: string
  period?: string
  /** 云图专属控件：范围/划分/指标/区间筛选器、证券数量与历史同步面板。 */
  controls?: boolean
  /**
   * 快速定位搜索框。云图页靠筛选器浏览，其余 Tab 靠页面内列表点选，
   * 都不需要它；个股详情页没有别的跳转入口，必须保留。
   */
  showSearch?: boolean
  securityCount?: number
}>(), {
  market: 'all', groupBy: 'industry', metric: 'change_pct', period: '1d',
  controls: true, showSearch: true, securityCount: 0
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
const watchlist = ref<Quote[]>([])
const watchlistStatus = ref<WatchlistResponse['status']>('unavailable')
const watchlistSymbols = ref<string[]>([])
const syncStatus = ref<SyncStatus | null>(null)
const startingBackfill = ref(false)
const retryingFailed = ref(false)
const retryMessage = ref('')
let searchTimer: number | undefined
let searchRequest = 0

async function loadWatchlist() {
  try {
    const result = await api.watchlist()
    watchlistStatus.value = result.status
    watchlistSymbols.value = result.symbols
    watchlist.value = result.status === 'live' || result.status === 'closed' ? result.quotes : []
  } catch {
    watchlistStatus.value = 'unavailable'
    watchlist.value = []
  }
}

const watchlistMessage = computed(() => {
  if (watchlistStatus.value === 'unavailable') return '实时行情暂不可用'
  if (watchlistStatus.value === 'closed') return watchlist.value.length ? '非交易时段，显示最近收盘数据' : '非交易时段，暂无本地收盘快照'
  if (watchlistStatus.value === 'empty') return '在详情页加入自选'
  return watchlist.value.length ? '' : '实时行情暂不可用'
})

function onWatchlistChanged() {
  loadWatchlist()
}
let syncTimer: number | undefined
let watchTimer: number | undefined

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

// 筛选器只在 controls=true 时渲染，因此这里只需向宿主派发变更。
function setOption() {
  emit('change', { market: selectedMarket.value, groupBy: selectedGroup.value, metric: selectedMetric.value, period: selectedPeriod.value })
}

// 非云图页的一行数据状态：正常只报截止日期；同步中报进度；有失败项报警。
// 失败与停滞会让下游全部分析静默失真，必须在任何页面都能看见。
const freshness = computed(() => {
  const backfill = syncStatus.value?.backfill
  if (!backfill) return { text: '同步状态未知', tone: 'warn', tip: '无法读取同步状态' }
  if (syncStatus.value?.backfill_running) {
    return {
      text: `同步中 ${backfill.complete}/${backfill.total}`,
      tone: 'busy',
      tip: `完整 ${backfill.complete} · 待处理 ${backfill.pending} · 失败 ${backfill.failed}`,
    }
  }
  if (backfill.failed > 0) {
    return {
      text: `${backfill.failed} 项同步失败`,
      tone: 'warn',
      tip: '到「市场 › 大盘云图」页可重试失败项',
    }
  }
  return {
    text: backfill.latest_date ? `数据截至 ${backfill.latest_date}` : '暂无数据日期',
    tone: 'ok',
    tip: `完整 ${backfill.complete}/${backfill.total}`,
  }
})

function onSearch() {
  window.clearTimeout(searchTimer)
  const query = keyword.value.trim()
  const request = ++searchRequest
  // 纯数字至少输入三位再查，避免短代码命中大量结果并扰乱输入焦点。
  if (!query || (/^\d+$/.test(query) && query.length < 3)) {
    searchResults.value = []
    return
  }
  searchTimer = window.setTimeout(async () => {
    try {
      const results = await api.search(query)
      // 后端无匹配（如拼音关键词）会返回 null，必须归一化为数组，否则模板渲染报错导致页面错乱。
      if (request === searchRequest) searchResults.value = Array.isArray(results) ? results : []
    } catch {
      if (request === searchRequest) searchResults.value = []
    }
  }, 250)
}

function openStock(symbol: string) {
  keyword.value = ''
  searchResults.value = []
  router.push(`/stock/${symbol}`)
}

async function startBackfill() {
  startingBackfill.value = true
  retryMessage.value = ''
  try {
    await api.startBackfill()
    retryMessage.value = '历史同步已继续'
    await loadSyncStatus()
  } catch (e: any) {
    retryMessage.value = e?.message || '继续同步失败'
  } finally {
    startingBackfill.value = false
  }
}

async function retryFailedBackfill() {
  retryingFailed.value = true
  retryMessage.value = ''
  try {
    const result = await api.retryFailedBackfill()
    retryMessage.value = result.requeued ? `已重排 ${result.requeued} 项` : '没有失败项'
    await loadSyncStatus()
  } catch (e: any) {
    retryMessage.value = e?.message || '重试失败'
  } finally {
    retryingFailed.value = false
  }
}

async function loadSyncStatus() {
  try { syncStatus.value = await api.syncStatus() } catch { /* 保留上次成功状态 */ }
}

onMounted(async () => {
  const [, syncResult] = await Promise.allSettled([loadWatchlist(), api.syncStatus()])
  if (syncResult.status === 'fulfilled') syncStatus.value = syncResult.value
  syncTimer = window.setInterval(loadSyncStatus, 5000)
  watchTimer = window.setInterval(() => {
    if (document.visibilityState === 'visible') loadWatchlist()
  }, 5000)
  window.addEventListener('gostock:watchlist-changed', onWatchlistChanged)
})

onUnmounted(() => {
  window.clearTimeout(searchTimer)
  window.clearInterval(syncTimer)
  window.clearInterval(watchTimer)
  window.removeEventListener('gostock:watchlist-changed', onWatchlistChanged)
})
</script>

<template>
  <aside class="market-sidebar" :class="{ 'nav-only': !controls && !showSearch }">
    <router-link to="/" class="brand">go-stock</router-link>

    <!-- 云图筛选器：只在大盘云图页出现，其余页面这些选项无处作用，属纯噪音。 -->
    <template v-if="controls">
      <label class="side-field"><span>范围</span><select v-model="selectedMarket" @change="setOption"><option v-for="[value, label] in marketOptions" :key="value" :value="value">{{ label }}</option></select></label>
      <label class="side-field"><span>划分</span><select v-model="selectedGroup" @change="setOption"><option v-for="[value, label] in groupOptions" :key="value" :value="value">{{ label }}</option></select></label>
      <label class="side-field"><span>指标</span><select v-model="selectedMetric" @change="setOption"><option v-for="[value, label] in metricOptions" :key="value" :value="value">{{ label }}</option></select></label>
      <label class="side-field"><span>区间</span><select v-model="selectedPeriod" @change="setOption"><option v-for="[value, label] in periodOptions" :key="value" :value="value">{{ label }}</option></select></label>
    </template>

    <div v-if="showSearch" class="side-search"><span>快速定位</span><input v-model="keyword" autocomplete="off" placeholder="输入代码/简称" @input="onSearch" @keydown.enter="searchResults[0] && openStock(searchResults[0].symbol)" />
      <div v-if="searchResults.length" class="search-results"><button v-for="result in searchResults" :key="result.symbol" @click="openStock(result.symbol)"><b>{{ result.name }}</b><span>{{ result.symbol }}</span></button></div>
    </div>
    <div v-if="controls && securityCount" class="side-stats"><span>证券数量</span><strong>{{ securityCount }}</strong></div>

    <!-- 非云图页只留一行数据新鲜度：完整同步面板是运维视图，但「数据截至哪天」
         决定了所有分析结论是否成立，静默掉会让人对着过期数据做决策。 -->
    <div v-if="!controls && syncStatus" class="freshness" :class="freshness.tone" :title="freshness.tip">
      <i /><span>{{ freshness.text }}</span>
    </div>

    <div v-if="controls && syncStatus" class="coverage-status" :class="{ active: syncStatus.backfill_running }">
      <div class="coverage-title"><span>历史数据同步</span><strong>{{ syncStatus.backfill_running ? '进行中' : syncStatus.backfill.pending > 0 ? '已暂停' : '已停止' }}</strong></div>
      <div class="coverage-progress"><i :style="{ width: `${syncProgress}%` }"></i></div>
      <span>完整 {{ syncStatus.backfill.complete }}/{{ syncStatus.backfill.total }} · {{ syncProgress.toFixed(1) }}%</span>
      <small>待处理 {{ syncStatus.backfill.pending }} · 处理中 {{ syncStatus.backfill.running }} · 失败 {{ syncStatus.backfill.failed }}</small>
      <small>部分 {{ syncStatus.backfill.partial }} · 空 {{ syncStatus.backfill.empty }}<template v-if="syncStatus.backfill.latest_date"> · 截至 {{ syncStatus.backfill.latest_date }}</template></small>
      <button v-if="syncStatus.backfill.pending > 0 && !syncStatus.backfill_running" class="retry-failed" :disabled="startingBackfill" @click="startBackfill">{{ startingBackfill ? '启动中' : '继续同步' }}</button>
      <button v-if="syncStatus.backfill.failed > 0 && !syncStatus.backfill_running" class="retry-failed" :disabled="retryingFailed" @click="retryFailedBackfill">{{ retryingFailed ? '重排中' : '重试失败项' }}</button>
      <small v-if="retryMessage" class="retry-message">{{ retryMessage }}</small>
    </div>

    <section class="sidebar-section watch-panel"><header><strong>自选股</strong><small>{{ watchlistSymbols.length }}/10</small></header><button v-for="item in watchlist" :key="item.symbol" @click="openStock(item.symbol)"><span><b>{{ item.name }}</b><small>{{ item.code }}</small></span><em :class="item.change_pct && item.change_pct > 0 ? 'positive' : 'negative'">{{ fmtPct(item.change_pct) }}</em></button><p v-if="watchlistMessage">{{ watchlistMessage }}</p></section>
    <div class="side-help"><strong>数据说明</strong><span>市场视图来自本地 MySQL</span><span>详情与自选股使用实时行情</span><span>交易日 08:10 盘前生成趋势分析</span><span>交易日 17:00 自动生成每日复盘</span><span>推荐仅供研究，不构成投资建议</span></div>
  </aside>
</template>

<style scoped>
.market-sidebar { position:relative; z-index:3; display:flex; min-height:0; flex-direction:column; gap:12px; padding:14px 8px; border-right:1px solid #354157; background:#1c2639; color:#edf1f7; overflow-y:auto; }
.brand { padding:0 7px 7px; border-bottom:1px solid #354157; color:#f0f3f8; font-size:16px; font-weight:720; }
.side-field { display:grid; grid-template-columns:48px 1fr; align-items:center; gap:8px; color:#adb7c7; font-size:13px; }.side-field select,.side-search input { width:100%; min-width:0; height:29px; border:1px solid #3c475c; border-radius:0; outline:none; background:#354055; color:#f1f4f8; font-size:13px; }.side-field select { padding:0 8px; }
.side-search { position:relative; display:grid; gap:8px; margin-top:6px; padding-top:12px; border-top:1px solid #354157; color:#c3cbd7; font-size:13px; }.side-search input { padding:6px 8px; }
.search-results { position:absolute; z-index:20; top:calc(100% + 3px); width:100%; max-height:260px; overflow:auto; border:1px solid #445067; background:#1d2739; box-shadow:0 12px 30px rgba(0,0,0,.4); }.search-results button { display:flex; width:100%; justify-content:space-between; gap:8px; padding:8px; border:0; border-bottom:1px solid #344056; border-radius:0; background:transparent; color:#edf2f9; text-align:left; }.search-results span { color:#9ba8bd; font-size:11px; }
.side-stats { display:flex; align-items:center; justify-content:space-between; padding:8px 7px; border-top:1px solid #354157; border-bottom:1px solid #354157; color:#9da9bb; font-size:12px; }.side-stats strong { color:#e7ecf4; font-size:13px; }.coverage-status { display:grid; gap:4px; padding:7px; border-left:2px solid #68758a; background:#252f41; color:#d9e0e9; font-size:10px; }.coverage-status.active { border-left-color:#d6a12c; background:#2b2c32; }.coverage-title { display:flex; align-items:center; justify-content:space-between; font-size:11px; }.coverage-title strong { color:#aeb9ca; font-size:10px; }.coverage-status.active .coverage-title strong { color:#e9c16c; }.coverage-progress { height:4px; overflow:hidden; background:#111a28; }.coverage-progress i { display:block; height:100%; background:#4fbc91; transition:width .25s; }.coverage-status small { color:#9ba8bd; font-size:9px; }.retry-failed { margin-top:3px; padding:4px 7px; border:1px solid #8c5e34; background:#3a2e25; color:#e9c16c; font-size:10px; cursor:pointer; }.retry-failed:disabled { opacity:.6; cursor:wait; }.retry-message { color:#e9c16c!important; }
/* 非云图页的一行数据新鲜度 */
.freshness { display:flex; align-items:center; gap:7px; padding:7px; border-left:2px solid #68758a; background:#252f41; color:#c3cbd7; font-size:11px; }
.freshness i { width:6px; height:6px; flex:0 0 auto; border-radius:50%; background:#68758a; }
.freshness.ok { border-left-color:#4fbc91; }.freshness.ok i { background:#4fbc91; }
.freshness.busy { border-left-color:#d6a12c; }.freshness.busy i { background:#e9c16c; }
.freshness.warn { border-left-color:#c96a72; color:#e9a7ac; }.freshness.warn i { background:#ef6a72; }
.sidebar-section { display:grid; gap:2px; }.sidebar-section header { display:flex; align-items:center; justify-content:space-between; padding:2px 7px 5px; color:#e2e7ef; font-size:12px; }.sidebar-section header small { color:#8390a4; font-size:10px; }.sidebar-section button { display:flex; min-width:0; align-items:center; justify-content:space-between; gap:5px; padding:5px 7px; border:0; border-bottom:1px solid #303b50; border-radius:0; background:#222d41; color:#ecf0f6; text-align:left; }.sidebar-section button:hover { background:#2b374c; }.sidebar-section button span { min-width:0; }.sidebar-section button b,.sidebar-section button small { display:block; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }.sidebar-section button b { font-size:11px; }.sidebar-section button small { margin-top:1px; color:#8996aa; font-size:9px; }.sidebar-section button em { flex:0 0 auto; color:#e9c16c; font-style:normal; font-size:11px; font-weight:700; }.sidebar-section button em.positive { color:#ef6a72; }.sidebar-section button em.negative { color:#28bd8b; }.sidebar-section p { padding:6px 7px; color:#738197; font-size:10px; }.side-help { display:grid; gap:7px; margin-top:auto; padding:10px 7px 4px; border-top:1px solid #354157; color:#9ba8bd; font-size:11px; line-height:1.4; }.side-help strong { color:#e2e7ef; font-size:12px; }
@media (max-width:900px) { .market-sidebar { display:grid; min-height:auto; grid-template-columns:repeat(2,minmax(0,1fr)); gap:9px; border-right:0; border-bottom:1px solid #354157; overflow:visible; }.brand,.side-help,.side-stats,.sidebar-section { display:none; }.coverage-status { grid-column:1/-1; }.side-search { margin:0; padding:0; border:0; }.freshness { grid-column:1/-1; }
  /* 移动端非云图页：品牌/自选股/说明本就隐藏，此时只剩数据新鲜度一行，
     收窄内边距让它退化成一条细状态条，而不是占位的空白区。 */
  .market-sidebar.nav-only { padding:6px 8px; gap:0; } }
@media (max-width:600px) { .market-sidebar { grid-template-columns:1fr 1fr; }.side-search { grid-column:1/-1; } }
</style>
