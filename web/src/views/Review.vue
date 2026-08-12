<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { api, fmtBig, fmtPct, pctClass, type DailyReviewReport, type DailyReviewRunSummary } from '../api'
import MarketSidebar from '../components/MarketSidebar.vue'

const router = useRouter()
const history = ref<DailyReviewRunSummary[]>([])
const activeID = ref<number | undefined>()
const report = ref<DailyReviewReport>({ available: false })
const loading = ref(false)
const enabled = ref(true)
const running = ref(false)
const message = ref('')
let pollTimer: number | undefined

const phaseMeta = computed(() => {
  const phase = report.value.market_phase
  if (phase === 'up') return { label: '上升', cls: 'up' }
  if (phase === 'down') return { label: '下跌', cls: 'down' }
  return { label: '震荡', cls: 'range' }
})

// 操作姿态：本地按近 20 日等权大盘历史数据确定性推演（落袋/扛单/扫货），不经 AI。
const stanceMeta = computed(() => {
  const stance = report.value.facts?.market_stance?.stance
  if (stance === 'take_profit') return { label: '落袋', cls: 'take-profit', hint: '锁定利润 · 不追高' }
  if (stance === 'accumulate') return { label: '扫货', cls: 'accumulate', hint: '回调企稳 · 分批吸纳' }
  return { label: '扛单', cls: 'hold', hint: '持仓等待 · 不加不减' }
})

const sectorFacts = computed(() => {
  const all = [...(report.value.facts?.strong_sectors || []), ...(report.value.facts?.weak_sectors || [])]
  return new Map(all.map(item => [item.sector_code, item]))
})

const recommendationKey = (date: string, symbol: string) => `${date}:${symbol}`

const recommendationFacts = computed(() =>
  new Map((report.value.facts?.latest_recommendations || []).map(item => [recommendationKey(item.date, item.symbol), item]))
)

const hotspotFacts = computed(() =>
  new Map((report.value.facts?.hotspot_checks || []).map(item => [item.sector_code, item]))
)

function verdictLabel(verdict: string) {
  return verdict === 'hit' ? '有效' : verdict === 'miss' ? '失效' : '观察中'
}

function hotspotVerdictLabel(verdict: string) {
  return verdict === 'hit' ? '兑现' : verdict === 'miss' ? '未兑现' : '部分兑现'
}

function directiveVerdictLabel(verdict: string) {
  return verdict === 'effective' ? '有效' : verdict === 'ineffective' ? '无效' : '待确认'
}

function modeLabel(mode?: string) {
  return mode === 'aggressive' ? '进攻' : mode === 'defensive' ? '防御' : '均衡'
}

async function loadReport(id?: number) {
  loading.value = true
  message.value = ''
  try {
    report.value = await api.dailyReview(id)
    activeID.value = id ?? history.value[0]?.id
  } catch (e: any) {
    message.value = e?.message || '复盘加载失败'
  } finally {
    loading.value = false
  }
}

async function refreshHistory(loadLatest = true) {
  try {
    history.value = await api.dailyReviewHistory(60)
    if (loadLatest) await loadReport(history.value[0]?.id)
  } catch (e: any) {
    message.value = e?.message || '历史记录加载失败'
  }
}

async function pollStatus() {
  try {
    const status = await api.dailyReviewStatus()
    running.value = status.running
    if (!status.running) {
      window.clearInterval(pollTimer)
      pollTimer = undefined
      await refreshHistory(true)
      message.value = status.last_error ? `复盘失败：${status.last_error}` : '复盘已完成'
    }
  } catch { /* 下一轮继续 */ }
}

async function runReview() {
  running.value = true
  message.value = '正在生成复盘…'
  try {
    await api.runDailyReview()
    window.clearInterval(pollTimer)
    pollTimer = window.setInterval(pollStatus, 3000)
  } catch (e: any) {
    running.value = false
    message.value = e?.message || '启动复盘失败'
  }
}

onMounted(async () => {
  await refreshHistory(true)
  const status = await api.dailyReviewStatus().catch(() => ({ enabled: false, running: false, last_error: '' }))
  enabled.value = status.enabled
  running.value = status.running
  if (status.last_error) message.value = `上次复盘失败：${status.last_error}`
  if (status.running) pollTimer = window.setInterval(pollStatus, 3000)
})

onUnmounted(() => window.clearInterval(pollTimer))
</script>

<template>
  <div class="review-shell">
    <MarketSidebar :controls="false" />
    <main class="review-content">
      <header class="review-header">
        <div class="title-block">
          <strong>每日复盘</strong>
          <small v-if="report.review_date">{{ report.review_date }} · {{ report.model }}</small>
        </div>
        <div class="tools">
          <select v-if="history.length" v-model.number="activeID" title="历史复盘" @change="loadReport(activeID)">
            <option v-for="item in history" :key="item.id" :value="item.id">{{ item.review_date }} · {{ item.market_phase }}</option>
          </select>
          <button :disabled="running || !enabled" :title="enabled ? '基于最近收盘数据生成复盘' : '请先配置 AI 模型'" @click="runReview">{{ running ? '复盘中…' : '立即复盘' }}</button>
        </div>
      </header>
      <p v-if="message" class="message">{{ message }}</p>

      <div v-if="loading" class="empty">加载中…</div>
      <div v-else-if="report.available === false || !report.review_date" class="empty">暂无复盘记录</div>
      <template v-else>
        <section class="market-band" :class="{ 'with-stance': !!report.facts?.market_stance }">
          <div class="phase" :class="phaseMeta.cls">
            <small>市场阶段</small>
            <b>{{ phaseMeta.label }}</b>
            <span>置信度 {{ report.confidence?.toFixed(0) }}%</span>
          </div>
          <div v-if="report.facts?.market_stance" class="stance" :class="stanceMeta.cls">
            <small>操作姿态 · 近{{ report.facts.market_stance.lookback_days }}日等权推演</small>
            <b>{{ stanceMeta.label }}</b>
            <span class="stance-hint">{{ stanceMeta.hint }}</span>
            <div class="stance-metrics">
              <span>5日动量 <b :class="pctClass(report.facts.market_stance.momentum_5d_pct)">{{ fmtPct(report.facts.market_stance.momentum_5d_pct) }}</b></span>
              <span>距高点 <b>-{{ report.facts.market_stance.drawdown_pct.toFixed(1) }}%</b></span>
              <span>距低点 <b>+{{ report.facts.market_stance.rebound_pct.toFixed(1) }}%</b></span>
              <span>上涨占比 <b>{{ (report.facts.market_stance.up_ratio_today * 100).toFixed(0) }}%</b></span>
            </div>
            <p class="stance-reason">{{ report.facts.market_stance.reason }}</p>
          </div>
          <div class="market-conclusion">
            <strong>{{ report.market_summary }}</strong>
            <p>{{ report.index_review }}</p>
            <p>{{ report.breadth_review }}</p>
          </div>
        </section>

        <section class="facts-band">
          <div class="section-head"><strong>指数收盘</strong><small>本地 16:00 收盘快照</small></div>
          <div class="index-grid">
            <div v-for="item in report.facts?.indices" :key="item.symbol" class="index-cell">
              <span>{{ item.name }}</span>
              <b>{{ item.price.toFixed(2) }}</b>
              <em :class="pctClass(item.change_pct)">{{ fmtPct(item.change_pct) }}</em>
            </div>
          </div>
          <div v-if="report.facts?.breadth" class="breadth-row">
            <div><small>上涨 / 下跌</small><b>{{ report.facts.breadth.up_count }} / {{ report.facts.breadth.down_count }}</b></div>
            <div><small>上涨占比</small><b>{{ (report.facts.breadth.up_ratio * 100).toFixed(1) }}%</b></div>
            <div><small>平均涨跌</small><b :class="pctClass(report.facts.breadth.avg_change_pct)">{{ fmtPct(report.facts.breadth.avg_change_pct) }}</b></div>
            <div><small>涨停 / 跌停</small><b>{{ report.facts.breadth.limit_up_count }} / {{ report.facts.breadth.limit_down_count }}</b></div>
            <div><small>成交额</small><b>{{ fmtBig(report.facts.breadth.total_amount) }}</b></div>
          </div>
        </section>

        <section class="panel sectors-panel">
          <div class="section-head"><strong>板块复盘</strong><small>{{ report.sector_assessments?.length || 0 }} 个强弱样本</small></div>
          <div class="sector-table">
            <div class="sector-row head"><span>板块</span><span>判断</span><span>当日</span><span>5日</span><span>上涨占比</span><span>量能</span><span>后续观察</span><span>风险</span></div>
            <div v-for="item in report.sector_assessments" :key="item.sector_code" class="sector-row">
              <span><b>{{ item.sector_name }}</b><small>{{ item.sector_code }}</small></span>
              <span class="strength" :class="item.strength">{{ item.strength === 'strong' ? '强' : item.strength === 'weak' ? '弱' : '中性' }}</span>
              <span :class="pctClass(sectorFacts.get(item.sector_code)?.avg_change)">{{ fmtPct(sectorFacts.get(item.sector_code)?.avg_change) }}</span>
              <span :class="pctClass(sectorFacts.get(item.sector_code)?.avg_change_5d)">{{ fmtPct(sectorFacts.get(item.sector_code)?.avg_change_5d) }}</span>
              <span>{{ ((sectorFacts.get(item.sector_code)?.up_ratio || 0) * 100).toFixed(0) }}%</span>
              <span>{{ sectorFacts.get(item.sector_code)?.amount_ratio.toFixed(2) }}x</span>
              <span>{{ item.outlook }}</span>
              <span>{{ item.risk }}</span>
            </div>
          </div>
        </section>

        <section v-if="report.hotspot_reviews?.length" class="panel hotspot-panel">
          <div class="section-head"><strong>盘前热点回验</strong><small>预测概念与当日板块表现逐项对齐</small></div>
          <div class="check-grid">
            <article v-for="item in report.hotspot_reviews" :key="item.sector_code" class="check-item">
              <div class="check-title">
                <span><b>{{ hotspotFacts.get(item.sector_code)?.sector_name }}</b><small>{{ item.sector_code }} · 置信度 {{ hotspotFacts.get(item.sector_code)?.confidence.toFixed(0) }}%</small></span>
                <em :class="item.verdict">{{ hotspotVerdictLabel(item.verdict) }}</em>
              </div>
              <div class="check-numbers">
                <span>当日 <b :class="pctClass(hotspotFacts.get(item.sector_code)?.avg_change)">{{ fmtPct(hotspotFacts.get(item.sector_code)?.avg_change) }}</b></span>
                <span>上涨占比 <b>{{ ((hotspotFacts.get(item.sector_code)?.up_ratio || 0) * 100).toFixed(0) }}%</b></span>
                <span>量能 <b>{{ hotspotFacts.get(item.sector_code)?.amount_ratio.toFixed(2) }}x</b></span>
              </div>
              <p>{{ item.assessment }}</p>
            </article>
          </div>
        </section>

        <section class="panel picks-panel">
          <div class="section-head"><strong>AI 趋势推荐深度复盘</strong><small>最近 5 个推荐日 · 最多 15 条</small></div>
          <div class="pick-grid">
            <button v-for="item in report.recommendation_reviews" :key="recommendationKey(item.recommendation_date, item.symbol)" class="pick-item" @click="router.push(`/stock/${item.symbol}`)">
              <div class="pick-title"><span><b>{{ item.name }}</b><small>{{ item.recommendation_date }} · {{ item.symbol }}</small></span><em :class="item.verdict">{{ verdictLabel(item.verdict) }}</em></div>
              <div class="pick-numbers">
                <span>当日 <b :class="pctClass(recommendationFacts.get(recommendationKey(item.recommendation_date, item.symbol))?.day_change_pct)">{{ fmtPct(recommendationFacts.get(recommendationKey(item.recommendation_date, item.symbol))?.day_change_pct) }}</b></span>
                <span>窗口 <b :class="pctClass(recommendationFacts.get(recommendationKey(item.recommendation_date, item.symbol))?.change_pct)">{{ fmtPct(recommendationFacts.get(recommendationKey(item.recommendation_date, item.symbol))?.change_pct) }}</b></span>
                <span>基准 <b :class="pctClass(recommendationFacts.get(recommendationKey(item.recommendation_date, item.symbol))?.benchmark_change_pct)">{{ fmtPct(recommendationFacts.get(recommendationKey(item.recommendation_date, item.symbol))?.benchmark_change_pct) }}</b></span>
                <span>超额 <b :class="pctClass(recommendationFacts.get(recommendationKey(item.recommendation_date, item.symbol))?.excess_change_pct)">{{ fmtPct(recommendationFacts.get(recommendationKey(item.recommendation_date, item.symbol))?.excess_change_pct) }}</b></span>
                <span>状态 <b>{{ recommendationFacts.get(recommendationKey(item.recommendation_date, item.symbol))?.frozen ? '已冻结' : '追踪中' }}</b></span>
              </div>
              <p>{{ item.performance }}</p>
              <p><strong>归因</strong>{{ item.attribution }}</p>
              <p><strong>后续</strong>{{ item.next_action }}</p>
            </button>
          </div>
        </section>

        <section v-if="report.previous_directive_reviews?.length" class="panel previous-panel">
          <div class="section-head"><strong>上次优化指令回验</strong><small>{{ report.facts?.previous_review.review_date }} · {{ report.facts?.previous_review.market_phase }}</small></div>
          <div class="directive-review-list">
            <div v-for="item in report.previous_directive_reviews" :key="item.action" class="directive-review-item">
              <em :class="item.verdict">{{ directiveVerdictLabel(item.verdict) }}</em>
              <p><b>{{ item.action }}</b><span>{{ item.comment }}</span></p>
            </div>
          </div>
        </section>

        <div class="decision-grid">
          <section class="panel lessons">
            <div class="section-head"><strong>有效与失效</strong></div>
            <div class="lesson-cols">
              <div><small>有效信号</small><p v-for="text in report.what_worked" :key="text">{{ text }}</p></div>
              <div><small>失效信号</small><p v-for="text in report.what_failed" :key="text">{{ text }}</p></div>
            </div>
          </section>
          <section class="panel directives">
            <div class="section-head"><strong>次日推荐优化指令</strong><small>自动注入 08:10 推荐</small></div>
            <div v-for="(item, index) in report.directives" :key="item.action" class="directive-item">
              <i>{{ index + 1 }}</i><p><b>{{ item.action }}</b><span>{{ item.rationale }}</span></p>
            </div>
          </section>
          <section v-if="report.risk_controls" class="panel risk-panel">
            <div class="section-head"><strong>风险控制</strong><small>{{ modeLabel(report.risk_controls.position_mode) }}模式</small></div>
            <div class="risk-numbers">
              <span><small>总仓上限</small><b>{{ report.risk_controls.max_position_pct.toFixed(0) }}%</b></span>
              <span><small>单票上限</small><b>{{ report.risk_controls.max_single_stock_pct.toFixed(0) }}%</b></span>
              <span><small>止损参考</small><b>{{ report.risk_controls.stop_loss_pct.toFixed(1) }}%</b></span>
            </div>
            <p v-for="text in report.risk_controls.avoid_conditions" :key="text">{{ text }}</p>
          </section>
        </div>
      </template>
    </main>
  </div>
</template>

<style scoped>
.review-shell { display:grid; grid-template-columns:212px minmax(0,1fr); width:100vw; height:100vh; overflow:hidden; background:#0f1826; color:#e7ecf4; }
.review-content { min-width:0; overflow-y:auto; padding:0 16px 24px; }
.review-header { position:sticky; z-index:4; top:0; display:flex; align-items:center; justify-content:space-between; min-height:56px; border-bottom:1px solid #26324a; background:#0f1826; }
.title-block { display:flex; align-items:baseline; gap:10px; }.title-block strong { font-size:17px; }.title-block small,.section-head small { color:#7f8ca2; font-size:11px; }
.tools { display:flex; gap:8px; }.tools select,.tools button { height:31px; border:1px solid #3a496a; border-radius:0; background:#182338; color:#dce3ed; font-size:12px; }.tools select { padding:0 8px; }.tools button { padding:0 12px; cursor:pointer; }.tools button:disabled { cursor:wait; opacity:.55; }
.message { margin:8px 0 0; color:#d8b967; font-size:12px; }.empty { padding:40px 4px; color:#758198; }
.market-band { display:grid; grid-template-columns:130px minmax(0,1fr); margin-top:14px; border:1px solid #2b3850; background:#131e33; }
.market-band.with-stance { grid-template-columns:130px 300px minmax(0,1fr); }
.phase { display:flex; flex-direction:column; justify-content:center; gap:4px; min-height:128px; padding:18px; border-right:1px solid #2b3850; }.phase small { color:#8491a7; }.phase b { font-size:30px; }.phase span { color:#9ba7b9; font-size:11px; }.phase.up b { color:#ef6a72; }.phase.down b { color:#46bd91; }.phase.range b { color:#e9c16c; }
.stance { display:flex; flex-direction:column; justify-content:center; gap:4px; padding:14px 18px; border-right:1px solid #2b3850; }.stance small { color:#8491a7; font-size:10px; }.stance>b { font-size:26px; }.stance.take-profit>b { color:#e9c16c; }.stance.accumulate>b { color:#ef6a72; }.stance.hold>b { color:#93a0b6; }
.stance-hint { color:#9ba7b9; font-size:11px; }
.stance-metrics { display:flex; flex-wrap:wrap; gap:4px 12px; margin-top:4px; color:#7f8ca2; font-size:10px; }.stance-metrics b { margin-left:3px; color:#dce3ed; font-size:10px; }
.stance-reason { margin:4px 0 0; color:#8794a8; font-size:10px; line-height:1.5; }
.market-conclusion { display:flex; flex-direction:column; justify-content:center; gap:8px; padding:18px 22px; }.market-conclusion strong { font-size:16px; line-height:1.5; }.market-conclusion p { margin:0; color:#aab4c4; font-size:12px; line-height:1.6; }
.facts-band,.panel { margin-top:12px; border:1px solid #26324a; background:#131e33; }.facts-band { padding:12px; }.section-head { display:flex; align-items:baseline; justify-content:space-between; gap:8px; margin-bottom:10px; }.section-head strong { font-size:13px; }
.index-grid { display:grid; grid-template-columns:repeat(auto-fit,minmax(120px,1fr)); gap:1px; background:#26324a; }.index-cell { display:grid; grid-template-columns:1fr auto; gap:4px 8px; padding:9px 10px; background:#182338; }.index-cell span { grid-column:1/-1; color:#8d99ad; font-size:10px; }.index-cell b { font-size:14px; }.index-cell em { font-size:12px; font-style:normal; }
.breadth-row { display:grid; grid-template-columns:repeat(5,minmax(100px,1fr)); gap:8px; margin-top:10px; }.breadth-row>div { display:flex; flex-direction:column; gap:4px; padding:8px 10px; border-left:2px solid #394865; background:#101a2b; }.breadth-row small { color:#7f8ca2; font-size:10px; }.breadth-row b { font-size:14px; }
.panel { padding:12px; }.sector-table { overflow-x:auto; }.sector-row { display:grid; grid-template-columns:120px 48px 65px 65px 70px 58px minmax(180px,1.4fr) minmax(160px,1fr); gap:10px; align-items:center; min-width:920px; padding:9px 8px; border-bottom:1px solid #202d43; color:#bdc6d4; font-size:11px; }.sector-row.head { color:#738097; }.sector-row span:first-child b,.sector-row span:first-child small { display:block; }.sector-row span:first-child b { color:#e7ecf4; font-size:12px; }.sector-row span:first-child small { margin-top:2px; color:#67748a; font-size:9px; }.strength { font-weight:700; }.strength.strong { color:#ef6a72; }.strength.weak { color:#46bd91; }.strength.neutral { color:#e9c16c; }
.check-grid { display:grid; grid-template-columns:repeat(3,minmax(220px,1fr)); gap:8px; }.check-item { min-width:0; padding:11px; border:1px solid #293750; background:#182338; }.check-title { display:flex; align-items:center; justify-content:space-between; gap:8px; }.check-title span b,.check-title span small { display:block; }.check-title span small { margin-top:2px; color:#718098; font-size:9px; }.check-title em,.directive-review-item em { flex:none; padding:2px 6px; font-size:10px; font-style:normal; }.check-title em.hit,.directive-review-item em.effective { background:#43282d; color:#ef8c92; }.check-title em.miss,.directive-review-item em.ineffective { background:#18382f; color:#67cba6; }.check-title em.mixed,.directive-review-item em.unclear { background:#3a3425; color:#e9c16c; }.check-numbers { display:flex; flex-wrap:wrap; gap:12px; margin:9px 0; color:#7f8ca2; font-size:10px; }.check-numbers b { margin-left:3px; color:#dce3ed; }.check-item p { margin:0; color:#9eabbd; font-size:11px; line-height:1.5; }
.pick-grid { display:grid; grid-template-columns:repeat(3,minmax(220px,1fr)); gap:8px; }.pick-item { min-width:0; padding:12px; border:1px solid #293750; border-radius:0; background:#182338; color:#dce3ed; text-align:left; cursor:pointer; }.pick-item:hover { border-color:#455675; }.pick-title { display:flex; align-items:center; justify-content:space-between; }.pick-title span b,.pick-title span small { display:block; }.pick-title span small { margin-top:2px; color:#718098; font-size:9px; }.pick-title em { padding:2px 6px; font-size:10px; font-style:normal; }.pick-title em.hit { background:#43282d; color:#ef8c92; }.pick-title em.miss { background:#18382f; color:#67cba6; }.pick-title em.watching { background:#3a3425; color:#e9c16c; }.pick-numbers { display:flex; flex-wrap:wrap; gap:8px 14px; margin:10px 0; color:#7f8ca2; font-size:10px; }.pick-numbers b { margin-left:3px; color:#dce3ed; }.pick-item p { margin:5px 0 0; color:#9eabbd; font-size:11px; line-height:1.5; }.pick-item p strong { margin-right:6px; color:#d3dbe7; }
.directive-review-list { display:grid; grid-template-columns:repeat(2,minmax(0,1fr)); gap:8px; }.directive-review-item { display:grid; grid-template-columns:auto minmax(0,1fr); align-items:start; gap:9px; padding:10px; background:#101a2b; }.directive-review-item p { margin:0; }.directive-review-item b,.directive-review-item span { display:block; }.directive-review-item b { font-size:11px; line-height:1.45; }.directive-review-item span { margin-top:4px; color:#8794a8; font-size:10px; line-height:1.45; }
.decision-grid { display:grid; grid-template-columns:1fr 1fr; gap:12px; }.decision-grid .panel { margin-top:12px; }.risk-panel { grid-column:1/-1; }.lesson-cols { display:grid; grid-template-columns:1fr 1fr; gap:12px; }.lesson-cols>div { padding:10px; background:#101a2b; }.lesson-cols small { color:#8592a7; }.lesson-cols p,.risk-panel>p { margin:7px 0 0; color:#b2bdcc; font-size:11px; line-height:1.5; }.directive-item { display:grid; grid-template-columns:24px 1fr; gap:8px; padding:8px 0; border-bottom:1px solid #202d43; }.directive-item i { display:flex; width:22px; height:22px; align-items:center; justify-content:center; background:#2a3a5c; color:#e9c16c; font-size:10px; font-style:normal; }.directive-item p { margin:0; }.directive-item b,.directive-item span { display:block; }.directive-item b { font-size:11px; }.directive-item span { margin-top:3px; color:#8794a8; font-size:10px; line-height:1.4; }.risk-numbers { display:grid; grid-template-columns:repeat(3,1fr); gap:8px; }.risk-numbers>span { display:flex; align-items:center; justify-content:space-between; padding:9px 12px; background:#101a2b; }.risk-numbers small { color:#8794a8; }.risk-numbers b { color:#e9c16c; }
.up { color:#ef6a72!important; }.down { color:#46bd91!important; }.dim { color:#93a0b6!important; }
@media (max-width:900px) { .review-shell { grid-template-columns:1fr; width:100%; height:auto; min-height:100vh; overflow:visible; }.review-content { overflow:visible; padding:0 10px 20px; }.review-header { top:0; }.market-band { grid-template-columns:100px 1fr; }.market-conclusion { grid-column:1/-1; border-top:1px solid #2b3850; }.breadth-row { grid-template-columns:repeat(2,1fr); }.pick-grid,.check-grid,.decision-grid { grid-template-columns:1fr; }.risk-panel { grid-column:auto; } }
@media (max-width:560px) { .review-header { align-items:flex-start; flex-direction:column; gap:8px; padding:10px 0; }.tools { width:100%; }.tools select { min-width:0; flex:1; }.market-band { grid-template-columns:1fr; }.phase,.stance { min-height:auto; border-right:0; border-bottom:1px solid #2b3850; }.market-conclusion { grid-column:auto; border-top:0; }.index-grid { grid-template-columns:repeat(2,1fr); }.lesson-cols,.risk-numbers,.directive-review-list { grid-template-columns:1fr; } }
</style>
