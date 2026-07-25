<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { api, fmt, fmtBig, fmtPct, pctClass, type HeatmapGroup, type IndexQuote } from '../api'

const router = useRouter()
const indices = ref<IndexQuote[]>([])
const groups = ref<HeatmapGroup[]>([])
const notice = ref('')
const error = ref('')
const indexError = ref('')
const market = ref('all')
const groupBy = ref('industry')
const metric = ref('change_pct')
const period = ref('1d')
const keyword = ref('')
const searchResults = ref<any[]>([])
const expanded = ref(false)
let timer: number | undefined
let searchTimer: number | undefined

const marketOptions = [['all', 'A股'], ['gem', '创业板'], ['star', '科创板'], ['bse', '北交所']]
const groupOptions = [['industry', '行业'], ['concept', '概念']]
const metricOptions = [['change_pct', '涨跌幅'], ['pe_ttm', '市盈率 TTM'], ['main_net_inflow', '主力资金']]
const periodOptions = [['1d', '今日'], ['3d', '三日'], ['5d', '五日']]

const itemCount = computed(() => groups.value.reduce((sum, group) => sum + group.items.length, 0))
const displayedGroups = computed(() => {
  const maxGroups = expanded.value ? Number.MAX_SAFE_INTEGER : 16
  const maxItemsPerGroup = expanded.value ? Number.MAX_SAFE_INTEGER : 18
  return groups.value
    .map(group => ({
      ...group,
      totalMarketValue: group.items.reduce((sum, item) => sum + (Number(item.total_mv) || 0), 0),
      items: [...group.items]
        .sort((a, b) => (Number(b.total_mv) || 0) - (Number(a.total_mv) || 0))
        .slice(0, maxItemsPerGroup)
    }))
    .sort((a, b) => b.totalMarketValue - a.totalMarketValue)
    .slice(0, maxGroups)
})
const hiddenGroupCount = computed(() => Math.max(0, groups.value.length - displayedGroups.value.length))
const hiddenItemCount = computed(() => Math.max(0, itemCount.value - displayedGroups.value.reduce((sum, group) => sum + group.items.length, 0)))
const marketValueBounds = computed(() => {
  const values = displayedGroups.value
    .flatMap(group => group.items)
    .map(item => Number(item.total_mv) || 0)
    .filter(value => value > 0)
  if (!values.length) return { min: 1, max: 1 }
  return { min: Math.min(...values), max: Math.max(...values) }
})

function pollInterval() {
  const now = new Date()
  const hm = now.getHours() * 100 + now.getMinutes()
  return now.getDay() > 0 && now.getDay() < 6 && ((hm >= 915 && hm <= 1135) || (hm >= 1300 && hm <= 1505)) ? 10000 : 60000
}

async function refresh() {
  const [indicesResult, heatmapResult] = await Promise.allSettled([
    api.indices(),
    api.heatmap(market.value, groupBy.value, metric.value, period.value)
  ])

  if (indicesResult.status === 'fulfilled') {
    indices.value = indicesResult.value
    indexError.value = ''
  } else {
    indexError.value = '大盘指数暂时不可用'
  }

  if (heatmapResult.status === 'fulfilled') {
    const heatmap = heatmapResult.value
    groups.value = heatmap.groups
    notice.value = heatmap.notice
    error.value = ''
  } else {
    error.value = heatmapResult.reason?.message || '市场云图加载失败'
  }
  window.clearTimeout(timer)
  timer = window.setTimeout(refresh, pollInterval())
}

function setOption(target: 'market' | 'groupBy' | 'metric' | 'period', value: string) {
  if (target === 'market') market.value = value
  if (target === 'groupBy') groupBy.value = value
  if (target === 'metric') metric.value = value
  if (target === 'period') period.value = value
  expanded.value = false
  refresh()
}

function tileValue(item: any) {
  if (metric.value === 'pe_ttm') return item.pe_ratio > 0 ? `${item.pe_ratio.toFixed(1)}x` : '-'
  if (metric.value === 'main_net_inflow') return item.main_net_inflow == null ? '-' : fmtBig(item.main_net_inflow)
  return fmtPct(item.period_change)
}

function tileColor(value: number) {
  if (!Number.isFinite(value) || value === 0) return '#4a5563'
  const strength = Math.min(0.86, 0.34 + Math.abs(value) / 11)
  return value > 0 ? `rgba(214, 60, 61, ${strength})` : `rgba(30, 146, 100, ${strength})`
}

function tileStyle(item: any) {
  const value = metric.value === 'pe_ttm' ? item.pe_ratio - 25 : item.period_change
  const marketValue = Number(item.total_mv) || 0
  const { min, max } = marketValueBounds.value
  // 市值跨度从数千万到数万亿，使用对数映射保留头部股差异且避免小盘股不可点击。
  const ratio = max > min && marketValue > 0
    ? (Math.log(marketValue) - Math.log(min)) / (Math.log(max) - Math.log(min))
    : 0
  const span = Math.round(1 + ratio * 7)
  return {
    background: tileColor(value),
    gridColumn: `span ${span}`,
    gridRow: `span ${Math.max(1, Math.round(1 + ratio * 3))}`
  }
}

function onSearch() {
  window.clearTimeout(searchTimer)
  if (!keyword.value.trim()) {
    searchResults.value = []
    return
  }
  searchTimer = window.setTimeout(async () => {
    try { searchResults.value = await api.search(keyword.value.trim()) } catch { searchResults.value = [] }
  }, 250)
}

function openStock(symbol: string) {
  keyword.value = ''
  searchResults.value = []
  router.push(`/stock/${symbol}`)
}

onMounted(refresh)
onUnmounted(() => { window.clearTimeout(timer); window.clearTimeout(searchTimer) })
</script>

<template>
  <main class="market-terminal">
    <section class="index-strip" aria-label="大盘指数">
      <button v-for="idx in indices" :key="idx.symbol" class="index-quote" @click="router.push(`/stock/${idx.symbol}`)">
        <span>{{ idx.name }}</span>
        <strong :class="pctClass(idx.change_pct)">{{ fmt(idx.price) }}</strong>
        <em :class="pctClass(idx.change_pct)">{{ fmtPct(idx.change_pct) }}</em>
        <small>成交 {{ fmtBig(idx.amount) }}</small>
      </button>
      <span v-if="indexError" class="index-unavailable">{{ indexError }}</span>
    </section>

    <section class="market-toolbar">
      <div class="terminal-title"><span class="status-dot"></span><h1>市场云图</h1><span>{{ itemCount }} 只证券</span></div>
      <div class="search-box">
        <input v-model="keyword" autocomplete="off" placeholder="搜索代码或名称" @input="onSearch" @keydown.enter="searchResults[0] && openStock(searchResults[0].symbol)" />
        <div v-if="searchResults.length" class="search-results">
          <button v-for="result in searchResults" :key="result.symbol" @click="openStock(result.symbol)">
            <b>{{ result.name }}</b><span>{{ result.symbol }} · {{ result.industry || result.type }}</span>
          </button>
        </div>
      </div>
    </section>

    <section class="filters" aria-label="云图筛选">
      <div class="filter-set"><span>市场</span><div class="segment"><button v-for="[value, label] in marketOptions" :key="value" :class="{ active: market === value }" @click="setOption('market', value)">{{ label }}</button></div></div>
      <div class="filter-set"><span>分组</span><div class="segment"><button v-for="[value, label] in groupOptions" :key="value" :class="{ active: groupBy === value }" @click="setOption('groupBy', value)">{{ label }}</button></div></div>
      <div class="filter-set"><span>指标</span><div class="segment"><button v-for="[value, label] in metricOptions" :key="value" :class="{ active: metric === value }" @click="setOption('metric', value)">{{ label }}</button></div></div>
      <div class="filter-set"><span>区间</span><div class="segment"><button v-for="[value, label] in periodOptions" :key="value" :class="{ active: period === value }" @click="setOption('period', value)">{{ label }}</button></div></div>
    </section>

    <section v-if="notice || error" class="market-notice" :class="{ error }">{{ error || notice }}</section>

    <section v-if="displayedGroups.length" class="heatmap" :class="{ expanded }" aria-label="行业市场云图">
      <article v-for="group in displayedGroups" :key="group.name" class="heatmap-group">
        <header><strong>{{ group.name }}</strong><span :class="pctClass(group.change_pct)">{{ fmtPct(group.change_pct) }}</span><small>{{ group.items.length }} 只</small></header>
        <div class="heatmap-tiles">
          <button v-for="item in group.items" :key="item.symbol" class="heatmap-tile" :style="tileStyle(item)" @click="router.push(`/stock/${item.symbol}`)">
            <b>{{ item.name }}</b><span>{{ item.code }}</span><em>{{ tileValue(item) }}</em>
            <small>{{ fmtBig(item.total_mv) }}</small>
          </button>
        </div>
      </article>
    </section>
    <div v-if="groups.length" class="heatmap-actions">
      <span v-if="!expanded && (hiddenGroupCount || hiddenItemCount)">首屏展示 {{ displayedGroups.length }} 个主要板块，其余 {{ hiddenGroupCount }} 个板块和 {{ hiddenItemCount }} 只证券已收起</span>
      <button class="ghost" @click="expanded = !expanded">{{ expanded ? '收起至首屏' : '展开全部板块' }}</button>
    </div>
    <section v-else-if="!notice && !error" class="empty-market">正在载入市场数据</section>
  </main>
</template>

<style scoped>
.market-terminal { min-width: 0; }
.index-strip { display:grid; grid-template-columns:repeat(6,minmax(0,1fr)); border:1px solid var(--border); background:#111820; margin-bottom:12px; }
.index-quote { min-width:0; padding:11px 13px; border:0; border-right:1px solid var(--border); border-radius:0; background:transparent; color:var(--text); text-align:left; font-variant-numeric:tabular-nums; }
.index-quote:last-child { border-right:0; }
.index-unavailable { grid-column:1 / -1; padding:10px 13px; color:var(--text-dim); font-size:12px; }
.index-quote span,.index-quote small { display:block; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; color:var(--text-dim); font-size:11px; }
.index-quote strong { display:inline-block; margin:5px 8px 4px 0; font-size:17px; }.index-quote em { font-style:normal; font-size:12px; }
.market-toolbar { display:flex; align-items:center; justify-content:space-between; gap:16px; padding:10px 0; }
.terminal-title { display:flex; align-items:center; gap:9px; }.terminal-title h1 { font-size:18px; font-weight:650; }.terminal-title span:last-child { color:var(--text-dim); font-size:12px; }
.status-dot { width:8px; height:8px; border-radius:50%; background:#e15a43; box-shadow:0 0 0 3px rgba(225,90,67,.14); }
.search-box { position:relative; }.search-box input { width:260px; }.search-results { position:absolute; z-index:4; right:0; top:calc(100% + 5px); width:340px; border:1px solid var(--border); background:#111820; box-shadow:0 12px 30px rgba(0,0,0,.32); }
.search-results button { display:flex; width:100%; justify-content:space-between; gap:12px; padding:10px 12px; border:0; border-bottom:1px solid var(--border); border-radius:0; background:transparent; color:var(--text); text-align:left; }.search-results span { color:var(--text-dim); font-size:12px; }
.filters { display:flex; flex-wrap:wrap; gap:16px 24px; padding:11px 0 14px; border-top:1px solid var(--border); border-bottom:1px solid var(--border); }
.filter-set { display:flex; align-items:center; gap:8px; }.filter-set>span { color:var(--text-dim); font-size:12px; }.segment { display:flex; background:#111820; border:1px solid var(--border); }.segment button { border:0; border-right:1px solid var(--border); border-radius:0; background:transparent; color:var(--text-dim); padding:5px 9px; font-size:12px; }.segment button:last-child { border-right:0; }.segment button.active { background:#263849; color:#eaf5ff; }
.market-notice { margin:14px 0; padding:10px 12px; border-left:3px solid #d59b33; background:#211d15; color:#dfc38a; font-size:13px; }.market-notice.error { border-color:var(--up); background:#271819; color:#f0abab; }
.heatmap { display:grid; grid-template-columns:repeat(4,minmax(0,1fr)); grid-template-rows:repeat(4,minmax(0,1fr)); gap:8px; height:calc(100vh - 224px); min-height:500px; margin-top:10px; overflow:hidden; }.heatmap.expanded { height:auto; min-height:0; max-height:none; overflow:visible; grid-template-rows:none; }.heatmap-group { min-width:0; min-height:0; overflow:hidden; border:1px solid var(--border); background:#111820; }.heatmap.expanded .heatmap-group { min-height:210px; }.heatmap-group header { display:flex; align-items:center; gap:8px; height:30px; padding:7px 9px; border-bottom:1px solid var(--border); font-size:12px; }.heatmap-group header span { font-variant-numeric:tabular-nums; }.heatmap-group header small { margin-left:auto; color:var(--text-dim); }
.heatmap-tiles { display:grid; height:calc(100% - 30px); grid-template-columns:repeat(8,minmax(0,1fr)); grid-template-rows:repeat(6,minmax(0,1fr)); grid-auto-flow:dense; padding:3px; gap:3px; overflow:hidden; }.heatmap-tile { min-width:0; min-height:0; height:100%; padding:6px; border:0; border-radius:0; color:#fff; text-align:left; text-shadow:0 1px 1px rgba(0,0,0,.3); overflow:hidden; }.heatmap-tile b,.heatmap-tile span,.heatmap-tile em,.heatmap-tile small { display:block; overflow:hidden; white-space:nowrap; text-overflow:ellipsis; }.heatmap-tile b { font-size:13px; }.heatmap-tile span { margin-top:2px; opacity:.78; font-size:10px; }.heatmap-tile em { margin-top:6px; font-style:normal; font-size:14px; font-weight:700; }.heatmap-tile small { margin-top:1px; opacity:.8; font-size:10px; }
.heatmap-actions { display:flex; align-items:center; justify-content:space-between; gap:12px; min-height:32px; padding:8px 0; color:var(--text-dim); font-size:12px; }.heatmap-actions button { flex:0 0 auto; }
.empty-market { padding:52px; color:var(--text-dim); text-align:center; }
@media (max-width:1100px) { .heatmap { grid-template-columns:repeat(3,minmax(0,1fr)); grid-template-rows:repeat(4,minmax(0,1fr)); height:auto; min-height:0; overflow:visible; }.heatmap-group { min-height:210px; }.heatmap-tiles { height:180px; } }
@media (max-width:900px) { .index-strip { grid-template-columns:repeat(3,minmax(0,1fr)); }.index-quote:nth-child(3) { border-right:0; }.index-quote:nth-child(-n+3) { border-bottom:1px solid var(--border); }.heatmap { grid-template-columns:repeat(2,minmax(0,1fr)); grid-template-rows:none; } }
@media (max-width:600px) { .index-strip { grid-template-columns:repeat(2,minmax(0,1fr)); }.index-quote { border-bottom:1px solid var(--border); }.index-quote:nth-child(even) { border-right:0; }.market-toolbar { align-items:flex-start; flex-direction:column; }.search-box,.search-box input { width:100%; }.search-results { left:0; width:100%; }.filters { gap:10px; }.filter-set { width:100%; justify-content:space-between; }.heatmap { grid-template-columns:1fr; }.heatmap-group { min-height:190px; }.heatmap-tiles { grid-template-columns:repeat(8,minmax(0,1fr)); grid-template-rows:repeat(6,minmax(0,1fr)); height:160px; }.heatmap-tile { min-height:0; }.heatmap-actions { align-items:flex-start; flex-direction:column; } }
</style>
