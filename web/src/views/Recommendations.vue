<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { api, fmt, fmtPct, pctClass, type EntryAdviceResponse, type MonteCarloResult, type Position, type Recommendation, type RecommendationRiskPolicy, type RecommendationStats } from '../api'

const router = useRouter()
const dates = ref<string[]>([])
const activeDate = ref('')
const items = ref<Recommendation[]>([])
const loading = ref(false)
const message = ref('')
const running = ref(false)
const stats = ref<RecommendationStats | null>(null)
const riskPolicy = ref<RecommendationRiskPolicy | null>(null)

// 盘中趋势持仓分析（30分钟8档，建仓与退出双阶段）
const entryAdvice = ref<EntryAdviceResponse | null>(null)
const positions = ref<Position[]>([])
const entryRunning = ref(false)
const entryMessage = ref('')

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
const activePositions = computed(() => positions.value.filter(item => item.status === 'pending_entry' || item.status === 'holding'))
const entryDailyPick = computed(() => {
  const list = entryAdvice.value?.items ?? []
  return list.find(item => item.source === 'daily_pick') || null
})
const entryLastWait = computed(() => {
  const list = entryAdvice.value?.items ?? []
  return list.find(item => item.action === 'wait') || null
})

async function loadEntryAdvice() {
  const [advice, lifecycle] = await Promise.all([
    api.entryAdvice().catch(() => null),
    api.positions(40).catch(() => ({ items: [] as Position[] }))
  ])
  entryAdvice.value = advice
  positions.value = lifecycle.items
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

const phaseLabel: Record<string, string> = { up: '上升', range: '震荡', down: '下降' }
// 候选风险上限由最近一次 AI 复盘的市场阶段自动决定：up 85 / range 75 / down 65，无复盘 70。
const riskPolicyText = computed(() => {
  if (!riskPolicy.value) return ''
  const p = riskPolicy.value
  const phase = phaseLabel[p.market_phase] || '无复盘'
  return `候选风险上限 ${p.max_risk_score.toFixed(0)} · 复盘阶段：${phase}${p.review_date ? `（${p.review_date}）` : ''}`
})

function fmtSigned(value: number | null | undefined, suffix = '%') {
  if (value == null) return '—'
  return `${value >= 0 ? '+' : ''}${value.toFixed(2)}${suffix}`
}

// 风险分档位：≤40 低风险、41-60 中风险、>60 偏高（超过 70 的候选不会出现）。
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
  riskPolicy.value = await api.recommendationRiskPolicy().catch(() => null)
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
})

function openStock(symbol: string) {
  router.push(`/stock/${symbol}`)
}

onMounted(async () => {
  await refreshDates()
  await loadEntryAdvice()
  // 页面加载时若已有推荐任务在执行（如刷新页面），继续轮询直至完成。
  const status = await api.recommendationStatus().catch(() => null)
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
        <div class="reco-title"><strong>AI 趋势推荐 · 持仓闭环</strong><small>实际建仓价 → AI 趋势退出价；退出后收益立即冻结，未建仓不计收益</small></div>
        <div class="reco-tools">
          <button class="run-btn" :disabled="running" @click="runAnalysis">{{ running ? '分析中…' : '立即生成' }}</button>
        </div>
      </header>
      <p v-if="message" class="reco-message">{{ message }}</p>

      <div class="entry-strip">
        <div class="perf-caption">
          <b>盘中趋势持仓分析</b>
          <small>10只自选按生命周期管理：入池后 D0+2 个交易日寻找建仓点；建仓后每30分钟综合大盘、板块、个股寻找退出机会；退出即移出自选并冻结收益</small>
          <button class="run-btn small" :disabled="entryRunning" @click="runEntryNow">{{ entryRunning ? '分析中…' : '立即分析' }}</button>
        </div>
        <p v-if="entryMessage" class="reco-message">{{ entryMessage }}</p>
        <div class="entry-cards">
          <div v-if="exitLatest" class="entry-card exit">
            <small>最近退出 · {{ exitLatest.created_at.slice(11) }} · {{ exitLatest.urgency }}</small>
            <b>{{ exitLatest.name || exitLatest.symbol }}<em v-if="exitLatest.code">{{ exitLatest.code }}</em></b>
            <span>{{ exitLatest.reason }}</span>
            <i v-if="exitLatest.price_low != null">建议退出区间 {{ fmt(exitLatest.price_low) }} - {{ fmt(exitLatest.price_high) }}</i>
          </div>
          <div v-if="entryLatest" class="entry-card hit">
            <small>最近建仓 · {{ entryLatest.created_at.slice(11) }}</small>
            <b>{{ entryLatest.name || entryLatest.symbol }}<em v-if="entryLatest.code">{{ entryLatest.code }}</em></b>
            <span>{{ entryLatest.reason }}</span>
            <i v-if="entryLatest.price_low != null">建议建仓区间 {{ fmt(entryLatest.price_low) }} - {{ fmt(entryLatest.price_high) }}</i>
          </div>
          <div v-else-if="entryLastWait" class="entry-card">
            <small>最近等待 · {{ entryLastWait.created_at.slice(11) }}</small>
            <b>暂无合适建仓机会</b><span>{{ entryLastWait.reason }}</span>
          </div>
          <div v-if="entryDailyPick" class="entry-card pick">
            <small>今日首选（已加入生命周期）</small>
            <b>{{ entryDailyPick.name || entryDailyPick.symbol }}<em v-if="entryDailyPick.code">{{ entryDailyPick.code }}</em></b>
            <span>{{ entryDailyPick.reason }}</span>
          </div>
        </div>
        <div v-if="activePositions.length" class="position-list">
          <button v-for="position in activePositions" :key="position.id" type="button" class="position-chip" @click="openStock(position.symbol)">
            <b>{{ position.name || position.symbol }}</b><span :class="position.status">{{ position.status === 'holding' ? `持有 ${position.hold_days} 天` : '等待建仓' }}</span><small>{{ position.entry_price == null ? '尚未建仓' : `成本 ${fmt(position.entry_price)}` }}</small>
          </button>
        </div>
      </div>

      <div v-if="stats" class="stats-bar">
        <div class="stat-cell hero">
          <small>已退出胜率<em>仅真实生命周期</em></small>
          <b :class="(stats.win_rate ?? 0) >= 50 ? 'up' : 'down'">{{ stats.win_rate == null ? '—' : stats.win_rate.toFixed(1) + '%' }}</b>
          <span>{{ stats.wins || 0 }} 胜 / {{ stats.losses || 0 }} 负 / {{ stats.breakeven || 0 }} 平</span>
        </div>
        <div class="stat-cell hero"><small>已实现收益合计</small><b :class="pctClass(stats.sum_change_pct)">{{ fmtSigned(stats.sum_change_pct) }}</b><span>{{ stats.frozen_picks }} 笔有效退出，非账户收益率</span></div>
        <div class="stat-cell"><small>持有中浮盈</small><b :class="pctClass(stats.unrealized_sum_pct)">{{ fmtSigned(stats.unrealized_sum_pct) }}</b><span>{{ stats.holding_picks || 0 }} 笔 · 均 {{ fmtSigned(stats.unrealized_avg_pct) }}</span></div>
        <div class="stat-cell"><small>单笔平均 / 中位数</small><b :class="pctClass(stats.avg_change_pct)">{{ fmtSigned(stats.avg_change_pct) }}</b><span>中位数 {{ fmtSigned(stats.median_pct) }}</span></div>
        <div class="stat-cell"><small>标准盈亏因子</small><b>{{ stats.profit_factor == null ? (stats.wins > 0 && stats.losses === 0 ? '∞' : '—') : stats.profit_factor.toFixed(2) }}</b><span>总盈 {{ fmtSigned(stats.gross_profit_pct) }} / 总亏 {{ fmtSigned(stats.gross_loss_pct) }}</span></div>
        <div class="stat-cell"><small>生命周期分布</small><b>{{ stats.lifecycle_picks || 0 }}</b><span>待建 {{ stats.pending_picks || 0 }} · 持有 {{ stats.holding_picks || 0 }} · 退出 {{ stats.exited_picks || 0 }} · 过期 {{ stats.expired_picks || 0 }}</span></div>
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
            <div class="reco-row head"><span>排名</span><span>股票</span><span>建仓价</span><span>当前/退出价</span><span>涨跌幅</span><span>动量分</span><span>风险分</span><span>核心依据</span><span>板块</span><span>模拟</span></div>
            <template v-for="item in items" :key="item.symbol">
              <button class="reco-row" @click="openStock(item.symbol)">
                <span class="rank">{{ item.rank }}</span>
                <span class="stock"><b>{{ item.name }}</b><small>{{ item.code }}</small></span>
                <span>{{ fmt(item.entry_price) }}</span>
                <span>{{ fmt(item.latest_price) }}<small class="track-tag" :title="item.exit_reason || ''">{{ item.position_status === 'expired' ? '未建仓过期' : item.position_status === 'pending_entry' ? '等待建仓' : item.exited ? 'AI已退出' : item.position_status === 'holding' ? `持有${item.tracked_days}天` : '仅推荐记录' }}</small></span>
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
            <p class="disclaimer">说明：每日盘前从趋势推荐中选出一只首选加入自选生命周期。入池后 D0+2 个交易日内由 AI 寻找建仓区间；建仓后每 30 分钟综合大盘、板块、个股判断趋势是否可持续，并给出持有、减仓或退出区间。AI 或确定性硬风控标记退出后立即移出自选，收益按退出参考价冻结且继续保留在推荐历史中；从未建仓的标的不计收益。没有生命周期记录的旧推荐仅供历史复盘，不进入胜率、已实现收益或持仓浮盈统计。历史表现不代表未来收益。模型：{{ items[0].model || '—' }}</p>
          </template>
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
.position-chip { display:grid; min-width:150px; grid-template-columns:1fr auto; gap:3px 8px; padding:7px 9px; border:1px solid #26324a; background:#101a2b; color:#e7ecf4; text-align:left; cursor:pointer; }
.position-chip b { overflow:hidden; font-size:11px; text-overflow:ellipsis; white-space:nowrap; }
.position-chip span { font-size:9px; }.position-chip span.holding { color:#ef8b91; }.position-chip span.pending_entry { color:#e9c16c; }
.position-chip small { grid-column:1/-1; color:#8895ab; font-size:9px; }
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
