<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { api, fmtPct, pctClass, type EntryAdviceResponse, type Position, type Recommendation, type RecommendationStats } from '../api'

const router = useRouter()
const loading = ref(true)
const error = ref('')
const recommendations = ref<Recommendation[]>([])
const stats = ref<RecommendationStats | null>(null)
const entryAdvice = ref<EntryAdviceResponse | null>(null)
const positions = ref<Position[]>([])

function signed(value: number | null | undefined, digits = 2) {
  if (value == null) return '—'
  return `${value >= 0 ? '+' : ''}${value.toFixed(digits)}%`
}

const statusLabels: Record<Position['status'], string> = {
  pending_entry: '等待建仓', holding: '持有中', exited: '已退出', expired: '未建仓过期',
}
const lifecycleRows = computed(() => positions.value.slice(0, 12))
function statusLabel(status: Position['status']) { return statusLabels[status] || status }
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
    return `${stats.value.pending_picks} 只等待盘中 AI 给出建仓区间，未建仓不计算收益`
  }
  return '只统计真实建仓生命周期；持有中计浮盈，AI/硬风控退出后才冻结收益'
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
const reasonRows = computed(() => recommendations.value.map(item => ({ ...item, strength: Math.max(0, Math.min(100, item.probability)), safety: Math.max(0, Math.min(100, 100 - (item.risk_score ?? 50))) })))

async function load() {
  loading.value = true; error.value = ''
  try {
    const [reco, overall, entry, lifecycle] = await Promise.all([api.recommendations(), api.recommendationStats(60), api.entryAdvice(), api.positions(200)])
    recommendations.value = reco; stats.value = overall; entryAdvice.value = entry; positions.value = lifecycle.items
  } catch (e: any) { error.value = e?.message || '首页统计加载失败' } finally { loading.value = false }
}
function openStock(symbol: string) { router.push(`/stock/${symbol}`) }
onMounted(load)
</script>

<template>
  <main class="overview">
    <header class="overview-header"><div><strong>趋势交易总览</strong><small>{{ lifecycleSummary }}</small></div><button type="button" :disabled="loading" title="刷新首页统计" @click="load">↻</button></header>
    <div v-if="loading" class="state">正在汇总真实持仓与结算数据…</div>
    <div v-else-if="error" class="state error">{{ error }}</div>
    <template v-else>
      <section class="metric-band">
        <article class="metric primary"><small>已退出胜率</small><b :class="(stats?.win_rate ?? 0) >= 50 ? 'up' : 'down'">{{ stats?.win_rate == null ? '0.0%' : `${stats.win_rate.toFixed(1)}%` }}</b><span>{{ stats?.frozen_picks ? `${stats.wins} 胜 / ${stats.losses} 负 / ${stats.breakeven} 平` : '暂无真实退出样本' }}</span></article>
        <article class="metric"><small>已实现收益合计</small><b :class="pctClass(stats?.sum_change_pct)">{{ signed(stats?.sum_change_pct) }}</b><span>{{ stats?.frozen_picks || 0 }} 笔已退出 · 非账户收益率</span></article>
        <article class="metric"><small>单笔平均 / 中位数</small><b :class="pctClass(stats?.avg_change_pct)">{{ signed(stats?.avg_change_pct) }}</b><span>中位数 {{ signed(stats?.median_pct) }}</span></article>
        <article class="metric"><small>持有中浮动收益</small><b :class="pctClass(stats?.unrealized_sum_pct)">{{ signed(stats?.unrealized_sum_pct) }}</b><span>{{ stats?.holding_picks || 0 }} 笔 · 均值 {{ signed(stats?.unrealized_avg_pct) }}</span></article>
        <article class="metric"><small>标准盈亏因子</small><b>{{ stats?.profit_factor == null ? ((stats?.wins || 0) > 0 && (stats?.losses || 0) === 0 ? '∞' : '—') : stats.profit_factor.toFixed(2) }}</b><span>总盈利 {{ signed(stats?.gross_profit_pct) }} / 总亏损 {{ signed(stats?.gross_loss_pct) }}</span></article>
        <article class="metric"><small>平均持有时间</small><b>{{ stats?.avg_hold_days == null ? '—' : `${stats.avg_hold_days.toFixed(1)} 天` }}</b><span>仅统计已退出交易</span></article>
        <article class="metric"><small>生命周期分布</small><b class="lifecycle-count">{{ stats?.lifecycle_picks || 0 }} 笔</b><span>待建 {{ stats?.pending_picks || 0 }} · 持有 {{ stats?.holding_picks || 0 }} · 退出 {{ stats?.exited_picks || 0 }} · 过期 {{ stats?.expired_picks || 0 }}</span></article>
        <article class="metric reference"><small>历史参考胜率</small><b :class="(stats?.reference_win_rate ?? 0) >= 50 ? 'up' : 'down'">{{ stats?.reference_win_rate == null ? '0.0%' : `${stats.reference_win_rate.toFixed(1)}%` }}</b><span>{{ stats?.reference_wins || 0 }} 胜 / {{ stats?.reference_losses || 0 }} 负 · {{ stats?.reference_frozen_picks || 0 }} 笔规则退出</span></article>
        <article class="metric reference"><small>历史参考收益点数</small><b :class="pctClass(stats?.reference_sum_change_pct)">{{ signed(stats?.reference_sum_change_pct) }}</b><span>{{ stats?.reference_picks || 0 }} 只旧推荐 · 不计入真实交易</span></article>
        <article class="metric"><small>已退出极值</small><b class="range"><i class="up">{{ signed(stats?.best_pct) }}</i><em>/</em><i class="down">{{ signed(stats?.worst_pct) }}</i></b><span>{{ stats?.best_name || '—' }} / {{ stats?.worst_name || '—' }}</span></article>
      </section>

      <section class="dashboard-grid">
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
          <div v-if="latestExit" class="signal exit"><small>最新退出 · {{ latestExit.created_at.slice(11) }}</small><button type="button" @click="openStock(latestExit.symbol)">{{ latestExit.name || latestExit.symbol }}<em>{{ latestExit.code }}</em></button><p>{{ latestExit.reason }}</p></div>
          <div v-else-if="latestEntry" class="signal entry"><small>最新建仓 · {{ latestEntry.created_at.slice(11) }}</small><button type="button" @click="openStock(latestEntry.symbol)">{{ latestEntry.name || latestEntry.symbol }}<em>{{ latestEntry.code }}</em></button><p>{{ latestEntry.reason }}</p></div>
          <div v-else class="signal waiting"><small>最新结论</small><b>等待趋势确认</b><p>{{ entryAdvice?.items.find(item => item.action === 'wait')?.reason || '当前尚未给出建仓建议。' }}</p></div>
          <div v-if="dailyPick" class="daily-pick"><span>今日首选</span><button type="button" @click="openStock(dailyPick.symbol)">{{ dailyPick.name || dailyPick.symbol }} <small>{{ dailyPick.code }}</small></button><p>{{ dailyPick.reason }}</p></div>
        </article>

        <article class="panel reason-panel">
          <header><div><b>最新推荐依据</b><small>动量强度与安全度并排比较，点击查看个股</small></div></header>
          <div v-if="reasonRows.length" class="reason-list"><button v-for="item in reasonRows" :key="item.symbol" type="button" class="reason-row" @click="openStock(item.symbol)">
            <span class="rank">{{ item.rank }}</span><span class="stock"><b>{{ item.name }}</b><small>{{ item.code }} · {{ item.sector }}</small></span>
            <span class="bars"><i><em>趋势</em><u><b :style="{width:`${item.strength}%`}"></b></u><strong>{{ item.probability.toFixed(1) }}</strong></i><i><em>安全</em><u class="safe"><b :style="{width:`${item.safety}%`}"></b></u><strong>{{ item.safety.toFixed(0) }}</strong></i></span>
            <span class="reason">{{ item.reason }}</span><span class="return" :class="pctClass(item.change_pct)">{{ fmtPct(item.change_pct) }}<small>{{ recommendationStatus(item) }}</small></span>
          </button></div><div v-else class="empty">暂无推荐数据</div>
        </article>

        <article class="panel lifecycle-panel">
          <header><div><b>最近生命周期明细</b><small>旧推荐历史无生命周期记录，不进入本表和交易统计</small></div></header>
          <div v-if="lifecycleRows.length" class="lifecycle-table">
            <button v-for="item in lifecycleRows" :key="item.id" type="button" class="lifecycle-row" @click="openStock(item.symbol)">
              <span class="stock"><b>{{ item.name || item.symbol }}</b><small>{{ item.code }} · 入池 {{ item.pick_date }}</small></span>
              <span class="status" :class="item.status">{{ statusLabel(item.status) }}</span>
              <span><small>成本</small>{{ item.entry_price == null ? '—' : item.entry_price.toFixed(2) }}</span>
              <span><small>{{ item.status === 'exited' ? '退出价' : '参考价' }}</small>{{ referenceLabel(item) }}</span>
              <strong :class="pctClass(item.change_pct)">{{ signed(item.change_pct) }}</strong>
              <span class="reason">{{ item.exit_reason || (item.status === 'holding' ? `已持有 ${item.hold_days} 个交易日` : '等待盘中信号') }}</span>
            </button>
          </div>
          <div v-else class="empty">尚无真实持仓生命周期记录</div>
        </article>
      </section>
    </template>
  </main>
</template>

<style scoped>
.overview{min-width:0;min-height:0;overflow:auto;padding:14px;background:#0f1826;color:#e7ecf4}.overview-header{display:flex;align-items:center;justify-content:space-between;margin-bottom:12px}.overview-header>div{display:flex;align-items:baseline;gap:10px}.overview-header strong{font-size:17px}.overview-header small{color:#8895ab;font-size:11px}.overview-header>button{width:30px;height:30px;border:1px solid #3a496a;border-radius:0;background:#1c2a47;color:#c4cddc;cursor:pointer;font-size:18px}.state,.empty{display:grid;min-height:120px;place-items:center;color:#75839a;font-size:13px}.state.error{color:#ef7d84}.metric-band{display:grid;grid-template-columns:repeat(6,minmax(130px,1fr));gap:8px}.metric{display:flex;min-width:0;flex-direction:column;gap:5px;padding:11px 12px;border:1px solid #26324a;background:#131e33}.metric.primary{border-color:#476188;background:#1b2a46}.metric.reference{border-color:#554d39;background:#211f1b}.metric.reference>small{color:#c7ac69}.metric>small{color:#8895ab;font-size:10px}.metric>b{overflow:hidden;font-size:21px;font-variant-numeric:tabular-nums;text-overflow:ellipsis;white-space:nowrap}.metric>span{overflow:hidden;color:#8895ab;font-size:10px;text-overflow:ellipsis;white-space:nowrap}.range{display:flex;align-items:center;gap:5px;font-size:16px!important}.range i,.range em{font-style:normal}.range em{color:#5f6d85}.up{color:#ef6a72!important}.down{color:#55b996!important}.dim{color:#93a0b6!important}.dashboard-grid{display:grid;grid-template-columns:minmax(0,2fr) minmax(280px,1fr);gap:10px;margin-top:10px}.panel{min-width:0;border:1px solid #26324a;background:#131e33}.panel>header{display:flex;min-height:48px;align-items:center;justify-content:space-between;gap:12px;padding:9px 12px;border-bottom:1px solid #26324a}.panel>header>div{display:flex;min-width:0;flex-direction:column;gap:3px}.panel>header b{font-size:13px}.panel>header small{color:#8895ab;font-size:10px}.panel>header>strong{font-size:20px;font-variant-numeric:tabular-nums}.chart-wrap{height:224px;padding:3px 8px 0}.chart-wrap svg{display:block;width:100%;height:100%}.zero{stroke:#3a496a;stroke-width:1;stroke-dasharray:4 4}.area{fill:#39648a;opacity:.17}.line{fill:none;stroke:#67a9d8;stroke-width:2.2;vector-effect:non-scaling-stroke}.point{fill:#e9c16c;stroke:#131e33;stroke-width:1.5}.point.tracking{fill:#7b879a}.axis{fill:#71809a;font-size:10px}.axis.y{text-anchor:end}.axis.x{text-anchor:middle}.signal-panel>header i{padding:3px 7px;font-size:10px;font-style:normal}.paused{background:#3d3423;color:#e9c16c}.watching{background:#19352e;color:#55b996}.signal{display:flex;min-height:108px;flex-direction:column;justify-content:center;gap:6px;padding:13px}.signal.entry{border-left:3px solid #ef6a72;background:#251f2a}.signal.exit{border-left:3px solid #e9c16c;background:#2b2620}.signal.waiting{border-left:3px solid #55b996}.signal small{color:#8895ab;font-size:10px}.signal button{align-self:flex-start;padding:0;border:0;background:transparent;color:#ef8b91;cursor:pointer;font-size:19px;font-weight:700}.signal button em{margin-left:7px;color:#8895ab;font-size:11px;font-style:normal;font-weight:400}.signal>b{font-size:18px}.signal p,.daily-pick p{margin:0;color:#b4bece;font-size:11px;line-height:1.5}.daily-pick{display:grid;grid-template-columns:62px 1fr;gap:4px 8px;padding:10px 13px;border-top:1px solid #26324a;background:#182338}.daily-pick>span{grid-row:1/3;align-self:center;color:#e9c16c;font-size:10px}.daily-pick button{justify-self:start;padding:0;border:0;background:transparent;color:#e7ecf4;cursor:pointer;font-size:13px;font-weight:700}.daily-pick button small{margin-left:5px;color:#8895ab;font-size:10px}.daily-pick p{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.reason-panel{grid-column:1/-1}.reason-list{display:grid}.reason-row{display:grid;grid-template-columns:34px 150px minmax(220px,.8fr) minmax(220px,1.2fr) 85px;gap:12px;align-items:center;padding:11px 12px;border:0;border-bottom:1px solid #202c42;background:transparent;color:#e7ecf4;text-align:left;cursor:pointer}.reason-row:hover{background:#182338}.rank{display:grid;width:25px;height:25px;place-items:center;background:#2a3a5c;color:#e9c16c;font-weight:700}.stock b{display:block;font-size:13px}.stock small{display:block;margin-top:2px;color:#8895ab;font-size:10px}.bars{display:flex;flex-direction:column;gap:5px}.bars>i{display:grid;grid-template-columns:30px minmax(80px,1fr) 34px;gap:6px;align-items:center;font-style:normal}.bars em{color:#8895ab;font-size:9px;font-style:normal}.bars u{height:5px;overflow:hidden;background:#25334b;text-decoration:none}.bars u>b{display:block;height:100%;background:#df6a71}.bars u.safe>b{background:#55b996}.bars strong{color:#aeb8c9;font-size:10px;font-weight:400}.reason{color:#b8c1d0;font-size:11px;line-height:1.45}.return{text-align:right;font-size:14px;font-weight:700}.return small{display:block;margin-top:3px;color:#8895ab;font-size:9px;font-weight:400}.lifecycle-panel{grid-column:1/-1}.lifecycle-table{display:grid}.lifecycle-row{display:grid;grid-template-columns:160px 92px 82px 82px 80px minmax(180px,1fr);gap:10px;align-items:center;padding:9px 12px;border:0;border-bottom:1px solid #202c42;background:transparent;color:#e7ecf4;text-align:left;cursor:pointer}.lifecycle-row:hover{background:#182338}.lifecycle-row>span{font-size:11px}.lifecycle-row>span>small{display:block;margin-bottom:2px;color:#71809a;font-size:9px}.lifecycle-row .status{justify-self:start;padding:3px 6px;background:#25334b;font-size:9px}.lifecycle-row .status.holding{background:#34272d;color:#ef8b91}.lifecycle-row .status.pending_entry{background:#3d3423;color:#e9c16c}.lifecycle-row .status.exited{background:#19352e;color:#55b996}.lifecycle-row .status.expired{color:#8895ab}.lifecycle-row>strong{text-align:right;font-size:13px}.lifecycle-row .reason{overflow:hidden;color:#8895ab;text-overflow:ellipsis;white-space:nowrap}@media(max-width:1200px){.metric-band{grid-template-columns:repeat(3,minmax(130px,1fr))}.dashboard-grid{grid-template-columns:1fr}.reason-panel{grid-column:auto}.reason-row{grid-template-columns:34px 130px minmax(180px,1fr) 70px}.reason-row .reason{display:none}}@media(max-width:700px){.overview{padding:8px}.metric-band{grid-template-columns:repeat(2,minmax(0,1fr))}.overview-header small{display:none}.reason-row{grid-template-columns:30px minmax(90px,1fr) 70px;gap:7px}.reason-row .bars,.reason-row .reason{display:none}.baseline-row{grid-template-columns:90px minmax(50px,1fr) 58px}.baseline-row>em{display:none}}
</style>
