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
const heatLegend = [-4, -3, -2, -1, 0, 1, 2, 3, 4]

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
  if (!Number.isFinite(value) || value === 0) return '#3e4857'
  const strength = Math.min(0.9, 0.38 + Math.abs(value) / 9)
  return value > 0 ? `rgba(208, 58, 67, ${strength})` : `rgba(0, 159, 109, ${strength})`
}

function legendColor(value: number) {
  return tileColor(value)
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
  <main class="heatmap-workspace">
    <aside class="heatmap-sidebar">
      <router-link to="/" class="brand">go-stock</router-link>
      <h1>云图设置</h1>
      <label class="side-field"><span>范围</span><select v-model="market" @change="setOption('market', market)"><option v-for="[value, label] in marketOptions" :key="value" :value="value">{{ label }}</option></select></label>
      <label class="side-field"><span>划分维度</span><select v-model="groupBy" @change="setOption('groupBy', groupBy)"><option v-for="[value, label] in groupOptions" :key="value" :value="value">{{ label }}</option></select></label>
      <label class="side-field"><span>数据指标</span><select v-model="metric" @change="setOption('metric', metric)"><option v-for="[value, label] in metricOptions" :key="value" :value="value">{{ label }}</option></select></label>
      <label class="side-field"><span>时间范围</span><select v-model="period" @change="setOption('period', period)"><option v-for="[value, label] in periodOptions" :key="value" :value="value">{{ label }}</option></select></label>
      <div class="side-search">
        <span>快速定位</span>
        <input v-model="keyword" autocomplete="off" placeholder="输入代码/简称" @input="onSearch" @keydown.enter="searchResults[0] && openStock(searchResults[0].symbol)" />
        <div v-if="searchResults.length" class="search-results">
          <button v-for="result in searchResults" :key="result.symbol" @click="openStock(result.symbol)"><b>{{ result.name }}</b><span>{{ result.symbol }}</span></button>
        </div>
      </div>
      <div class="side-help"><strong>数据说明</strong><span>面积代表流通市值，红绿深浅代表指标强弱。</span><span>点击证券可查看本地日K详情。</span></div>
    </aside>

    <section class="heatmap-canvas">
      <header class="canvas-header">
        <div class="canvas-title"><span class="live-dot"></span><strong>大盘云图</strong><small>{{ itemCount }} 只证券</small></div>
        <div class="legend"><span>注：面积代表流通市值大小，红绿色深浅代表涨跌幅大小</span><i v-for="value in heatLegend" :key="value" :style="{ background: legendColor(value) }">{{ value > 0 ? `+${value}%` : `${value}%` }}</i></div>
      </header>

      <section class="index-strip" aria-label="大盘指数">
        <button v-for="idx in indices" :key="idx.symbol" class="index-quote" @click="router.push(`/stock/${idx.symbol}`)"><span>{{ idx.name }}</span><strong :class="pctClass(idx.change_pct)">{{ fmt(idx.price) }}</strong><em :class="pctClass(idx.change_pct)">{{ fmtPct(idx.change_pct) }}</em></button>
        <span v-if="indexError" class="index-unavailable">{{ indexError }}</span>
      </section>

      <section v-if="notice || error" class="market-notice" :class="{ error }">{{ error || notice }}</section>
      <section v-if="displayedGroups.length" class="heatmap" :class="{ expanded }" aria-label="行业市场云图">
        <article v-for="group in displayedGroups" :key="group.name" class="heatmap-group">
          <header><strong>{{ group.name }}</strong><span :class="pctClass(group.change_pct)">{{ fmtPct(group.change_pct) }}</span><small>{{ group.items.length }} 只</small></header>
          <div class="heatmap-tiles">
            <button v-for="item in group.items" :key="item.symbol" class="heatmap-tile" :style="tileStyle(item)" @click="router.push(`/stock/${item.symbol}`)"><b>{{ item.name }}</b><span>{{ item.code }}</span><em>{{ tileValue(item) }}</em><small>{{ fmtBig(item.total_mv) }}</small></button>
          </div>
        </article>
      </section>
      <div v-if="groups.length" class="heatmap-actions"><span v-if="!expanded && (hiddenGroupCount || hiddenItemCount)">首屏展示 {{ displayedGroups.length }} 个主要板块，其余 {{ hiddenGroupCount }} 个板块和 {{ hiddenItemCount }} 只证券已收起</span><button class="ghost" @click="expanded = !expanded">{{ expanded ? '收起至首屏' : '展开全部板块' }}</button></div>
      <section v-else-if="!notice && !error" class="empty-market">正在载入市场数据</section>
    </section>
  </main>
</template>

<style scoped>
.heatmap-workspace { display:grid; grid-template-columns:210px minmax(0,1fr); width:100vw; min-height:calc(100vh - 38px); margin-left:calc(50% - 50vw); background:#151f31; color:#e9edf4; }
.heatmap-sidebar { position:relative; display:flex; flex-direction:column; gap:14px; padding:14px 8px; border-right:1px solid #344056; background:#1c2639; }.brand { padding:0 7px; color:#e9edf4; font-size:16px; font-weight:720; }.heatmap-sidebar h1 { padding:0 7px; font-size:14px; font-weight:600; }
.side-field { display:grid; grid-template-columns:54px 1fr; align-items:center; gap:8px; color:#aeb8c9; font-size:13px; }.side-field select,.side-search input { width:100%; min-width:0; border:1px solid #3b465a; border-radius:0; background:#354055; color:#f0f3f8; font-size:13px; }.side-field select { height:29px; padding:0 8px; }.side-search { position:relative; display:grid; gap:8px; padding-top:7px; border-top:1px solid #354055; color:#c1c9d5; font-size:13px; }.side-search input { height:29px; padding:6px 8px; }
.search-results { position:absolute; z-index:10; top:calc(100% + 3px); left:0; width:100%; max-height:240px; overflow:auto; border:1px solid #3b465a; background:#1d2739; box-shadow:0 12px 30px rgba(0,0,0,.35); }.search-results button { display:flex; width:100%; justify-content:space-between; gap:8px; padding:8px; border:0; border-bottom:1px solid #344056; border-radius:0; background:transparent; color:#edf2f9; text-align:left; }.search-results span { color:#9ba8bd; font-size:11px; }
.side-help { display:grid; gap:8px; margin-top:auto; padding:14px 7px 0; border-top:1px solid #354055; color:#9ba8bd; font-size:12px; line-height:1.5; }.side-help strong { color:#e1e7ef; font-size:13px; }
.heatmap-canvas { min-width:0; padding:0 12px 10px; overflow:hidden; }.canvas-header { display:flex; min-height:34px; align-items:center; justify-content:space-between; gap:16px; border-bottom:1px solid #344056; }.canvas-title { display:flex; align-items:center; gap:7px; white-space:nowrap; }.canvas-title strong { font-size:15px; }.canvas-title small { color:#aeb8c9; font-size:12px; }.live-dot { width:7px; height:7px; background:#00a56f; box-shadow:0 0 0 3px rgba(0,165,111,.14); }
.legend { display:flex; align-items:center; justify-content:flex-end; gap:2px; min-width:0; color:#aeb8c9; font-size:12px; white-space:nowrap; }.legend>span { margin-right:8px; overflow:hidden; text-overflow:ellipsis; }.legend i { min-width:42px; padding:4px 5px; color:#fff; font-style:normal; text-align:center; }
.index-strip { display:grid; grid-template-columns:repeat(6,minmax(0,1fr)); margin:8px 0; border:1px solid #344056; background:#1c2639; }.index-quote { min-width:0; padding:7px 9px; border:0; border-right:1px solid #344056; border-radius:0; background:transparent; color:#e9edf4; text-align:left; font-variant-numeric:tabular-nums; }.index-quote:last-child { border-right:0; }.index-quote span { display:block; overflow:hidden; color:#aeb8c9; font-size:11px; text-overflow:ellipsis; white-space:nowrap; }.index-quote strong { display:inline-block; margin:3px 7px 0 0; font-size:15px; }.index-quote em { font-style:normal; font-size:11px; }.index-unavailable { grid-column:1/-1; padding:7px 9px; color:#aeb8c9; font-size:12px; }
.market-notice { margin:8px 0; padding:8px 10px; border-left:3px solid #d6a12c; background:#342d1d; color:#e9c986; font-size:12px; }.market-notice.error { border-color:#db4b57; background:#3a2329; color:#ffb1b8; }
.heatmap { display:grid; grid-template-columns:repeat(4,minmax(0,1fr)); grid-template-rows:repeat(4,minmax(0,1fr)); gap:4px; height:calc(100vh - 132px); min-height:520px; overflow:hidden; }.heatmap.expanded { height:auto; min-height:0; grid-template-rows:none; overflow:visible; }.heatmap-group { min-width:0; min-height:0; overflow:hidden; border:1px solid #3a465a; background:#1d2739; }.heatmap.expanded .heatmap-group { min-height:220px; }.heatmap-group header { display:flex; align-items:center; gap:6px; height:22px; padding:3px 6px; border-bottom:1px solid #3a465a; color:#d5dce8; font-size:11px; }.heatmap-group header span { font-variant-numeric:tabular-nums; }.heatmap-group header small { margin-left:auto; color:#8e9bb0; }
.heatmap-tiles { display:grid; height:calc(100% - 22px); grid-template-columns:repeat(10,minmax(0,1fr)); grid-template-rows:repeat(7,minmax(0,1fr)); grid-auto-flow:dense; gap:1px; padding:1px; overflow:hidden; }.heatmap-tile { min-width:0; min-height:0; height:100%; padding:4px; border:0; border-radius:0; color:#fff; text-align:center; text-shadow:0 1px 1px rgba(0,0,0,.35); overflow:hidden; }.heatmap-tile:hover { filter:brightness(1.16); opacity:1; }.heatmap-tile b,.heatmap-tile span,.heatmap-tile em,.heatmap-tile small { display:block; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }.heatmap-tile b { font-size:12px; }.heatmap-tile span { margin-top:1px; opacity:.82; font-size:9px; }.heatmap-tile em { margin-top:3px; font-style:normal; font-size:12px; font-weight:700; }.heatmap-tile small { margin-top:1px; opacity:.8; font-size:9px; }
.heatmap-actions { display:flex; align-items:center; justify-content:space-between; gap:12px; min-height:30px; padding:6px 0; color:#9ba8bd; font-size:12px; }.heatmap-actions button { flex:0 0 auto; border-radius:0; }.empty-market { padding:52px; color:#aeb8c9; text-align:center; }
@media (max-width:1200px) { .heatmap { grid-template-columns:repeat(3,minmax(0,1fr)); grid-template-rows:repeat(4,minmax(0,1fr)); height:auto; min-height:0; overflow:visible; }.heatmap-group { min-height:220px; } }
@media (max-width:900px) { .heatmap-workspace { grid-template-columns:1fr; width:auto; min-height:0; margin:0; }.heatmap-sidebar { display:grid; grid-template-columns:repeat(2,minmax(0,1fr)); gap:10px; border-right:0; border-bottom:1px solid #344056; }.brand,.heatmap-sidebar h1 { display:none; }.side-help { display:none; }.heatmap-canvas { padding:0 8px 8px; }.legend>span { display:none; }.heatmap { grid-template-columns:repeat(2,minmax(0,1fr)); grid-template-rows:none; }.index-strip { grid-template-columns:repeat(3,minmax(0,1fr)); }.index-quote:nth-child(3) { border-right:0; }.index-quote:nth-child(-n+3) { border-bottom:1px solid #344056; } }
@media (max-width:600px) { .heatmap-sidebar { grid-template-columns:1fr; }.canvas-header { min-height:34px; }.legend { display:none; }.index-strip { grid-template-columns:repeat(2,minmax(0,1fr)); }.index-quote { border-bottom:1px solid #344056; }.index-quote:nth-child(even) { border-right:0; }.heatmap { grid-template-columns:1fr; }.heatmap-group { min-height:190px; }.heatmap-tiles { height:168px; }.heatmap-actions { align-items:flex-start; flex-direction:column; } }
</style>
