<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { hierarchy, treemap, treemapSquarify, type HierarchyRectangularNode } from 'd3-hierarchy'
import { api, fmtBig, fmtPct, type HeatmapGroup, type HeatmapItem } from '../api'
import MarketSidebar from '../components/MarketSidebar.vue'

interface TreeDatum {
  name: string
  item?: HeatmapItem
  children?: TreeDatum[]
  weight?: number
}

const router = useRouter()
const groups = ref<HeatmapGroup[]>([])
const notice = ref('')
const error = ref('')
const market = ref('all')
const groupBy = ref('industry')
const metric = ref('change_pct')
const period = ref('1d')
const mapHost = ref<HTMLElement | null>(null)
const mapWidth = ref(0)
const mapHeight = ref(0)
let timer: number | undefined
let resizeObserver: ResizeObserver | undefined

const heatLegend = [-4, -3, -2, -1, 0, 1, 2, 3, 4]

const itemCount = computed(() => groups.value.reduce((sum, group) => sum + group.items.length, 0))
// 个股市值占比压缩：极少数超大市值（银行、白酒龙头）会占据云图绝大部分空间，
// 用对数映射 + 软上限（在最大值的 12% 处封顶）使头部不至于吞掉整屏。
const MAX_STOCK_RATIO = 0.12

function adjustedWeight(totalMV: number, allMvs: number[]) {
  if (!allMvs.length) return 1
  const max = Math.max(...allMvs)
  const logMV = Math.log(Math.max(totalMV, 1) + 1)
  const logMax = Math.log(Math.max(max, 1) + 1)
  const minLog = Math.log(Math.min(...allMvs) + 1)
  const ratio = (logMV - minLog) / Math.max(logMax - minLog, 0.0001)
  // ratio ∈ [0,1]，最大股按对数归一后等于 1
  // 再用最大个股占比上限压制：ratio=1 时缩为 MAX_STOCK_RATIO，等比扩展到 0
  const share = ratio * MAX_STOCK_RATIO
  return share + 1 // 基础权重 1，配合 share 让小盘股也有可见空间
}

const layout = computed(() => {
  if (!groups.value.length || mapWidth.value <= 0 || mapHeight.value <= 0) {
    return { sectors: [] as HierarchyRectangularNode<TreeDatum>[], stocks: [] as HierarchyRectangularNode<TreeDatum>[] }
  }
  const allMvs = groups.value.flatMap(g => g.items.map(i => Number(i.total_mv) || 0)).filter(v => v > 0)
  const data: TreeDatum = {
    name: 'A股',
    children: groups.value.map(group => ({
      name: group.name,
      children: group.items.map(item => ({ name: item.name, item, weight: adjustedWeight(Number(item.total_mv) || 0, allMvs) }))
    }))
  }
  const root = hierarchy(data)
    .sum(node => node.item ? Math.max((Number(node.item.total_mv) || 0), 1) * (node.weight || 1) : 0)
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

function openStock(symbol: string) {
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
})
onUnmounted(() => {
  window.clearTimeout(timer)
  resizeObserver?.disconnect()
})
</script>

<template>
  <main class="heatmap-workspace">
    <MarketSidebar :market="market" :group-by="groupBy" :metric="metric" :period="period" :security-count="itemCount" @change="options => { market = options.market; groupBy = options.groupBy; metric = options.metric; period = options.period; setOption() }" />

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
.heatmap-canvas { display:grid; min-width:0; min-height:0; grid-template-rows:35px minmax(0,1fr); overflow:hidden; }.canvas-header { display:flex; min-width:0; align-items:center; justify-content:space-between; gap:16px; padding:0 8px; border-bottom:1px solid #354157; background:#182234; }.canvas-title { display:flex; align-items:center; gap:7px; white-space:nowrap; }.canvas-title strong { font-size:14px; }.live-dot { width:7px; height:7px; background:#00a56f; box-shadow:0 0 0 3px rgba(0,165,111,.14); }
.legend { display:flex; min-width:0; align-items:center; justify-content:flex-end; gap:2px; color:#aeb8c9; font-size:12px; white-space:nowrap; }.legend>span { margin-right:8px; overflow:hidden; text-overflow:ellipsis; }.legend i { min-width:43px; padding:4px 5px; color:#fff; font-style:normal; text-align:center; }
.market-notice { position:absolute; z-index:30; top:40px; left:220px; padding:8px 11px; border-left:3px solid #d6a12c; background:#342d1d; color:#e9c986; font-size:12px; }.market-notice.error { border-color:#db4b57; background:#3a2329; color:#ffb1b8; }
.treemap-stage { position:relative; min-width:0; min-height:0; margin:7px 8px 8px; overflow:hidden; background:#151f31; }.treemap-stage.empty { display:grid; place-items:center; }.sector-frame { position:absolute; z-index:1; overflow:hidden; border:1px solid #43516a; pointer-events:none; }.sector-frame>span { display:block; height:18px; overflow:hidden; padding:1px 4px; background:#1b2638; color:#d5dce7; font-size:11px; line-height:17px; text-overflow:ellipsis; white-space:nowrap; }
.stock-tile { position:absolute; z-index:2; display:flex; min-width:0; min-height:0; flex-direction:column; align-items:center; justify-content:center; overflow:hidden; padding:1px 2px; border:0; border-radius:0; outline:1px solid rgba(17,27,42,.72); color:#fff; text-align:center; text-shadow:0 1px 1px rgba(0,0,0,.48); transition:filter .1s, outline-color .1s; }.stock-tile:hover { z-index:4; filter:brightness(1.22); outline:2px solid #ffd400; opacity:1; }.stock-tile b,.stock-tile em,.stock-tile small { display:block; max-width:100%; overflow:hidden; font-style:normal; line-height:1.12; text-overflow:ellipsis; white-space:nowrap; }.stock-tile b { font-size:9px; }.stock-tile em { margin-top:1px; font-size:9px; font-weight:700; }.stock-tile small { margin-top:2px; opacity:.82; font-size:8px; }.stock-tile.md b { font-size:11px; }.stock-tile.md em { font-size:10px; }.stock-tile.lg b { font-size:14px; }.stock-tile.lg em { margin-top:3px; font-size:13px; }.stock-tile.xl b { font-size:20px; }.stock-tile.xl em { margin-top:5px; font-size:18px; }.stock-tile.xl small { font-size:10px; }.loading { color:#aeb8c9; font-size:13px; }
@media (max-width:900px) { .heatmap-workspace { grid-template-columns:1fr; height:auto; min-height:100vh; overflow:visible; }.heatmap-canvas { grid-template-rows:35px 70vh; }.legend>span { display:none; }.treemap-stage { min-height:0; } }
@media (max-width:600px) { .legend { display:none; }.heatmap-canvas { grid-template-rows:35px 72vh; }.market-notice { left:8px; right:8px; }.stock-tile.xl b { font-size:15px; }.stock-tile.xl em { font-size:14px; } }
</style>
