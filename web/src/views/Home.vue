<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { api, fmtPct, pctClass, type EntryAdviceResponse, type Position, type Recommendation, type RecommendationPerformance, type RecommendationShadowStats, type RecommendationStats } from '../api'

const router = useRouter()
const loading = ref(true)
const error = ref('')
const recommendations = ref<Recommendation[]>([])
const performance = ref<RecommendationPerformance[]>([])
const stats = ref<RecommendationStats | null>(null)
const shadowStats = ref<RecommendationShadowStats[]>([])
const entryAdvice = ref<EntryAdviceResponse | null>(null)
const positions = ref<Position[]>([])
const strategyNames: Record<string, string> = { ai: 'AI 推荐', trend: '趋势基线', low_risk: '低风险基线' }

function signed(value: number | null | undefined, digits = 2) {
  if (value == null) return '—'
  return `${value >= 0 ? '+' : ''}${value.toFixed(digits)}%`
}

const profitFactor = computed(() => {
  const current = stats.value
  if (!current || current.avg_win_pct == null || current.avg_loss_pct == null || current.avg_loss_pct === 0) return null
  return current.avg_win_pct / -current.avg_loss_pct
})
const latestEntry = computed(() => entryAdvice.value?.items.find(item => item.action === 'entry') || null)
const latestExit = computed(() => entryAdvice.value?.items.find(item => item.action === 'exit') || null)
const activePositionCount = computed(() => positions.value.filter(item => item.status === 'pending_entry' || item.status === 'holding').length)
const dailyPick = computed(() => entryAdvice.value?.items.find(item => item.source === 'daily_pick') || null)

const equityChart = computed(() => {
  const rows = [...performance.value].reverse().filter(row => row.sum_change_pct != null)
  let cumulative = 0
  const values = rows.map(row => ({ date: row.date, value: cumulative += row.sum_change_pct || 0, finished: row.finished }))
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
const shadowMax = computed(() => Math.max(1, ...shadowStats.value.map(item => Math.abs(item.avg_change_pct || 0))))

async function load() {
  loading.value = true; error.value = ''
  try {
    const [reco, perf, overall, shadow, entry, lifecycle] = await Promise.all([api.recommendations(), api.recommendationPerformance(30), api.recommendationStats(60), api.recommendationShadowStats(60), api.entryAdvice(), api.positions(40)])
    recommendations.value = reco; performance.value = perf; stats.value = overall; shadowStats.value = shadow; entryAdvice.value = entry; positions.value = lifecycle.items
  } catch (e: any) { error.value = e?.message || '首页统计加载失败' } finally { loading.value = false }
}
function openStock(symbol: string) { router.push(`/stock/${symbol}`) }
onMounted(load)
</script>

<template>
  <main class="overview">
    <header class="overview-header"><div><strong>趋势交易总览</strong><small>推荐依据、盈利质量与当前建仓信号</small></div><button type="button" :disabled="loading" title="刷新首页统计" @click="load">↻</button></header>
    <div v-if="loading" class="state">正在汇总推荐与盈利数据…</div>
    <div v-else-if="error" class="state error">{{ error }}</div>
    <template v-else>
      <section class="metric-band">
        <article class="metric primary"><small>个股胜率</small><b :class="(stats?.win_rate ?? 0) >= 50 ? 'up' : 'down'">{{ stats?.win_rate == null ? '—' : `${stats.win_rate.toFixed(1)}%` }}</b><span>{{ stats?.wins || 0 }} 胜 / {{ stats?.frozen_picks || 0 }} 只已退出</span></article>
        <article class="metric"><small>单日组合胜率</small><b :class="(stats?.day_win_rate ?? 0) >= 50 ? 'up' : 'down'">{{ stats?.day_win_rate == null ? '—' : `${stats.day_win_rate.toFixed(1)}%` }}</b><span>{{ stats?.day_wins || 0 }} 胜 / {{ stats?.day_frozen || 0 }} 日</span></article>
        <article class="metric"><small>平均收益</small><b :class="pctClass(stats?.avg_change_pct)">{{ signed(stats?.avg_change_pct) }}</b><span>中位数 {{ signed(stats?.median_pct) }}</span></article>
        <article class="metric"><small>累计收益点数</small><b :class="pctClass(stats?.sum_change_pct)">{{ signed(stats?.sum_change_pct) }}</b><span>追踪中 {{ stats?.tracking_picks || 0 }} 只</span></article>
        <article class="metric"><small>盈亏比</small><b>{{ profitFactor == null ? '—' : profitFactor.toFixed(2) }}</b><span>均盈 {{ signed(stats?.avg_win_pct) }} / 均亏 {{ signed(stats?.avg_loss_pct) }}</span></article>
        <article class="metric"><small>极值区间</small><b class="range"><i class="up">{{ signed(stats?.best_pct) }}</i><em>/</em><i class="down">{{ signed(stats?.worst_pct) }}</i></b><span>{{ stats?.best_name || '—' }} / {{ stats?.worst_name || '—' }}</span></article>
      </section>

      <section class="dashboard-grid">
        <article class="panel equity-panel">
          <header><div><b>累计组合收益曲线</b><small>近 30 个推荐日，各日 3 只收益点数逐日累计</small></div><strong :class="pctClass(equityChart.total)">{{ signed(equityChart.total) }}</strong></header>
          <div v-if="equityChart.values.length" class="chart-wrap"><svg :viewBox="`0 0 ${equityChart.width} ${equityChart.height}`" role="img" aria-label="累计组合收益曲线">
            <line :x1="equityChart.left" :x2="equityChart.width - equityChart.right" :y1="equityChart.zeroY" :y2="equityChart.zeroY" class="zero"/><polygon :points="equityChart.area" class="area"/><polyline :points="equityChart.points" class="line"/>
            <circle v-for="(point,index) in equityChart.values" :key="point.date" :cx="equityChart.x(index)" :cy="equityChart.y(point.value)" r="3" :class="['point',{tracking:!point.finished}]"><title>{{ point.date }}：累计 {{ signed(point.value) }}</title></circle>
            <text :x="equityChart.left-8" :y="equityChart.y(equityChart.max)+4" class="axis y">{{ equityChart.max.toFixed(1) }}</text><text :x="equityChart.left-8" :y="equityChart.y(equityChart.min)+4" class="axis y">{{ equityChart.min.toFixed(1) }}</text>
            <text v-for="label in equityChart.labels" :key="label.date" :x="equityChart.x(equityChart.values.indexOf(label))" :y="equityChart.height-8" class="axis x">{{ label.date.slice(5) }}</text>
          </svg></div><div v-else class="empty">暂无可绘制的收益数据</div>
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
            <span class="reason">{{ item.reason }}</span><span class="return" :class="pctClass(item.change_pct)">{{ fmtPct(item.change_pct) }}<small>{{ item.exited ? '已退出' : `追踪 ${item.tracked_days} 天` }}</small></span>
          </button></div><div v-else class="empty">暂无推荐数据</div>
        </article>

        <article class="panel baseline-panel">
          <header><div><b>AI vs 确定性基线</b><small>同候选池、同趋势退出统计口径</small></div></header>
          <div v-if="shadowStats.length" class="baseline-list"><div v-for="item in shadowStats" :key="item.strategy" class="baseline-row" :class="{ai:item.strategy==='ai'}"><span><b>{{ strategyNames[item.strategy] || item.strategy }}</b><small>{{ item.frozen_picks }} 只已退出</small></span><i><u :class="pctClass(item.avg_change_pct)" :style="{width:`${Math.abs(item.avg_change_pct||0)/shadowMax*100}%`}"></u></i><strong :class="pctClass(item.avg_change_pct)">{{ signed(item.avg_change_pct) }}</strong><em>{{ item.win_rate == null ? '—' : `${item.win_rate.toFixed(1)}% 胜率` }}</em></div></div><div v-else class="empty">暂无基线对照数据</div>
        </article>
      </section>
    </template>
  </main>
</template>

<style scoped>
.overview{min-width:0;min-height:0;overflow:auto;padding:14px;background:#0f1826;color:#e7ecf4}.overview-header{display:flex;align-items:center;justify-content:space-between;margin-bottom:12px}.overview-header>div{display:flex;align-items:baseline;gap:10px}.overview-header strong{font-size:17px}.overview-header small{color:#8895ab;font-size:11px}.overview-header>button{width:30px;height:30px;border:1px solid #3a496a;border-radius:0;background:#1c2a47;color:#c4cddc;cursor:pointer;font-size:18px}.state,.empty{display:grid;min-height:120px;place-items:center;color:#75839a;font-size:13px}.state.error{color:#ef7d84}.metric-band{display:grid;grid-template-columns:repeat(6,minmax(130px,1fr));gap:8px}.metric{display:flex;min-width:0;flex-direction:column;gap:5px;padding:11px 12px;border:1px solid #26324a;background:#131e33}.metric.primary{border-color:#476188;background:#1b2a46}.metric>small{color:#8895ab;font-size:10px}.metric>b{overflow:hidden;font-size:21px;font-variant-numeric:tabular-nums;text-overflow:ellipsis;white-space:nowrap}.metric>span{overflow:hidden;color:#8895ab;font-size:10px;text-overflow:ellipsis;white-space:nowrap}.range{display:flex;align-items:center;gap:5px;font-size:16px!important}.range i,.range em{font-style:normal}.range em{color:#5f6d85}.up{color:#ef6a72!important}.down{color:#55b996!important}.dim{color:#93a0b6!important}.dashboard-grid{display:grid;grid-template-columns:minmax(0,2fr) minmax(280px,1fr);gap:10px;margin-top:10px}.panel{min-width:0;border:1px solid #26324a;background:#131e33}.panel>header{display:flex;min-height:48px;align-items:center;justify-content:space-between;gap:12px;padding:9px 12px;border-bottom:1px solid #26324a}.panel>header>div{display:flex;min-width:0;flex-direction:column;gap:3px}.panel>header b{font-size:13px}.panel>header small{color:#8895ab;font-size:10px}.panel>header>strong{font-size:20px;font-variant-numeric:tabular-nums}.chart-wrap{height:224px;padding:3px 8px 0}.chart-wrap svg{display:block;width:100%;height:100%}.zero{stroke:#3a496a;stroke-width:1;stroke-dasharray:4 4}.area{fill:#39648a;opacity:.17}.line{fill:none;stroke:#67a9d8;stroke-width:2.2;vector-effect:non-scaling-stroke}.point{fill:#e9c16c;stroke:#131e33;stroke-width:1.5}.point.tracking{fill:#7b879a}.axis{fill:#71809a;font-size:10px}.axis.y{text-anchor:end}.axis.x{text-anchor:middle}.signal-panel>header i{padding:3px 7px;font-size:10px;font-style:normal}.paused{background:#3d3423;color:#e9c16c}.watching{background:#19352e;color:#55b996}.signal{display:flex;min-height:108px;flex-direction:column;justify-content:center;gap:6px;padding:13px}.signal.entry{border-left:3px solid #ef6a72;background:#251f2a}.signal.exit{border-left:3px solid #e9c16c;background:#2b2620}.signal.waiting{border-left:3px solid #55b996}.signal small{color:#8895ab;font-size:10px}.signal button{align-self:flex-start;padding:0;border:0;background:transparent;color:#ef8b91;cursor:pointer;font-size:19px;font-weight:700}.signal button em{margin-left:7px;color:#8895ab;font-size:11px;font-style:normal;font-weight:400}.signal>b{font-size:18px}.signal p,.daily-pick p{margin:0;color:#b4bece;font-size:11px;line-height:1.5}.daily-pick{display:grid;grid-template-columns:62px 1fr;gap:4px 8px;padding:10px 13px;border-top:1px solid #26324a;background:#182338}.daily-pick>span{grid-row:1/3;align-self:center;color:#e9c16c;font-size:10px}.daily-pick button{justify-self:start;padding:0;border:0;background:transparent;color:#e7ecf4;cursor:pointer;font-size:13px;font-weight:700}.daily-pick button small{margin-left:5px;color:#8895ab;font-size:10px}.daily-pick p{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.reason-panel{grid-column:1/-1}.reason-list{display:grid}.reason-row{display:grid;grid-template-columns:34px 150px minmax(220px,.8fr) minmax(220px,1.2fr) 85px;gap:12px;align-items:center;padding:11px 12px;border:0;border-bottom:1px solid #202c42;background:transparent;color:#e7ecf4;text-align:left;cursor:pointer}.reason-row:hover{background:#182338}.rank{display:grid;width:25px;height:25px;place-items:center;background:#2a3a5c;color:#e9c16c;font-weight:700}.stock b{display:block;font-size:13px}.stock small{display:block;margin-top:2px;color:#8895ab;font-size:10px}.bars{display:flex;flex-direction:column;gap:5px}.bars>i{display:grid;grid-template-columns:30px minmax(80px,1fr) 34px;gap:6px;align-items:center;font-style:normal}.bars em{color:#8895ab;font-size:9px;font-style:normal}.bars u{height:5px;overflow:hidden;background:#25334b;text-decoration:none}.bars u>b{display:block;height:100%;background:#df6a71}.bars u.safe>b{background:#55b996}.bars strong{color:#aeb8c9;font-size:10px;font-weight:400}.reason{color:#b8c1d0;font-size:11px;line-height:1.45}.return{text-align:right;font-size:14px;font-weight:700}.return small{display:block;margin-top:3px;color:#8895ab;font-size:9px;font-weight:400}.baseline-list{display:grid;gap:9px;padding:12px}.baseline-row{display:grid;grid-template-columns:105px minmax(80px,1fr) 66px 76px;gap:8px;align-items:center;padding:8px 9px;background:#182338}.baseline-row.ai{border-left:2px solid #e9c16c;background:#1c2a47}.baseline-row>span b{display:block;font-size:11px}.baseline-row>span small{display:block;margin-top:2px;color:#8895ab;font-size:9px}.baseline-row>i{height:7px;background:#25334b;font-style:normal}.baseline-row>i u{display:block;height:100%;background:#93a0b6;text-decoration:none}.baseline-row>i u.up{background:#ef6a72}.baseline-row>i u.down{background:#55b996}.baseline-row>strong{text-align:right;font-size:12px}.baseline-row>em{color:#8895ab;font-size:9px;font-style:normal;text-align:right}@media(max-width:1200px){.metric-band{grid-template-columns:repeat(3,minmax(130px,1fr))}.dashboard-grid{grid-template-columns:1fr}.reason-panel{grid-column:auto}.reason-row{grid-template-columns:34px 130px minmax(180px,1fr) 70px}.reason-row .reason{display:none}}@media(max-width:700px){.overview{padding:8px}.metric-band{grid-template-columns:repeat(2,minmax(0,1fr))}.overview-header small{display:none}.reason-row{grid-template-columns:30px minmax(90px,1fr) 70px;gap:7px}.reason-row .bars,.reason-row .reason{display:none}.baseline-row{grid-template-columns:90px minmax(50px,1fr) 58px}.baseline-row>em{display:none}}
</style>
