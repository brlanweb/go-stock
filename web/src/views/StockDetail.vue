<script setup lang="ts">
import { ref, onMounted, onUnmounted, watch } from 'vue'
import { init, dispose, type Chart } from 'klinecharts'
import { api, fmt, fmtPct, fmtBig, pctClass, type Quote } from '../api'

const props = defineProps<{ symbol: string }>()

const quote = ref<Quote | null>(null)
const tab = ref<'day' | 'week' | 'month'>('day')
const errMsg = ref('')
const syncMsg = ref('')
const syncing = ref(false)
let klChart: Chart | null = null

async function refreshQuote() {
  try {
    quote.value = await api.quote(props.symbol)
    errMsg.value = ''
  } catch (e: any) {
    errMsg.value = e.message
  }
}

async function drawKline(period: 'day' | 'week' | 'month') {
  const klines = await api.kline(props.symbol, period, 'qfq', 300)
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
    klChart = init('kl-chart', { styles: chartStyles })
    klChart?.createIndicator('VOL')
    klChart?.createIndicator('MA', false, { id: 'candle_pane' })
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
  grid: { horizontal: { color: '#21262d' }, vertical: { color: '#21262d' } },
  candle: {
    bar: { upColor: '#121820', downColor: '#f3f5f7', upBorderColor: '#121820', downBorderColor: '#d7dde3', upWickColor: '#121820', downWickColor: '#d7dde3' },
    priceMark: { last: { upColor: '#121820', downColor: '#f3f5f7' } }
  },
  indicator: {
    bars: [{ upColor: '#121820', downColor: '#f3f5f7' }]
  }
} as any

watch(() => props.symbol, () => {
  refreshQuote()
  nextTickDraw()
})

onMounted(async () => {
  refreshQuote()
  await nextTickDraw()
})

onUnmounted(() => {
  if (klChart) { dispose('kl-chart'); klChart = null }
})
</script>

<template>
  <div>
    <router-link to="/" class="dim" style="font-size:13px">← 返回市场云图</router-link>

    <!-- 报价头 -->
    <div class="panel" style="margin-top:8px" v-if="quote">
      <div style="display:flex;align-items:baseline;gap:16px;flex-wrap:wrap">
        <h2>{{ quote.name }} <span class="dim" style="font-size:13px">{{ quote.symbol }}</span></h2>
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
      <div id="kl-chart" style="height:420px"></div>
    </div>
  </div>
</template>

<style scoped>
.chart-toolbar { display:flex; align-items:center; justify-content:space-between; gap:12px; margin-bottom:12px; }
.sync-actions { display:flex; gap:8px; }.sync-msg { margin:-3px 0 10px; color:var(--text-dim); font-size:12px; }
@media (max-width:600px) { .chart-toolbar { align-items:flex-start; flex-direction:column; } }
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
