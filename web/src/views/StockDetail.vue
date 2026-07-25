<script setup lang="ts">
import { computed, ref, onMounted, onUnmounted, watch } from 'vue'
import { useRouter } from 'vue-router'
import { init, dispose, registerIndicator, CandleType, LineType, type Chart, type KLineData } from 'klinecharts'
import { api, fmt, fmtPct, fmtBig, pctClass, type Quote, type StockDetailPayload } from '../api'
import AgentPanel from '../components/AgentPanel.vue'

// Vegas 隧道：EMA144 / EMA169（过滤隧道）+ EMA576 / EMA676（趋势隧道）。
const VEGAS_PERIODS = [144, 169, 576, 676]
const VEGAS_COLORS = ['#e6a23c', '#f2c96b', '#409eff', '#7cc0ff']
let vegasRegistered = false

function registerVegas() {
  if (vegasRegistered) return
  registerIndicator({
    name: 'VEGAS',
    shortName: 'VEGAS',
    calcParams: VEGAS_PERIODS,
    figures: VEGAS_PERIODS.map(p => ({ key: `ema${p}`, title: `EMA${p}: `, type: 'line' })),
    styles: {
      lines: VEGAS_COLORS.map(color => ({ color, size: 1, smooth: false, style: LineType.Solid, dashedValue: [2, 2] }))
    },
    calc: (dataList: KLineData[]) => {
      const multipliers = VEGAS_PERIODS.map(p => 2 / (p + 1))
      const emas: number[] = new Array(VEGAS_PERIODS.length).fill(NaN)
      return dataList.map((k, index) => {
        const result: Record<string, number> = {}
        VEGAS_PERIODS.forEach((p, i) => {
          if (index === 0) emas[i] = k.close
          else emas[i] = k.close * multipliers[i] + emas[i] * (1 - multipliers[i])
          if (index + 1 >= p) result[`ema${p}`] = emas[i]
        })
        return result
      })
    }
  })
  vegasRegistered = true
}


const props = defineProps<{ symbol: string }>()
const router = useRouter()
const quote = ref<Quote | null>(null)
const detail = ref<StockDetailPayload | null>(null)
const tab = ref<'day' | 'week' | 'month'>('day')
const errMsg = ref('')
const syncMsg = ref('')
const syncing = ref(false)
const watched = ref(false)
const agentOpen = ref(false)
const conceptsHover = ref(false)
const VISIBLE_CONCEPT_COUNT = 5
const visibleConcepts = computed(() => detail.value?.concepts.slice(0, VISIBLE_CONCEPT_COUNT) || [])
const hiddenConcepts = computed(() => detail.value?.concepts.slice(VISIBLE_CONCEPT_COUNT) || [])
let klChart: Chart | null = null

async function loadWatchState() {
  try {
    const items = await api.watchlist()
    watched.value = items.some(item => typeof item === 'string' ? item === props.symbol : item.symbol === props.symbol)
  } catch { watched.value = false }
}

async function toggleWatch() {
  try {
    if (watched.value) await api.delWatch(props.symbol)
    else await api.addWatch(props.symbol)
    watched.value = !watched.value
    window.dispatchEvent(new CustomEvent('gostock:watchlist-changed', { detail: { symbol: props.symbol, watched: watched.value } }))
  } catch (e: any) { errMsg.value = e.message || '自选操作失败' }
}

async function refreshQuote() {
  try {
    quote.value = await api.quote(props.symbol)
    errMsg.value = ''
  } catch (e: any) {
    errMsg.value = e.message
  }
}

async function loadDetail() {
  try {
    detail.value = await api.stockDetail(props.symbol)
  } catch (e: any) {
    // 详情接口失败不影响行情展示
    console.warn('loadDetail', e)
  }
}

function openSector(code: string) {
  router.push(`/sector/${encodeURIComponent(code)}`)
}

async function drawKline(period: 'day' | 'week' | 'month') {
  const klines = await api.kline(props.symbol, period, 'qfq', 700)
  if (!klChart) return
  klChart.applyNewData(klines.map(k => ({
    timestamp: new Date(k.date).getTime(),
    open: k.open, high: k.high, low: k.low, close: k.close,
    volume: k.volume, turnover: k.amount
  })))
}

async function switchTab(t: 'day' | 'week' | 'month') {
  tab.value = t
  await nextTickDraw()
}

async function nextTickDraw() {
  await new Promise(r => setTimeout(r))
  if (!klChart) {
    registerVegas()
    klChart = init('kl-chart', { styles: chartStyles })
    klChart?.createIndicator('VOL')
    klChart?.createIndicator('VEGAS', true, { id: 'candle_pane' })
  }
  await drawKline(tab.value)
}

async function syncHistory(mode: 'latest' | 'missing' | 'full') {
  syncing.value = true
  syncMsg.value = ''
  try {
    await api.syncStock(props.symbol, mode)
    syncMsg.value = mode === 'full' ? '历史全量同步已在后台启动' : '数据同步已在后台启动'
  } catch (e: any) {
    syncMsg.value = e.message || '同步启动失败'
  } finally {
    syncing.value = false
  }
}

const chartStyles = {
  grid: {
    show: true,
    horizontal: { show: true, color: '#d9dee5', size: 1, style: 'dashed', dashedValue: [2, 2] },
    vertical: { show: true, color: '#e4e8ed', size: 1, style: 'dashed', dashedValue: [2, 2] }
  },
  candle: {
    type: CandleType.CandleUpStroke,
    bar: {
      upColor: '#ffffff', downColor: '#111820', noChangeColor: '#7b8490',
      upBorderColor: '#111820', downBorderColor: '#111820', noChangeBorderColor: '#7b8490',
      upWickColor: '#111820', downWickColor: '#111820', noChangeWickColor: '#7b8490'
    },
    priceMark: { last: { upColor: '#ffffff', downColor: '#111820', noChangeColor: '#7b8490' } }
  },
  indicator: {
    bars: [{ upColor: '#d2d8df', downColor: '#111820', noChangeColor: '#7b8490', borderColor: '#111820', borderSize: 1, borderStyle: 'solid', borderDashedValue: [], borderRadius: 0 }]
  },
  xAxis: { tickText: { color: '#56606d' }, axisLine: { color: '#bfc6cf' }, tickLine: { color: '#bfc6cf' } },
  yAxis: { tickText: { color: '#56606d' }, axisLine: { color: '#bfc6cf' }, tickLine: { color: '#bfc6cf' } }
} as any

watch(() => props.symbol, () => {
  refreshQuote()
  loadDetail()
  loadWatchState()
  nextTickDraw()
})

onMounted(async () => {
  refreshQuote()
  loadDetail()
  loadWatchState()
  await nextTickDraw()
})

onUnmounted(() => {
  if (klChart) { dispose('kl-chart'); klChart = null }
})
</script>

<template>
  <div class="stock-detail">
    <div class="detail-topbar">
      <router-link to="/" class="back-link">← 返回市场云图</router-link>
    </div>

    <!-- 报价头 -->
    <div class="panel quote-panel" v-if="quote">
      <div class="quote-header">
        <h2 class="stock-title">{{ quote.name }} <span class="dim code">{{ quote.symbol }}</span></h2>
        <button class="watch-button" :class="{ active: watched }" @click="toggleWatch">{{ watched ? '已加入自选' : '加入自选' }}</button>
        <span class="price-now" :class="pctClass(quote.change_pct)">{{ fmt(quote.price) }}</span>
        <span class="price-change" :class="pctClass(quote.change_pct)">
          {{ fmt(quote.change_amount) }} ({{ fmtPct(quote.change_pct) }})
        </span>
      </div>
      <div class="quote-grid">
        <div><span class="dim">今开</span> {{ fmt(quote.open) }}</div>
        <div><span class="dim">最高</span> {{ fmt(quote.high) }}</div>
        <div><span class="dim">最低</span> {{ fmt(quote.low) }}</div>
        <div><span class="dim">昨收</span> {{ fmt(quote.pre_close) }}</div>
        <div><span class="dim">成交量</span> {{ fmtBig(quote.volume) }}</div>
        <div><span class="dim">成交额</span> {{ fmtBig(quote.amount) }}</div>
        <div><span class="dim">量比</span> {{ fmt(quote.volume_ratio) }}</div>
        <div><span class="dim">换手</span> {{ quote.turnover_rate != null ? fmt(quote.turnover_rate) + '%' : '-' }}</div>
        <div><span class="dim">振幅</span> {{ quote.amplitude != null ? fmt(quote.amplitude) + '%' : '-' }}</div>
        <div><span class="dim">PE(动)</span> {{ fmt(quote.pe_ratio) }}</div>
        <div><span class="dim">PB</span> {{ fmt(quote.pb_ratio) }}</div>
        <div><span class="dim">总市值</span> {{ fmtBig(quote.total_mv) }}</div>
        <div><span class="dim">流通市值</span> {{ fmtBig(quote.circ_mv) }}</div>
        <div><span class="dim">52周高</span> {{ fmt(quote.high_52w) }}</div>
        <div><span class="dim">52周低</span> {{ fmt(quote.low_52w) }}</div>
        <div v-if="detail"><span class="dim">所属行业</span>
          <button v-if="detail.industry" class="tag industry inline" @click="openSector(detail.industry_code || ('industry:' + detail.industry))">{{ detail.industry }} ›</button>
          <span v-else class="dim">未分类</span>
        </div>
        <div v-if="detail" class="concepts-cell" @mouseleave="conceptsHover=false">
          <span class="dim">所属概念</span>
          <div class="concepts-wrap" @mouseenter="conceptsHover=true">
            <button v-for="c in visibleConcepts" :key="c.sector_code" class="tag concept inline" @click="openSector(c.sector_code)">{{ c.sector_name }} ›</button>
            <button v-if="detail.concepts.length > VISIBLE_CONCEPT_COUNT" class="tag concept inline more" :title="hiddenConcepts.map(c=>c.sector_name).join('、')" @mouseenter="conceptsHover=true">{{ detail.concepts.length - VISIBLE_CONCEPT_COUNT }} 个更多 ›</button>
            <div v-if="conceptsHover && detail.concepts.length > VISIBLE_CONCEPT_COUNT" class="concepts-popup">
              <button v-for="c in hiddenConcepts" :key="c.sector_code" class="tag concept" @click="openSector(c.sector_code)">{{ c.sector_name }} ›</button>
            </div>
          </div>
        </div>
      </div>

      <details class="order-book-details" v-if="quote.asks?.length || quote.bids?.length">
        <summary>五档盘口</summary>
        <div class="order-book">
          <div v-for="(a, i) in (quote.asks || []).slice().reverse()" :key="'a'+i" class="ob-row">
            <span class="dim">卖{{ (quote.asks || []).length - i }}</span>
            <span class="up">{{ fmt(a.price) }}</span>
            <span>{{ fmtBig(a.volume) }}</span>
          </div>
          <div class="ob-sep"></div>
          <div v-for="(b, i) in quote.bids || []" :key="'b'+i" class="ob-row">
            <span class="dim">买{{ i + 1 }}</span>
            <span class="down">{{ fmt(b.price) }}</span>
            <span>{{ fmtBig(b.volume) }}</span>
          </div>
        </div>
      </details>
    </div>
    <div v-if="errMsg" class="panel dim">{{ errMsg }}</div>

    <!-- 图表 -->
    <div class="panel">
      <div class="chart-toolbar">
        <div style="display:flex;gap:8px">
          <button :class="{ ghost: tab !== 'day' }" @click="switchTab('day')">日K</button>
          <button :class="{ ghost: tab !== 'week' }" @click="switchTab('week')">周K</button>
          <button :class="{ ghost: tab !== 'month' }" @click="switchTab('month')">月K</button>
        </div>
        <div class="sync-actions">
          <button class="ghost" :disabled="syncing" title="同步最新或缺失交易日" @click="syncHistory('missing')">同步缺失</button>
          <button :disabled="syncing" title="从上市以来重新拉取日K" @click="syncHistory('full')">历史全量</button>
          <button class="ghost" title="AI 行情助理" @click="agentOpen = true">Agent</button>
        </div>
      </div>
      <div v-if="syncMsg" class="sync-msg">{{ syncMsg }}</div>
      <div id="kl-chart" class="kline-chart"></div>
    </div>

    <AgentPanel :detail="detail" v-if="agentOpen" />
  </div>
</template>

<style scoped>
.stock-detail { display:grid; min-width:0; width:100%; min-height:calc(100vh - 42px); grid-template-rows:auto auto minmax(460px,1fr); gap:8px; padding:6px 0 8px; }
.detail-topbar { min-height:22px; }
.back-link { display:inline-block; color:#56606d; font-size:13px; }
.stock-detail .panel { margin:0; padding:10px 12px; border:1px solid #cdd3db; border-radius:3px; background:#fff; color:#121820; box-shadow:none; }
.quote-panel { margin-top:0; }
.stock-detail .dim { color:#687280; }
.stock-detail .up { color:#bd2e35; }.stock-detail .down { color:#0f765d; }
.kline-chart { width:100%; height:calc(100vh - 320px); min-height:460px; background:#eef1f4; }
.chart-toolbar { display:flex; align-items:center; justify-content:space-between; gap:12px; margin-bottom:6px; }
.sync-actions { display:flex; gap:8px; }.sync-msg { margin:0 0 6px; color:#687280; font-size:12px; }
@media (max-width:900px) { .stock-detail { min-height:auto; grid-template-rows:auto auto auto; }.stock-detail .panel { min-width:0; overflow:hidden; }.kline-chart { width:100%; height:62vh; min-height:480px; } }
@media (max-width:600px) { .chart-toolbar { align-items:flex-start; flex-direction:column; }.kline-chart { height:65vh; min-height:420px; } }
.quote-header { display:flex; align-items:center; gap:14px; flex-wrap:wrap; padding-bottom:6px; border-bottom:1px dashed #cdd3db; margin-bottom:4px; }
.stock-title { margin:0; font-size:18px; line-height:1.4; font-weight:600; display:inline-flex; align-items:baseline; gap:6px; }
.stock-title .code { font-size:12px; font-weight:400; }
.price-now { font-size:24px; font-weight:700; line-height:1; }
.price-change { font-size:14px; font-weight:500; line-height:1; }
.watch-button { padding:4px 10px; border:1px solid var(--border); background:transparent; color:var(--text-dim); font-size:12px; }
.watch-button.active { border-color:#d6a12c; color:#9a6a00; }
.quote-grid {
  display: grid;
  grid-template-columns: repeat(6, minmax(0, 1fr));
  grid-auto-rows: 22px;
  column-gap: 18px;
  row-gap: 4px;
  margin-top: 4px;
  font-size: 13px;
  align-items: center;
}
.quote-grid > div { display:flex; align-items:center; min-width:0; min-height:20px; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; gap:4px; }
.quote-grid > div:nth-child(13) { grid-column: span 1; }
.quote-grid > div:nth-child(14) { grid-column: span 1; }
.quote-grid > div:nth-child(15) { grid-column: span 1; }
.quote-grid > div:nth-child(16),
.quote-grid > div:nth-child(17) { grid-column: span 3; flex-wrap:wrap; white-space:normal; align-items:center; min-height:22px; overflow:visible; }
.tag.inline { padding:1px 8px; border:1px solid #c8d3df; background:#f3f6f9; color:#1c2a3a; font-size:12px; cursor:pointer; border-radius:3px; line-height:1.6; }
.tag.inline:hover { background:#e2eaf2; }
.tag.industry { border-color:#9ec5ff; color:#0f3d75; background:#e7f1ff; }
.tag.concept { border-color:#d8a7d8; color:#5a1f5a; background:#faeefa; }
.tag.more { background:#eef2f7; border-style:dashed; }
.concepts-cell { position:relative; min-width:0; overflow:visible; }
.concepts-wrap { display:flex; flex-wrap:wrap; gap:4px; min-width:0; flex:1; }
.concepts-popup { position:absolute; top:100%; left:64px; z-index:5; display:flex; flex-wrap:wrap; gap:6px; max-width:560px; padding:8px 10px; border:1px solid #c8d3df; border-radius:4px; background:#fff; box-shadow:0 12px 28px rgba(15,28,48,.18); }
.concepts-popup .tag { padding:2px 8px; border:1px solid #c8d3df; background:#f3f6f9; color:#1c2a3a; font-size:12px; cursor:pointer; border-radius:3px; }
.concepts-popup .tag:hover { background:#e2eaf2; }
.order-book {
  margin-top: 6px;
  display: grid;
  grid-template-columns: 1fr;
  max-width: 240px;
  font-size: 12px;
  border: 1px solid var(--border);
  border-radius: 6px;
  padding: 8px 12px;
}
.order-book-details { margin-top:6px; font-size:12px; color:#687280; }
.order-book-details > summary { cursor:pointer; user-select:none; padding:4px 0; list-style:revert; }
.order-book-details[open] > summary { padding-bottom:6px; }
.ob-row { display: flex; justify-content: space-between; padding: 1px 0; }
.ob-sep { border-top: 1px dashed var(--border); margin: 4px 0; }
.tags-panel { display:flex; flex-direction:column; gap:8px; padding:10px 12px; }
.tags-row { display:flex; align-items:center; gap:8px; flex-wrap:wrap; }
.tags-row .dim { min-width:64px; font-size:12px; }
.tag { padding:3px 9px; border:1px solid #c8d3df; background:#f3f6f9; color:#1c2a3a; font-size:12px; cursor:pointer; border-radius:3px; }
.tag:hover { background:#e2eaf2; }
.tag.industry { border-color:#9ec5ff; color:#0f3d75; background:#e7f1ff; }
.tag.concept { border-color:#d8a7d8; color:#5a1f5a; background:#faeefa; }
.concepts { display:flex; flex-wrap:wrap; gap:6px; }
.tag-inline { display:flex; align-items:center; gap:8px; margin-top:10px; flex-wrap:wrap; }
.tag-inline .dim { font-size:12px; min-width:64px; }
.concepts-row { position:relative; }
.concepts-wrap { position:relative; }
.concepts-popup { position:absolute; top:calc(100% + 4px); left:64px; z-index:5; display:flex; flex-wrap:wrap; gap:6px; max-width:520px; padding:10px 12px; border:1px solid #c8d3df; border-radius:4px; background:#fff; box-shadow:0 12px 28px rgba(15,28,48,.18); }
.concepts-popup .tag { font-size:12px; }
</style>
