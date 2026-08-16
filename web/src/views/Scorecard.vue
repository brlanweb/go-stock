<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api, fmtPct, pctClass, type PositionReview, type RecommendationShadowStats, type StrategyParamsResponse, type StrategyScorecard } from '../api'

type Phase = StrategyScorecard['phase']

const loading = ref(true)
const error = ref('')
const phase = ref<Phase>('all')
const report = ref<StrategyScorecard | null>(null)
const params = ref<StrategyParamsResponse | null>(null)
const reviews = ref<PositionReview[]>([])
const shadows = ref<RecommendationShadowStats[]>([])

const phaseOptions: Array<{ value: Phase; label: string }> = [
  { value: 'all', label: '全部周期' },
  { value: 'up', label: '上升' },
  { value: 'range', label: '震荡' },
  { value: 'down', label: '下降' },
]
const stageRows = computed(() => {
  if (!report.value) return []
  return [
    { key: 'selection', name: '选股', weight: 30, data: report.value.stages.selection },
    { key: 'opportunity', name: '机会判断', weight: 20, data: report.value.stages.opportunity },
    { key: 'entry', name: '建仓', weight: 20, data: report.value.stages.entry },
    { key: 'exit', name: '离场', weight: 30, data: report.value.stages.exit },
  ]
})
const shadowRows = computed(() => shadows.value.filter(item => ['ai', 'mechanical_5d', 'stop_4', 'stop_8'].includes(item.strategy)))
const activeChanges = computed(() => params.value?.changes.filter(item => item.status === 'active') || [])
const latestChanges = computed(() => params.value?.changes.slice(0, 8) || [])

const equityChart = computed(() => {
  const values = report.value?.equity || []
  const width = 900, height = 210, left = 48, right = 18, top = 16, bottom = 28
  const x = (index: number) => left + (values.length <= 1 ? (width - left - right) / 2 : index / (values.length - 1) * (width - left - right))
  const emptyY = (_value: number) => top
  if (!values.length) return { values, width, height, left, right, min: 1, max: 1, line: '', area: '', zeroY: top, x, y: emptyY, labels: [] as typeof values }
  const equities = values.map(item => item.equity)
  const min = Math.min(...equities, 1), max = Math.max(...equities, 1)
  const span = Math.max(max - min, .01), plotH = height - top - bottom
  const y = (value: number) => top + (max - value) / span * plotH
  const line = values.map((item, index) => `${x(index)},${y(item.equity)}`).join(' ')
  const zeroY = y(1)
  const area = values.length ? `${left},${zeroY} ${line} ${x(values.length - 1)},${zeroY}` : ''
  const labels = values.filter((_, index) => index === 0 || index === values.length - 1 || index % Math.max(1, Math.ceil(values.length / 5)) === 0)
  return { values, width, height, left, right, min, max, line, area, zeroY, x, y, labels }
})

const stageLabels: Record<string, string> = { selection: '选股', opportunity: '机会', entry: '建仓', exit: '离场', market: '市场' }
const exitLabels: Record<string, string> = {
  ai: 'AI判断', stop_loss: '硬止损', trailing_stop: '移动止盈', take_profit: '目标止盈',
  time_stop: '时间止损', trend_break: '趋势破位', systemic: '系统性风险',
}
const shadowLabels: Record<string, string> = { ai: '当前策略', mechanical_5d: '机械持有5日', stop_4: '固定止损4%', stop_8: '固定止损8%' }
const paramLabels: Record<string, string> = {
  stop_loss_pct: '基础止损', stop_loss_atr_mult: 'ATR倍数', stop_loss_max_pct: '止损上限',
  trailing_arm_pct: '移动止盈激活', trailing_giveback_pct: '允许回撤', take_profit_pct: '目标止盈',
  time_stop_days: '时间止损天数', time_stop_min_pct: '时间止损收益线', max_hold_days: '最长持有天数',
}
const metricLabels: Record<string, string> = {
  avg_5d_net_pct: '机械5日均值', positive_rate: '正收益率', avg_excess_pct: '平均超额',
  entry_conversion_rate: '建仓转化率', wait_decisions: '等待决策', entered_mechanical_avg_pct: '建仓组机械收益',
  expired_mechanical_avg_pct: '放弃组机械收益', filter_advantage_pct: '过滤优势', entry_efficiency_pct: '日内建仓效率',
  avg_mae_pct: '平均MAE', avg_capture_rate_pct: '平均捕获率', avg_mfe_pct: '平均MFE', avg_post_exit_5d_pct: '离场后5日',
}
function isPctMetric(key: string) { return key.includes('pct') || key.includes('rate') }
function number(value: number | null | undefined, digits = 2) { return value == null ? '—' : value.toFixed(digits) }
function scoreClass(value: number) { return value >= 65 ? 'good' : value < 45 ? 'bad' : 'neutral' }
function statusLabel(status: string) { return ({ active: '观察中', kept: '已保留', reverted: '已回滚', rejected: '已拒绝' } as Record<string, string>)[status] || status }

async function load() {
  loading.value = true
  error.value = ''
  try {
    const [scorecard, paramData, reviewData, shadowData] = await Promise.all([
      api.strategyScorecard(60, phase.value), api.strategyParams(), api.positionReviews(30), api.recommendationShadowStats(60),
    ])
    report.value = scorecard
    params.value = paramData
    reviews.value = reviewData.items
    shadows.value = shadowData
  } catch (e: any) {
    error.value = e?.message || '考核数据加载失败'
  } finally {
    loading.value = false
  }
}
function setPhase(value: Phase) { if (phase.value !== value) { phase.value = value; load() } }

onMounted(load)
</script>

<template>
  <main class="scorecard-workspace">
    <header class="scorecard-header">
      <div><strong>策略考核</strong><small>确定性事实 · 60 个推荐日滚动窗口 · 风险调整评分</small></div>
      <div class="header-actions">
        <div class="phase-switch" role="group" aria-label="市场周期">
          <button v-for="option in phaseOptions" :key="option.value" type="button" :class="{ active: phase === option.value }" @click="setPhase(option.value)">{{ option.label }}</button>
        </div>
        <button class="refresh" type="button" :disabled="loading" title="刷新考核数据" aria-label="刷新考核数据" @click="load">↻</button>
      </div>
    </header>

    <div v-if="loading" class="state">正在计算风险调整指标与逐笔归因…</div>
    <div v-else-if="error" class="state error">{{ error }}</div>
    <template v-else-if="report">
      <section class="score-band">
        <div class="overall-score" :class="scoreClass(report.overall.score)">
          <span>综合考核</span><b>{{ number(report.overall.score, 1) }}</b><small>置信度 {{ number(report.overall.confidence, 0) }}% · {{ report.overall.samples }} 笔结算</small>
        </div>
        <div class="headline-metrics">
          <div><span>复合结算收益</span><b :class="pctClass(report.overall.total_return_pct)">{{ fmtPct(report.overall.total_return_pct) }}</b></div>
          <div><span>最大回撤</span><b class="risk">{{ report.overall.max_drawdown_pct == null ? '—' : `-${number(report.overall.max_drawdown_pct)}%` }}</b></div>
          <div><span>交易 Sharpe</span><b>{{ number(report.overall.trade_sharpe) }}</b></div>
          <div><span>盈亏因子</span><b>{{ number(report.overall.profit_factor) }}</b></div>
          <div><span>实际 vs 机械</span><b :class="pctClass(report.overall.actual_vs_mechanical_pct)">{{ fmtPct(report.overall.actual_vs_mechanical_pct) }}</b></div>
          <div><span>异常排除</span><b>{{ report.data_quality.excluded_samples }}</b></div>
        </div>
      </section>

      <section class="stage-band" aria-label="四环节评分">
        <article v-for="stage in stageRows" :key="stage.key" class="stage-item">
          <header><div><span>{{ stage.name }}</span><small>权重 {{ stage.weight }}%</small></div><b :class="scoreClass(stage.data.score)">{{ number(stage.data.score, 1) }}</b></header>
          <div class="score-track"><i :style="{ width: `${stage.data.score}%` }"></i></div>
          <p>{{ stage.data.summary }}</p>
          <div class="stage-metrics"><span v-for="(value,key) in stage.data.metrics" :key="key"><small>{{ metricLabels[key] || key }}</small><b>{{ isPctMetric(key) ? `${number(value)}%` : number(value) }}</b></span></div>
          <footer>{{ stage.data.samples }} 样本 · 置信度 {{ number(stage.data.confidence, 0) }}%</footer>
        </article>
      </section>

      <section class="main-grid">
        <article class="panel equity-panel">
          <header><div><b>结算净值与回撤</b><small>同日交易先等权，再按退出日复合</small></div><span>起点 1.000</span></header>
          <div v-if="equityChart.values.length" class="chart-wrap">
            <svg :viewBox="`0 0 ${equityChart.width} ${equityChart.height}`" role="img" aria-label="结算净值曲线">
              <line :x1="equityChart.left" :x2="equityChart.width-equityChart.right" :y1="equityChart.zeroY" :y2="equityChart.zeroY" class="zero"/>
              <polygon :points="equityChart.area" class="area"/><polyline :points="equityChart.line" class="line"/>
              <circle v-for="(point,index) in equityChart.values" :key="point.date" :cx="equityChart.x!(index)" :cy="equityChart.y!(point.equity)" r="3"><title>{{ point.date }} 净值 {{ point.equity.toFixed(4) }} · 回撤 {{ point.drawdown_pct.toFixed(2) }}%</title></circle>
              <text v-for="label in equityChart.labels" :key="label.date" :x="equityChart.x!(equityChart.values.indexOf(label))" :y="equityChart.height-8">{{ label.date.slice(5) }}</text>
            </svg>
          </div>
          <div v-else class="empty">当前周期暂无已结算交易</div>
          <footer>{{ report.methodology.equity_curve }}</footer>
        </article>

        <article class="panel shadow-panel">
          <header><div><b>执行参数影子组</b><small>同一批 AI 选股，比较固定执行纪律</small></div></header>
          <div class="shadow-table">
            <div class="table-head"><span>策略</span><span>样本</span><span>胜率</span><span>平均</span></div>
            <div v-for="item in shadowRows" :key="item.strategy"><b>{{ shadowLabels[item.strategy] || item.strategy }}</b><span>{{ item.frozen_picks }}</span><span>{{ item.win_rate == null ? '—' : `${number(item.win_rate, 1)}%` }}</span><strong :class="pctClass(item.avg_change_pct)">{{ fmtPct(item.avg_change_pct) }}</strong></div>
          </div>
          <footer>机械组固定持有 5 日；止损扫描组严格执行 T+1，并统一扣除交易成本。</footer>
        </article>

        <article class="panel params-panel">
          <header><div><b>动态风控参数</b><small>边界、步长、冻结与回滚由确定性代码执行</small></div><i v-if="activeChanges.length">{{ activeChanges.length }} 项观察中</i></header>
          <div class="param-grid">
            <div v-for="item in params?.params" :key="item.key"><span>{{ paramLabels[item.key] || item.key }}</span><b>{{ number(item.value, item.step < 1 ? 1 : 0) }}</b><small>{{ item.min }}–{{ item.max }} · 步长 {{ item.step }}<em v-if="item.frozen_until">冻结至 {{ item.frozen_until }}</em></small></div>
          </div>
          <footer>提案至少 {{ params?.min_samples }} 个历史结算样本；冻结 {{ params?.freeze_days }} 天后且新增满 {{ params?.evaluation_min_samples }} 笔才验收；总分下降超过 {{ params?.rollback_drop_score }} 分自动回滚。</footer>
        </article>
      </section>

      <section class="lower-grid">
        <article class="panel review-panel">
          <header><div><b>逐笔离场复盘</b><small>MFE / MAE / 捕获率与离场后 5 日验证</small></div><span>{{ reviews.length }} 笔</span></header>
          <div v-if="reviews.length" class="review-table">
            <div class="table-head"><span>标的 / 退出</span><span>归因</span><span>净收益</span><span>MFE / MAE</span><span>捕获率</span><span>后5日</span><span>结论</span></div>
            <div v-for="item in reviews" :key="item.id" class="review-row">
              <span class="stock"><b>{{ item.name || item.symbol }}</b><small>{{ item.code }} · {{ item.review_date }} · {{ exitLabels[item.exit_kind] || item.exit_kind }}</small></span>
              <span><i class="blame">{{ stageLabels[item.blame_stage] || item.blame_stage }}</i></span>
              <strong :class="pctClass(item.net_change_pct)">{{ fmtPct(item.net_change_pct) }}</strong>
              <span>{{ fmtPct(item.mfe_pct) }} / {{ fmtPct(item.mae_pct) }}</span>
              <span>{{ item.capture_rate_pct == null ? '—' : `${number(item.capture_rate_pct, 1)}%` }}</span>
              <span :class="pctClass(item.post_exit_5d_pct)">{{ fmtPct(item.post_exit_5d_pct) }}</span>
              <span class="reason">{{ item.reason }}</span>
            </div>
          </div>
          <div v-else class="empty">暂无已退出交易复盘</div>
        </article>

        <article class="panel audit-panel">
          <header><div><b>参数变更审计</b><small>所有提案、应用、验收和回滚留痕</small></div></header>
          <div v-if="latestChanges.length" class="audit-list">
            <div v-for="item in latestChanges" :key="item.id"><span class="status-tag" :class="item.status">{{ statusLabel(item.status) }}</span><p><b>{{ paramLabels[item.param_key] || item.param_key }}</b><span>{{ item.previous }} → {{ item.applied }}</span><small>{{ item.rationale }}</small></p><time>{{ item.effective_date }}<em v-if="item.status === 'active'">最早 {{ item.evaluate_after }} 验收</em></time></div>
          </div>
          <div v-else class="empty">尚无参数调整记录</div>
        </article>
      </section>

      <aside class="method-note"><b>口径说明</b><span>{{ report.methodology.mechanical_baseline }}</span><span>{{ report.methodology.risk_note }}</span></aside>
    </template>
  </main>
</template>

<style scoped>
.scorecard-workspace{min-height:100%;overflow:auto;background:#141d2d;color:#e7ebf2}.scorecard-header{position:sticky;z-index:10;top:0;display:flex;min-height:52px;align-items:center;justify-content:space-between;gap:18px;padding:7px 16px;border-bottom:1px solid #344057;background:#182234}.scorecard-header>div:first-child{display:flex;flex-direction:column}.scorecard-header strong{font-size:15px}.scorecard-header small{margin-top:2px;color:#8794a8;font-size:11px}.header-actions,.phase-switch{display:flex;align-items:center}.phase-switch{height:30px;border:1px solid #3c4960}.phase-switch button{height:28px;padding:0 12px;border:0;border-right:1px solid #3c4960;border-radius:0;background:#1b273a;color:#9ba7b8;cursor:pointer;font-size:12px}.phase-switch button:last-child{border-right:0}.phase-switch button.active{background:#d6a83b;color:#171d27;font-weight:700}.refresh{width:30px;height:30px;margin-left:8px;border:1px solid #3c4960;border-radius:2px;background:#202c40;color:#dbe1ea;cursor:pointer;font-size:18px}.refresh:disabled{opacity:.5}.state{display:grid;min-height:60vh;place-items:center;color:#9ba7b8}.state.error{color:#ff9ca5}.score-band{display:grid;grid-template-columns:210px minmax(0,1fr);border-bottom:1px solid #303b50}.overall-score{display:grid;grid-template-columns:1fr auto;align-items:end;padding:17px 20px;border-right:1px solid #303b50}.overall-score span{color:#aab4c3;font-size:12px}.overall-score b{grid-row:1/3;grid-column:2;font-size:42px;line-height:1}.overall-score small{color:#7f8ca1;font-size:10px}.overall-score.good b,.stage-item b.good{color:#e95b65}.overall-score.bad b,.stage-item b.bad{color:#22aa7b}.overall-score.neutral b,.stage-item b.neutral{color:#e7bd53}.headline-metrics{display:grid;grid-template-columns:repeat(6,minmax(100px,1fr))}.headline-metrics div{display:flex;min-width:0;flex-direction:column;justify-content:center;padding:12px 14px;border-right:1px solid #303b50}.headline-metrics span{overflow:hidden;color:#8996a9;font-size:11px;text-overflow:ellipsis;white-space:nowrap}.headline-metrics b{margin-top:6px;font-size:19px}.headline-metrics .risk{color:#f19a55}.up{color:#ed6570!important}.down{color:#26b17f!important}.stage-band{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));border-bottom:1px solid #303b50}.stage-item{min-width:0;padding:14px 16px;border-right:1px solid #303b50}.stage-item:last-child{border-right:0}.stage-item header{display:flex;align-items:center;justify-content:space-between}.stage-item header div{display:flex;align-items:baseline;gap:8px}.stage-item header span{font-weight:700}.stage-item header small,.stage-item footer{color:#7f8ca1;font-size:10px}.stage-item header>b{font-size:24px}.score-track{height:4px;margin:8px 0 10px;background:#2d394d}.score-track i{display:block;height:100%;background:#d7aa42}.stage-item p{min-height:28px;margin:0;color:#9aa6b8;font-size:11px;line-height:1.4}.stage-metrics{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:6px;margin-top:10px}.stage-metrics span{display:flex;min-width:0;justify-content:space-between;gap:5px;padding:5px 6px;background:#1b2638}.stage-metrics small{overflow:hidden;color:#79879b;font-size:9px;text-overflow:ellipsis;white-space:nowrap}.stage-metrics b{color:#dce2eb;font-size:10px}.stage-item footer{margin-top:9px}.main-grid{display:grid;grid-template-columns:minmax(0,1.55fr) minmax(260px,.75fr);border-bottom:1px solid #303b50}.panel{min-width:0;border-right:1px solid #303b50;border-bottom:1px solid #303b50;background:#172132}.panel>header{display:flex;min-height:42px;align-items:center;justify-content:space-between;padding:8px 13px;border-bottom:1px solid #303b50}.panel>header div{display:flex;min-width:0;flex-direction:column}.panel>header b{font-size:12px}.panel>header small{margin-top:2px;color:#7f8ca1;font-size:10px}.panel>header>span,.panel>header>i{color:#d8b451;font-size:10px;font-style:normal}.panel>footer{padding:8px 13px;border-top:1px solid #2d384b;color:#77859a;font-size:10px;line-height:1.45}.equity-panel{grid-row:span 2}.chart-wrap{padding:10px 13px 3px}.chart-wrap svg{display:block;width:100%;height:210px;overflow:visible}.chart-wrap .zero{stroke:#596477;stroke-dasharray:4 4}.chart-wrap .area{fill:rgba(211,169,67,.1)}.chart-wrap .line{fill:none;stroke:#d8b451;stroke-width:2}.chart-wrap circle{fill:#e5c66f;stroke:#172132;stroke-width:2}.chart-wrap text{fill:#748196;font-size:10px;text-anchor:middle}.shadow-table>div,.review-table>div{display:grid;align-items:center}.shadow-table>div{grid-template-columns:1.5fr repeat(3,1fr);min-height:36px;padding:0 12px;border-bottom:1px solid #283447}.shadow-table span,.shadow-table strong{font-size:11px;text-align:right}.shadow-table b{font-size:11px}.table-head{min-height:28px!important;background:#1b2739;color:#77859a}.param-grid{display:grid;grid-template-columns:repeat(3,minmax(0,1fr))}.param-grid>div{display:flex;min-width:0;flex-direction:column;padding:9px 10px;border-right:1px solid #293548;border-bottom:1px solid #293548}.param-grid span{overflow:hidden;color:#8d9aad;font-size:10px;text-overflow:ellipsis;white-space:nowrap}.param-grid b{margin:3px 0;font-size:16px}.param-grid small{color:#66758b;font-size:9px}.param-grid em{display:block;margin-top:3px;color:#d5ad4d;font-style:normal}.lower-grid{display:grid;grid-template-columns:minmax(0,1.65fr) minmax(300px,.65fr)}.review-table{overflow:auto}.review-table>div{grid-template-columns:1.35fr .45fr .55fr .75fr .55fr .55fr 1.7fr;min-width:900px}.review-row{min-height:48px;padding:5px 11px;border-bottom:1px solid #283447}.review-row>span,.review-row>strong{font-size:10px}.stock{display:flex;min-width:0;flex-direction:column}.stock small{margin-top:2px;color:#77859a}.blame,.status-tag{display:inline-block;padding:2px 5px;border:1px solid #455168;color:#c7d0dd;font-size:9px;font-style:normal}.reason{overflow:hidden;color:#9aa6b7;text-overflow:ellipsis;white-space:nowrap}.audit-list>div{display:grid;grid-template-columns:55px minmax(0,1fr) auto;gap:8px;align-items:start;padding:9px 11px;border-bottom:1px solid #293548}.status-tag.active{border-color:#bd9438;color:#e1bd64}.status-tag.reverted{border-color:#298966;color:#54c497}.audit-list p{display:flex;min-width:0;flex-direction:column;margin:0}.audit-list p b{font-size:10px}.audit-list p span{margin:3px 0;color:#d8b451;font-size:11px}.audit-list p small{overflow:hidden;color:#7f8ca1;font-size:9px;text-overflow:ellipsis;white-space:nowrap}.audit-list time{display:flex;flex-direction:column;color:#77859a;font-size:9px;text-align:right}.audit-list time em{margin-top:3px;color:#bb984a;font-style:normal}.empty{display:grid;min-height:150px;place-items:center;color:#748196;font-size:11px}.method-note{display:flex;gap:18px;padding:10px 15px;background:#121a28;color:#728096;font-size:10px}.method-note b{flex:0 0 auto;color:#b7c0cd}.method-note span{line-height:1.5}
@media(max-width:1100px){.headline-metrics{grid-template-columns:repeat(3,1fr)}.stage-band{grid-template-columns:repeat(2,1fr)}.main-grid,.lower-grid{grid-template-columns:1fr}.equity-panel{grid-row:auto}.panel{border-right:0}}
@media(max-width:700px){.scorecard-header{position:static;align-items:flex-start;flex-direction:column}.header-actions{width:100%}.phase-switch{flex:1}.phase-switch button{flex:1;padding:0 5px}.score-band{grid-template-columns:1fr}.overall-score{border-right:0;border-bottom:1px solid #303b50}.headline-metrics{grid-template-columns:repeat(2,1fr)}.stage-band{grid-template-columns:1fr}.stage-item{border-right:0}.param-grid{grid-template-columns:repeat(2,1fr)}.method-note{flex-direction:column;gap:5px}}
</style>
