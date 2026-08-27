<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import {
  api,
  fmtPct,
  pctClass,
  type DailyReviewReport,
  type HotspotReport,
  type MarketSentiment,
  type Position,
  type RecommendationBasketPerformance,
  type TradeAccount,
} from '../api'

const router = useRouter()

const account = ref<TradeAccount | null>(null)
const accountError = ref('')
const accountLoading = ref(true)
const positions = ref<Position[]>([])
const basketPerformance = ref<RecommendationBasketPerformance[]>([])
const hotspot = ref<HotspotReport | null>(null)
const review = ref<DailyReviewReport | null>(null)
const sentiment = ref<MarketSentiment | null>(null)
const error = ref('')

const basketElement = ref<HTMLElement | null>(null)
const basketWidth = ref(960)
let basketObserver: ResizeObserver | null = null

function money(value: number | null | undefined) {
  if (value == null) return '—'
  return new Intl.NumberFormat('zh-CN', { style: 'currency', currency: 'CNY', signDisplay: 'exceptZero', minimumFractionDigits: 2, maximumFractionDigits: 2 }).format(value)
}

function signed(value: number | null | undefined, digits = 2) {
  if (value == null) return '—'
  return `${value >= 0 ? '+' : ''}${value.toFixed(digits)}%`
}

function pnlClass(value: number | null | undefined) {
  if (value == null || value === 0) return 'flat'
  return value > 0 ? 'up' : 'down'
}

function positionStatusLabel(status: Position['status']) {
  return ({
    pending_entry: '等待建仓', holding: '持有中', exited: '已退出', expired: '已过期', removed: '已移除',
  } as const)[status] || status
}

const sentimentCls = computed(() => {
  if (!sentiment.value) return 's-none'
  const score = sentiment.value.score
  if (score <= 24) return 's-extreme-fear'
  if (score <= 44) return 's-fear'
  if (score <= 55) return 's-neutral'
  if (score <= 74) return 's-greed'
  return 's-extreme-greed'
})

const actionRows = computed(() => positions.value.filter(p => p.status === 'pending_entry' || p.status === 'holding').slice(0, 6))

const hotConcepts = computed(() => {
  if (!hotspot.value?.concepts) return [] as NonNullable<HotspotReport['concepts']>
  return [...hotspot.value.concepts]
    .filter(c => c.stats && c.confidence > 0)
    .sort((a, b) => b.confidence - a.confidence)
    .slice(0, 4)
})

const reviewKeypoints = computed(() => {
  if (!review.value) return [] as string[]
  const items: string[] = []
  if (review.value.market_summary) items.push(review.value.market_summary)
  if (review.value.breadth_review) items.push(review.value.breadth_review)
  for (const dir of review.value.directives?.slice(0, 2) || []) {
    if (dir.rationale) items.push(`${dir.action}：${dir.rationale}`)
  }
  return items.filter(Boolean).slice(0, 4)
})

const reviewTags = computed(() => {
  if (!review.value?.facts) return [] as string[]
  const out: string[] = []
  for (const sector of review.value.facts.strong_sectors?.slice(0, 3) || []) {
    out.push(`强势 ${sector.sector_name} ${sector.avg_change.toFixed(2)}%`)
  }
  for (const sector of review.value.facts.weak_sectors?.slice(0, 2) || []) {
    out.push(`弱势 ${sector.sector_name} ${sector.avg_change.toFixed(2)}%`)
  }
  return out
})

const basketChart = computed(() => {
  const values = [...basketPerformance.value]
    .reverse()
    .filter(item => item.avg_change_pct != null)
    .map(item => ({ ...item, value: item.avg_change_pct as number }))
  const width = Math.max(320, basketWidth.value)
  const height = width < 640 ? 200 : 220
  const left = 44, right = 14, top = 14, bottom = 28
  const rawMin = Math.min(0, ...values.map(item => item.value))
  const rawMax = Math.max(0, ...values.map(item => item.value))
  const padding = Math.max(.6, (rawMax - rawMin) * .14)
  const min = rawMin - padding, max = rawMax + padding
  const plotW = width - left - right, plotH = height - top - bottom
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
  const labelIndexes = new Set(Array.from({ length: Math.min(6, values.length) }, (_, index) => Math.round(index * (values.length - 1) / Math.max(Math.min(6, values.length) - 1, 1))))
  const labels = values.map((item, index) => ({ ...item, index })).filter(item => labelIndexes.has(item.index))
  const grid = Array.from({ length: 4 }, (_, index) => {
    const value = max - index * (max - min) / 3
    return { value, y: y(value) }
  })
  return { width, height, left, right, top, plotBottom: height - bottom, values, min, max, zeroY, x, y, smoothPath, areaPath, labels, grid }
})

function openRisk() { router.push({ path: '/', query: { view: 'risk' } }) }
function openHotspot() { router.push({ path: '/', query: { view: 'hotspot' } }) }
function openReview() { router.push({ path: '/', query: { view: 'review' } }) }
function openTrade() { router.push({ path: '/', query: { view: 'reco' } }) }
function openStock(symbol: string) { router.push(`/stock/${symbol}`) }

async function observeBasket() {
  await nextTick()
  basketObserver?.disconnect()
  if (!basketElement.value) return
  const update = () => { basketWidth.value = Math.floor(basketElement.value?.clientWidth || 960) }
  update()
  basketObserver = new ResizeObserver(update)
  basketObserver.observe(basketElement.value)
}

async function loadAccount() {
  accountLoading.value = true
  try {
    account.value = await api.tradeAccount()
    accountError.value = ''
  } catch (e: any) {
    account.value = null
    accountError.value = e?.message || '账户数据不可用'
  } finally {
    accountLoading.value = false
  }
}

async function loadSide() {
  const [posResult, basketResult, hotspotResult, reviewResult, riskResult] = await Promise.allSettled([
    api.positions(200),
    api.recommendationBasketPerformance(30),
    api.hotspot(),
    api.dailyReview(),
    api.riskGate(),
  ])
  positions.value = posResult.status === 'fulfilled' ? posResult.value.items : []
  basketPerformance.value = basketResult.status === 'fulfilled' ? basketResult.value : []
  hotspot.value = hotspotResult.status === 'fulfilled' ? hotspotResult.value : null
  review.value = reviewResult.status === 'fulfilled' ? reviewResult.value : null
  sentiment.value = riskResult.status === 'fulfilled' ? riskResult.value.market_sentiment : null
  await observeBasket()
}

async function load() {
  error.value = ''
  await loadAccount()
  try {
    await loadSide()
  } catch (e: any) {
    error.value = e?.message || '首页加载失败'
  }
}

onMounted(load)
onBeforeUnmount(() => basketObserver?.disconnect())
</script>

<template>
  <main class="home-overview">
    <header class="home-header">
      <div class="brand">
        <strong>go-stock</strong>
        <router-link to="/?view=risk" class="sentiment" :class="sentimentCls" title="点击查看风险板块">
          <template v-if="sentiment">
            <b>{{ sentiment.score }}</b>
            <span><i>市场风险情绪</i><em>{{ sentiment.label }}</em></span>
          </template>
          <template v-else>
            <b>—</b>
            <span><i>市场风险情绪</i><em>数据不足</em></span>
          </template>
        </router-link>
      </div>
      <button type="button" class="refresh" :disabled="accountLoading" title="刷新首页" aria-label="刷新首页" @click="load">↻</button>
    </header>

    <section class="hero" aria-label="账户核心">
      <article class="hero-card total">
        <span>当前总盈亏</span>
        <strong :class="pnlClass(account?.total_pnl)">{{ money(account?.total_pnl) }}</strong>
        <small>账户总资产减去初始资金</small>
      </article>
      <article class="hero-card today">
        <span>今日盈亏</span>
        <strong :class="pnlClass(account?.today_pnl)">{{ money(account?.today_pnl) }}</strong>
        <small>今日持仓变动 + 今日卖出已扣费用</small>
      </article>
    </section>

    <section class="grid" aria-label="总览区块">
      <article class="panel positions">
        <header><div><b>持仓信息</b><small>等待建仓 / 持有中，扣往返成本</small></div><button type="button" class="link-btn" @click="openTrade">全部明细 →</button></header>
        <div v-if="actionRows.length" class="rows">
          <button v-for="item in actionRows" :key="item.id" type="button" class="row" @click="openStock(item.symbol)">
            <span class="status" :class="item.status">{{ positionStatusLabel(item.status) }}</span>
            <span class="name"><b>{{ item.name || item.symbol }}</b><small>{{ item.code }}</small></span>
            <span class="num"><small>成本</small>{{ item.entry_price == null ? '—' : item.entry_price.toFixed(2) }}</span>
            <span class="num"><small>现价</small>{{ item.reference_price == null ? '—' : item.reference_price.toFixed(2) }}</span>
            <strong :class="pctClass(item.change_pct)">{{ signed(item.change_pct) }}</strong>
          </button>
        </div>
        <div v-else class="empty">当前没有需要处理的持仓</div>
      </article>

      <article class="panel hotspot">
        <header><div><b>热点有效决策线索</b><small>当前主线与高置信概念，趋势可参与</small></div><button type="button" class="link-btn" @click="openHotspot">热点漏斗 →</button></header>
        <ul v-if="hotConcepts.length" class="hot-list">
          <li v-for="concept in hotConcepts" :key="concept.sector_code">
            <button type="button" @click="router.push({ path: '/', query: { view: 'hotspot', sector: concept.sector_code } })">
              <span class="hot-name"><b>{{ concept.sector_name }}</b><small>{{ concept.status === 'accelerating' ? '加速' : concept.status === 'overheated' ? '过热' : '潜伏' }} · 置信度 {{ concept.confidence.toFixed(0) }}</small></span>
              <span class="hot-stat" :class="concept.stats.avg_change >= 0 ? 'up' : 'down'">{{ fmtPct(concept.stats.avg_change) }}</span>
            </button>
            <p>{{ concept.reason }}</p>
          </li>
        </ul>
        <div v-else class="empty">热点数据不可用，进入热点板块手动运行</div>
      </article>

      <article class="panel review">
        <header><div><b>复盘关键信息</b><small>最近一次每日复盘的方向与板块结论</small></div><button type="button" class="link-btn" @click="openReview">每日复盘 →</button></header>
        <div v-if="review && (reviewKeypoints.length || reviewTags.length)" class="review-body">
          <ul v-if="reviewKeypoints.length">
            <li v-for="(line, index) in reviewKeypoints" :key="index">{{ line }}</li>
          </ul>
          <div v-if="reviewTags.length" class="tags">
            <i v-for="tag in reviewTags" :key="tag">{{ tag }}</i>
          </div>
          <p v-if="review.market_phase" class="phase">当前阶段：<b :class="review.market_phase">{{ review.market_phase === 'up' ? '上行' : review.market_phase === 'down' ? '下行' : '震荡' }}</b></p>
        </div>
        <div v-else class="empty">复盘数据未生成</div>
      </article>

      <article class="panel risk">
        <header><div><b>风险关键信息</b><small>境内趋势与隔夜外盘综合判断</small></div><button type="button" class="link-btn" @click="openRisk">风险板块 →</button></header>
        <div v-if="sentiment" class="risk-body">
          <div class="sentiment-bar" :class="sentimentCls"><span :style="{ left: `${sentiment.score}%` }" /></div>
          <p class="risk-score">综合分 <b>{{ sentiment.score }}</b> · 标签 <em>{{ sentiment.label }}</em></p>
          <p class="risk-factors">波动率 <b>{{ sentiment.volatility.toFixed(0) }}</b> · 动量 <b>{{ sentiment.momentum.toFixed(0) }}</b> · 宽度 <b>{{ sentiment.breadth.toFixed(0) }}</b> · 趋势 <b>{{ sentiment.trend.toFixed(0) }}</b> · 外盘 <b>{{ sentiment.global_appetite.toFixed(0) }}</b></p>
        </div>
        <div v-else class="empty">风险数据不可用</div>
      </article>

      <article class="panel basket">
        <header><div><b>历史推荐组合走势</b><small>每日三只等权，仅最强按真实交易结算</small></div><button type="button" class="link-btn" @click="openTrade">历史推荐 →</button></header>
        <div v-if="basketChart.values.length" ref="basketElement" class="chart-wrap">
          <svg :width="basketChart.width" :height="basketChart.height" :viewBox="`0 0 ${basketChart.width} ${basketChart.height}`" role="img" aria-label="每日三只趋势推荐等权平均表现">
            <defs>
              <clipPath id="basketGainClip"><rect :x="basketChart.left" :y="basketChart.top" :width="basketChart.width-basketChart.left-basketChart.right" :height="Math.max(0,basketChart.zeroY-basketChart.top)"/></clipPath>
              <clipPath id="basketLossClip"><rect :x="basketChart.left" :y="basketChart.zeroY" :width="basketChart.width-basketChart.left-basketChart.right" :height="Math.max(0,basketChart.plotBottom-basketChart.zeroY)"/></clipPath>
            </defs>
            <g class="grid">
              <line v-for="tick in basketChart.grid" :key="tick.value" :x1="basketChart.left" :x2="basketChart.width-basketChart.right" :y1="tick.y" :y2="tick.y" />
              <text v-for="tick in basketChart.grid" :key="`label-${tick.value}`" :x="basketChart.left-6" :y="tick.y+3">{{ tick.value.toFixed(1) }}</text>
            </g>
            <path :d="basketChart.areaPath" class="area gain" clip-path="url(#basketGainClip)" />
            <path :d="basketChart.areaPath" class="area loss" clip-path="url(#basketLossClip)" />
            <line :x1="basketChart.left" :x2="basketChart.width-basketChart.right" :y1="basketChart.zeroY" :y2="basketChart.zeroY" class="zero" />
            <path :d="basketChart.smoothPath" class="line gain" clip-path="url(#basketGainClip)" />
            <path :d="basketChart.smoothPath" class="line loss" clip-path="url(#basketLossClip)" />
            <circle v-for="(point, index) in basketChart.values" :key="point.date" :cx="basketChart.x(index)" :cy="basketChart.y(point.value)" r="2.5" :class="['pt', point.value >= 0 ? 'gain' : 'loss']"><title>{{ point.date }} 平均 {{ signed(point.value) }}</title></circle>
            <text v-for="label in basketChart.labels" :key="label.date" :x="basketChart.x(label.index)" :y="basketChart.height-8" class="axis">{{ label.date.slice(5) }}</text>
          </svg>
        </div>
        <div v-else class="empty">等待推荐日数据</div>
      </article>
    </section>

    <p v-if="error" class="state-error">{{ error }}</p>
    <p v-if="accountError" class="state-error">{{ accountError }}</p>
  </main>
</template>

<style scoped>
.home-overview{min-width:0;min-height:100%;overflow:auto;padding:18px;background:#0f1826;color:#e7ecf4;letter-spacing:0}
.home-header{display:flex;align-items:center;justify-content:space-between;margin-bottom:14px}
.brand{display:flex;min-width:0;align-items:center;gap:14px}
.brand>strong{font-size:18px;letter-spacing:.5px}
.sentiment{display:grid;grid-template-columns:auto auto;align-items:center;gap:6px 12px;padding:7px 14px;border:1px solid #2d3b54;border-radius:6px;color:#e7ecf4;text-decoration:none;background:#131e33}
.sentiment:hover{background:#182338}
.sentiment b{font-size:22px;line-height:1;font-variant-numeric:tabular-nums}
.sentiment span{display:grid;gap:1px}
.sentiment small{color:#8895ab;font-size:10px}
.sentiment em{font-size:13px;font-style:normal}
.sentiment.s-extreme-fear b,.sentiment.s-fear b{color:#55b996}
.sentiment.s-neutral b{color:#e9c16c}
.sentiment.s-greed b{color:#df9462}
.sentiment.s-extreme-greed b{color:#ef6a72}
.sentiment.s-extreme-fear{border-left:4px solid #28755d}
.sentiment.s-fear{border-left:4px solid #55b996}
.sentiment.s-neutral{border-left:4px solid #e9c16c}
.sentiment.s-greed{border-left:4px solid #df9462}
.sentiment.s-extreme-greed{border-left:4px solid #ef6a72}
.sentiment.s-none{border-left:4px solid #6b7b94}
.refresh{display:grid;width:32px;height:32px;place-items:center;border:1px solid #3a496a;border-radius:3px;background:#18243a;color:#c4cddc;cursor:pointer;font-size:19px}
.refresh:hover{background:#21304b}.refresh:disabled{cursor:wait;opacity:.55}
.hero{display:grid;grid-template-columns:minmax(0,1fr) minmax(0,1fr);gap:12px;margin-bottom:14px}
.hero-card{display:flex;min-width:0;min-height:142px;flex-direction:column;justify-content:center;gap:10px;padding:22px;border:1px solid #2d3b54;border-radius:6px;background:#131e33}
.hero-card.total{border-top:4px solid #67a9d8}
.hero-card.today{border-top:4px solid #e9c16c}
.hero-card span{color:#98a5b9;font-size:13px}
.hero-card strong{max-width:100%;font-size:clamp(28px,4.2vw,52px);font-variant-numeric:tabular-nums;line-height:1.05;overflow-wrap:anywhere}
.hero-card small{color:#6f7d97;font-size:11px}
.up{color:#ef6a72}.down{color:#55b996}.flat{color:#d6dce6}
.grid{display:grid;grid-template-columns:minmax(0,1.1fr) minmax(0,1fr);gap:12px}
.panel{min-width:0;border:1px solid #2d3b54;border-radius:6px;background:#131e33;display:flex;flex-direction:column}
.panel>header{display:flex;min-height:46px;align-items:center;justify-content:space-between;gap:12px;padding:9px 14px;border-bottom:1px solid #2d3b54}
.panel>header>div{display:flex;flex-direction:column;gap:2px}
.panel>header b{font-size:14px}
.panel>header small{color:#8895ab;font-size:11px}
.link-btn{flex:0 0 auto;padding:4px 10px;border:1px solid #3d5680;border-radius:3px;background:#16233b;color:#8fb4e3;cursor:pointer;font-size:10px;white-space:nowrap}
.link-btn:hover{background:#1d3050;color:#bcd6f5}
.empty{padding:24px;color:#75839a;font-size:12px;text-align:center}
.rows{display:grid}
.row{display:grid;grid-template-columns:84px minmax(120px,1.4fr) 88px 88px 88px;gap:10px;align-items:center;padding:9px 14px;border:0;border-bottom:1px solid #202c42;background:transparent;color:#e7ecf4;text-align:left;cursor:pointer}
.row:hover{background:#182338}
.row:last-child{border-bottom:0}
.row .status{justify-self:start;padding:2px 6px;background:#25334b;font-size:10px}
.row .status.holding{background:#34272d;color:#ef8b91}
.row .status.pending_entry{background:#3d3423;color:#e9c16c}
.row .name b{display:block;font-size:12px}
.row .name small{display:block;margin-top:2px;color:#8895ab;font-size:10px}
.row .num>small{display:block;margin-bottom:2px;color:#71809a;font-size:9px}
.row .num{font-size:12px;font-variant-numeric:tabular-nums}
.row>strong{text-align:right;font-size:13px;font-variant-numeric:tabular-nums}
.hot-list{display:grid;gap:0}
.hot-list li{padding:10px 14px;border-bottom:1px solid #202c42}
.hot-list li:last-child{border-bottom:0}
.hot-list button{display:flex;width:100%;align-items:center;justify-content:space-between;gap:10px;padding:0;border:0;background:transparent;color:#e7ecf4;text-align:left;cursor:pointer}
.hot-name b{display:block;font-size:13px}
.hot-name small{display:block;margin-top:2px;color:#8895ab;font-size:10px}
.hot-stat{font-size:14px;font-weight:700;font-variant-numeric:tabular-nums}
.hot-list p{margin:6px 0 0;color:#9ba8bd;font-size:11px;line-height:1.4}
.review-body{padding:12px 14px;display:grid;gap:10px}
.review-body ul{margin:0;padding-left:18px;color:#b4bece;font-size:12px;line-height:1.5}
.review-body .tags{display:flex;flex-wrap:wrap;gap:5px}
.review-body .tags i{padding:2px 7px;background:#1f2c43;border-radius:3px;color:#cad5e7;font-size:10px;font-style:normal}
.review-body .phase{margin:0;color:#8895ab;font-size:11px}
.review-body .phase b{color:#e9c16c}
.review-body .phase b.down{color:#55b996}
.review-body .phase b.up{color:#ef6a72}
.risk-body{padding:14px;display:grid;gap:10px}
.sentiment-bar{position:relative;height:10px;border-radius:5px;background:linear-gradient(90deg,#28755d 0%,#55b996 20%,#e9c16c 44%,#df9462 56%,#ef6a72 80%,#ef6a72 100%);overflow:visible}
.sentiment-bar>span{position:absolute;top:-3px;width:4px;height:16px;background:#f5f7fb;border-radius:2px;transform:translateX(-2px);box-shadow:0 0 0 2px #131e33}
.risk-score,.risk-factors{margin:0;color:#b4bece;font-size:12px;line-height:1.5}
.risk-score b{color:#e7ecf4;font-size:18px;font-weight:700;margin:0 4px}
.risk-score em{color:#e9c16c;font-style:normal;font-weight:600}
.risk-factors b{color:#e7ecf4;margin:0 2px;font-variant-numeric:tabular-nums;font-weight:500}
.chart-wrap{width:100%;height:220px;padding:6px 10px 4px;overflow:hidden}
.chart-wrap svg{display:block;width:100%;height:100%}
.grid line{stroke:#212d42;stroke-width:1}
.grid text{fill:#6b7b94;font-size:9px;text-anchor:end;font-variant-numeric:tabular-nums}
.area.gain{fill:#d95561;opacity:.18}
.area.loss{fill:#35a37d;opacity:.18}
.line{fill:none;stroke-width:2;vector-effect:non-scaling-stroke}
.line.gain{stroke:#f2606d}
.line.loss{stroke:#2eb98a}
.zero{stroke:#7286a3;stroke-width:1;stroke-dasharray:4 4}
.pt.gain{fill:#f2606d;stroke:#131e33;stroke-width:1}
.pt.loss{fill:#2eb98a;stroke:#131e33;stroke-width:1}
.axis{fill:#71809a;font-size:10px;text-anchor:middle}
.state-error{margin:8px 0 0;color:#ef7d84;font-size:12px}
@media(max-width:960px){
  .grid{grid-template-columns:1fr}
  .row{grid-template-columns:72px minmax(0,1fr) 80px 80px 80px}
  .hero{grid-template-columns:1fr}
  .hero-card{min-height:120px;padding:18px}
  .hero-card strong{font-size:clamp(24px,9vw,38px)}
}
</style>
