<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { api, fmt, fmtPct, pctClass, type EntryAdvice, type EntryAdviceResponse, type MonteCarloResult, type Position, type Recommendation, type RecommendationRun, type RecommendationStats, type TradeAccount, type TradeOrder, type WatchlistResponse } from '../api'

const router = useRouter()
const dates = ref<string[]>([])
const activeDate = ref('')
const items = ref<Recommendation[]>([])
const loading = ref(false)
const message = ref('')
const running = ref(false)
// latestRun 用于把「主动空仓」与「尚未运行/无数据」区分开。仅对最近一次运行
// 的日期有效，浏览更早的历史日期时不适用。
const latestRun = ref<RecommendationRun | null>(null)

const emptyPickReason = computed(() => {
  const run = latestRun.value
  if (!run || run.pick_count > 0) return ''
  if (activeDate.value && activeDate.value !== run.analysis_date) return ''
  const gate = run.gate_level === 'red' ? '红灯' : run.gate_level === 'yellow' ? '黄灯' : '绿灯'
  return `${run.analysis_date} 主动空仓：风向${gate}，当日上限 ${run.max_picks} 只，${run.candidate_count} 只候选中无一达到建仓标准。${run.gate_reason || ''}`
})
const stats = ref<RecommendationStats | null>(null)

// 自选股 AI 分析（交易时段每小时，AI 只给建议）
const entryAdvice = ref<EntryAdviceResponse | null>(null)
const positions = ref<Position[]>([])
const tradeAccount = ref<TradeAccount | null>(null)
const tradeOrders = ref<TradeOrder[]>([])
const watchlistData = ref<WatchlistResponse | null>(null)
const expandedAdviceSymbol = ref('')
const symbolAdvice = ref<Record<string, EntryAdvice[]>>({})
const entryRunning = ref(false)
const entryMessage = ref('')
const positionActionID = ref<number | null>(null)

// 蒙特卡洛模拟：按推荐股展开的路径分布统计
const mcOpenSymbol = ref('')
const mcResult = ref<MonteCarloResult | null>(null)
const mcLoading = ref(false)
const mcError = ref('')
const mcDays = ref(10)

const entryLatest = computed(() => {
  const list = entryAdvice.value?.items ?? []
  return list.find(item => item.action === 'entry') || null
})
const exitLatest = computed(() => {
  const list = entryAdvice.value?.items ?? []
  return list.find(item => item.action === 'exit') || null
})
const activePositions = computed(() => positions.value.filter(item => item.status === 'holding'))
const tradeRows = computed(() => {
  const holding = new Map(activePositions.value.map(item => [item.symbol, item]))
  return (watchlistData.value?.quotes || []).map(quote => ({ ...quote, position: holding.get(quote.symbol) || null }))
})
const entryDailyPick = computed(() => {
  const list = entryAdvice.value?.items ?? []
  return list.find(item => item.source === 'daily_pick') || null
})
const entryLastWait = computed(() => {
  const list = entryAdvice.value?.items ?? []
  return list.find(item => item.action === 'wait') || null
})

async function loadEntryAdvice() {
  const [advice, lifecycle, account, orders, watchlist] = await Promise.all([
    api.entryAdvice().catch(() => null),
    api.positions(100).catch(() => ({ items: [] as Position[] })),
    api.tradeAccount().catch(() => null),
    api.tradeOrders(100).catch(() => ({ items: [] as TradeOrder[] })),
    api.watchlist().catch(() => null),
  ])
  entryAdvice.value = advice
  positions.value = lifecycle.items
  tradeAccount.value = account
  tradeOrders.value = orders.items
  watchlistData.value = watchlist
}

let entryPollTimer: number | undefined
async function pollEntryStatus() {
  try {
    const status = await api.entryStatus()
    if (status.running) return
    window.clearInterval(entryPollTimer)
    entryPollTimer = undefined
    entryRunning.value = false
    entryMessage.value = status.last_error ? `分析结束：${status.last_error}` : '分析完成'
    await loadEntryAdvice()
  } catch { /* 下一轮继续 */ }
}

async function runEntryNow() {
  entryRunning.value = true
  entryMessage.value = '正在分析建仓机会…'
  try {
    await api.runEntryAnalysis()
    window.clearInterval(entryPollTimer)
    entryPollTimer = window.setInterval(pollEntryStatus, 3000)
  } catch (e: any) {
    entryRunning.value = false
    entryMessage.value = e?.message || '建仓分析启动失败'
  }
}

async function confirmPositionAction(row: { symbol: string; name: string; position: Position | null }) {
  const entering = !row.position
  const action = entering ? '建仓' : '平仓'
  const amountText = entering ? '默认买入 100 万元' : '按现价最多卖出 100 万元市值'
  if (!window.confirm(`确认${action} ${row.name || row.symbol}？\n${amountText}`)) return
  positionActionID.value = row.position?.id || -1
  entryMessage.value = ''
  try {
    const result = entering ? await api.enterSymbol(row.symbol) : await api.exitSymbol(row.symbol)
    entryMessage.value = `${row.name || row.symbol} 已${action} ${result.shares} 股，成交额 ${formatMoney(result.amount)}，余额 ${formatMoney(result.cash)}`
    await Promise.all([loadEntryAdvice(), refreshDates()])
  } catch (e: any) {
    entryMessage.value = e?.message || `${action}失败`
  } finally {
    positionActionID.value = null
  }
}

function formatMoney(value: number | null | undefined) {
  if (value == null) return '—'
  return new Intl.NumberFormat('zh-CN', { style: 'currency', currency: 'CNY', maximumFractionDigits: 2 }).format(value)
}

async function toggleAdvice(symbol: string) {
  if (expandedAdviceSymbol.value === symbol) {
    expandedAdviceSymbol.value = ''
    return
  }
  expandedAdviceSymbol.value = symbol
  if (!symbolAdvice.value[symbol]) {
    const result = await api.symbolAdvice(symbol, 50).catch(() => ({ items: [] as EntryAdvice[] }))
    symbolAdvice.value[symbol] = result.items
  }
}

async function toggleMonteCarlo(item: Recommendation, event: Event) {
  event.stopPropagation()
  if (mcOpenSymbol.value === item.symbol) {
    mcOpenSymbol.value = ''
    mcResult.value = null
    return
  }
  mcOpenSymbol.value = item.symbol
  mcResult.value = null
  mcError.value = ''
  mcLoading.value = true
  try {
    mcResult.value = await api.recommendationMonteCarlo(item.symbol, mcDays.value)
  } catch (e: any) {
    mcError.value = e?.message || '模拟失败'
  } finally {
    mcLoading.value = false
  }
}

async function reloadMonteCarlo() {
  if (!mcOpenSymbol.value) return
  mcResult.value = null
  mcError.value = ''
  mcLoading.value = true
  try {
    mcResult.value = await api.recommendationMonteCarlo(mcOpenSymbol.value, mcDays.value)
  } catch (e: any) {
    mcError.value = e?.message || '模拟失败'
  } finally {
    mcLoading.value = false
  }
}

function fmtSigned(value: number | null | undefined, suffix = '%') {
  if (value == null) return '—'
  return `${value >= 0 ? '+' : ''}${value.toFixed(2)}${suffix}`
}

// 风险分仅按档位着色展示，不参与推荐过滤、降权或排序。
function riskClass(value: number | null | undefined) {
  if (value == null) return ''
  if (value <= 40) return 'risk-low'
  if (value <= 60) return 'risk-mid'
  return 'risk-high'
}

async function loadDate(date: string) {
  activeDate.value = date
  loading.value = true
  mcOpenSymbol.value = ''
  mcResult.value = null
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
  dates.value = await api.recommendationHistory(365).catch(() => [] as string[])
  if (dates.value.length) await loadDate(dates.value[0])
}

let pollTimer: number | undefined

// 手动生成为异步任务（AI 主请求可能耗时数分钟），通过状态接口轮询完成情况。
async function pollRunStatus() {
  try {
    const status = await api.recommendationStatus()
    if (status.running) return
    window.clearInterval(pollTimer)
    pollTimer = undefined
    running.value = false
    latestRun.value = status.latest_run
    message.value = status.last_error ? `生成失败：${status.last_error}` : '生成完成'
    await refreshDates()
  } catch { /* 下一轮继续 */ }
}

async function runAnalysis() {
  running.value = true
  message.value = '正在生成推荐…'
  try {
    await api.runRecommendations()
    window.clearInterval(pollTimer)
    pollTimer = window.setInterval(pollRunStatus, 3000)
  } catch (e: any) {
    running.value = false
    message.value = e?.message || '分析启动失败'
  }
}

onUnmounted(() => {
  window.clearInterval(pollTimer)
  window.clearInterval(entryPollTimer)
  window.removeEventListener('gostock:watchlist-changed', onWatchlistChanged)
})

function openStock(symbol: string) {
  router.push(`/stock/${symbol}`)
}

function onWatchlistChanged() {
  loadEntryAdvice()
}

onMounted(async () => {
  window.addEventListener('gostock:watchlist-changed', onWatchlistChanged)
  await refreshDates()
  await loadEntryAdvice()
  // 页面加载时若已有推荐任务在执行（如刷新页面），继续轮询直至完成。
  const status = await api.recommendationStatus().catch(() => null)
  latestRun.value = status?.latest_run ?? null
  if (status?.running) {
    running.value = true
    message.value = '正在生成推荐…'
    pollTimer = window.setInterval(pollRunStatus, 3000)
  }
})
</script>

<template>
  <main class="reco-content">
      <header class="reco-header">
        <div class="reco-title"><strong>AI 趋势推荐 · 手动持仓闭环</strong><small>AI 持续分析并记录建议；只有手动点击建仓/平仓才改变真实持仓状态</small></div>
        <div class="reco-tools">
          <button class="run-btn" :disabled="running" @click="runAnalysis">{{ running ? '分析中…' : '立即生成' }}</button>
        </div>
      </header>
      <p v-if="message" class="reco-message">{{ message }}</p>

      <div class="entry-strip">
        <div class="perf-caption">
          <b>盘中趋势持仓分析</b>
          <small>AI 每小时分析全部自选股，只记录买入、卖出或持有建议；真实资金与持仓仅由手动操作改变</small>
          <button class="run-btn small" :disabled="entryRunning" @click="runEntryNow">{{ entryRunning ? '分析中…' : '立即分析' }}</button>
        </div>
        <p v-if="entryMessage" class="reco-message">{{ entryMessage }}</p>
        <div class="entry-cards">
          <div v-if="exitLatest" class="entry-card exit">
            <small>{{ exitLatest.source === 'manual' ? '最近手动平仓' : '最近平仓建议' }} · {{ exitLatest.created_at.slice(11) }} · {{ exitLatest.urgency }}</small>
            <b>{{ exitLatest.name || exitLatest.symbol }}<em v-if="exitLatest.code">{{ exitLatest.code }}</em></b>
            <span>{{ exitLatest.reason }}</span>
            <i v-if="exitLatest.price_low != null">建议退出区间 {{ fmt(exitLatest.price_low) }} - {{ fmt(exitLatest.price_high) }}</i>
          </div>
          <div v-if="entryLatest" class="entry-card hit">
            <small>{{ entryLatest.source === 'manual' ? '最近手动建仓' : '最近建仓建议' }} · {{ entryLatest.created_at.slice(11) }}</small>
            <b>{{ entryLatest.name || entryLatest.symbol }}<em v-if="entryLatest.code">{{ entryLatest.code }}</em></b>
            <span>{{ entryLatest.reason }}</span>
            <i v-if="entryLatest.price_low != null">建议建仓区间 {{ fmt(entryLatest.price_low) }} - {{ fmt(entryLatest.price_high) }}</i>
          </div>
          <div v-else-if="entryLastWait" class="entry-card">
            <small>最近等待 · {{ entryLastWait.created_at.slice(11) }}</small>
            <b>暂无合适建仓机会</b><span>{{ entryLastWait.reason }}</span>
          </div>
          <div v-if="entryDailyPick" class="entry-card pick">
            <small>今日唯一最强（已加入建仓池）</small>
            <b>{{ entryDailyPick.name || entryDailyPick.symbol }}<em v-if="entryDailyPick.code">{{ entryDailyPick.code }}</em></b>
            <span>{{ entryDailyPick.reason }}</span>
          </div>
        </div>
        <div v-if="tradeAccount" class="account-strip">
          <span><small>可用余额</small><b>{{ formatMoney(tradeAccount.cash) }}</b></span>
          <span><small>持仓市值</small><b>{{ formatMoney(tradeAccount.market_value) }}</b></span>
          <span><small>账户总资产</small><b>{{ formatMoney(tradeAccount.total_assets) }}</b></span>
          <span><small>已实现盈亏</small><b :class="pctClass(tradeAccount.realized_pnl)">{{ formatMoney(tradeAccount.realized_pnl) }}</b></span>
          <span><small>浮动盈亏</small><b :class="pctClass(tradeAccount.unrealized_pnl)">{{ formatMoney(tradeAccount.unrealized_pnl) }}</b></span>
          <span><small>操作总盈亏</small><b :class="pctClass(tradeAccount.total_pnl)">{{ formatMoney(tradeAccount.total_pnl) }}</b></span>
        </div>
        <div v-if="tradeRows.length" class="position-list">
          <div v-for="row in tradeRows" :key="row.symbol" class="position-record">
            <div class="position-chip" role="button" tabindex="0" @click="openStock(row.symbol)" @keydown.enter="openStock(row.symbol)">
              <b>{{ row.name || row.symbol }}</b>
              <span :class="row.position ? 'holding' : 'pending_entry'">{{ row.position ? `持有 ${row.position.shares} 股` : '自选观察' }}</span>
              <small>{{ row.position ? `成本 ${fmt(row.position.entry_price)} · 市值 ${formatMoney(row.position.market_value)}` : `现价 ${fmt(row.price)}` }}</small>
              <button type="button" class="position-action" :class="row.position ? 'exit' : 'enter'" :disabled="positionActionID !== null" @click.stop="confirmPositionAction(row)">{{ positionActionID === (row.position?.id || -1) ? '处理中…' : row.position ? '平仓' : '建仓' }}</button>
              <button type="button" class="advice-action" @click.stop="toggleAdvice(row.symbol)">{{ expandedAdviceSymbol === row.symbol ? '收起分析' : '分析记录' }}</button>
            </div>
            <div v-if="expandedAdviceSymbol === row.symbol" class="advice-history">
              <div v-for="advice in symbolAdvice[row.symbol] || []" :key="advice.id"><time>{{ advice.created_at }}</time><b>{{ advice.action === 'entry' ? '买入' : advice.action === 'exit' || advice.action === 'reduce' ? '卖出' : '持有' }}</b><span>{{ advice.reason }}</span></div>
              <p v-if="!(symbolAdvice[row.symbol] || []).length">暂无 AI 分析记录</p>
            </div>
          </div>
        </div>
        <section v-if="tradeOrders.length" class="order-history">
          <header><b>建仓与平仓记录</b><small>成交额、费用、余额与盈亏均按真实操作流水计算</small></header>
          <div class="order-row head"><span>时间</span><span>股票</span><span>操作</span><span>股数</span><span>成交额</span><span>费用</span><span>本次盈亏</span><span>AI分析</span></div>
          <div v-for="order in tradeOrders" :key="order.id" class="order-row"><span>{{ order.created_at }}</span><span>{{ order.name || order.symbol }}</span><span>{{ order.side === 'buy' ? '建仓' : '平仓' }}</span><span>{{ order.shares }}</span><span>{{ formatMoney(order.amount) }}</span><span>{{ formatMoney(order.fee) }}</span><span :class="pctClass(order.realized_pnl)">{{ order.side === 'buy' ? '—' : formatMoney(order.realized_pnl) }}</span><button class="order-advice-btn" @click="toggleAdvice(order.symbol)">查看</button></div>
          <div v-if="expandedAdviceSymbol" class="advice-history order-advice">
            <div v-for="advice in symbolAdvice[expandedAdviceSymbol] || []" :key="advice.id"><time>{{ advice.created_at }}</time><b>{{ advice.action === 'entry' ? '买入' : advice.action === 'exit' || advice.action === 'reduce' ? '卖出' : '持有' }}</b><span>{{ advice.reason }}</span></div>
            <p v-if="!(symbolAdvice[expandedAdviceSymbol] || []).length">暂无 AI 分析记录</p>
          </div>
        </section>
      </div>

      <div v-if="stats" class="stats-bar">
        <div class="stat-cell hero">
          <small>已退出胜率<em>仅真实生命周期</em></small>
          <b :class="(stats.win_rate ?? 0) >= 50 ? 'up' : 'down'">{{ stats.win_rate == null ? '0.0%' : stats.win_rate.toFixed(1) + '%' }}</b>
          <span>{{ stats.frozen_picks ? `${stats.wins} 胜 / ${stats.losses} 负 / ${stats.breakeven} 平` : '暂无真实退出样本' }}</span>
        </div>
        <div class="stat-cell hero"><small>已实现收益合计</small><b :class="pctClass(stats.sum_change_pct)">{{ fmtSigned(stats.sum_change_pct) }}</b><span>{{ stats.frozen_picks }} 笔有效退出，非账户收益率</span></div>
        <div class="stat-cell"><small>持有中浮盈</small><b :class="pctClass(stats.unrealized_sum_pct)">{{ fmtSigned(stats.unrealized_sum_pct) }}</b><span>{{ stats.holding_picks || 0 }} 笔 · 均 {{ fmtSigned(stats.unrealized_avg_pct) }}</span></div>
        <div class="stat-cell"><small>单笔平均 / 中位数</small><b :class="pctClass(stats.avg_change_pct)">{{ fmtSigned(stats.avg_change_pct) }}</b><span>中位数 {{ fmtSigned(stats.median_pct) }}</span></div>
        <div class="stat-cell"><small>标准盈亏因子</small><b>{{ stats.profit_factor == null ? (stats.wins > 0 && stats.losses === 0 ? '∞' : '—') : stats.profit_factor.toFixed(2) }}</b><span>总盈 {{ fmtSigned(stats.gross_profit_pct) }} / 总亏 {{ fmtSigned(stats.gross_loss_pct) }}</span></div>
        <div class="stat-cell"><small>生命周期分布</small><b>{{ stats.lifecycle_picks || 0 }}</b><span>待建 {{ stats.pending_picks || 0 }} · 持有 {{ stats.holding_picks || 0 }} · 退出 {{ stats.exited_picks || 0 }} · 过期 {{ stats.expired_picks || 0 }} · 移除 {{ stats.removed_picks || 0 }}</span></div>
        <div class="stat-cell reference"><small>历史参考胜率<em>不计真实交易</em></small><b :class="(stats.reference_win_rate ?? 0) >= 50 ? 'up' : 'down'">{{ stats.reference_win_rate == null ? '0.0%' : stats.reference_win_rate.toFixed(1) + '%' }}</b><span>{{ stats.reference_wins || 0 }} 胜 / {{ stats.reference_losses || 0 }} 负 · {{ stats.reference_frozen_picks || 0 }} 笔退出</span></div>
        <div class="stat-cell reference"><small>历史参考收益点数</small><b :class="pctClass(stats.reference_sum_change_pct)">{{ fmtSigned(stats.reference_sum_change_pct, ' 点') }}</b><span>{{ stats.reference_picks || 0 }} 只旧推荐 · 规则模拟</span></div>
        <div class="stat-cell"><small>平均持有</small><b>{{ stats.avg_hold_days == null ? '—' : stats.avg_hold_days.toFixed(1) + ' 天' }}</b><span>仅统计已退出交易</span></div>
        <div class="stat-cell"><small>已退出极值</small><b><i class="up">{{ fmtSigned(stats.best_pct) }}</i> / <i class="down">{{ fmtSigned(stats.worst_pct) }}</i></b><span>{{ stats.best_name || '—' }} / {{ stats.worst_name || '—' }}</span></div>
      </div>

      <div class="reco-body">
        <aside class="date-list">
          <button v-for="date in dates" :key="date" :class="{ active: date === activeDate }" @click="loadDate(date)">{{ date }}</button>
          <p v-if="!dates.length" class="empty">暂无历史记录，请配置模型后生成</p>
        </aside>

        <section class="reco-table">
          <div v-if="loading" class="empty">加载中…</div>
          <template v-else-if="items.length">
            <div class="reco-row head"><span>排名</span><span>股票</span><span>建仓价</span><span>当前/退出价</span><span>涨跌幅</span><span title="0-100 的相对机会分，只表达候选间的相对强弱排序，不是胜率或收益预期">机会分<small>非概率</small></span><span title="确定性风险分，≥75 的候选已在盘前直接剔除，不进入 AI 评审">风险分<small>&lt;75</small></span><span>核心依据</span><span>板块</span><span>模拟</span></div>
            <template v-for="item in items" :key="item.symbol">
              <button class="reco-row" @click="openStock(item.symbol)">
                <span class="rank">{{ item.rank }}</span>
                <span class="stock"><b>{{ item.name }}</b><small>{{ item.code }}</small></span>
                <span>{{ fmt(item.entry_price) }}</span>
                <span>{{ fmt(item.latest_price) }}<small class="track-tag" :title="item.exit_reason || ''">{{ item.position_status === 'expired' ? '未建仓过期' : item.position_status === 'removed' ? '移除自选放弃' : item.position_status === 'pending_entry' ? '等待建仓' : item.position_status === 'exited' ? '已手动平仓' : item.position_status === 'holding' ? `持有${item.tracked_days}天` : item.reference_only && item.change_pct != null ? `参考${item.tracked_days}天` : '仅推荐记录' }}</small></span>
                <span :class="pctClass(item.change_pct)">{{ fmtPct(item.change_pct) }}</span>
                <span class="score">{{ item.probability.toFixed(1) }}</span>
                <span class="risk" :class="riskClass(item.risk_score)">{{ item.risk_score == null ? '—' : item.risk_score.toFixed(0) }}</span>
                <span class="reason">{{ item.reason }}</span>
                <span class="sector">{{ item.sector }}</span>
                <span><button class="mc-btn" :class="{ active: mcOpenSymbol === item.symbol }" @click="toggleMonteCarlo(item, $event)">蒙特卡洛</button></span>
              </button>
              <div v-if="mcOpenSymbol === item.symbol" class="mc-panel">
                <div class="mc-head">
                  <b>{{ item.name }} · 蒙特卡洛模拟</b>
                  <label>推演
                    <select v-model.number="mcDays" @change="reloadMonteCarlo">
                      <option :value="5">5 日</option>
                      <option :value="10">10 日</option>
                      <option :value="20">20 日</option>
                    </select>
                  </label>
                </div>
                <div v-if="mcLoading" class="empty">模拟中…</div>
                <div v-else-if="mcError" class="empty">{{ mcError }}</div>
                <div v-else-if="mcResult" class="mc-grid">
                  <div class="mc-cell hero"><small>上涨概率</small><b :class="mcResult.win_rate >= 50 ? 'up' : 'down'">{{ mcResult.win_rate.toFixed(1) }}%</b></div>
                  <div class="mc-cell"><small>期望收益</small><b :class="pctClass(mcResult.avg_return_pct)">{{ fmtSigned(mcResult.avg_return_pct) }}</b></div>
                  <div class="mc-cell"><small>中位数</small><b :class="pctClass(mcResult.median_pct)">{{ fmtSigned(mcResult.median_pct) }}</b></div>
                  <div class="mc-cell"><small>涨超 +5%</small><b class="up">{{ mcResult.prob_gain_5_pct.toFixed(1) }}%</b></div>
                  <div class="mc-cell"><small>跌超 -5%</small><b class="down">{{ mcResult.prob_loss_5_pct.toFixed(1) }}%</b></div>
                  <div class="mc-cell"><small>悲观 P5</small><b class="down">{{ fmtSigned(mcResult.p5_pct) }}</b></div>
                  <div class="mc-cell"><small>P25 ~ P75</small><b>{{ fmtSigned(mcResult.p25_pct) }} ~ {{ fmtSigned(mcResult.p75_pct) }}</b></div>
                  <div class="mc-cell"><small>乐观 P95</small><b class="up">{{ fmtSigned(mcResult.p95_pct) }}</b></div>
                </div>
                <p v-if="mcResult" class="mc-note">基于最近 {{ mcResult.sample_days }} 个真实日收益率有放回抽样，模拟 {{ mcResult.paths }} 条未来 {{ mcResult.days }} 个交易日路径（基准价 {{ mcResult.base_price.toFixed(2) }}，确定性种子可复现）。模拟不构成投资建议。</p>
              </div>
            </template>
            <p class="disclaimer">说明：每日推荐最多 3 只（按指数风向档位收紧为 3/2/1 只，无合格标的时为 0 只）并显示在左侧，点击“+”后才加入自选。AI 每小时分析全部自选股并给出买入、卖出或持有建议，但不会改变持仓；只有用户点击建仓/平仓才产生资金流水。未手动交易的推荐仍按次日开盘至第 10 个交易日收盘展示参考走势，不计入真实账户盈亏。历史表现不代表未来收益。模型：{{ items[0].model || '—' }}</p>
          </template>
          <div v-else-if="emptyPickReason" class="empty empty-pick">
            <b>今日主动空仓</b>
            <span>{{ emptyPickReason }}</span>
            <small>空仓是风险闸门生效后的正常结论，不是任务失败。</small>
          </div>
          <div v-else class="empty">该日期暂无推荐数据</div>
        </section>
      </div>
  </main>
</template>

<style scoped>
/* 以首页 Tab 面板形式嵌入 Dashboard，自身不再带侧边栏外壳 */
.reco-content { display:flex; min-width:0; min-height:0; flex-direction:column; padding:0 14px 14px; overflow:hidden; background:#0f1826; color:#e7ecf4; }
.reco-header { display:flex; align-items:center; justify-content:space-between; padding:12px 2px; border-bottom:1px solid #26324a; }
.reco-title strong { font-size:16px; }.reco-title small { margin-left:10px; color:#8895ab; font-size:12px; }
.reco-title .risk-policy { color:#d8b967; }
.run-btn { padding:6px 14px; border:1px solid #3a496a; border-radius:0; background:#233150; color:#e7ecf4; font-size:13px; cursor:pointer; }.run-btn:disabled { cursor:wait; opacity:.6; }
.reco-message { margin:8px 2px 0; color:#d8b967; font-size:12px; }
.stats-bar { display:grid; grid-template-columns:repeat(auto-fit,minmax(128px,1fr)); gap:8px; margin-top:12px; }
.stat-cell { display:flex; flex-direction:column; gap:4px; padding:10px 12px; border:1px solid #26324a; background:#131e33; }
.stat-cell.hero { border-color:#3d4f77; background:#1c2a47; }
.stat-cell.reference { border-color:#554d39; background:#211f1b; }
.stat-cell.reference>small { color:#c7ac69; }
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
.shadow-strip { margin-top:10px; padding:10px 12px; border:1px solid #26324a; background:#131e33; }
.shadow-table { display:grid; gap:1px; }
.shadow-row { display:grid; grid-template-columns:110px 70px 110px 90px 90px 100px minmax(90px,1fr); gap:10px; align-items:center; padding:7px 8px; background:#182338; font-size:12px; font-variant-numeric:tabular-nums; }
.shadow-row.head { background:#101a2b; color:#8895ab; font-size:11px; }
.shadow-row.hero { border-left:2px solid #e9c16c; background:#1c2a47; }
.shadow-row small { margin-left:4px; color:#8895ab; font-size:9px; }
.shadow-row .up { color:#ef6a72; }.shadow-row .down { color:#55b996; }.shadow-row .dim { color:#93a0b6; }
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
.reco-row { display:grid; grid-template-columns:48px 135px 86px 86px 78px 64px 56px minmax(140px,1fr) 90px 78px; gap:10px; align-items:center; padding:11px 10px; border:0; border-bottom:1px solid #1e2a40; background:transparent; color:#e7ecf4; text-align:left; cursor:pointer; }
.reco-row.head { position:sticky; top:0; background:#101a2b; color:#8895ab; font-size:12px; cursor:default; }
.reco-row:not(.head):hover { background:#1a2540; }
.reco-row .rank { display:inline-flex; width:26px; height:26px; align-items:center; justify-content:center; background:#2a3a5c; color:#e9c16c; font-weight:700; }
.reco-row .stock b { font-size:14px; }.reco-row .stock small { display:block; margin-top:2px; color:#8895ab; font-size:11px; }
.reco-row .score { color:#ef6a72; font-size:16px; font-weight:700; }
.reco-row .risk { font-size:14px; font-weight:700; }
.reco-row .risk-low { color:#55b996; }
.reco-row .risk-mid { color:#e9c16c; }
.reco-row .risk-high { color:#ef6a72; }
.reco-row .reason { color:#c4cddc; font-size:12px; line-height:1.4; }
.reco-row .sector { color:#93a0b6; font-size:12px; }
.reco-row .up { color:#ef6a72; }.reco-row .down { color:#55b996; }.reco-row .dim { color:#93a0b6; }
.track-tag { display:block; margin-top:2px; color:#8895ab; font-size:10px; }
.disclaimer { padding:10px 4px; color:#6f7c92; font-size:11px; line-height:1.5; }
.empty { padding:20px; color:#6f7c92; font-size:13px; }
/* 主动空仓是策略结论，需与「无数据」的灰色空态明显区分。 */
.empty-pick { display:grid; gap:6px; color:#d8a657; }
.empty-pick b { font-size:14px; }
.empty-pick small { color:#8b96a8; }
/* 盘中建仓建议 */
.entry-strip { margin-top:12px; padding:10px 12px; border:1px solid #26324a; background:#131e33; }
.entry-strip .perf-caption { align-items:center; }
.entry-strip .perf-caption small { flex:1; }
.run-btn.small { padding:4px 10px; font-size:12px; }
.entry-cards { display:grid; grid-template-columns:repeat(auto-fit,minmax(280px,1fr)); gap:8px; }
.entry-card { display:flex; flex-direction:column; gap:4px; padding:10px 12px; border:1px solid #26324a; background:#182338; }
.entry-card.hit { border-color:#3d5c3f; background:#1b2f22; }
.entry-card.exit { border-color:#70434a; background:#2b1e27; }
.entry-card.pick { border-color:#3d4f77; background:#1c2a47; }
.entry-card>small { color:#8895ab; font-size:11px; }
.entry-card>b { font-size:16px; }
.entry-card>b em { margin-left:6px; color:#8895ab; font-size:11px; font-style:normal; font-weight:400; }
.entry-card>span { color:#c4cddc; font-size:12px; line-height:1.5; }
.entry-card>i { color:#d8b967; font-size:11px; font-style:normal; }
.position-list { display:flex; gap:6px; margin-top:8px; overflow-x:auto; }
.position-chip { display:grid; min-width:190px; grid-template-columns:1fr auto; gap:5px 8px; padding:7px 9px; border:1px solid #26324a; background:#101a2b; color:#e7ecf4; text-align:left; cursor:pointer; }
.position-chip b { overflow:hidden; font-size:11px; text-overflow:ellipsis; white-space:nowrap; }
.position-chip span { font-size:9px; }.position-chip span.holding { color:#ef8b91; }.position-chip span.pending_entry { color:#e9c16c; }
.position-chip small { color:#8895ab; font-size:9px; }
.position-action { grid-row:2; grid-column:2; min-width:46px; padding:3px 8px; border:1px solid #3a496a; background:#233150; color:#e7ecf4; font-size:10px; cursor:pointer; }
.position-action.enter { border-color:#4f6f4f; color:#8fd09a; }
.position-action.exit { border-color:#70434a; color:#ef8b91; }
.position-action:disabled { cursor:wait; opacity:.6; }
.account-strip { display:grid; grid-template-columns:repeat(6,minmax(120px,1fr)); gap:1px; margin-top:9px; background:#26324a; }
.account-strip span { display:grid; gap:3px; padding:8px 10px; background:#101a2b; }.account-strip small { color:#8895ab; font-size:9px; }.account-strip b { font-size:13px; font-variant-numeric:tabular-nums; }
.position-record { min-width:260px; }.position-record .position-chip { min-width:260px; grid-template-columns:minmax(90px,1fr) auto auto; }.position-record .position-chip small { grid-column:1/2; }
.advice-action { grid-row:2; grid-column:3; padding:3px 7px; border:1px solid #47536a; background:#1c293e; color:#b8c2d1; font-size:9px; cursor:pointer; }
.advice-history { max-height:180px; overflow:auto; border:1px solid #26324a; border-top:0; background:#0d1625; }.advice-history div { display:grid; grid-template-columns:84px 32px minmax(140px,1fr); gap:6px; padding:5px 7px; border-bottom:1px solid #202b3e; font-size:9px; }.advice-history time { color:#78859b; }.advice-history b { color:#e9c16c; }.advice-history span { color:#b8c2d1; }.advice-history p { padding:7px; color:#78859b; font-size:9px; }
.order-history { margin-top:10px; border:1px solid #26324a; background:#101a2b; }.order-history>header { display:flex; justify-content:space-between; padding:8px 10px; }.order-history>header b { font-size:12px; }.order-history>header small { color:#8895ab; font-size:10px; }.order-row { display:grid; grid-template-columns:120px minmax(100px,1fr) 55px 70px 130px 100px 130px 52px; gap:8px; align-items:center; padding:6px 10px; border-top:1px solid #202b3e; font-size:10px; font-variant-numeric:tabular-nums; }.order-row.head { color:#8895ab; background:#0d1625; }
.order-advice-btn { padding:3px 5px; border:1px solid #47536a; background:#1c293e; color:#b8c2d1; font-size:9px; cursor:pointer; }.order-advice { margin:0 10px 10px; border-top:1px solid #26324a; }
@media (max-width:900px) { .account-strip { grid-template-columns:repeat(2,minmax(0,1fr)); }.order-history { overflow-x:auto; }.order-row { min-width:760px; } }
/* 蒙特卡洛 */
.mc-btn { padding:4px 8px; border:1px solid #3a496a; border-radius:0; background:#233150; color:#c4cddc; font-size:11px; cursor:pointer; }
.mc-btn:hover, .mc-btn.active { border-color:#e9c16c; color:#e9c16c; }
.mc-panel { padding:10px 12px; border-bottom:1px solid #1e2a40; background:#101a2b; }
.mc-head { display:flex; align-items:center; justify-content:space-between; margin-bottom:8px; }
.mc-head b { font-size:13px; }
.mc-head label { color:#8895ab; font-size:12px; }
.mc-head select { margin-left:6px; padding:2px 6px; border:1px solid #3a496a; background:#182338; color:#e7ecf4; font-size:12px; }
.mc-grid { display:grid; grid-template-columns:repeat(auto-fit,minmax(110px,1fr)); gap:8px; }
.mc-cell { display:flex; flex-direction:column; gap:3px; padding:8px 10px; border:1px solid #26324a; background:#182338; }
.mc-cell.hero { border-color:#3d4f77; background:#1c2a47; }
.mc-cell small { color:#8895ab; font-size:10px; }
.mc-cell b { font-size:15px; font-variant-numeric:tabular-nums; }
.mc-cell .up { color:#ef6a72; }.mc-cell .down { color:#55b996; }
.mc-note { margin:8px 0 0; color:#6f7c92; font-size:11px; line-height:1.5; }
@media (max-width:900px) {
  .reco-content { height:auto; overflow:visible; }
  .reco-body { grid-template-columns:1fr; }
  .date-list { flex-direction:row; flex-wrap:wrap; max-height:none; }
  .shadow-row { grid-template-columns:90px 50px 70px 70px 70px; }
  .shadow-row span:nth-child(6), .shadow-row span:nth-child(7) { display:none; }
  .reco-row { grid-template-columns:36px minmax(90px,1fr) 76px 76px 70px; gap:6px; padding:10px 6px; }
  .reco-row .score, .reco-row .risk, .reco-row .reason, .reco-row .sector, .reco-row span:nth-child(10), .reco-row.head span:nth-child(6), .reco-row.head span:nth-child(7), .reco-row.head span:nth-child(8), .reco-row.head span:nth-child(9), .reco-row.head span:nth-child(10) { display:none; }
}
</style>
