<script setup lang="ts">
import { ref, onMounted, onUnmounted, watch } from 'vue'
import { init, dispose, registerIndicator, CandleType, LineType, type Chart, type KLineData } from 'klinecharts'
import { api, fmt, fmtPct, fmtBig, pctClass, type Quote } from '../api'

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

const quote = ref<Quote | null>(null)
const tab = ref<'day' | 'week' | 'month'>('day')
const errMsg = ref('')
const syncMsg = ref('')
const syncing = ref(false)
const watched = ref(false)
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
  loadWatchState()
  nextTickDraw()
})

onMounted(async () => {
  refreshQuote()
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
      <div style="display:flex;align-items:baseline;gap:16px;flex-wrap:wrap">
        <h2>{{ quote.name }} <span class="dim" style="font-size:13px">{{ quote.symbol }}</span></h2>
        <button class="watch-button" :class="{ active: watched }" @click="toggleWatch">{{ watched ? '已加入自选' : '加入自选' }}</button>
        <span :class="pctClass(quote.change_pct)" style="font-size:28px;font-weight:700">{{ fmt(quote.price) }}</span>
        <span :class="pctClass(quote.change_pct)" style="font-size:16px">
          {{ fmt(quote.change_amount) }} ({{ fmtPct(quote.change_pct) }})
        </span>
        <span class="dim" style="font-size:12px">
          本地快照 · {{ new Date(quote.fetched_at).toLocaleString('zh-CN', { hour12: false }) }}
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
      </div>

      <!-- 五档盘口 -->
      <div v-if="quote.asks?.length || quote.bids?.length" class="order-book">
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
        </div>
      </div>
      <div v-if="syncMsg" class="sync-msg">{{ syncMsg }}</div>
      <div id="kl-chart" class="kline-chart"></div>
    </div>
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
.watch-button { padding:5px 9px; border:1px solid var(--border); background:transparent; color:var(--text-dim); }.watch-button.active { border-color:#d6a12c; color:#9a6a00; }
.chart-toolbar { display:flex; align-items:center; justify-content:space-between; gap:12px; margin-bottom:6px; }
.sync-actions { display:flex; gap:8px; }.sync-msg { margin:0 0 6px; color:#687280; font-size:12px; }
@media (max-width:900px) { .stock-detail { min-height:auto; grid-template-rows:auto auto auto; }.stock-detail .panel { min-width:0; overflow:hidden; }.kline-chart { width:100%; height:62vh; min-height:480px; } }
@media (max-width:600px) { .chart-toolbar { align-items:flex-start; flex-direction:column; }.kline-chart { height:65vh; min-height:420px; } }
.quote-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(140px, 1fr));
  gap: 8px 16px;
  margin-top: 12px;
  font-size: 13px;
}
.quote-grid .dim { margin-right: 6px; font-size: 12px; }
.order-book {
  margin-top: 12px;
  display: grid;
  grid-template-columns: 1fr;
  max-width: 260px;
  font-size: 12px;
  border: 1px solid var(--border);
  border-radius: 6px;
  padding: 8px 12px;
}
.ob-row { display: flex; justify-content: space-between; padding: 2px 0; }
.ob-sep { border-top: 1px dashed var(--border); margin: 4px 0; }
</style>
