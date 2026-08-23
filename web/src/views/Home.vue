<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { api, fmtPct, pctClass, type EntryAdviceResponse, type Position, type Recommendation, type RecommendationBasketPerformance, type RecommendationRiskPolicy, type RecommendationStats, type RiskGateOverview } from '../api'
import MiniChart from '../components/MiniChart.vue'

const router = useRouter()
const loading = ref(true)
const error = ref('')
// 风险感知指标独立加载：外盘/风向门接口异常不得阻塞首页绩效主体，
// 缺数据时按黄灯口径展示「未知」，绝不静默显示为安全。
const riskGate = ref<RiskGateOverview | null>(null)
const riskPolicy = ref<RecommendationRiskPolicy | null>(null)
const riskError = ref('')
const recommendations = ref<Recommendation[]>([])
const basketPerformance = ref<RecommendationBasketPerformance[]>([])
const stats = ref<RecommendationStats | null>(null)
const entryAdvice = ref<EntryAdviceResponse | null>(null)
const positions = ref<Position[]>([])
const basketChartElement = ref<HTMLElement | null>(null)
const basketChartWidth = ref(960)
const activeBasketDate = ref('')
let basketResizeObserver: ResizeObserver | null = null

function signed(value: number | null | undefined, digits = 2) {
  if (value == null) return '—'
  return `${value >= 0 ? '+' : ''}${value.toFixed(digits)}%`
}

function signedPoints(value: number | null | undefined, digits = 2) {
  if (value == null) return '—'
  return `${value >= 0 ? '+' : ''}${value.toFixed(digits)} 点`
}

const statusLabels: Record<Position['status'], string> = {
  pending_entry: '等待建仓', holding: '持有中', exited: '已退出', expired: '未建仓过期', removed: '移除自选放弃',
}
// 退出归因：区分确定性风控与 AI 判断，便于复盘各条纪律的实际贡献。
const exitKindLabels: Record<string, string> = {
  manual: '手动平仓', ai: 'AI建议', stop_loss: '硬止损建议', trailing_stop: '移动止盈建议',
  take_profit: '目标止盈建议', time_stop: '时间止损建议', trend_break: '趋势破位建议', systemic: '系统性风险建议',
}
function statusLabel(status: Position['status']) { return statusLabels[status] || status }
function exitKindLabel(kind?: string) { return kind ? exitKindLabels[kind] || kind : '' }
function referenceLabel(item: Position) {
  if (item.status === 'exited') return item.exit_price == null ? '—' : item.exit_price.toFixed(2)
  if (item.status === 'holding') return item.reference_price == null ? '—' : item.reference_price.toFixed(2)
  return '—'
}
function recommendationStatus(item: Recommendation) {
  if (item.position_status === 'pending_entry') return '等待建仓'
  if (item.position_status === 'holding') return `持有 ${item.tracked_days} 天`
  if (item.position_status === 'exited') return '已退出'
  if (item.position_status === 'expired') return '未建仓过期'
  if (item.position_status === 'removed') return '移除自选放弃'
  if (item.reference_only && item.change_pct != null) return `参考走势 ${item.tracked_days} 天`
  return '仅推荐记录'
}
const latestEntry = computed(() => entryAdvice.value?.items.find(item => item.action === 'entry') || null)
const latestExit = computed(() => entryAdvice.value?.items.find(item => item.action === 'exit') || null)
const activePositionCount = computed(() => positions.value.filter(item => item.status === 'pending_entry' || item.status === 'holding').length)
const dailyPick = computed(() => entryAdvice.value?.items.find(item => item.source === 'daily_pick') || null)
const lifecycleSummary = computed(() => {
  if (!stats.value?.lifecycle_picks) return '最新推荐尚未建立交易生命周期'
  if (stats.value.pending_picks > 0 && stats.value.holding_picks === 0 && stats.value.exited_picks === 0) {
    return `${stats.value.pending_picks} 只等待手动确认建仓，AI 建仓区间仅作决策参考`
  }
  return '只统计手动确认的真实建仓生命周期；持有中计浮盈，手动平仓后冻结收益'
})

const equityChart = computed(() => {
  const exited = positions.value
    .filter(item => item.status === 'exited' && item.change_pct != null)
    .sort((a, b) => (a.exit_date || a.updated_at).localeCompare(b.exit_date || b.updated_at) || a.id - b.id)
  let cumulative = 0
  const values = exited.map(item => ({
    id: item.id,
    date: item.exit_date || item.updated_at.slice(0, 10),
    name: item.name || item.symbol,
    changePct: item.change_pct as number,
    value: cumulative += item.change_pct as number,
  }))
  const width = 900, height = 224, left = 52, right = 18, top = 18, bottom = 28
  const min = Math.min(0, ...values.map(item => item.value)), max = Math.max(0, ...values.map(item => item.value))
  const span = Math.max(max - min, 1), plotW = width - left - right, plotH = height - top - bottom
  const x = (index: number) => left + (values.length <= 1 ? plotW / 2 : index / (values.length - 1) * plotW)
  const y = (value: number) => top + (max - value) / span * plotH
  const points = values.map((item, index) => `${x(index)},${y(item.value)}`).join(' ')
  const area = values.length ? `${left},${y(0)} ${points} ${x(values.length - 1)},${y(0)}` : ''
  const labels = values.filter((_, index) => index === 0 || index === values.length - 1 || index % Math.max(1, Math.ceil(values.length / 6)) === 0)
  return { width, height, left, right, values, points, area, min, max, zeroY: y(0), x, y, labels, total: cumulative }
})

const basketChart = computed(() => {
  const values = [...basketPerformance.value]
    .reverse()
    .filter(item => item.avg_change_pct != null)
    .map(item => ({ ...item, value: item.avg_change_pct as number }))
  const width = Math.max(320, basketChartWidth.value)
  const compact = width < 640
  const height = compact ? 210 : 224
  const left = compact ? 38 : 50, right = compact ? 12 : 18, top = 16, bottom = compact ? 28 : 32
  const plotW = width - left - right, plotH = height - top - bottom
  const rawMin = Math.min(0, ...values.map(item => item.value))
  const rawMax = Math.max(0, ...values.map(item => item.value))
  const padding = Math.max(.6, (rawMax - rawMin) * .14)
  const min = rawMin - padding, max = rawMax + padding
  const x = (index: number) => left + (values.length <= 1 ? plotW / 2 : index / (values.length - 1) * plotW)
  const y = (value: number) => top + (max - value) / Math.max(max - min, 1) * plotH
  const coordinates = values.map((item, index) => ({ x: x(index), y: y(item.value) }))
  const smoothPath = coordinates.length
    ? coordinates.slice(1).reduce((path, point, index) => {
        const previous = coordinates[index]
        const controlX = (previous.x + point.x) / 2
        return `${path} C ${controlX},${previous.y} ${controlX},${point.y} ${point.x},${point.y}`
      }, `M ${coordinates[0].x},${coordinates[0].y}`)
    : ''
  const zeroY = y(0)
  const areaPath = coordinates.length ? `${smoothPath} L ${coordinates[coordinates.length - 1].x},${zeroY} L ${coordinates[0].x},${zeroY} Z` : ''
  const labelCount = compact ? 4 : Math.min(8, values.length)
  const labelIndexes = new Set(Array.from({ length: labelCount }, (_, index) => Math.round(index * (values.length - 1) / Math.max(labelCount - 1, 1))))
  const labels = values.map((item, index) => ({ ...item, index })).filter(item => labelIndexes.has(item.index))
  const grid = Array.from({ length: 5 }, (_, index) => {
    const value = max - index * (max - min) / 4
    return { value, y: y(value) }
  })
  const samples = values.reduce((sum, item) => sum + item.stocks, 0)
  const frozen = values.reduce((sum, item) => sum + item.frozen_stocks, 0)
  const tracking = values.reduce((sum, item) => sum + item.tracking_stocks, 0)
  const latest = values.length ? values[values.length - 1] : null
  const best = values.length ? values.reduce((current, item) => item.value > current.value ? item : current) : null
  const worst = values.length ? values.reduce((current, item) => item.value < current.value ? item : current) : null
  // 零轴上下分别裁剪，让盈利区填红、亏损区填绿（A股红涨绿跌口径）。
  const plotBottom = height - bottom
  return {
    width, height, left, right, top, plotBottom, values, min, max, zeroY, x, y,
    smoothPath, areaPath, labels, grid, samples, frozen, tracking, latest, best, worst,
  }
})
const activeBasketPoint = computed(() => basketChart.value.values.find(item => item.date === activeBasketDate.value) || basketChart.value.latest)
const reasonRows = computed(() => recommendations.value.map(item => ({ ...item, strength: Math.max(0, Math.min(100, item.probability)), safety: Math.max(0, Math.min(100, 100 - (item.risk_score ?? 50))) })))

// 风险档位语义与推荐链路实际行为一一对应（见 analysis/recommendation.go runDailyAt）：
// green 正常推荐并自动建仓；yellow 只生成推荐观察、不自动建仓且风险上限压到 down 档；
// red 直接跳过当日推荐与建仓。数据缺失一律按 yellow 保守显示，不显示为绿灯。
const gateLevelMeta: Record<string, { label: string; action: string; cls: string }> = {
  green: { label: '绿灯', action: '正常推荐并自动建仓', cls: 'lv-green' },
  yellow: { label: '黄灯', action: '仅推荐观察 · 不自动建仓', cls: 'lv-yellow' },
  red: { label: '红灯', action: '跳过当日推荐与建仓', cls: 'lv-red' },
}
function gateMeta(level?: string | null) {
  return gateLevelMeta[level || ''] || { label: '未知', action: '风险数据不可用，按保守处理', cls: 'lv-yellow' }
}
const finalGate = computed(() => gateMeta(riskGate.value?.final_level))
const globalGate = computed(() => riskGate.value?.global_gate || null)
const marketGate = computed(() => riskGate.value?.market_gate || null)
// 自动建仓需同时满足「全局开关开启」与「综合档位绿灯」。这是
// 「推荐可见 ≠ 系统已建仓」的关键提示，避免照着观察性推荐手动追单。
const autoEntryOn = computed(() => riskGate.value?.auto_entry_enabled === true && riskGate.value?.final_level === 'green')
// 区分两种未建仓原因：开关停用是策略性决定，非绿灯是当日风险拦截。
const autoEntryHint = computed(() => {
  if (!riskGate.value) return '风险数据不可用，请按最保守口径处理'
  if (!riskGate.value.auto_entry_enabled) return '策略验证期：推荐仅作观察，开仓由你人工决策'
  if (riskGate.value.final_level !== 'green') return '非绿灯仅生成推荐观察，请勿照单手动追买'
  return '绿灯下盘前自动建仓最强标的'
})
const autoEntryLabel = computed(() => {
  if (riskGate.value && !riskGate.value.auto_entry_enabled) return '已停用'
  return autoEntryOn.value ? '已开启' : '已暂停'
})
const phaseLabels: Record<string, string> = { up: '上行', range: '震荡', down: '下行' }
const riskPolicyText = computed(() => {
  if (!riskPolicy.value) return '—'
  return `${riskPolicy.value.max_risk_score.toFixed(0)}`
})
const riskPolicyHint = computed(() => {
  if (!riskPolicy.value) return '复盘阶段未知 · 取基准 70'
  const phase = phaseLabels[riskPolicy.value.market_phase] || '未知'
  return `复盘${riskPolicy.value.review_date || '—'} · ${phase}阶段`
})
function openRiskTab() { router.push({ path: '/', query: { view: 'risk' } }) }
// 首页只做总览；完整推荐历史、持仓操作与蒙特卡洛都在「交易」Tab，避免两处重复维护。
function openTradeTab() { router.push({ path: '/', query: { view: 'reco' } }) }

// 三道风险门收敛成一组状态点，用颜色而非成段文字表达档位。
const gateDots = computed(() => [
  { key: 'global', label: '外盘', cls: gateMeta(globalGate.value?.level).cls, text: globalGate.value ? `${gateMeta(globalGate.value.level).label} ${globalGate.value.score}` : '无数据', tip: globalGate.value?.reason || '今日尚未采集外盘因子' },
  { key: 'market', label: '境内', cls: gateMeta(marketGate.value?.level).cls, text: marketGate.value ? gateMeta(marketGate.value.level).label : '无数据', tip: marketGate.value?.reason || '指数风向数据不可用' },
  { key: 'policy', label: '惩罚起点', cls: 'lv-plain', text: riskPolicyText.value, tip: riskPolicyHint.value },
])

// ---- 指标卡图形化数据 ----
// 每张卡都配一条迷你图：占比条(bar)、双向标尺(gauge)、构成条(split/stack)。
// 数字仍是主角，图形只负责让「好/坏、多/少」在扫视中立刻成立。
const winSegments = computed(() => [
  { value: stats.value?.wins ?? 0, label: '胜', tone: 'up' as const },
  { value: stats.value?.losses ?? 0, label: '负', tone: 'down' as const },
  { value: stats.value?.breakeven ?? 0, label: '平', tone: 'flat' as const },
])
const referenceWinSegments = computed(() => [
  { value: stats.value?.reference_wins ?? 0, label: '胜', tone: 'up' as const },
  { value: stats.value?.reference_losses ?? 0, label: '负', tone: 'down' as const },
])
const profitSegments = computed(() => [
  { value: Math.abs(stats.value?.gross_profit_pct ?? 0), label: '总盈利', tone: 'up' as const },
  { value: Math.abs(stats.value?.gross_loss_pct ?? 0), label: '总亏损', tone: 'down' as const },
])
const extremeSegments = computed(() => [
  { value: Math.abs(stats.value?.best_pct ?? 0), label: '最好', tone: 'up' as const },
  { value: Math.abs(stats.value?.worst_pct ?? 0), label: '最差', tone: 'down' as const },
])
const lifecycleSegments = computed(() => [
  { value: stats.value?.pending_picks ?? 0, label: '待建', tone: 'warn' as const },
  { value: stats.value?.holding_picks ?? 0, label: '持有', tone: 'up' as const },
  { value: stats.value?.exited_picks ?? 0, label: '退出', tone: 'down' as const },
  { value: stats.value?.expired_picks ?? 0, label: '过期', tone: 'flat' as const },
])
// gauge 量程取「当前值与参考刻度的较大者」，保证极端值不撑爆、正常值不贴边。
function gaugeMax(value: number | null | undefined, base: number) {
  return Math.max(base, Math.abs(value ?? 0) * 1.15)
}

// 待办：只列出需要人工动作的持仓（等待建仓 / 持有中），完整明细在「交易」Tab。
const actionRows = computed(() => positions.value.filter(item => item.status === 'pending_entry' || item.status === 'holding').slice(0, 6))

async function observeBasketChart() {
  await nextTick()
  basketResizeObserver?.disconnect()
  if (!basketChartElement.value) return
  const updateWidth = () => { basketChartWidth.value = Math.floor(basketChartElement.value?.clientWidth || 960) }
  updateWidth()
  basketResizeObserver = new ResizeObserver(updateWidth)
  basketResizeObserver.observe(basketChartElement.value)
}

async function load() {
  loading.value = true; error.value = ''
  try {
    const [reco, basket, overall, entry, lifecycle] = await Promise.all([api.recommendations(), api.recommendationBasketPerformance(30), api.recommendationStats(60), api.entryAdvice(), api.positions(200)])
    recommendations.value = reco; basketPerformance.value = basket; stats.value = overall; entryAdvice.value = entry; positions.value = lifecycle.items
  } catch (e: any) { error.value = e?.message || '首页统计加载失败' } finally { loading.value = false; await observeBasketChart() }
  loadRisk()
}

// 风险指标与绩效统计解耦加载：风险接口不可用时首页其余部分照常可用，
// 但风险带会显式显示「不可用」，不会退化成看起来安全的空白。
async function loadRisk() {
  try {
    const [gate, policy] = await Promise.all([api.riskGate(), api.recommendationRiskPolicy()])
    riskGate.value = gate; riskPolicy.value = policy; riskError.value = ''
  } catch (e: any) {
    riskGate.value = null; riskPolicy.value = null
    riskError.value = e?.message || '风险感知数据不可用'
  }
}
function openStock(symbol: string) { router.push(`/stock/${symbol}`) }
onMounted(load)
onBeforeUnmount(() => basketResizeObserver?.disconnect())
</script>

<template>
  <main class="overview">
    <header class="overview-header"><div><strong>趋势交易总览</strong><small>{{ lifecycleSummary }}</small></div><button type="button" :disabled="loading" title="刷新首页统计" @click="load">↻</button></header>

    <!-- 风险灯条：一行读完「今天能不能做、系统会不会自己开仓」。
         独立于绩效统计加载，任何状态下都优先可见；点击进入风险感知明细。 -->
    <section class="risk-strip" :class="finalGate.cls">
      <button type="button" class="risk-light" title="查看风险感知明细" @click="openRiskTab">
        <i class="lamp" />
        <span class="light-text"><b>{{ finalGate.label }}</b><em>{{ finalGate.action }}</em></span>
      </button>

      <button type="button" class="risk-entry" :class="autoEntryOn ? 'on' : 'off'" :title="autoEntryHint" @click="openRiskTab">
        <i class="switch"><u /></i>
        <span><small>自动建仓</small><b>{{ autoEntryLabel }}</b></span>
      </button>

      <div class="risk-dots">
        <button v-for="dot in gateDots" :key="dot.key" type="button" class="dot-item" :title="dot.tip" @click="openRiskTab">
          <i class="dot" :class="dot.cls" />
          <span><small>{{ dot.label }}</small><b>{{ dot.text }}</b></span>
        </button>
      </div>

      <p v-if="riskError" class="risk-alert">风险感知不可用：{{ riskError }} —— 请按最保守口径处理</p>
    </section>

    <div v-if="loading" class="state">正在汇总真实持仓与结算数据…</div>
    <div v-else-if="error" class="state error">{{ error }}</div>
    <template v-else>
      <!-- 绩效指标：数字 + 迷你图。图形负责「好坏多少」一眼可判，
           文字只留必要口径，不再逐卡堆三行说明。 -->
      <section class="metric-band">
        <article class="metric primary">
          <small>已退出胜率</small>
          <b :class="(stats?.win_rate ?? 0) >= 50 ? 'up' : 'down'">{{ stats?.win_rate == null ? '0.0%' : `${stats.win_rate.toFixed(1)}%` }}</b>
          <MiniChart kind="bar" :value="stats?.win_rate ?? 0" />
          <span v-if="stats?.frozen_picks"><i class="up">{{ stats.wins }}</i>/<i class="down">{{ stats.losses }}</i>/<i class="dim">{{ stats.breakeven }}</i> 胜负平</span>
          <span v-else>暂无真实退出样本</span>
        </article>

        <article class="metric">
          <small>已实现收益合计</small>
          <b :class="pctClass(stats?.sum_change_pct)">{{ signed(stats?.sum_change_pct) }}</b>
          <MiniChart kind="gauge" :value="stats?.sum_change_pct ?? 0" :max="gaugeMax(stats?.sum_change_pct, 20)" />
          <span>{{ stats?.frozen_picks || 0 }} 笔已退出</span>
        </article>

        <article class="metric">
          <small>单笔平均</small>
          <b :class="pctClass(stats?.avg_change_pct)">{{ signed(stats?.avg_change_pct) }}</b>
          <MiniChart kind="gauge" :value="stats?.avg_change_pct ?? 0" :max="gaugeMax(stats?.avg_change_pct, 10)" />
          <span>中位数 {{ signed(stats?.median_pct) }}</span>
        </article>

        <article class="metric">
          <small>持有中浮动</small>
          <b :class="pctClass(stats?.unrealized_sum_pct)">{{ signed(stats?.unrealized_sum_pct) }}</b>
          <MiniChart kind="gauge" :value="stats?.unrealized_sum_pct ?? 0" :max="gaugeMax(stats?.unrealized_sum_pct, 10)" />
          <span>{{ stats?.holding_picks || 0 }} 笔 · 均 {{ signed(stats?.unrealized_avg_pct) }}</span>
        </article>

        <article class="metric">
          <small>标准盈亏因子</small>
          <b>{{ stats?.profit_factor == null ? ((stats?.wins || 0) > 0 && (stats?.losses || 0) === 0 ? '∞' : '—') : stats.profit_factor.toFixed(2) }}</b>
          <MiniChart kind="split" :segments="profitSegments" />
          <span>盈 {{ signed(stats?.gross_profit_pct) }} · 亏 {{ signed(stats?.gross_loss_pct) }}</span>
        </article>

        <article class="metric">
          <small>平均持有时间</small>
          <b>{{ stats?.avg_hold_days == null ? '—' : `${stats.avg_hold_days.toFixed(1)} 天` }}</b>
          <MiniChart kind="bar" :value="stats?.avg_hold_days ?? 0" :max="15" />
          <span>上限 15 天 · 仅已退出</span>
        </article>

        <article class="metric">
          <small>生命周期分布</small>
          <b>{{ stats?.lifecycle_picks || 0 }} 笔</b>
          <MiniChart kind="stack" :segments="lifecycleSegments" />
          <span class="legend-inline">
            <i class="k warn" />待建 {{ stats?.pending_picks || 0 }}
            <i class="k up" />持有 {{ stats?.holding_picks || 0 }}
            <i class="k down" />退出 {{ stats?.exited_picks || 0 }}
            <i class="k flat" />过期 {{ stats?.expired_picks || 0 }}
          </span>
        </article>

        <article class="metric reference">
          <small>历史参考胜率</small>
          <b :class="(stats?.reference_win_rate ?? 0) >= 50 ? 'up' : 'down'">{{ stats?.reference_win_rate == null ? '0.0%' : `${stats.reference_win_rate.toFixed(1)}%` }}</b>
          <MiniChart kind="split" :segments="referenceWinSegments" />
          <span>{{ stats?.reference_frozen_picks || 0 }} 笔规则退出</span>
        </article>

        <article class="metric reference">
          <small>历史参考收益点数</small>
          <b :class="pctClass(stats?.reference_sum_change_pct)">{{ signedPoints(stats?.reference_sum_change_pct) }}</b>
          <MiniChart kind="gauge" :value="stats?.reference_sum_change_pct ?? 0" :max="gaugeMax(stats?.reference_sum_change_pct, 30)" />
          <span>{{ stats?.reference_picks || 0 }} 只旧推荐 · 不计入真实交易</span>
        </article>

        <article class="metric">
          <small>已退出极值</small>
          <b class="range"><i class="up">{{ signed(stats?.best_pct) }}</i><em>/</em><i class="down">{{ signed(stats?.worst_pct) }}</i></b>
          <MiniChart kind="split" :segments="extremeSegments" />
          <span>{{ stats?.best_name || '—' }} / {{ stats?.worst_name || '—' }}</span>
        </article>
      </section>

      <section class="dashboard-grid">
        <article class="panel basket-panel">
          <header class="basket-header">
            <div><b>每日三只趋势推荐组合</b><small>唯一最强按手动建仓/平仓结算；其余 2 只按次日开盘至第 10 个交易日收盘结算</small></div>
            <div class="basket-summary" aria-label="组合表现摘要">
              <span><small>最新</small><strong :class="pctClass(basketChart.latest?.value)">{{ signed(basketChart.latest?.value) }}</strong></span>
              <span><small>最高</small><strong class="up">{{ signed(basketChart.best?.value) }}</strong></span>
              <span><small>最低</small><strong class="down">{{ signed(basketChart.worst?.value) }}</strong></span>
            </div>
          </header>
          <div v-if="basketChart.values.length" ref="basketChartElement" class="chart-wrap basket-chart" @mouseleave="activeBasketDate = ''">
            <svg :width="basketChart.width" :height="basketChart.height" :viewBox="`0 0 ${basketChart.width} ${basketChart.height}`" role="img" aria-label="每日三只趋势推荐等权平均表现">
              <defs>
                <!-- 以零轴为界裁剪：上方盈利区填红，下方亏损区填绿 -->
                <clipPath id="basketGainClip"><rect :x="basketChart.left" :y="basketChart.top" :width="basketChart.width-basketChart.left-basketChart.right" :height="Math.max(0,basketChart.zeroY-basketChart.top)"/></clipPath>
                <clipPath id="basketLossClip"><rect :x="basketChart.left" :y="basketChart.zeroY" :width="basketChart.width-basketChart.left-basketChart.right" :height="Math.max(0,basketChart.plotBottom-basketChart.zeroY)"/></clipPath>
              </defs>
              <g class="basket-grid"><line v-for="tick in basketChart.grid" :key="tick.value" :x1="basketChart.left" :x2="basketChart.width-basketChart.right" :y1="tick.y" :y2="tick.y"/><text v-for="tick in basketChart.grid" :key="`label-${tick.value}`" :x="basketChart.left-8" :y="tick.y+3">{{ tick.value.toFixed(1) }}</text></g>
              <path :d="basketChart.areaPath" class="basket-area gain" clip-path="url(#basketGainClip)"/>
              <path :d="basketChart.areaPath" class="basket-area loss" clip-path="url(#basketLossClip)"/>
              <line :x1="basketChart.left" :x2="basketChart.width-basketChart.right" :y1="basketChart.zeroY" :y2="basketChart.zeroY" class="basket-zero"/>
              <path :d="basketChart.smoothPath" class="basket-line gain" clip-path="url(#basketGainClip)"/>
              <path :d="basketChart.smoothPath" class="basket-line loss" clip-path="url(#basketLossClip)"/>
              <g v-for="(point,index) in basketChart.values" :key="point.date" class="basket-hit" tabindex="0" role="button" :aria-label="`${point.date}，等权平均 ${signed(point.value)}`" @mouseenter="activeBasketDate=point.date" @focus="activeBasketDate=point.date" @blur="activeBasketDate=''">
                <circle :cx="basketChart.x(index)" :cy="basketChart.y(point.value)" r="11" class="basket-target"/>
                <circle :cx="basketChart.x(index)" :cy="basketChart.y(point.value)" :r="activeBasketPoint?.date===point.date?5:3.5" :class="['basket-point',point.value>=0?'gain':'loss',activeBasketPoint?.date===point.date?'active':'']"/>
              </g>
              <text v-for="label in basketChart.labels" :key="label.date" :x="basketChart.x(label.index)" :y="basketChart.height-8" class="axis x">{{ label.date.slice(5) }}</text>
              <g v-if="activeBasketPoint" class="basket-tooltip" :transform="`translate(${Math.min(Math.max(basketChart.x(basketChart.values.indexOf(activeBasketPoint)), basketChart.left+72), basketChart.width-basketChart.right-72)},${Math.max(42,basketChart.y(activeBasketPoint.value)-34)})`">
                <rect x="-68" y="-22" width="136" height="38" rx="3"/>
                <text y="-7">{{ activeBasketPoint.date }}</text><text y="9" :class="activeBasketPoint.value>=0?'gain':'loss'">平均 {{ signed(activeBasketPoint.value) }}</text>
              </g>
            </svg>
          </div>
          <footer>
            <span>{{ basketChart.values.length }} 个推荐日 · {{ basketChart.samples }} 只样本</span>
            <i class="legend"><em class="gain"></em>盈利<em class="loss"></em>亏损</i>
            <strong>已冻结 {{ basketChart.frozen }} · 跟踪中 {{ basketChart.tracking }}</strong>
          </footer>
          <div class="rule-entry">
            <span>组合口径：每日 3 只等权；唯一最强使用手动交易结果，其余 2 只从次日开盘跟踪并在第 10 个交易日收盘冻结。</span>
            <RouterLink to="/rules">查看完整规则档案 <span aria-hidden="true">→</span></RouterLink>
          </div>
        </article>

        <article class="panel equity-panel">
          <header><div><b>已实现收益点数累计</b><small>仅按真实退出交易逐笔累计；用于策略观察，不代表账户净值</small></div><strong :class="pctClass(equityChart.total)">{{ signed(equityChart.total) }}</strong></header>
          <div v-if="equityChart.values.length" class="chart-wrap"><svg :viewBox="`0 0 ${equityChart.width} ${equityChart.height}`" role="img" aria-label="已实现收益点数累计曲线">
            <line :x1="equityChart.left" :x2="equityChart.width - equityChart.right" :y1="equityChart.zeroY" :y2="equityChart.zeroY" class="zero"/><polygon :points="equityChart.area" class="area"/><polyline :points="equityChart.points" class="line"/>
            <circle v-for="(point,index) in equityChart.values" :key="point.id" :cx="equityChart.x(index)" :cy="equityChart.y(point.value)" r="3" class="point"><title>{{ point.date }} {{ point.name }}：本笔 {{ signed(point.changePct) }}，累计 {{ signed(point.value) }}</title></circle>
            <text :x="equityChart.left-8" :y="equityChart.y(equityChart.max)+4" class="axis y">{{ equityChart.max.toFixed(1) }}</text><text :x="equityChart.left-8" :y="equityChart.y(equityChart.min)+4" class="axis y">{{ equityChart.min.toFixed(1) }}</text>
            <text v-for="label in equityChart.labels" :key="label.date" :x="equityChart.x(equityChart.values.indexOf(label))" :y="equityChart.height-8" class="axis x">{{ label.date.slice(5) }}</text>
          </svg></div><div v-else class="empty">{{ stats?.pending_picks ? '等待实际建仓与退出后生成收益曲线' : '暂无可绘制的真实交易收益' }}</div>
        </article>

        <article class="panel signal-panel">
          <header><div><b>当前趋势持仓</b><small>30分钟分析 · 大盘/板块/个股三层风控</small></div><i class="watching">活跃 {{ activePositionCount }}/10</i></header>
          <div v-if="latestExit" class="signal exit"><small>{{ latestExit.source === 'manual' ? '最新手动平仓' : '最新平仓建议' }} · {{ latestExit.created_at.slice(11) }}</small><button type="button" @click="openStock(latestExit.symbol)">{{ latestExit.name || latestExit.symbol }}<em>{{ latestExit.code }}</em></button><p>{{ latestExit.reason }}</p></div>
          <div v-else-if="latestEntry" class="signal entry"><small>{{ latestEntry.source === 'manual' ? '最新手动建仓' : '最新建仓建议' }} · {{ latestEntry.created_at.slice(11) }}</small><button type="button" @click="openStock(latestEntry.symbol)">{{ latestEntry.name || latestEntry.symbol }}<em>{{ latestEntry.code }}</em></button><p>{{ latestEntry.reason }}</p></div>
          <div v-else class="signal waiting"><small>最新结论</small><b>等待趋势确认</b><p>{{ entryAdvice?.items.find(item => item.action === 'wait')?.reason || '当前尚未给出建仓建议。' }}</p></div>
          <div v-if="dailyPick" class="daily-pick"><span>今日唯一最强</span><button type="button" @click="openStock(dailyPick.symbol)">{{ dailyPick.name || dailyPick.symbol }} <small>{{ dailyPick.code }}</small></button><p>{{ dailyPick.reason }}</p></div>
        </article>

        <article class="panel reason-panel">
          <header>
            <div><b>最新推荐</b><small>趋势强度与安全度对比，点击查看个股</small></div>
            <button type="button" class="link-btn" @click="openTradeTab">历史推荐 →</button>
          </header>
          <div v-if="reasonRows.length" class="reason-list"><button v-for="item in reasonRows" :key="item.symbol" type="button" class="reason-row" @click="openStock(item.symbol)">
            <span class="rank">{{ item.rank }}</span><span class="stock"><b>{{ item.name }}</b><small>{{ item.code }} · {{ item.sector }}</small></span>
            <span class="bars"><i><em>趋势</em><u><b :style="{width:`${item.strength}%`}"></b></u><strong>{{ item.probability.toFixed(1) }}</strong></i><i><em>安全</em><u class="safe"><b :style="{width:`${item.safety}%`}"></b></u><strong>{{ item.safety.toFixed(0) }}</strong></i></span>
            <span class="reason">{{ item.reason }}</span><span class="return" :class="pctClass(item.change_pct)">{{ fmtPct(item.change_pct) }}<small>{{ recommendationStatus(item) }}</small></span>
          </button></div><div v-else class="empty">暂无推荐数据</div>
        </article>

        <!-- 待办：只保留「需要你动手」的持仓。完整历史明细、建平仓确认与
             蒙特卡洛都在「交易」Tab，首页不再重复整张表。 -->
        <article class="panel action-panel">
          <header>
            <div><b>待办持仓</b><small>仅列出等待建仓与持有中；收益已扣往返成本</small></div>
            <button type="button" class="link-btn" @click="openTradeTab">全部明细与建平仓 →</button>
          </header>
          <div v-if="actionRows.length" class="action-table">
            <button v-for="item in actionRows" :key="item.id" type="button" class="action-row" @click="openStock(item.symbol)">
              <span class="status" :class="item.status">{{ statusLabel(item.status) }}</span>
              <span class="stock"><b>{{ item.name || item.symbol }}</b><small>{{ item.code }}</small></span>
              <span class="num"><small>成本</small>{{ item.entry_price == null ? '—' : item.entry_price.toFixed(2) }}</span>
              <span class="num"><small>现价</small>{{ referenceLabel(item) }}</span>
              <strong :class="pctClass(item.change_pct)">{{ signed(item.change_pct) }}</strong>
              <span class="reason">
                <em v-if="item.exit_kind" class="exit-kind" :class="item.exit_kind">{{ exitKindLabel(item.exit_kind) }}</em>
                {{ item.exit_reason || (item.status === 'holding' ? `已持有 ${item.hold_days} 个交易日` : '等待手动建仓') }}
              </span>
            </button>
          </div>
          <div v-else class="empty">当前没有需要处理的持仓</div>
        </article>
      </section>
    </template>
  </main>
</template>

<style scoped>
.overview{min-width:0;min-height:0;overflow:auto;padding:14px;background:#0f1826;color:#e7ecf4}.overview-header{display:flex;align-items:center;justify-content:space-between;margin-bottom:12px}.overview-header>div{display:flex;align-items:baseline;gap:10px}.overview-header strong{font-size:17px}.overview-header small{color:#8895ab;font-size:11px}.overview-header>button{width:30px;height:30px;border:1px solid #3a496a;border-radius:0;background:#1c2a47;color:#c4cddc;cursor:pointer;font-size:18px}.state,.empty{display:grid;min-height:120px;place-items:center;color:#75839a;font-size:13px}.state.error{color:#ef7d84}/* ---- 风险灯条 ---- */
.risk-strip{display:flex;flex-wrap:wrap;align-items:stretch;gap:0;margin-bottom:10px;border:1px solid #26324a;border-left:4px solid #6b7b94;background:#131e33}
.risk-strip.lv-green{border-left-color:#55b996}.risk-strip.lv-yellow{border-left-color:#e9c16c}.risk-strip.lv-red{border-left-color:#ef6a72}
.risk-strip button{border:0;border-radius:0;background:transparent;color:#e7ecf4;cursor:pointer;text-align:left}
.risk-light{display:flex;align-items:center;gap:11px;padding:10px 16px 10px 14px;border-right:1px solid #26324a!important}
.risk-light:hover,.risk-entry:hover,.dot-item:hover{background:#182338}
.lamp{width:14px;height:14px;flex:0 0 auto;border-radius:50%;background:#6b7b94}
.lv-green .lamp{background:#55b996;box-shadow:0 0 0 4px rgba(85,185,150,.16)}
.lv-yellow .lamp{background:#e9c16c;box-shadow:0 0 0 4px rgba(233,193,108,.16)}
.lv-red .lamp{background:#ef6a72;box-shadow:0 0 0 4px rgba(239,106,114,.18)}
.light-text{display:flex;flex-direction:column;gap:2px}
.light-text b{font-size:19px;line-height:1.1}
.lv-green .light-text b{color:#55b996}.lv-yellow .light-text b{color:#e9c16c}.lv-red .light-text b{color:#ef6a72}
.light-text em{color:#93a0b6;font-size:10.5px;font-style:normal}
/* 自动建仓：用开关形态表达状态，比文字更快识别 */
.risk-entry{display:flex;align-items:center;gap:9px;padding:10px 16px;border-right:1px solid #26324a!important}
.risk-entry .switch{position:relative;display:block;width:30px;height:15px;flex:0 0 auto;border-radius:9px;background:#39445c}
.risk-entry .switch u{position:absolute;top:2px;left:2px;width:11px;height:11px;border-radius:50%;background:#93a0b6;text-decoration:none;transition:left .16s ease,background .16s ease}
.risk-entry.on .switch{background:#1f4c3f}.risk-entry.on .switch u{left:17px;background:#55b996}
.risk-entry span{display:flex;flex-direction:column;gap:2px}
.risk-entry small{color:#8895ab;font-size:10px}
.risk-entry b{font-size:13px}
.risk-entry.on b{color:#55b996}.risk-entry.off b{color:#e9c16c}
/* 三道门收敛为状态点 */
.risk-dots{display:flex;flex:1 1 auto;flex-wrap:wrap;align-items:stretch}
.dot-item{display:flex;align-items:center;gap:8px;padding:10px 16px;border-right:1px solid #26324a!important}
.dot{width:8px;height:8px;flex:0 0 auto;border-radius:50%;background:#6b7b94}
.dot.lv-green{background:#55b996}.dot.lv-yellow{background:#e9c16c}.dot.lv-red{background:#ef6a72}.dot.lv-plain{background:#67a9d8}
.dot-item span{display:flex;flex-direction:column;gap:2px}
.dot-item small{color:#8895ab;font-size:10px}
.dot-item b{font-size:13px;font-variant-numeric:tabular-nums}
.risk-alert{width:100%;margin:0;padding:6px 12px;border-top:1px solid #3a2226;background:#3a2226;color:#ef8b91;font-size:10px}
.metric-band{display:grid;grid-template-columns:repeat(5,minmax(150px,1fr));gap:8px}.metric{display:flex;min-width:0;flex-direction:column;gap:6px;padding:11px 12px;border:1px solid #26324a;background:#131e33}
/* 指标卡内的迷你图与图例 */
.metric .mini{margin:1px 0 2px}
.legend-inline{display:flex;flex-wrap:wrap;align-items:center;gap:3px 7px;white-space:normal!important}
.legend-inline .k{width:7px;height:7px;flex:0 0 auto;border-radius:1px}
.legend-inline .k.up{background:#ef6a72}.legend-inline .k.down{background:#55b996}.legend-inline .k.warn{background:#e9c16c}.legend-inline .k.flat{background:#7b879a}
.metric>span>i{font-style:normal;font-variant-numeric:tabular-nums}
.link-btn{flex:0 0 auto;padding:4px 10px;border:1px solid #3d5680;border-radius:3px;background:#16233b;color:#8fb4e3;cursor:pointer;font-size:10px;white-space:nowrap}
.link-btn:hover,.link-btn:focus-visible{background:#1d3050;color:#bcd6f5;outline:none}.metric.primary{border-color:#476188;background:#1b2a46}.metric.reference{border-color:#554d39;background:#211f1b}.metric.reference>small{color:#c7ac69}.metric>small{color:#8895ab;font-size:10px}.metric>b{overflow:hidden;font-size:21px;font-variant-numeric:tabular-nums;text-overflow:ellipsis;white-space:nowrap}.metric>span{overflow:hidden;color:#8895ab;font-size:10px;text-overflow:ellipsis;white-space:nowrap}.range{display:flex;align-items:center;gap:5px;font-size:16px!important}.range i,.range em{font-style:normal}.range em{color:#5f6d85}.up{color:#ef6a72!important}.down{color:#55b996!important}.dim{color:#93a0b6!important}.dashboard-grid{display:grid;grid-template-columns:minmax(0,2fr) minmax(280px,1fr);gap:10px;margin-top:10px}.panel{min-width:0;border:1px solid #26324a;background:#131e33}.panel>header{display:flex;min-height:48px;align-items:center;justify-content:space-between;gap:12px;padding:9px 12px;border-bottom:1px solid #26324a}.panel>header>div{display:flex;min-width:0;flex-direction:column;gap:3px}.panel>header b{font-size:13px}.panel>header small{color:#8895ab;font-size:10px}.panel>header>strong{font-size:20px;font-variant-numeric:tabular-nums}.basket-panel{grid-column:1/-1;width:100%}.basket-header{align-items:stretch!important}.basket-header>div:first-child{justify-content:center}.basket-summary{display:grid!important;grid-template-columns:repeat(3,minmax(78px,1fr));flex:0 0 auto;border-left:1px solid #26324a}.basket-summary>span{display:flex;min-width:78px;flex-direction:column;justify-content:center;gap:2px;padding:0 14px}.basket-summary small{font-size:9px!important}.basket-summary strong{font-size:14px;font-variant-numeric:tabular-nums}.basket-panel>footer{display:flex;flex-wrap:wrap;align-items:center;gap:8px 16px;padding:8px 12px;border-top:1px solid #26324a;color:#8895ab;font-size:10px}.basket-panel>footer strong{margin-left:auto;color:#b9c2d1;font-weight:500}.basket-chart{width:100%;height:224px;padding:0;overflow:hidden}.basket-chart svg{overflow:visible}.basket-grid line{stroke:#212d42;stroke-width:1}.basket-grid text{fill:#6b7b94;font-size:9px;text-anchor:end;font-variant-numeric:tabular-nums}.basket-zero{stroke:#7286a3;stroke-width:1.1;stroke-dasharray:5 4}.basket-area.gain{fill:#d95561;opacity:.17}.basket-area.loss{fill:#35a37d;opacity:.17}.basket-line{fill:none;stroke-width:2.1;stroke-linecap:round;stroke-linejoin:round;vector-effect:non-scaling-stroke}.basket-line.gain{stroke:#f2606d}.basket-line.loss{stroke:#2eb98a}.basket-target{fill:transparent}.basket-point{stroke:#101a2c;stroke-width:2;vector-effect:non-scaling-stroke;transition:r .12s ease,stroke .12s ease}.basket-point.gain{fill:#f2606d}.basket-point.loss{fill:#2eb98a}.basket-point.active{stroke:#f4f7fb;stroke-width:2.5}.basket-hit{cursor:crosshair;outline:none}.basket-tooltip{pointer-events:none}.basket-tooltip rect{fill:#0b1220;stroke:#42536e;stroke-width:1}.basket-tooltip text{fill:#c2cbdb;font-size:9px;text-anchor:middle;font-variant-numeric:tabular-nums}.basket-tooltip text.gain{fill:#f2606d}.basket-tooltip text.loss{fill:#2eb98a}.legend{display:flex;align-items:center;gap:5px;font-style:normal;color:#7c8aa2}.legend em{width:9px;height:9px;margin-left:7px;border-radius:2px}.legend em.gain{background:#d95561}.legend em.loss{background:#35a37d}.rule-entry{display:flex;flex-wrap:wrap;align-items:center;justify-content:space-between;gap:8px 14px;padding:9px 12px;border-top:1px solid #26324a;background:#101b2e;color:#8895ab;font-size:10px;line-height:1.6}.rule-entry a{padding:4px 11px;border:1px solid #3d5680;border-radius:3px;background:#16233b;color:#8fb4e3;font-size:10px;white-space:nowrap;transition:background .15s ease,color .15s ease}.rule-entry a:hover,.rule-entry a:focus-visible{background:#1d3050;color:#bcd6f5;outline:none}.chart-wrap{height:224px;padding:3px 8px 0}.chart-wrap svg{display:block;width:100%;height:100%}.zero{stroke:#3a496a;stroke-width:1;stroke-dasharray:4 4}.area{fill:#39648a;opacity:.17}.line{fill:none;stroke:#67a9d8;stroke-width:2.2;vector-effect:non-scaling-stroke}.point{fill:#e9c16c;stroke:#131e33;stroke-width:1.5}.point.tracking{fill:#7b879a}.axis{fill:#71809a;font-size:10px}.axis.y{text-anchor:end}.axis.x{text-anchor:middle}.signal-panel>header i{padding:3px 7px;font-size:10px;font-style:normal}.paused{background:#3d3423;color:#e9c16c}.watching{background:#19352e;color:#55b996}.signal{display:flex;min-height:108px;flex-direction:column;justify-content:center;gap:6px;padding:13px}.signal.entry{border-left:3px solid #ef6a72;background:#251f2a}.signal.exit{border-left:3px solid #e9c16c;background:#2b2620}.signal.waiting{border-left:3px solid #55b996}.signal small{color:#8895ab;font-size:10px}.signal button{align-self:flex-start;padding:0;border:0;background:transparent;color:#ef8b91;cursor:pointer;font-size:19px;font-weight:700}.signal button em{margin-left:7px;color:#8895ab;font-size:11px;font-style:normal;font-weight:400}.signal>b{font-size:18px}.signal p,.daily-pick p{margin:0;color:#b4bece;font-size:11px;line-height:1.5}.daily-pick{display:grid;grid-template-columns:62px 1fr;gap:4px 8px;padding:10px 13px;border-top:1px solid #26324a;background:#182338}.daily-pick>span{grid-row:1/3;align-self:center;color:#e9c16c;font-size:10px}.daily-pick button{justify-self:start;padding:0;border:0;background:transparent;color:#e7ecf4;cursor:pointer;font-size:13px;font-weight:700}.daily-pick button small{margin-left:5px;color:#8895ab;font-size:10px}.daily-pick p{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.reason-panel{grid-column:1/-1}.reason-list{display:grid}.reason-row{display:grid;grid-template-columns:34px 150px minmax(220px,.8fr) minmax(220px,1.2fr) 85px;gap:12px;align-items:center;padding:11px 12px;border:0;border-bottom:1px solid #202c42;background:transparent;color:#e7ecf4;text-align:left;cursor:pointer}.reason-row:hover{background:#182338}.rank{display:grid;width:25px;height:25px;place-items:center;background:#2a3a5c;color:#e9c16c;font-weight:700}.stock b{display:block;font-size:13px}.stock small{display:block;margin-top:2px;color:#8895ab;font-size:10px}.bars{display:flex;flex-direction:column;gap:5px}.bars>i{display:grid;grid-template-columns:30px minmax(80px,1fr) 34px;gap:6px;align-items:center;font-style:normal}.bars em{color:#8895ab;font-size:9px;font-style:normal}.bars u{height:5px;overflow:hidden;background:#25334b;text-decoration:none}.bars u>b{display:block;height:100%;background:#df6a71}.bars u.safe>b{background:#55b996}.bars strong{color:#aeb8c9;font-size:10px;font-weight:400}.reason{color:#b8c1d0;font-size:11px;line-height:1.45}.return{text-align:right;font-size:14px;font-weight:700}.return small{display:block;margin-top:3px;color:#8895ab;font-size:9px;font-weight:400}.action-panel{grid-column:1/-1}.action-table{display:grid}.action-row{display:grid;grid-template-columns:76px minmax(120px,180px) 82px 82px 82px minmax(160px,1fr);gap:10px;align-items:center;padding:9px 12px;border:0;border-bottom:1px solid #202c42;background:transparent;color:#e7ecf4;text-align:left;cursor:pointer}.action-row:hover{background:#182338}.action-row>span{font-size:11px}.action-row .num>small{display:block;margin-bottom:2px;color:#71809a;font-size:9px}.action-row .status{justify-self:start;padding:3px 7px;background:#25334b;font-size:9px}.action-row .status.holding{background:#34272d;color:#ef8b91}.action-row .status.pending_entry{background:#3d3423;color:#e9c16c}.action-row>strong{text-align:right;font-size:13px;font-variant-numeric:tabular-nums}.exit-kind{margin-right:6px;padding:2px 5px;background:#25334b;color:#aeb8c9;font-size:9px;font-style:normal}.exit-kind.stop_loss{background:#3a2226;color:#ef8b91}.exit-kind.trailing_stop,.exit-kind.take_profit{background:#19352e;color:#55b996}.exit-kind.time_stop{background:#3d3423;color:#e9c16c}.exit-kind.systemic{background:#3a2226;color:#ef8b91}@media(max-width:1200px){.metric-band{grid-template-columns:repeat(3,minmax(140px,1fr))}.dashboard-grid{grid-template-columns:1fr}.reason-panel{grid-column:auto}.reason-row{grid-template-columns:34px 130px minmax(180px,1fr) 70px}.reason-row .reason{display:none}.action-row{grid-template-columns:76px minmax(110px,1fr) 82px 82px}.action-row .num:nth-of-type(2),.action-row .reason{display:none}}@media(max-width:700px){.overview{padding:8px}.metric-band{grid-template-columns:repeat(2,minmax(0,1fr))}.risk-light,.risk-entry,.dot-item{padding:8px 12px}.risk-dots{width:100%;border-top:1px solid #26324a}.overview-header small{display:none}.basket-header{flex-direction:column}.basket-header>div:first-child{min-height:38px}.basket-summary{width:100%;border-top:1px solid #26324a;border-left:0}.basket-summary>span{min-width:0;padding:7px 9px}.basket-chart{height:210px}.basket-panel>footer strong{margin-left:0}.reason-row{grid-template-columns:30px minmax(90px,1fr) 70px;gap:7px}.reason-row .bars,.reason-row .reason{display:none}.baseline-row{grid-template-columns:90px minmax(50px,1fr) 58px}.baseline-row>em{display:none}}
</style>
