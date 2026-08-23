<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { hierarchy, treemap, treemapSquarify, type HierarchyRectangularNode } from 'd3-hierarchy'
import { api, fmtBig, fmtPct, type HeatmapGroup, type HeatmapItem } from '../api'
import MarketSidebar from '../components/MarketSidebar.vue'
import HotspotFunnel from '../components/HotspotFunnel.vue'
import Home from './Home.vue'
import Review from './Review.vue'
import Recommendations from './Recommendations.vue'
import Indicators from './Indicators.vue'
import Scorecard from './Scorecard.vue'
import RiskSentinel from './RiskSentinel.vue'

interface TreeDatum {
  name: string
  item?: HeatmapItem
  group?: HeatmapGroup
  children?: TreeDatum[]
  weight?: number
}

const router = useRouter()
const route = useRoute()

// 导航按「交易日决策流程」分两级，主 Tab 只留 5 个：
//   总览(home) → 市场(heatmap/hotspot) → 交易(reco) → 复盘(review/scorecard/indicators) → 风险(risk)
// 二级页仍用同一套 ?view= 值，因此旧链接与书签（?view=reco 等）全部保持有效，
// 归属关系由 viewGroup 反查，进入时自动展开对应主 Tab。
type HomeView = 'home' | 'heatmap' | 'risk' | 'review' | 'reco' | 'scorecard' | 'hotspot' | 'indicators'
const homeViews: HomeView[] = ['home', 'heatmap', 'risk', 'review', 'reco', 'scorecard', 'hotspot', 'indicators']

type NavGroup = 'home' | 'market' | 'trade' | 'retro' | 'risk'
interface NavItem { key: NavGroup; label: string; views: HomeView[]; subLabels?: string[]; live?: boolean }
const navItems: NavItem[] = [
  { key: 'home', label: '总览', views: ['home'] },
  { key: 'market', label: '市场', views: ['heatmap', 'hotspot'], subLabels: ['大盘云图', '热点漏斗'], live: true },
  { key: 'trade', label: '交易', views: ['reco'] },
  { key: 'retro', label: '复盘', views: ['review', 'scorecard', 'indicators'], subLabels: ['每日复盘', '策略考核', '指标与回测'] },
  { key: 'risk', label: '风险', views: ['risk'] },
]
const viewGroup = new Map<HomeView, NavGroup>(
  navItems.flatMap(item => item.views.map(view => [view, item.key] as [HomeView, NavGroup]))
)

function normalizeView(value: unknown): HomeView {
  return homeViews.includes(value as HomeView) ? value as HomeView : 'home'
}
const activeView = ref<HomeView>(normalizeView(route.query.view))
watch(() => route.query.view, value => { if (value) activeView.value = normalizeView(value) })

const activeGroup = computed<NavGroup>(() => viewGroup.get(activeView.value) || 'home')
// 当前主 Tab 下的二级页；只有一项时不渲染二级栏，避免无意义的一层点击。
const activeSubItems = computed(() => {
  const item = navItems.find(nav => nav.key === activeGroup.value)
  if (!item || !item.subLabels || item.views.length < 2) return []
  return item.views.map((view, index) => ({ view, label: item.subLabels![index] }))
})
// 切换主 Tab 时进入该组第一个二级页；点击当前组则保持已选二级页不跳回。
function selectGroup(item: NavItem) {
  if (activeGroup.value === item.key) return
  activeView.value = item.views[0]
}
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
    <!-- 云图筛选器与快速定位都只服务于大盘云图；其余 Tab 用页面内列表点选，
         侧栏只保留品牌、自选股、数据新鲜度与口径说明。 -->
    <MarketSidebar
      :market="market" :group-by="groupBy" :metric="metric" :period="period"
      :controls="activeView === 'heatmap'"
      :show-search="activeView === 'heatmap'"
      :security-count="itemCount"
      @change="options => { market = options.market; groupBy = options.groupBy; metric = options.metric; period = options.period; setOption() }"
    />

    <section class="heatmap-canvas" :class="{ 'hotspot-mode': activeView !== 'heatmap', 'has-subtabs': activeSubItems.length > 0 }">
      <header class="canvas-header">
        <nav class="workspace-tabs" aria-label="主视图">
          <button
            v-for="item in navItems" :key="item.key" type="button"
            :class="{ active: activeGroup === item.key }"
            :aria-current="activeGroup === item.key ? 'page' : undefined"
            @click="selectGroup(item)"
          >{{ item.label }}</button>
        </nav>
        <div v-if="activeView === 'heatmap'" class="legend"><span>面积＝市值，颜色＝涨跌幅</span><i v-for="value in heatLegend" :key="value" :style="{ background: tileColor(value) }">{{ value > 0 ? `+${value}%` : `${value}%` }}</i></div>
      </header>

      <!-- 二级栏：仅在主 Tab 含多个子页时出现 -->
      <nav v-if="activeSubItems.length" class="workspace-subtabs" aria-label="子视图">
        <button
          v-for="sub in activeSubItems" :key="sub.view" type="button"
          :class="{ active: activeView === sub.view }"
          @click="activeView = sub.view"
        >{{ sub.label }}</button>
        <span v-if="activeView === 'hotspot'" class="sub-caption">数据初筛 → 关系收敛 → AI 产业链分析 → 本地回验</span>
      </nav>
      <Home v-if="activeView === 'home'" />
      <template v-else-if="activeView === 'heatmap'">
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
      <HotspotFunnel v-else-if="activeView === 'hotspot'" />
      <RiskSentinel v-else-if="activeView === 'risk'" />
      <Review v-else-if="activeView === 'review'" />
      <Recommendations v-else-if="activeView === 'reco'" />
      <Scorecard v-else-if="activeView === 'scorecard'" />
      <Indicators v-else-if="activeView === 'indicators'" />
    </section>
  </main>
</template>

<style scoped>
.heatmap-workspace { display:grid; grid-template-columns:212px minmax(0,1fr); width:100vw; height:100vh; overflow:hidden; background:#151f31; color:#edf1f7; }
.heatmap-canvas { display:grid; min-width:0; min-height:0; grid-template-rows:38px minmax(0,1fr); overflow:hidden; }
.heatmap-canvas.has-subtabs { grid-template-rows:38px 30px minmax(0,1fr); }
.canvas-header { display:flex; min-width:0; align-items:center; justify-content:space-between; gap:16px; overflow:hidden; padding:0 8px; border-bottom:1px solid #354157; background:#182234; }
.workspace-tabs { align-self:stretch; display:flex; min-width:0; align-items:stretch; overflow-x:auto; scrollbar-width:none; }
.workspace-tabs::-webkit-scrollbar { display:none; }
.workspace-tabs button { display:flex; min-width:88px; align-items:center; justify-content:center; padding:0 20px; border:0; border-bottom:2px solid transparent; border-radius:0; background:transparent; color:#94a1b5; cursor:pointer; font-size:14px; font-weight:700; letter-spacing:1px; }
.workspace-tabs button:hover { color:#e2e7ef; }
.workspace-tabs button.active { border-bottom-color:#e0b64f; background:#202d41; color:#f0c760; }
/* 二级栏：视觉上明显弱于主栏，避免与主导航争夺注意力 */
.workspace-subtabs { display:flex; min-width:0; align-items:center; gap:2px; overflow-x:auto; padding:0 10px; border-bottom:1px solid #26324a; background:#131e2f; scrollbar-width:none; }
.workspace-subtabs::-webkit-scrollbar { display:none; }
.workspace-subtabs button { padding:3px 12px; border:1px solid transparent; border-radius:2px; background:transparent; color:#8895ab; cursor:pointer; font-size:11.5px; white-space:nowrap; }
.workspace-subtabs button:hover { color:#dbe3ee; }
.workspace-subtabs button.active { border-color:#3a496a; background:#1e2c45; color:#e9c16c; }
.sub-caption { overflow:hidden; margin-left:10px; color:#75839a; font-size:10px; text-overflow:ellipsis; white-space:nowrap; }
.legend { display:flex; min-width:0; align-items:center; justify-content:flex-end; gap:2px; color:#aeb8c9; font-size:12px; white-space:nowrap; }.legend>span { margin-right:8px; overflow:hidden; text-overflow:ellipsis; }.legend i { min-width:43px; padding:4px 5px; color:#fff; font-style:normal; text-align:center; }
.market-notice { position:absolute; z-index:30; top:40px; left:220px; padding:8px 11px; border-left:3px solid #d6a12c; background:#342d1d; color:#e9c986; font-size:12px; }.market-notice.error { border-color:#db4b57; background:#3a2329; color:#ffb1b8; }
.treemap-stage { position:relative; min-width:0; min-height:0; margin:7px 8px 8px; overflow:hidden; background:#151f31; }.treemap-stage.empty { display:grid; place-items:center; }.sector-frame { position:absolute; z-index:5; overflow:hidden; border:1px solid #43516a; pointer-events:none; }.sector-title { position:absolute; z-index:1; top:0; right:0; left:0; display:flex; width:100%; height:19px; min-width:0; align-items:center; gap:4px; overflow:hidden; padding:1px 4px; border:0; border-radius:0; background:#1b2638; color:#d5dce7; cursor:pointer; font-size:11px; line-height:17px; text-align:left; pointer-events:auto; }.sector-title:hover { color:#ffd400; }.sector-title span { flex:1 1 auto; min-width:0; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }.sector-title i { flex:0 1 auto; min-width:0; overflow:hidden; color:#8794a8; font-size:8px; font-style:normal; text-overflow:ellipsis; white-space:nowrap; }
.stock-tile { position:absolute; z-index:4; display:flex; min-width:0; min-height:0; flex-direction:column; align-items:center; justify-content:center; overflow:hidden; padding:1px 2px; border:0; border-radius:0; outline:1px solid rgba(17,27,42,.72); color:#fff; text-align:center; text-shadow:0 1px 1px rgba(0,0,0,.48); transition:filter .1s, outline-color .1s; }.stock-tile:hover { z-index:4; filter:brightness(1.22); outline:2px solid #ffd400; opacity:1; }.stock-tile b,.stock-tile em,.stock-tile small { display:block; max-width:100%; overflow:hidden; font-style:normal; line-height:1.12; text-overflow:ellipsis; white-space:nowrap; }.stock-tile b { font-size:9px; }.stock-tile em { margin-top:1px; font-size:9px; font-weight:700; }.stock-tile small { margin-top:2px; opacity:.82; font-size:8px; }.stock-tile.md b { font-size:11px; }.stock-tile.md em { font-size:10px; }.stock-tile.lg b { font-size:14px; }.stock-tile.lg em { margin-top:3px; font-size:13px; }.stock-tile.xl b { font-size:20px; }.stock-tile.xl em { margin-top:5px; font-size:18px; }.stock-tile.xl small { font-size:10px; }.loading { color:#aeb8c9; font-size:13px; }
@media (max-width:900px) { .heatmap-workspace { grid-template-columns:1fr; height:auto; min-height:100vh; overflow:visible; }.heatmap-canvas { grid-template-rows:35px 70vh; }.heatmap-canvas.hotspot-mode { grid-template-rows:35px auto; overflow:visible; }.legend>span { display:none; }.treemap-stage { min-height:0; } }
@media (max-width:600px) { .legend { display:none; }.heatmap-canvas { grid-template-rows:35px 72vh; }.market-notice { left:8px; right:8px; }.stock-tile.xl b { font-size:15px; }.stock-tile.xl em { font-size:14px; } }
</style>
