<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { api, fmt, fmtPct, pctClass, type Recommendation, type RecommendationPerformance, type RecommendationStats } from '../api'
import MarketSidebar from '../components/MarketSidebar.vue'

const router = useRouter()
const dates = ref<string[]>([])
const activeDate = ref('')
const items = ref<Recommendation[]>([])
const performance = ref<RecommendationPerformance[]>([])
const loading = ref(false)
const message = ref('')
const running = ref(false)
const stats = ref<RecommendationStats | null>(null)

function fmtSigned(value: number | null | undefined, suffix = '%') {
  if (value == null) return '—'
  return `${value >= 0 ? '+' : ''}${value.toFixed(2)}${suffix}`
}

// 最近 5 个推荐日全部推荐股（通常 15 只）的总口径：
// 每只按加入日开盘价买入、追踪窗口收盘价（未满 5 个交易日为当前收盘）计算后求和。
const overall = computed(() => {
  let stocks = 0
  let sum = 0
  let counted = 0
  for (const p of performance.value) {
    stocks += p.stocks
    if (p.sum_change_pct != null) {
      sum += p.sum_change_pct
      counted += p.stocks
    }
  }
  return { stocks, counted, sum: counted > 0 ? sum : null, avg: counted > 0 ? sum / counted : null }
})

// 30 个交易日图表：每个推荐日 3 只股票按同一追踪口径的涨跌幅求和。
const chartData = ref<RecommendationPerformance[]>([])

const chart = computed(() => {
  const rows = [...chartData.value].reverse().filter(p => p.sum_change_pct != null)
  const values = rows.map(p => p.sum_change_pct as number)
  const max = Math.max(...values, 0.5)
  const min = Math.min(...values, -0.5)
  const span = max - min
  const width = 940
  const height = 180
  const padTop = 14
  const padBottom = 24
  const plotH = height - padTop - padBottom
  const zeroY = padTop + (max / span) * plotH
  const step = rows.length > 0 ? width / rows.length : width
  const barW = Math.max(4, Math.min(26, step * 0.55))
  const bars = rows.map((p, i) => {
    const value = p.sum_change_pct as number
    const h = Math.max(1, (Math.abs(value) / span) * plotH)
    return {
      date: p.date,
      label: p.date.slice(5),
      value,
      finished: p.finished,
      x: step * i + (step - barW) / 2,
      y: value >= 0 ? zeroY - h : zeroY,
      w: barW,
      h,
      up: value >= 0
    }
  })
  const labelEvery = Math.max(1, Math.ceil(rows.length / 10))
  return { width, height, zeroY, bars, step, labelEvery, max, min }
})

async function loadDate(date: string) {
  activeDate.value = date
  loading.value = true
  try {
    items.value = await api.recommendations(date)
  } catch (e: any) {
    items.value = []
    message.value = e?.message || '加载失败'
  } finally {
    loading.value = false
  }
}

async function refreshDates() {
  stats.value = await api.recommendationStats(60).catch(() => null)
  performance.value = await api.recommendationPerformance(5).catch(() => [] as RecommendationPerformance[])
  chartData.value = await api.recommendationPerformance(30).catch(() => [] as RecommendationPerformance[])
  dates.value = await api.recommendationHistory(365).catch(() => [] as string[])
  if (dates.value.length) await loadDate(dates.value[0])
}

async function runAnalysis() {
  running.value = true
  message.value = ''
  try {
    await api.runRecommendations()
    message.value = '分析已启动，约 10 秒后自动刷新'
    window.setTimeout(refreshDates, 10000)
  } catch (e: any) {
    message.value = e?.message || '分析启动失败'
  } finally {
    running.value = false
  }
}

function openStock(symbol: string) {
  router.push(`/stock/${symbol}`)
}

onMounted(refreshDates)
</script>

<template>
  <div class="reco-shell">
    <MarketSidebar :controls="false" />
    <main class="reco-content">
      <header class="reco-header">
        <div class="reco-title"><strong>AI 趋势推荐 · 成功率评估</strong><small>口径：推荐日开盘价买入 → 第 5 个交易日收盘价冻结</small></div>
        <div class="reco-tools">
          <button class="run-btn" :disabled="running" @click="runAnalysis">{{ running ? '分析中…' : '立即生成' }}</button>
        </div>
      </header>
      <p v-if="message" class="reco-message">{{ message }}</p>

      <div v-if="stats && stats.frozen_picks > 0" class="stats-bar">
        <div class="stat-cell hero">
          <small>个股胜率<em>近 {{ stats.total_days }} 个推荐日</em></small>
          <b :class="(stats.win_rate ?? 0) >= 50 ? 'up' : 'down'">{{ stats.win_rate == null ? '—' : stats.win_rate.toFixed(1) + '%' }}</b>
          <span>{{ stats.wins }} 胜 / {{ stats.frozen_picks }} 只已冻结</span>
        </div>
        <div class="stat-cell hero">
          <small>单日组合胜率<em>3 只求和为正</em></small>
          <b :class="(stats.day_win_rate ?? 0) >= 50 ? 'up' : 'down'">{{ stats.day_win_rate == null ? '—' : stats.day_win_rate.toFixed(1) + '%' }}</b>
          <span>{{ stats.day_wins }} 胜 / {{ stats.day_frozen }} 日已冻结</span>
        </div>
        <div class="stat-cell"><small>平均收益</small><b :class="pctClass(stats.avg_change_pct)">{{ fmtSigned(stats.avg_change_pct) }}</b></div>
        <div class="stat-cell"><small>中位数</small><b :class="pctClass(stats.median_pct)">{{ fmtSigned(stats.median_pct) }}</b></div>
        <div class="stat-cell"><small>盈亏比</small><b>{{ stats.avg_win_pct != null && stats.avg_loss_pct != null && stats.avg_loss_pct !== 0 ? (stats.avg_win_pct / -stats.avg_loss_pct).toFixed(2) : '—' }}</b><span>均盈 {{ fmtSigned(stats.avg_win_pct) }} / 均亏 {{ fmtSigned(stats.avg_loss_pct) }}</span></div>
        <div class="stat-cell"><small>累计收益点数</small><b :class="pctClass(stats.sum_change_pct)">{{ fmtSigned(stats.sum_change_pct, ' 点') }}</b><span>追踪中 {{ stats.tracking_picks }} 只未计入</span></div>
        <div class="stat-cell"><small>最佳</small><b class="up">{{ fmtSigned(stats.best_pct) }}</b><span>{{ stats.best_name || '—' }}</span></div>
        <div class="stat-cell"><small>最差</small><b class="down">{{ fmtSigned(stats.worst_pct) }}</b><span>{{ stats.worst_name || '—' }}</span></div>
      </div>

      <div v-if="chart.bars.length" class="chart-strip">
        <div class="perf-caption"><b>近 30 个推荐日每日组合涨跌</b><small>每日 3 只推荐股按加入日开盘价买入的涨跌幅求和；红涨绿跌，浅色为追踪中，点击柱体查看当日明细</small></div>
        <svg class="perf-chart" :viewBox="`0 0 ${chart.width} ${chart.height}`" preserveAspectRatio="none" role="img" aria-label="近30个推荐日组合涨跌柱状图">
          <line :x1="0" :y1="chart.zeroY" :x2="chart.width" :y2="chart.zeroY" class="zero-line" />
          <g v-for="(bar, i) in chart.bars" :key="bar.date" class="bar-group" @click="loadDate(bar.date)">
            <rect :x="chart.step * i" y="0" :width="chart.step" :height="chart.height" class="bar-hit" />
            <rect :x="bar.x" :y="bar.y" :width="bar.w" :height="bar.h" :class="['bar', bar.up ? 'up' : 'down', { tracking: !bar.finished, active: bar.date === activeDate }]">
              <title>{{ bar.date }}：{{ bar.value >= 0 ? '+' : '' }}{{ bar.value.toFixed(2) }} 点{{ bar.finished ? '（已冻结）' : '（追踪中）' }}</title>
            </rect>
            <text v-if="i % chart.labelEvery === 0" :x="chart.step * i + chart.step / 2" :y="chart.height - 8" class="bar-label">{{ bar.label }}</text>
          </g>
        </svg>
      </div>

      <div v-if="performance.length" class="perf-strip">
        <div class="perf-caption"><b>近 5 个推荐日组合表现</b><small>每只按加入日开盘价买入，追踪 5 个交易日后冻结</small></div>
        <div class="perf-cards">
          <div class="perf-card total">
            <small>合计 {{ overall.stocks }} 只<i class="tracking">近 5 个推荐日</i></small>
            <b :class="pctClass(overall.sum)">{{ overall.sum == null ? '—' : `${overall.sum >= 0 ? '+' : ''}${overall.sum.toFixed(2)}` }}<em> 点</em></b>
            <span :class="pctClass(overall.avg)">均 {{ fmtPct(overall.avg) }}</span>
          </div>
          <button v-for="p in performance" :key="p.date" class="perf-card" :class="{ active: p.date === activeDate }" @click="loadDate(p.date)">
            <small>{{ p.date.slice(5) }}<i v-if="p.finished" class="frozen">已冻结</i><i v-else class="tracking">第 {{ p.tracked_days }} 天</i></small>
            <b :class="pctClass(p.sum_change_pct)">{{ p.sum_change_pct == null ? '—' : `${p.sum_change_pct >= 0 ? '+' : ''}${p.sum_change_pct.toFixed(2)}` }}<em> 点</em></b>
            <span :class="pctClass(p.avg_change_pct)">均 {{ fmtPct(p.avg_change_pct) }}</span>
          </button>
        </div>
      </div>

      <div class="reco-body">
        <aside class="date-list">
          <button v-for="date in dates" :key="date" :class="{ active: date === activeDate }" @click="loadDate(date)">{{ date }}</button>
          <p v-if="!dates.length" class="empty">暂无历史记录，请配置模型后生成</p>
        </aside>

        <section class="reco-table">
          <div v-if="loading" class="empty">加载中…</div>
          <template v-else-if="items.length">
            <div class="reco-row head"><span>排名</span><span>股票</span><span>加入日开盘</span><span>窗口收盘</span><span>涨跌幅</span><span>动量分</span><span>核心依据</span><span>板块</span></div>
            <button v-for="item in items" :key="item.symbol" class="reco-row" @click="openStock(item.symbol)">
              <span class="rank">{{ item.rank }}</span>
              <span class="stock"><b>{{ item.name }}</b><small>{{ item.code }}</small></span>
              <span>{{ fmt(item.entry_price) }}</span>
              <span>{{ fmt(item.latest_price) }}<small v-if="item.tracked_days > 0" class="track-tag">{{ item.tracked_days >= 5 ? '已冻结' : `第${item.tracked_days}天` }}</small></span>
              <span :class="pctClass(item.change_pct)">{{ fmtPct(item.change_pct) }}</span>
              <span class="score">{{ item.probability.toFixed(1) }}</span>
              <span class="reason">{{ item.reason }}</span>
              <span class="sector">{{ item.sector }}</span>
            </button>
            <p class="disclaimer">说明：涨跌幅按“加入日开盘价买入，加入日起第 5 个交易日收盘价冻结”口径计算；动量分仅为基于历史价格动量的相对排序，非真实统计概率；历史表现不代表未来收益。模型：{{ items[0].model || '—' }}</p>
          </template>
          <div v-else class="empty">该日期暂无推荐数据</div>
        </section>
      </div>
    </main>
  </div>
</template>

<style scoped>
.reco-shell { display:grid; grid-template-columns:212px minmax(0,1fr); width:100vw; height:100vh; overflow:hidden; background:#0f1826; color:#e7ecf4; }
.reco-content { display:flex; min-width:0; min-height:0; flex-direction:column; padding:0 14px 14px; overflow:hidden; }
.reco-header { display:flex; align-items:center; justify-content:space-between; padding:12px 2px; border-bottom:1px solid #26324a; }
.reco-title strong { font-size:16px; }.reco-title small { margin-left:10px; color:#8895ab; font-size:12px; }
.run-btn { padding:6px 14px; border:1px solid #3a496a; border-radius:0; background:#233150; color:#e7ecf4; font-size:13px; cursor:pointer; }.run-btn:disabled { cursor:wait; opacity:.6; }
.reco-message { margin:8px 2px 0; color:#d8b967; font-size:12px; }
.stats-bar { display:grid; grid-template-columns:repeat(auto-fit,minmax(128px,1fr)); gap:8px; margin-top:12px; }
.stat-cell { display:flex; flex-direction:column; gap:4px; padding:10px 12px; border:1px solid #26324a; background:#131e33; }
.stat-cell.hero { border-color:#3d4f77; background:#1c2a47; }
.stat-cell>small { display:flex; align-items:baseline; gap:6px; color:#8895ab; font-size:11px; }
.stat-cell>small em { color:#5f6d85; font-size:9px; font-style:normal; }
.stat-cell>b { font-size:20px; font-variant-numeric:tabular-nums; }
.stat-cell.hero>b { font-size:24px; }
.stat-cell>span { color:#8895ab; font-size:10px; font-variant-numeric:tabular-nums; }
.stat-cell .up { color:#ef6a72; }.stat-cell .down { color:#55b996; }.stat-cell .dim { color:#93a0b6; }
.perf-strip { margin-top:12px; padding:10px 12px; border:1px solid #26324a; background:#131e33; }
.perf-caption { display:flex; align-items:baseline; gap:10px; margin-bottom:8px; }
.perf-caption b { font-size:13px; color:#e7ecf4; }
.perf-caption small { color:#8895ab; font-size:11px; }
.perf-cards { display:grid; grid-template-columns:repeat(auto-fit,minmax(120px,1fr)); gap:8px; }
.perf-card { display:flex; flex-direction:column; gap:4px; padding:9px 11px; border:1px solid #26324a; border-radius:0; background:#182338; color:#e7ecf4; text-align:left; cursor:pointer; transition:border-color .14s, background .14s; }
.perf-card:hover { border-color:#3a496a; }
.perf-card.active { border-color:#e9c16c; background:#22314e; }
.perf-card.total { border-color:#3d4f77; background:#1c2a47; cursor:default; }
.perf-card.total>b { font-size:20px; }
.perf-card>small { display:flex; align-items:center; gap:6px; color:#8895ab; font-size:11px; }
.perf-card>small i { padding:1px 5px; font-size:9px; font-style:normal; }
.perf-card>small i.frozen { background:#2a3a5c; color:#93a0b6; }
.perf-card>small i.tracking { background:#3d3423; color:#e9c16c; }
.perf-card>b { font-size:18px; font-variant-numeric:tabular-nums; }
.perf-card>b em { font-size:10px; font-style:normal; font-weight:400; opacity:.7; }
.perf-card>span { font-size:11px; font-variant-numeric:tabular-nums; }
.perf-card .up { color:#ef6a72; }.perf-card .down { color:#55b996; }.perf-card .dim { color:#93a0b6; }
.chart-strip { margin-top:10px; padding:10px 12px 6px; border:1px solid #26324a; background:#131e33; }
.perf-chart { display:block; width:100%; height:150px; }
.zero-line { stroke:#3a496a; stroke-width:1; stroke-dasharray:3 3; }
.bar-group { cursor:pointer; }
.bar-hit { fill:transparent; }
.bar-group:hover .bar { filter:brightness(1.25); }
.bar { transition:filter .12s; }
.bar.up { fill:#d24b55; }
.bar.down { fill:#2f9d78; }
.bar.tracking { opacity:.45; }
.bar.active { stroke:#e9c16c; stroke-width:1.5; opacity:1; }
.bar-label { fill:#71809a; font-size:10px; text-anchor:middle; }
.reco-body { display:grid; min-height:0; grid-template-columns:132px minmax(0,1fr); gap:14px; margin-top:12px; overflow:hidden; }
.date-list { display:flex; min-height:0; flex-direction:column; gap:3px; overflow-y:auto; padding-right:4px; }
.date-list button { padding:7px 9px; border:0; border-left:2px solid transparent; border-radius:0; background:#182338; color:#c4cddc; font-size:12px; text-align:left; cursor:pointer; }
.date-list button.active { border-left-color:#e9c16c; background:#22314e; color:#fff; }
.date-list .empty { padding:8px; color:#6f7c92; font-size:11px; }
.reco-table { display:flex; min-height:0; flex-direction:column; overflow-y:auto; }
.reco-row { display:grid; grid-template-columns:48px 135px 86px 86px 78px 64px minmax(180px,1fr) 90px; gap:10px; align-items:center; padding:11px 10px; border:0; border-bottom:1px solid #1e2a40; background:transparent; color:#e7ecf4; text-align:left; cursor:pointer; }
.reco-row.head { position:sticky; top:0; background:#101a2b; color:#8895ab; font-size:12px; cursor:default; }
.reco-row:not(.head):hover { background:#1a2540; }
.reco-row .rank { display:inline-flex; width:26px; height:26px; align-items:center; justify-content:center; background:#2a3a5c; color:#e9c16c; font-weight:700; }
.reco-row .stock b { font-size:14px; }.reco-row .stock small { display:block; margin-top:2px; color:#8895ab; font-size:11px; }
.reco-row .score { color:#ef6a72; font-size:16px; font-weight:700; }
.reco-row .reason { color:#c4cddc; font-size:12px; line-height:1.4; }
.reco-row .sector { color:#93a0b6; font-size:12px; }
.reco-row .up { color:#ef6a72; }.reco-row .down { color:#55b996; }.reco-row .dim { color:#93a0b6; }
.track-tag { display:block; margin-top:2px; color:#8895ab; font-size:10px; }
.disclaimer { padding:10px 4px; color:#6f7c92; font-size:11px; line-height:1.5; }
.empty { padding:20px; color:#6f7c92; font-size:13px; }
@media (max-width:900px) {
  .reco-shell { grid-template-columns:1fr; height:auto; min-height:100vh; overflow:visible; }
  .reco-content { height:auto; overflow:visible; }
  .reco-body { grid-template-columns:1fr; }
  .date-list { flex-direction:row; flex-wrap:wrap; max-height:none; }
  .reco-row { grid-template-columns:36px minmax(90px,1fr) 76px 76px 70px; gap:6px; padding:10px 6px; }
  .reco-row .score, .reco-row .reason, .reco-row .sector, .reco-row.head span:nth-child(6), .reco-row.head span:nth-child(7), .reco-row.head span:nth-child(8) { display:none; }
}
</style>
