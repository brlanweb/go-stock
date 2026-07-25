<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { hierarchy, treemap, treemapSquarify, type HierarchyRectangularNode } from 'd3-hierarchy'
import { api, fmtBig, fmtPct, type HeatmapGroup, type HeatmapItem, type Quote, type Recommendation } from '../api'

interface TreeDatum {
  name: string
  item?: HeatmapItem
  children?: TreeDatum[]
}

const router = useRouter()
const groups = ref<HeatmapGroup[]>([])
const notice = ref('')
const error = ref('')
const market = ref('all')
const groupBy = ref('industry')
const metric = ref('change_pct')
const period = ref('1d')
const keyword = ref('')
const searchResults = ref<any[]>([])
const recommendations = ref<Recommendation[]>([])
const watchlist = ref<Quote[]>([])
const mapHost = ref<HTMLElement | null>(null)
const mapWidth = ref(0)
const mapHeight = ref(0)
let timer: number | undefined
let searchTimer: number | undefined
let resizeObserver: ResizeObserver | undefined

const marketOptions = [['all', 'A股'], ['gem', '创业板'], ['star', '科创板'], ['bse', '北交所']]
const groupOptions = [['industry', '行业'], ['concept', '概念']]
const metricOptions = [['change_pct', '涨跌幅'], ['pe_ttm', '市盈率 TTM'], ['main_net_inflow', '主力资金']]
const periodOptions = [['1d', '今日'], ['3d', '三日'], ['5d', '五日']]
const heatLegend = [-4, -3, -2, -1, 0, 1, 2, 3, 4]

const itemCount = computed(() => groups.value.reduce((sum, group) => sum + group.items.length, 0))
const layout = computed(() => {
  if (!groups.value.length || mapWidth.value <= 0 || mapHeight.value <= 0) {
    return { sectors: [] as HierarchyRectangularNode<TreeDatum>[], stocks: [] as HierarchyRectangularNode<TreeDatum>[] }
  }
  const data: TreeDatum = {
    name: 'A股',
    children: groups.value.map(group => ({
      name: group.name,
      children: group.items.map(item => ({ name: item.name, item }))
    }))
  }
  const root = hierarchy(data)
    .sum(node => node.item ? Math.max(Number(node.item.total_mv) || 0, 1) : 0)
    .sort((a, b) => (b.value || 0) - (a.value || 0))
  const rectangularRoot = treemap<TreeDatum>()
    .size([mapWidth.value, mapHeight.value])
    .tile(treemapSquarify.ratio(1.2))
    .paddingOuter(1)
    .paddingInner(1)
    .paddingTop(node => node.depth === 1 ? 19 : 0)
    .round(true)(root)
  return {
    sectors: rectangularRoot.children || [],
    stocks: rectangularRoot.leaves()
  }
})

function pollInterval() {
  return 60000
}

async function refreshSidebar() {
  const [recommendationResult, watchlistResult] = await Promise.allSettled([api.recommendations(), api.watchlist()])
  if (recommendationResult.status === 'fulfilled') recommendations.value = recommendationResult.value
  if (watchlistResult.status === 'fulfilled') watchlist.value = watchlistResult.value.filter((item): item is Quote => typeof item !== 'string')
}

async function refresh() {
  try {
    const heatmap = await api.heatmap(market.value, groupBy.value, metric.value, period.value)
    groups.value = heatmap.groups
    notice.value = heatmap.notice
    error.value = ''
  } catch (e: any) {
    error.value = e?.message || '市场云图加载失败'
  }
  window.clearTimeout(timer)
  timer = window.setTimeout(refresh, pollInterval())
}

function setOption() {
  refresh()
}

function metricNumber(item: HeatmapItem) {
  if (metric.value === 'pe_ttm') return Number(item.pe_ratio) - 25
  if (metric.value === 'main_net_inflow') return Number(item.main_net_inflow) || 0
  return Number(item.period_change) || 0
}

function tileValue(item: HeatmapItem) {
  if (metric.value === 'pe_ttm') return item.pe_ratio > 0 ? `${item.pe_ratio.toFixed(1)}x` : '-'
  if (metric.value === 'main_net_inflow') return item.main_net_inflow == null ? '-' : fmtBig(item.main_net_inflow)
  return fmtPct(item.period_change)
}

function tileColor(value: number) {
  if (!Number.isFinite(value) || Math.abs(value) < 0.005) return '#39475a'
  const intensity = Math.min(1, Math.abs(value) / 4)
  if (value > 0) return `rgb(${Math.round(104 + 114 * intensity)}, ${Math.round(58 - 25 * intensity)}, ${Math.round(70 - 22 * intensity)})`
  return `rgb(${Math.round(34 - 20 * intensity)}, ${Math.round(112 + 48 * intensity)}, ${Math.round(96 + 20 * intensity)})`
}

function rectStyle(node: HierarchyRectangularNode<TreeDatum>) {
  return {
    left: `${node.x0}px`,
    top: `${node.y0}px`,
    width: `${Math.max(0, node.x1 - node.x0)}px`,
    height: `${Math.max(0, node.y1 - node.y0)}px`
  }
}

function stockStyle(node: HierarchyRectangularNode<TreeDatum>) {
  return { ...rectStyle(node), background: tileColor(metricNumber(node.data.item!)) }
}

function tileSize(node: HierarchyRectangularNode<TreeDatum>) {
  const width = node.x1 - node.x0
  const height = node.y1 - node.y0
  const area = width * height
  if (area > 16000) return 'xl'
  if (area > 6000) return 'lg'
  if (area > 1800) return 'md'
  return 'sm'
}

function showName(node: HierarchyRectangularNode<TreeDatum>) {
  return node.x1 - node.x0 >= 30 && node.y1 - node.y0 >= 17
}
function showValue(node: HierarchyRectangularNode<TreeDatum>) {
  return node.x1 - node.x0 >= 42 && node.y1 - node.y0 >= 32
}
function showCode(node: HierarchyRectangularNode<TreeDatum>) {
  return node.x1 - node.x0 >= 78 && node.y1 - node.y0 >= 57
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

onMounted(async () => {
  await nextTick()
  if (mapHost.value) {
    resizeObserver = new ResizeObserver(entries => {
      const rect = entries[0]?.contentRect
      if (rect) {
        mapWidth.value = Math.floor(rect.width)
        mapHeight.value = Math.floor(rect.height)
      }
    })
    resizeObserver.observe(mapHost.value)
  }
  refresh()
  refreshSidebar()
})
onUnmounted(() => {
  window.clearTimeout(timer)
  window.clearTimeout(searchTimer)
  resizeObserver?.disconnect()
})
</script>

<template>
  <main class="heatmap-workspace">
    <aside class="heatmap-sidebar">
      <router-link to="/" class="brand">go-stock</router-link>
      <h1>云图设置</h1>
      <label class="side-field"><span>范围</span><select v-model="market" @change="setOption"><option v-for="[value, label] in marketOptions" :key="value" :value="value">{{ label }}</option></select></label>
      <label class="side-field"><span>划分维度</span><select v-model="groupBy" @change="setOption"><option v-for="[value, label] in groupOptions" :key="value" :value="value">{{ label }}</option></select></label>
      <label class="side-field"><span>数据指标</span><select v-model="metric" @change="setOption"><option v-for="[value, label] in metricOptions" :key="value" :value="value">{{ label }}</option></select></label>
      <label class="side-field"><span>时间范围</span><select v-model="period" @change="setOption"><option v-for="[value, label] in periodOptions" :key="value" :value="value">{{ label }}</option></select></label>
      <div class="side-search"><span>快速定位</span><input v-model="keyword" autocomplete="off" placeholder="输入代码/简称" @input="onSearch" @keydown.enter="searchResults[0] && openStock(searchResults[0].symbol)" />
        <div v-if="searchResults.length" class="search-results"><button v-for="result in searchResults" :key="result.symbol" @click="openStock(result.symbol)"><b>{{ result.name }}</b><span>{{ result.symbol }}</span></button></div>
      </div>
      <div class="side-stats"><span>证券数量</span><strong>{{ itemCount }}</strong></div>
      <section class="sidebar-section recommendation-panel"><header><strong>AI 趋势推荐</strong><small>未来10日</small></header><button v-for="item in recommendations" :key="item.symbol" @click="openStock(item.symbol)"><span><b>{{ item.name }}</b><small>{{ item.sector }}</small></span><em>{{ item.probability.toFixed(0) }}%</em></button><p v-if="!recommendations.length">等待每日分析结果</p></section>
      <section class="sidebar-section watch-panel"><header><strong>自选股</strong><small>{{ watchlist.length }}/10</small></header><button v-for="item in watchlist" :key="item.symbol" @click="openStock(item.symbol)"><span><b>{{ item.name }}</b><small>{{ item.code }}</small></span><em :class="item.change_pct && item.change_pct > 0 ? 'positive' : 'negative'">{{ fmtPct(item.change_pct) }}</em></button><p v-if="!watchlist.length">在详情页加入自选</p></section>
      <div class="side-help"><strong>使用说明</strong><span>面积代表流通市值大小</span><span>颜色深浅代表指标强弱</span><span>点击色块查看个股详情</span></div>
    </aside>

    <section class="heatmap-canvas">
      <header class="canvas-header">
        <div class="canvas-title"><span class="live-dot"></span><strong>大盘云图</strong></div>
        <div class="legend"><span>注：面积代表流通市值大小，红绿色深浅代表涨跌幅大小</span><i v-for="value in heatLegend" :key="value" :style="{ background: tileColor(value) }">{{ value > 0 ? `+${value}%` : `${value}%` }}</i></div>
      </header>
      <section v-if="notice || error" class="market-notice" :class="{ error }">{{ error || notice }}</section>
      <section ref="mapHost" class="treemap-stage" :class="{ empty: !layout.stocks.length }">
        <template v-if="layout.stocks.length">
          <div v-for="sector in layout.sectors" :key="sector.data.name" class="sector-frame" :style="rectStyle(sector)"><span>{{ sector.data.name }}</span></div>
          <button v-for="node in layout.stocks" :key="node.data.item!.symbol" class="stock-tile" :class="tileSize(node)" :style="stockStyle(node)" :title="`${node.data.item!.name} ${node.data.item!.code} ${tileValue(node.data.item!)}`" @click="openStock(node.data.item!.symbol)">
            <b v-if="showName(node)">{{ node.data.item!.name }}</b><em v-if="showValue(node)">{{ tileValue(node.data.item!) }}</em><small v-if="showCode(node)">{{ node.data.item!.code }}</small>
          </button>
        </template>
        <span v-else-if="!notice && !error" class="loading">正在载入市场数据</span>
      </section>
    </section>
  </main>
</template>

<style scoped>
.heatmap-workspace { display:grid; grid-template-columns:212px minmax(0,1fr); width:100vw; height:100vh; overflow:hidden; background:#151f31; color:#edf1f7; }
.heatmap-sidebar { position:relative; z-index:3; display:flex; min-height:0; flex-direction:column; gap:13px; padding:14px 8px; border-right:1px solid #354157; background:#1c2639; }.brand { padding:0 7px 7px; border-bottom:1px solid #354157; color:#f0f3f8; font-size:16px; font-weight:720; }.heatmap-sidebar h1 { padding:0 7px; font-size:14px; font-weight:600; }
.side-field { display:grid; grid-template-columns:55px 1fr; align-items:center; gap:8px; color:#adb7c7; font-size:13px; }.side-field select,.side-search input { width:100%; min-width:0; height:29px; border:1px solid #3c475c; border-radius:0; outline:none; background:#354055; color:#f1f4f8; font-size:13px; }.side-field select { padding:0 8px; }.side-search { position:relative; display:grid; gap:8px; margin-top:10px; padding-top:16px; border-top:1px solid #354157; color:#c3cbd7; font-size:13px; }.side-search input { padding:6px 8px; }
.search-results { position:absolute; z-index:20; top:calc(100% + 3px); width:100%; max-height:260px; overflow:auto; border:1px solid #445067; background:#1d2739; box-shadow:0 12px 30px rgba(0,0,0,.4); }.search-results button { display:flex; width:100%; justify-content:space-between; gap:8px; padding:8px; border:0; border-bottom:1px solid #344056; border-radius:0; background:transparent; color:#edf2f9; text-align:left; }.search-results span { color:#9ba8bd; font-size:11px; }.side-stats { display:flex; align-items:center; justify-content:space-between; padding:8px 7px; border-top:1px solid #354157; border-bottom:1px solid #354157; color:#9da9bb; font-size:12px; }.side-stats strong { color:#e7ecf4; font-size:13px; }
.sidebar-section { display:grid; gap:2px; }.sidebar-section header { display:flex; align-items:center; justify-content:space-between; padding:2px 7px 5px; color:#e2e7ef; font-size:12px; }.sidebar-section header small { color:#8390a4; font-size:10px; }.sidebar-section button { display:flex; min-width:0; align-items:center; justify-content:space-between; gap:5px; padding:5px 7px; border:0; border-bottom:1px solid #303b50; border-radius:0; background:#222d41; color:#ecf0f6; text-align:left; }.sidebar-section button:hover { background:#2b374c; opacity:1; }.sidebar-section button span { min-width:0; }.sidebar-section button b,.sidebar-section button small { display:block; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }.sidebar-section button b { font-size:11px; }.sidebar-section button small { margin-top:1px; color:#8996aa; font-size:9px; }.sidebar-section button em { flex:0 0 auto; color:#e9c16c; font-style:normal; font-size:11px; font-weight:700; }.sidebar-section button em.positive { color:#ef6a72; }.sidebar-section button em.negative { color:#28bd8b; }.sidebar-section p { padding:6px 7px; color:#738197; font-size:10px; }.side-help { display:grid; gap:8px; margin-top:auto; padding:10px 7px 4px; border-top:1px solid #354157; color:#9ba8bd; font-size:12px; line-height:1.45; }.side-help strong { color:#e2e7ef; font-size:13px; }
.heatmap-canvas { display:grid; min-width:0; min-height:0; grid-template-rows:35px minmax(0,1fr); overflow:hidden; }.canvas-header { display:flex; min-width:0; align-items:center; justify-content:space-between; gap:16px; padding:0 8px; border-bottom:1px solid #354157; background:#182234; }.canvas-title { display:flex; align-items:center; gap:7px; white-space:nowrap; }.canvas-title strong { font-size:14px; }.live-dot { width:7px; height:7px; background:#00a56f; box-shadow:0 0 0 3px rgba(0,165,111,.14); }
.legend { display:flex; min-width:0; align-items:center; justify-content:flex-end; gap:2px; color:#aeb8c9; font-size:12px; white-space:nowrap; }.legend>span { margin-right:8px; overflow:hidden; text-overflow:ellipsis; }.legend i { min-width:43px; padding:4px 5px; color:#fff; font-style:normal; text-align:center; }
.market-notice { position:absolute; z-index:30; top:40px; left:220px; padding:8px 11px; border-left:3px solid #d6a12c; background:#342d1d; color:#e9c986; font-size:12px; }.market-notice.error { border-color:#db4b57; background:#3a2329; color:#ffb1b8; }
.treemap-stage { position:relative; min-width:0; min-height:0; margin:7px 8px 8px; overflow:hidden; background:#151f31; }.treemap-stage.empty { display:grid; place-items:center; }.sector-frame { position:absolute; z-index:1; overflow:hidden; border:1px solid #43516a; pointer-events:none; }.sector-frame>span { display:block; height:18px; overflow:hidden; padding:1px 4px; background:#1b2638; color:#d5dce7; font-size:11px; line-height:17px; text-overflow:ellipsis; white-space:nowrap; }
.stock-tile { position:absolute; z-index:2; display:flex; min-width:0; min-height:0; flex-direction:column; align-items:center; justify-content:center; overflow:hidden; padding:1px 2px; border:0; border-radius:0; outline:1px solid rgba(17,27,42,.72); color:#fff; text-align:center; text-shadow:0 1px 1px rgba(0,0,0,.48); transition:filter .1s, outline-color .1s; }.stock-tile:hover { z-index:4; filter:brightness(1.22); outline:2px solid #ffd400; opacity:1; }.stock-tile b,.stock-tile em,.stock-tile small { display:block; max-width:100%; overflow:hidden; font-style:normal; line-height:1.12; text-overflow:ellipsis; white-space:nowrap; }.stock-tile b { font-size:9px; }.stock-tile em { margin-top:1px; font-size:9px; font-weight:700; }.stock-tile small { margin-top:2px; opacity:.82; font-size:8px; }.stock-tile.md b { font-size:11px; }.stock-tile.md em { font-size:10px; }.stock-tile.lg b { font-size:14px; }.stock-tile.lg em { margin-top:3px; font-size:13px; }.stock-tile.xl b { font-size:20px; }.stock-tile.xl em { margin-top:5px; font-size:18px; }.stock-tile.xl small { font-size:10px; }.loading { color:#aeb8c9; font-size:13px; }
@media (max-width:900px) { .heatmap-workspace { grid-template-columns:1fr; height:auto; min-height:100vh; overflow:visible; }.heatmap-sidebar { display:grid; grid-template-columns:repeat(2,minmax(0,1fr)); gap:9px; border-right:0; border-bottom:1px solid #354157; }.brand,.heatmap-sidebar h1,.side-help,.side-stats,.sidebar-section { display:none; }.side-search { margin:0; padding:0; border:0; }.heatmap-canvas { grid-template-rows:35px 70vh; }.legend>span { display:none; }.treemap-stage { min-height:0; } }
@media (max-width:600px) { .heatmap-sidebar { grid-template-columns:1fr; }.legend { display:none; }.heatmap-canvas { grid-template-rows:35px 72vh; }.market-notice { left:8px; right:8px; }.stock-tile.xl b { font-size:15px; }.stock-tile.xl em { font-size:14px; } }
</style>
