<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { api, fmtBig, fmtPct, type HotspotReport, type HotspotRunSummary } from '../api'

const router = useRouter()
const report = ref<HotspotReport>({})
const history = ref<HotspotRunSummary[]>([])
const activeRunId = ref<number | ''>('')
const activeStage = ref(0)
const loading = ref(true)
const running = ref(false)
const enabled = ref(false)
const error = ref('')
let statusTimer: number | undefined

const stages = computed(() => [
  { key: 'screen', index: '01', title: '市场热度初筛', sub: '涨幅 · 量能 · 广度', count: report.value.screened?.length || 0 },
  { key: 'relation', index: '02', title: '概念关系收敛', sub: '成分重叠 · 邻接发现', count: report.value.data_relations?.length || 0 },
  { key: 'ai', index: '03', title: 'AI 产业链分析', sub: '主线归因 · 卡点推理', count: report.value.mainlines?.length || 0 },
  { key: 'final', index: '04', title: '有效热点输出', sub: '本地回验 · 风险分层', count: report.value.concepts?.length || 0 }
])

const conceptNames = computed(() => {
  const names = new Map<string, string>()
  report.value.screened?.forEach(item => names.set(item.sector_code, item.sector_name))
  report.value.data_relations?.forEach(item => {
    names.set(item.from_code, item.from_name)
    names.set(item.to_code, item.to_name)
  })
  report.value.concepts?.forEach(item => names.set(item.sector_code, item.sector_name))
  return names
})

const maxHeatScore = computed(() => {
  const scores = report.value.screened?.map(item => item.heat_score) || []
  return Math.max(...scores, 0.01)
})

const statusMeta: Record<string, { label: string; className: string }> = {
  accelerating: { label: '加速确认', className: 'hot' },
  latent: { label: '潜伏观察', className: 'latent' },
  overheated: { label: '过热警示', className: 'risk' }
}

async function load() {
  loading.value = true
  try {
    const [result, status, runs] = await Promise.all([api.hotspot(), api.hotspotStatus(), api.hotspotHistory(30).catch(() => [] as HotspotRunSummary[])])
    report.value = result
    enabled.value = status.enabled
    running.value = status.running
    history.value = runs
    activeRunId.value = runs.length ? runs[0].id : ''
    error.value = ''
  } catch (e: any) {
    error.value = e?.message || '热点漏斗加载失败'
  } finally {
    loading.value = false
  }
}

async function loadRun() {
  if (activeRunId.value === '') return
  loading.value = true
  try {
    report.value = await api.hotspot(Number(activeRunId.value))
    error.value = ''
  } catch (e: any) {
    error.value = e?.message || '历史报告加载失败'
  } finally {
    loading.value = false
  }
}

async function runAnalysis() {
  running.value = true
  error.value = ''
  try {
    await api.runHotspot()
    await load()
    activeStage.value = 3
  } catch (e: any) {
    error.value = e?.message || 'AI 热点分析失败'
  } finally {
    running.value = false
  }
}

function openSector(code: string) {
  router.push(`/sector/${encodeURIComponent(code)}`)
}

function openStock(symbol: string) {
  router.push(`/stock/${encodeURIComponent(symbol)}`)
}

function relationName(code: string) {
  return conceptNames.value.get(code) || code
}

onMounted(() => {
  load()
  statusTimer = window.setInterval(async () => {
    if (!running.value) return
    try {
      const status = await api.hotspotStatus()
      running.value = status.running
      if (!status.running) await load()
    } catch { /* keep current state */ }
  }, 5000)
})
onUnmounted(() => window.clearInterval(statusTimer))
</script>

<template>
  <section class="funnel-workspace">
    <aside class="funnel-visual" aria-label="热点筛选漏斗">
      <div class="funnel-heading">
        <div class="heading-copy"><strong>热点发现漏斗</strong><span>本地行情证据 · AI 产业链推理</span></div>
        <div class="heading-meta">
          <select v-if="history.length" v-model="activeRunId" class="run-select" aria-label="历史运行记录" @change="loadRun">
            <!-- created_at 是运行时间，report_date 是所用收盘数据日期（周一盘前为上周五），分开标注避免误解 -->
            <option v-for="run in history" :key="run.id" :value="run.id">{{ run.created_at.slice(5) }} 运行 · 数据 {{ run.report_date.slice(5) }}</option>
          </select>
          <span class="schedule-badge"><i></i>交易日 08:00</span>
        </div>
      </div>
      <div class="funnel-stack">
        <div class="funnel-intake"><span>概念市场全量扫描</span><i></i><span>本地数据库</span></div>
        <div class="funnel-chart">
          <button
            v-for="(stage, index) in stages"
            :key="stage.key"
            type="button"
            class="funnel-seg"
            :class="[`seg-${index + 1}`, { active: activeStage === index }]"
            @click="activeStage = index"
          >
            <b>{{ stage.title }}</b>
            <em>{{ stage.count }} {{ index === 1 ? '关系' : index === 2 ? '主线' : '概念' }}</em>
            <small>{{ stage.sub }}</small>
          </button>
          <button
            type="button"
            class="funnel-seg seg-outlet"
            :class="{ active: activeStage === 3 }"
            @click="activeStage = 3"
          >
            <b>有效决策线索</b>
            <em>{{ report.concepts?.length || 0 }} 概念</em>
          </button>
        </div>
      </div>
      <div class="funnel-actions">
        <button type="button" class="run-button" :disabled="!enabled || running" @click="runAnalysis">
          <i v-if="running" class="spin"></i>{{ running ? 'AI 分析中…' : '运行 AI 分析' }}
        </button>
        <span v-if="!enabled" class="config-state warning"><i></i>AI 能力未就绪</span>
        <span v-else-if="report.model" class="config-state"><i></i>{{ report.model }}</span>
        <span v-else class="config-state"><i></i>共享趋势推荐 AI</span>
      </div>
    </aside>

    <section class="stage-results">
      <header class="results-header">
        <div><span>漏斗层级 {{ stages[activeStage].index }}</span><strong>{{ stages[activeStage].title }}</strong></div>
        <p><i :class="{ running }"></i>{{ running ? '正在重新分析' : stages[activeStage].sub }}</p>
      </header>

      <div v-if="loading" class="state-message">正在读取热点分析结果</div>
      <div v-else-if="error" class="state-message error">{{ error }}</div>
      <div v-else-if="!report.report_date" class="state-message">暂无热点报告，运行 AI 分析后生成结果</div>

      <div v-else-if="activeStage === 0" class="result-scroll screen-list">
        <button v-for="(item, index) in report.screened" :key="item.sector_code" type="button" class="screen-row" @click="openSector(item.sector_code)">
          <span class="rank">{{ String(index + 1).padStart(2, '0') }}</span>
          <span class="primary">
            <b>{{ item.sector_name }}</b>
            <small>{{ item.stock_count }} 只成分股 · 涨停 {{ item.limit_up_count }}</small>
            <span class="heat-bar"><i :style="{ '--heat': `${Math.min(100, Math.max(6, item.heat_score / maxHeatScore * 100))}%` }"></i></span>
          </span>
          <span><small>当日</small><b :class="item.avg_change >= 0 ? 'up' : 'down'">{{ fmtPct(item.avg_change) }}</b></span>
          <span><small>5 日</small><b :class="item.avg_change_5d >= 0 ? 'up' : 'down'">{{ fmtPct(item.avg_change_5d) }}</b></span>
          <span><small>量能</small><b>{{ item.amount_ratio.toFixed(2) }}x</b></span>
          <span><small>广度</small><b>{{ (item.up_ratio * 100).toFixed(0) }}%</b></span>
        </button>
      </div>

      <div v-else-if="activeStage === 1" class="result-scroll relation-list">
        <div v-for="relation in report.data_relations" :key="`${relation.from_code}-${relation.to_code}`" class="relation-row">
          <button type="button" @click="openSector(relation.from_code)">{{ relation.from_name }}</button>
          <span class="relation-line"><i></i><em>{{ (relation.jaccard * 100).toFixed(1) }}% 重叠</em><i></i></span>
          <button type="button" @click="openSector(relation.to_code)">{{ relation.to_name }}</button>
          <small>共同成分 {{ relation.common_count }} 只</small>
        </div>
      </div>

      <div v-else-if="activeStage === 2" class="result-scroll ai-list">
        <section v-for="mainline in report.mainlines" :key="mainline.name" class="mainline-block">
          <header><strong>{{ mainline.name }}</strong><span>{{ mainline.concept_codes.length }} 个关联概念</span></header>
          <p>{{ mainline.thesis }}</p>
          <div class="chain-map">
            <div v-for="relation in mainline.relations" :key="`${relation.from_code}-${relation.to_code}`" class="chain-row">
              <button type="button" @click="openSector(relation.from_code)">{{ relationName(relation.from_code) }}</button>
              <span><b>{{ relation.type }}</b><small>{{ relation.reason }}</small></span>
              <button type="button" @click="openSector(relation.to_code)">{{ relationName(relation.to_code) }}</button>
            </div>
          </div>
        </section>
      </div>

      <div v-else class="result-scroll final-list">
        <article v-for="concept in report.concepts" :key="concept.sector_code" class="concept-result">
          <header>
            <button type="button" @click="openSector(concept.sector_code)">{{ concept.sector_name }}</button>
            <span :class="statusMeta[concept.status]?.className">{{ statusMeta[concept.status]?.label || concept.status }}</span>
            <b>{{ concept.confidence.toFixed(0) }}<small> AI 置信度</small></b>
          </header>
          <p>{{ concept.reason }}</p>
          <div class="evidence-strip">
            <span><small>5 日涨幅</small><b :class="concept.stats.avg_change_5d >= 0 ? 'up' : 'down'">{{ fmtPct(concept.stats.avg_change_5d) }}</b></span>
            <span><small>20 日涨幅</small><b :class="concept.stats.avg_change_20d >= 0 ? 'up' : 'down'">{{ fmtPct(concept.stats.avg_change_20d) }}</b></span>
            <span><small>量能放大</small><b>{{ concept.stats.amount_ratio.toFixed(2) }}x</b></span>
            <span><small>上涨广度</small><b>{{ (concept.stats.up_ratio * 100).toFixed(0) }}%</b></span>
            <span><small>成交额</small><b>{{ fmtBig(concept.stats.total_amount) }}</b></span>
          </div>
          <div class="stock-strip">
            <button v-for="stock in concept.stocks.slice(0, 6)" :key="stock.symbol" type="button" @click="openStock(stock.symbol)">
              <b>{{ stock.name }}</b><span :class="stock.change_pct >= 0 ? 'up' : 'down'">{{ fmtPct(stock.change_pct) }}</span>
            </button>
          </div>
          <footer><b>证伪条件</b><span>{{ concept.invalidation }}</span><button type="button" @click="openSector(concept.sector_code)">查看全部成分股</button></footer>
        </article>
      </div>
    </section>
  </section>
</template>

<style scoped>
.funnel-workspace { display:grid; grid-template-columns:minmax(360px,39%) minmax(0,1fr); min-width:0; min-height:0; height:100%; background:#111827; color:#e8edf5; }

/* ---- 左侧漏斗 ---- */
.funnel-visual { display:grid; min-width:0; min-height:0; grid-template-rows:76px minmax(0,1fr) 70px; border-right:1px solid #344056; background:#151f31; }
.funnel-heading { display:flex; align-items:center; justify-content:space-between; gap:14px; padding:0 24px; border-bottom:1px solid #303c51; background:#182337; }
.heading-copy { display:flex; min-width:0; flex-direction:column; gap:5px; }
.heading-copy strong { font-size:17px; letter-spacing:0; }
.heading-copy span { overflow:hidden; color:#8391a6; font-size:11px; text-overflow:ellipsis; white-space:nowrap; }
.heading-meta { display:flex; flex:0 0 auto; flex-direction:column; align-items:flex-end; gap:5px; }
.run-select { max-width:210px; padding:3px 6px; border:1px solid #39465c; border-radius:3px; background:#1b2740; color:#c8d2e0; font-size:11px; }
.schedule-badge,.heading-date { display:flex; align-items:center; gap:6px; color:#97a5b9; font-size:10px; }
.schedule-badge i { width:6px; height:6px; border-radius:50%; background:#18a976; box-shadow:0 0 0 3px rgba(24,169,118,.13); }
.heading-date { color:#d5b457; }
.funnel-stack { display:flex; min-height:0; flex-direction:column; align-items:stretch; justify-content:center; padding:14px 26px 14px; }
.funnel-intake { display:flex; width:100%; align-items:center; justify-content:space-between; padding:0 4px 10px; color:#718097; font-size:10px; text-transform:uppercase; }
.funnel-intake i { height:1px; flex:1; margin:0 10px; background:#39465c; }
.funnel-chart { display:flex; min-height:0; flex:1; max-height:520px; flex-direction:column; gap:2px; }
.funnel-seg { position:relative; display:flex; min-height:0; flex:1; flex-direction:column; align-items:center; justify-content:center; gap:3px; padding:4px 8px; border:0; border-radius:0; color:#fff; cursor:pointer; text-shadow:0 1px 2px rgba(10,16,28,.35); transition:filter .15s, transform .15s; }
.funnel-seg b { max-width:100%; overflow:hidden; font-size:14px; text-overflow:ellipsis; white-space:nowrap; }
.funnel-seg em { font-size:12px; font-style:normal; font-weight:700; opacity:.95; font-variant-numeric:tabular-nums; }
.funnel-seg small { max-width:100%; overflow:hidden; font-size:9px; opacity:.72; text-overflow:ellipsis; white-space:nowrap; }
.funnel-seg:hover { filter:brightness(1.1); }
.funnel-seg.active { filter:brightness(1.14) saturate(1.08); transform:scale(1.015); z-index:2; }
.funnel-seg.active::after { content:''; position:absolute; inset:0; background:linear-gradient(180deg, rgba(255,255,255,.14), rgba(255,255,255,0)); clip-path:inherit; pointer-events:none; }
.seg-1 { background:#6d8de6; clip-path:polygon(0 0, 100% 0, 88% 100%, 12% 100%); }
.seg-2 { background:#4c5f88; clip-path:polygon(12% 0, 88% 0, 79% 100%, 21% 100%); }
.seg-3 { background:#67b9ae; clip-path:polygon(21% 0, 79% 0, 72% 100%, 28% 100%); }
.seg-4 { background:#2a8e9a; clip-path:polygon(28% 0, 72% 0, 67% 100%, 33% 100%); }
.seg-outlet { background:#f0a94b; clip-path:polygon(33% 0, 67% 0, 67% 100%, 33% 100%); color:#3a2606; text-shadow:none; }
.seg-outlet b { font-size:13px; }
.seg-outlet em { font-size:13px; }
.seg-1 small, .seg-2 small, .seg-3 small, .seg-4 small { letter-spacing:.5px; }
.funnel-actions { display:flex; align-items:center; justify-content:center; gap:12px; padding:0 16px; border-top:1px solid #303c51; background:#141e30; }
.run-button { display:flex; height:36px; align-items:center; gap:8px; padding:0 18px; border:1px solid #d8af49; border-radius:4px; background:#ddb34d; color:#221805; cursor:pointer; font-weight:700; box-shadow:none; }
.run-button:hover:not(:disabled) { background:#eac65e; }
.run-button:disabled { cursor:not-allowed; opacity:.4; }
.config-state { display:flex; min-width:0; align-items:center; gap:6px; overflow:hidden; color:#8290a5; font-size:10px; text-overflow:ellipsis; white-space:nowrap; }
.config-state i { width:6px; height:6px; flex:0 0 auto; border-radius:50%; background:#18a976; }
.config-state.warning i { background:#d8a738; }
.spin { width:12px; height:12px; border:2px solid rgba(34,24,5,.35); border-top-color:#221805; border-radius:50%; animation:hotspot-spin .7s linear infinite; }
@keyframes hotspot-spin { to { transform:rotate(360deg); } }
.funnel-actions>span { color:#8290a5; font-size:10px; }

/* ---- 右侧结果 ---- */
.stage-results { display:grid; min-width:0; min-height:0; grid-template-rows:66px minmax(0,1fr); background:#eef1f5; color:#17202d; }
.results-header { display:flex; min-width:0; align-items:center; justify-content:space-between; padding:0 24px; border-bottom:1px solid #d7dde5; background:#f9fafb; }
.results-header div { display:flex; align-items:baseline; gap:12px; }
.results-header span { color:#8b96a5; font-size:10px; letter-spacing:0; }
.results-header strong { font-size:17px; }
.results-header p { display:flex; align-items:center; gap:7px; margin:0; color:#748195; font-size:11px; }
.results-header p i { width:6px; height:6px; border-radius:50%; background:#18a976; }
.results-header p i.running { animation:status-pulse 1.2s ease-in-out infinite; background:#d6a63d; }
@keyframes status-pulse { 50% { box-shadow:0 0 0 5px rgba(214,166,61,.15); } }
.result-scroll { min-height:0; overflow:auto; padding:16px 20px 22px; }
.state-message { display:grid; place-items:center; color:#687586; }
.state-message.error { color:#b6353e; }
.up { color:#d03a44 !important; }
.down { color:#0a8a63 !important; }

/* L1 初筛列表 */
.screen-list { display:flex; flex-direction:column; gap:8px; }
.screen-row { display:grid; min-height:60px; grid-template-columns:40px minmax(120px,1.3fr) repeat(4,minmax(64px,1fr)); align-items:center; gap:12px; padding:0 16px; border:1px solid #e3e7ec; border-radius:10px; background:#fff; color:#1b2533; cursor:pointer; text-align:left; box-shadow:0 1px 2px rgba(23,32,45,.04); transition:border-color .14s, box-shadow .14s; }
.screen-row:hover { border-color:#d9b45c; box-shadow:0 4px 14px rgba(23,32,45,.08); }
.screen-row .rank { display:grid; width:28px; height:28px; place-items:center; border-radius:8px; background:#eef1f5; color:#7f8b9b; font-size:11px; font-weight:700; }
.screen-row span { display:flex; flex-direction:column; gap:3px; }
.screen-row small { color:#8a95a4; font-size:10px; }
.screen-row b { font-size:13px; font-variant-numeric:tabular-nums; }
.screen-row .primary b { font-size:15px; }
.heat-bar { position:relative; height:4px; margin-top:3px; overflow:hidden; border-radius:2px; background:#e8ebef; }
.heat-bar i { position:absolute; inset:0; width:var(--heat,50%); border-radius:2px; background:linear-gradient(90deg,#e4b85a,#d03a44); }

/* L2 关系列表 */
.relation-list { display:flex; flex-direction:column; gap:8px; }
.relation-row { display:grid; min-height:62px; grid-template-columns:minmax(96px,1fr) minmax(150px,1.7fr) minmax(96px,1fr) 96px; align-items:center; gap:8px; padding:0 16px; border:1px solid #e3e7ec; border-radius:10px; background:#fff; }
.relation-row>button, .chain-row>button { overflow:hidden; border:0; border-radius:0; background:transparent; color:#1c4f7c; cursor:pointer; font-size:13px; font-weight:700; text-overflow:ellipsis; white-space:nowrap; }
.relation-row>button:hover, .chain-row>button:hover { text-decoration:underline; }
.relation-line { display:flex; align-items:center; gap:8px; color:#687789; }
.relation-line i { position:relative; height:2px; flex:1; border-radius:1px; background:linear-gradient(90deg,#c4cedb,#8ba0b8); }
.relation-line i:last-child { background:linear-gradient(90deg,#8ba0b8,#c4cedb); }
.relation-line i:last-child::after { content:''; position:absolute; top:50%; right:0; border:4px solid transparent; border-left:6px solid #8ba0b8; transform:translateY(-50%); }
.relation-line em { padding:3px 8px; border-radius:99px; background:#eef2f6; font-size:11px; font-style:normal; font-weight:700; color:#4c6076; }
.relation-row>small { color:#768394; font-size:11px; text-align:right; }

/* L3 AI 主线 */
.ai-list { display:flex; flex-direction:column; gap:14px; }
.mainline-block { overflow:hidden; border:1px solid #e3e7ec; border-radius:12px; background:#fff; box-shadow:0 1px 3px rgba(23,32,45,.05); }
.mainline-block>header { display:flex; align-items:center; justify-content:space-between; padding:14px 18px 6px; }
.mainline-block>header strong { position:relative; padding-left:12px; font-size:16px; }
.mainline-block>header strong::before { content:''; position:absolute; top:50%; left:0; width:4px; height:16px; border-radius:2px; background:linear-gradient(180deg,#e0b64f,#c98f2b); transform:translateY(-50%); }
.mainline-block>header span { color:#7b8796; font-size:11px; }
.mainline-block>p { margin:0; padding:0 18px 13px 30px; color:#536173; font-size:12px; line-height:1.65; }
.chain-map { border-top:1px dashed #e0e4e9; padding:6px 0; }
.chain-row { display:grid; grid-template-columns:minmax(88px,1fr) minmax(170px,2fr) minmax(88px,1fr); align-items:center; gap:12px; min-height:60px; padding:6px 18px; }
.chain-row+.chain-row { border-top:1px solid #f0f2f5; }
.chain-row>span { display:flex; min-width:0; flex-direction:column; align-items:center; gap:5px; color:#637184; text-align:center; }
.chain-row>span b { position:relative; z-index:1; padding:3px 10px; border:1px solid #e2cf9a; border-radius:99px; background:#faf5e7; color:#8f6a1c; font-size:11px; }
.chain-row>span small { font-size:10px; line-height:1.45; color:#7d8a99; }
.chain-row>span::before { content:''; width:78%; height:0; margin-bottom:-11px; border-top:2px dashed #cbb26d; }

/* L4 有效概念 */
.final-list { display:flex; flex-direction:column; gap:16px; }
.concept-result { flex:0 0 auto; overflow:hidden; border:1px solid #e3e7ec; border-radius:12px; background:#fff; box-shadow:0 2px 6px rgba(23,32,45,.06); }
.concept-result>header { display:flex; min-height:54px; align-items:center; gap:10px; padding:0 18px; border-bottom:1px solid #eceff3; }
.concept-result>header>button { border:0; background:transparent; color:#153f63; cursor:pointer; font-size:18px; font-weight:700; }
.concept-result>header>button:hover { color:#0d5c94; text-decoration:underline; }
.concept-result>header>span { padding:3px 9px; border-radius:99px; font-size:11px; font-weight:700; }
.concept-result>header>span.hot { background:#fbe9ea; color:#bd3039; }
.concept-result>header>span.latent { background:#faf1dc; color:#8a6414; }
.concept-result>header>span.risk { background:#eceff2; color:#5d6a7a; }
.concept-result>header>b { display:flex; margin-left:auto; align-items:baseline; gap:5px; font-size:22px; color:#1f2c3d; font-variant-numeric:tabular-nums; }
.concept-result>header>b small { color:#8a95a3; font-size:10px; font-weight:400; }
.concept-result>p { margin:0; padding:12px 18px; color:#43536a; font-size:12.5px; line-height:1.7; }
.evidence-strip { display:grid; grid-template-columns:repeat(5,1fr); border-top:1px solid #eceff3; border-bottom:1px solid #eceff3; background:#f8f9fb; }
.evidence-strip span { display:grid; min-width:0; min-height:68px; grid-template-rows:auto minmax(20px,auto); align-content:center; justify-items:center; row-gap:5px; padding:8px 6px; }
.evidence-strip span+span { border-left:1px solid #e8ebef; }
.evidence-strip small { display:block; color:#8a95a4; font-size:10px; line-height:1.3; white-space:nowrap; }
.evidence-strip b { display:block; max-width:100%; font-size:14px; font-variant-numeric:tabular-nums; line-height:1.4; overflow:visible; white-space:nowrap; }
.stock-strip { display:flex; min-width:0; gap:8px; padding:12px 18px; overflow:auto; }
.stock-strip button { display:flex; min-width:112px; height:36px; flex:0 0 auto; align-items:center; justify-content:space-between; gap:8px; padding:0 11px; border:1px solid #dde2e9; border-radius:8px; background:#fbfcfd; cursor:pointer; transition:border-color .14s, background .14s; }
.stock-strip button:hover { border-color:#d9b45c; background:#fffdf5; }
.stock-strip button b { font-size:12px; }
.stock-strip button span { font-size:12px; font-weight:700; font-variant-numeric:tabular-nums; }
.concept-result>footer { display:flex; min-height:40px; align-items:center; gap:8px; padding:6px 18px; border-top:1px solid #eceff3; background:#fcfcfd; color:#687586; font-size:11px; }
.concept-result>footer b { flex:0 0 auto; color:#a03b43; }
.concept-result>footer span { flex:1; line-height:1.5; }
.concept-result>footer button { flex:0 0 auto; border:0; background:transparent; color:#1c4f7c; cursor:pointer; font-weight:700; }
.concept-result>footer button:hover { text-decoration:underline; }

@media (max-width:980px) { .funnel-workspace { grid-template-columns:300px minmax(0,1fr); }.funnel-seg b { font-size:12px; }.funnel-seg small { display:none; }.evidence-strip { grid-template-columns:repeat(3,1fr); }.screen-row { grid-template-columns:34px minmax(100px,1fr) repeat(2,68px); }.screen-row>span:nth-last-child(-n+2) { display:none; } }
@media (max-width:720px) { .funnel-workspace { display:flex; height:auto; min-height:100%; flex-direction:column; }.funnel-visual { min-height:480px; border-right:0; }.funnel-stack { padding:16px 20px 12px; }.funnel-chart { max-height:360px; }.stage-results { min-height:620px; }.relation-row { grid-template-columns:1fr 1fr; }.relation-line,.relation-row>small { display:none; } }
</style>
