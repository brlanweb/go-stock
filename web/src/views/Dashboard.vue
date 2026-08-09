<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { hierarchy, treemap, treemapSquarify, type HierarchyRectangularNode } from 'd3-hierarchy'
import { api, fmtBig, fmtPct, type HeatmapGroup, type HeatmapItem } from '../api'
import MarketSidebar from '../components/MarketSidebar.vue'
import HotspotFunnel from '../components/HotspotFunnel.vue'

interface TreeDatum {
  name: string
  item?: HeatmapItem
  group?: HeatmapGroup
  children?: TreeDatum[]
  weight?: number
}

const router = useRouter()
const activeView = ref<'heatmap' | 'hotspot'>('heatmap')
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

const itemCount = computed(() => groups.value.reduce((sum, group) => sum + (Number(group.stock_count) || group.items.length), 0))
const groupByNameForView = computed(() => new Map(groups.value.map(group => [group.name, group])))
// 板块内部严格按流通市值分配面积，和常见市场云图口径一致。
function stockWeight(circMV: number, totalMV: number) {
  return Math.max(circMV || totalMV, 1)
}

// 板块权重由后端统一计算，行业与概念使用同一热度口径。
function sectorWeight(group: HeatmapGroup) {
  const weight = Number(group.area_weight)
  return Number.isFinite(weight) && weight > 0 ? weight : 100
}

const layout = computed(() => {
  if (!groups.value.length || mapWidth.value <= 0 || mapHeight.value <= 0) {
    return { sectors: [] as HierarchyRectangularNode<TreeDatum>[], stocks: [] as HierarchyRectangularNode<TreeDatum>[] }
  }

  const root = hierarchy<TreeDatum>({
    name: 'A股',
    children: groups.value.map(group => ({
      name: group.name,
      group,
      children: group.items.map(item => ({
        name: item.name,
        item,
        weight: sectorWeight(group) * stockWeight(Number(item.circ_mv) || 0, Number(item.total_mv) || 0) /
          Math.max(group.items.reduce((sum, current) => sum + stockWeight(Number(current.circ_mv) || 0, Number(current.total_mv) || 0), 0), 1)
      }))
    }))
  })
    .sum(node => node.weight || 0)
    .sort((a, b) => (b.value || 0) - (a.value || 0))

  // 叶子权重先按板块面积归一，再按板块内部流通市值占比分配。
  // D3 单棵嵌套树同时决定板块和个股坐标，避免两次布局产生偏移。

  const result = treemap<TreeDatum>()
    .size([mapWidth.value, mapHeight.value])
    .tile(treemapSquarify.ratio(1.15))
    .paddingOuter(2)
    .paddingInner(2)
    .paddingTop(node => node.depth === 1 ? 20 : 0)
    .round(true)(root)

  return {
    sectors: result.children || [],
    stocks: result.leaves().filter(node => !!node.data.item)
  }
})

function pollInterval() {
  return 60000
}

async function refresh() {
  try {
    const limit = groupBy.value === 'industry' ? 31 : 32
    const heatmap = await api.heatmap(market.value, groupBy.value, metric.value, period.value, limit)
    groups.value = heatmap.groups
    notice.value = heatmap.notice || (heatmap.groups.length ? '' : '暂无市场云图数据')
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

function openSector(name: string) {
  const group = groupByNameForView.value.get(name)
  if (!group) return
  const code = group.sector_code || (group.sector_type === 'industry' ? `industry:${group.name}` : '')
  if (code) router.push(`/sector/${encodeURIComponent(code)}`)
}

// mapHost 位于 v-if 分支内，切换 Tab 会卸载并重建 DOM；
// 必须跟随元素变化重新 observe，否则尺寸停留在 0 导致云图无法恢复。
watch(mapHost, host => {
  resizeObserver?.disconnect()
  if (!host) return
  resizeObserver = new ResizeObserver(entries => {
    const rect = entries[0]?.contentRect
    if (rect && rect.width > 0 && rect.height > 0) {
      mapWidth.value = Math.floor(rect.width)
      mapHeight.value = Math.floor(rect.height)
    }
  })
  resizeObserver.observe(host)
})

onMounted(async () => {
  await nextTick()
  refresh()
})
onUnmounted(() => {
  window.clearTimeout(timer)
  resizeObserver?.disconnect()
})
</script>

<template>
  <main class="heatmap-workspace">
    <MarketSidebar :market="market" :group-by="groupBy" :metric="metric" :period="period" :controls="activeView === 'heatmap'" :security-count="itemCount" @change="options => { market = options.market; groupBy = options.groupBy; metric = options.metric; period = options.period; setOption() }" />

    <section class="heatmap-canvas" :class="{ 'hotspot-mode': activeView === 'hotspot' }">
      <header class="canvas-header">
        <nav class="workspace-tabs" aria-label="首页视图">
          <button type="button" :class="{ active: activeView === 'heatmap' }" @click="activeView = 'heatmap'"><span class="live-dot"></span>大盘云图</button>
          <button type="button" :class="{ active: activeView === 'hotspot' }" @click="activeView = 'hotspot'">热点漏斗</button>
        </nav>
        <div v-if="activeView === 'heatmap'" class="legend"><span>板块面积以成分市值为主，概念兼顾活跃度；个股面积代表市值，颜色代表涨跌幅</span><i v-for="value in heatLegend" :key="value" :style="{ background: tileColor(value) }">{{ value > 0 ? `+${value}%` : `${value}%` }}</i></div>
        <div v-else class="hotspot-caption">数据初筛 → 关系收敛 → AI 产业链分析 → 本地数据回验</div>
      </header>
      <template v-if="activeView === 'heatmap'">
        <section v-if="notice || error" class="market-notice" :class="{ error }">{{ error || notice }}</section>
        <section ref="mapHost" class="treemap-stage" :class="{ empty: !layout.stocks.length }">
          <template v-if="layout.stocks.length">
            <div v-for="sector in layout.sectors" :key="sector.data.name" class="sector-frame" :style="rectStyle(sector)"><button type="button" class="sector-title" :title="`查看${sector.data.name}板块全部 ${groupByNameForView.get(sector.data.name)?.stock_count || 0} 只成分股`" @click="openSector(sector.data.name)"><span>{{ sector.data.name }}</span><i v-if="groupByNameForView.get(sector.data.name) && (sector.x1 - sector.x0) >= 150">{{ fmtBig(groupByNameForView.get(sector.data.name)!.total_mv) }}</i></button></div>
            <button v-for="node in layout.stocks" :key="node.data.item!.symbol" class="stock-tile" :class="tileSize(node)" :style="stockStyle(node)" :title="`${node.data.item!.name} ${node.data.item!.code} ${tileValue(node.data.item!)}`" @click="openStock(node.data.item!.symbol)">
              <b v-if="showName(node)">{{ node.data.item!.name }}</b><em v-if="showValue(node)">{{ tileValue(node.data.item!) }}</em><small v-if="showCode(node)">{{ node.data.item!.code }}</small>
            </button>
          </template>
          <span v-else-if="!notice && !error" class="loading">正在载入市场数据</span>
        </section>
      </template>
      <HotspotFunnel v-else />
    </section>
  </main>
</template>

<style scoped>
.heatmap-workspace { display:grid; grid-template-columns:212px minmax(0,1fr); width:100vw; height:100vh; overflow:hidden; background:#151f31; color:#edf1f7; }
.heatmap-canvas { display:grid; min-width:0; min-height:0; grid-template-rows:35px minmax(0,1fr); overflow:hidden; }.canvas-header { display:flex; min-width:0; align-items:center; justify-content:space-between; gap:16px; padding:0 8px; border-bottom:1px solid #354157; background:#182234; }.workspace-tabs { align-self:stretch; display:flex; align-items:stretch; }.workspace-tabs button { display:flex; min-width:112px; align-items:center; justify-content:center; gap:7px; padding:0 14px; border:0; border-bottom:2px solid transparent; border-radius:0; background:transparent; color:#94a1b5; cursor:pointer; font-size:13px; font-weight:700; }.workspace-tabs button:hover { color:#e2e7ef; }.workspace-tabs button.active { border-bottom-color:#e0b64f; background:#202d41; color:#f0c760; }.hotspot-caption { overflow:hidden; color:#8e9cb0; font-size:11px; text-overflow:ellipsis; white-space:nowrap; }.live-dot { width:7px; height:7px; background:#00a56f; box-shadow:0 0 0 3px rgba(0,165,111,.14); }
.legend { display:flex; min-width:0; align-items:center; justify-content:flex-end; gap:2px; color:#aeb8c9; font-size:12px; white-space:nowrap; }.legend>span { margin-right:8px; overflow:hidden; text-overflow:ellipsis; }.legend i { min-width:43px; padding:4px 5px; color:#fff; font-style:normal; text-align:center; }
.market-notice { position:absolute; z-index:30; top:40px; left:220px; padding:8px 11px; border-left:3px solid #d6a12c; background:#342d1d; color:#e9c986; font-size:12px; }.market-notice.error { border-color:#db4b57; background:#3a2329; color:#ffb1b8; }
.treemap-stage { position:relative; min-width:0; min-height:0; margin:7px 8px 8px; overflow:hidden; background:#151f31; }.treemap-stage.empty { display:grid; place-items:center; }.sector-frame { position:absolute; z-index:5; overflow:hidden; border:1px solid #43516a; pointer-events:none; }.sector-title { position:absolute; z-index:1; top:0; right:0; left:0; display:flex; width:100%; height:19px; min-width:0; align-items:center; gap:4px; overflow:hidden; padding:1px 4px; border:0; border-radius:0; background:#1b2638; color:#d5dce7; cursor:pointer; font-size:11px; line-height:17px; text-align:left; pointer-events:auto; }.sector-title:hover { color:#ffd400; }.sector-title span { flex:1 1 auto; min-width:0; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }.sector-title i { flex:0 1 auto; min-width:0; overflow:hidden; color:#8794a8; font-size:8px; font-style:normal; text-overflow:ellipsis; white-space:nowrap; }
.stock-tile { position:absolute; z-index:4; display:flex; min-width:0; min-height:0; flex-direction:column; align-items:center; justify-content:center; overflow:hidden; padding:1px 2px; border:0; border-radius:0; outline:1px solid rgba(17,27,42,.72); color:#fff; text-align:center; text-shadow:0 1px 1px rgba(0,0,0,.48); transition:filter .1s, outline-color .1s; }.stock-tile:hover { z-index:4; filter:brightness(1.22); outline:2px solid #ffd400; opacity:1; }.stock-tile b,.stock-tile em,.stock-tile small { display:block; max-width:100%; overflow:hidden; font-style:normal; line-height:1.12; text-overflow:ellipsis; white-space:nowrap; }.stock-tile b { font-size:9px; }.stock-tile em { margin-top:1px; font-size:9px; font-weight:700; }.stock-tile small { margin-top:2px; opacity:.82; font-size:8px; }.stock-tile.md b { font-size:11px; }.stock-tile.md em { font-size:10px; }.stock-tile.lg b { font-size:14px; }.stock-tile.lg em { margin-top:3px; font-size:13px; }.stock-tile.xl b { font-size:20px; }.stock-tile.xl em { margin-top:5px; font-size:18px; }.stock-tile.xl small { font-size:10px; }.loading { color:#aeb8c9; font-size:13px; }
@media (max-width:900px) { .heatmap-workspace { grid-template-columns:1fr; height:auto; min-height:100vh; overflow:visible; }.heatmap-canvas { grid-template-rows:35px 70vh; }.heatmap-canvas.hotspot-mode { grid-template-rows:35px auto; overflow:visible; }.legend>span { display:none; }.treemap-stage { min-height:0; } }
@media (max-width:600px) { .legend { display:none; }.heatmap-canvas { grid-template-rows:35px 72vh; }.market-notice { left:8px; right:8px; }.stock-tile.xl b { font-size:15px; }.stock-tile.xl em { font-size:14px; } }
</style>
