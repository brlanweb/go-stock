<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { api, fmt, fmtPct, pctClass, type GlobalRiskGate, type RiskGateOverview } from '../api'

// 风险感知板块：境内风向门（T-1 收盘结构，滞后确认）+ 全球风险门（隔夜外盘，
// 提前预警）+ 融合档位（取最严）。目标是在千股跌停类普跌日开盘前给出红灯。

const overview = ref<RiskGateOverview | null>(null)
const history = ref<GlobalRiskGate[]>([])
const error = ref('')
const running = ref(false)
let timer: number | undefined

const levelMeta: Record<string, { label: string; desc: string }> = {
  green: { label: '绿灯', desc: '正常推荐并自动建仓' },
  yellow: { label: '黄灯', desc: '仅生成推荐观察，不自动建仓，风险上限收紧' },
  red: { label: '红灯', desc: '跳过当日推荐与建仓，持仓进入防御模式' }
}

const factorHints: Record<string, string> = {
  a50: 'A股开盘预期最直接的定价品种，权重最高',
  china_adr: '中概股隔夜情绪传导',
  us_equity: '全球风险偏好（标普500 与纳斯达克均值）',
  vix: '恐慌水平（绝对值）与单日跳升双口径',
  fx: 'USDCNH 上涨 = 人民币贬值 = 资金外流压力'
}

const finalLevel = computed(() => overview.value?.final_level || 'yellow')
const globalGate = computed(() => overview.value?.global_gate || null)
const marketGate = computed(() => overview.value?.market_gate || null)

function levelClass(level?: string | null) {
  return level === 'red' ? 'lv-red' : level === 'green' ? 'lv-green' : 'lv-yellow'
}

function scoreText(score: number) {
  return score === 0 ? '0' : `${score}`
}

async function refresh() {
  try {
    const [gate, his] = await Promise.all([api.riskGate(), api.riskGateHistory(30)])
    overview.value = gate
    history.value = his.items || []
    error.value = ''
  } catch (e: any) {
    error.value = e?.message || '风险感知数据加载失败'
  }
  window.clearTimeout(timer)
  timer = window.setTimeout(refresh, 300000)
}

async function runNow() {
  if (running.value) return
  running.value = true
  try {
    await api.runRiskGate()
    await refresh()
  } catch (e: any) {
    error.value = e?.message || '触发风险感知失败'
  } finally {
    running.value = false
  }
}

onMounted(refresh)
onUnmounted(() => window.clearTimeout(timer))
</script>

<template>
  <section class="risk-sentinel">
    <div v-if="error" class="risk-error">{{ error }}</div>

    <!-- 三灯总览 -->
    <div class="gate-cards">
      <article class="gate-card final" :class="levelClass(finalLevel)">
        <header>综合风险档位</header>
        <div class="light">{{ levelMeta[finalLevel]?.label || finalLevel }}</div>
        <p>{{ levelMeta[finalLevel]?.desc }}</p>
      </article>
      <article class="gate-card" :class="levelClass(globalGate?.level)">
        <header>全球风险门 <small>隔夜外盘 · 提前预警</small></header>
        <div class="light">
          {{ globalGate ? levelMeta[globalGate.level]?.label : '无数据' }}
          <em v-if="globalGate">评分 {{ scoreText(globalGate.score) }}</em>
        </div>
        <p>{{ globalGate?.reason || '今日尚未采集，可点击「立即感知」' }}</p>
        <footer v-if="globalGate">{{ globalGate.trade_date }}<span v-if="globalGate.created_at"> · {{ globalGate.created_at }}</span></footer>
      </article>
      <article class="gate-card" :class="levelClass(marketGate?.level)">
        <header>境内风向门 <small>T-1 收盘结构 · 滞后确认</small></header>
        <div class="light">{{ marketGate ? levelMeta[marketGate.level]?.label : '无数据' }}</div>
        <p>{{ marketGate?.reason || '指数风向数据不可用' }}</p>
        <footer v-if="marketGate">分析基准 {{ marketGate.trade_date }}</footer>
      </article>
      <button class="run-btn" type="button" :disabled="running" @click="runNow">{{ running ? '感知中…' : '立即感知' }}</button>
    </div>

    <!-- 隔夜外盘信号明细 -->
    <div class="panel">
      <header>隔夜外盘信号<small>确定性打分：0 正常 / -1 走弱 / -2 恶化 / -3 暴跌（仅A50）；总分 ≤ -2 黄灯、≤ -4 红灯，核心数据缺失按黄灯保守降档</small></header>
      <table v-if="globalGate?.signals?.length">
        <thead>
          <tr><th>因子</th><th>最新值</th><th>涨跌幅</th><th>得分</th><th>判定</th></tr>
        </thead>
        <tbody>
          <tr v-for="signal in globalGate.signals" :key="signal.factor" :class="{ dim: !signal.has_data }">
            <td>
              <b>{{ signal.name }}</b>
              <small>{{ factorHints[signal.factor] }}</small>
            </td>
            <td>{{ signal.has_data && signal.price > 0 ? fmt(signal.price) : '-' }}</td>
            <td :class="pctClass(signal.has_data ? signal.change_pct : null)">{{ signal.has_data ? fmtPct(signal.change_pct) : '缺失' }}</td>
            <td><i class="score" :class="{ bad: signal.score <= -2, warn: signal.score === -1 }">{{ scoreText(signal.score) }}</i></td>
            <td class="note">{{ signal.note || (signal.has_data ? '正常' : '数据缺失，未参与计分') }}</td>
          </tr>
        </tbody>
      </table>
      <p v-else class="empty">今日尚无外盘信号，交易日 08:05 自动采集，或点击「立即感知」。</p>
    </div>

    <!-- 境内指数结构 -->
    <div class="panel" v-if="marketGate?.indices?.length">
      <header>境内指数结构<small>收盘价与 MA20 相对位置 + 5 日动量 + 市场宽度</small></header>
      <table>
        <thead>
          <tr><th>指数</th><th>收盘</th><th>MA20</th><th>位置</th><th>5日动量</th></tr>
        </thead>
        <tbody>
          <tr v-for="index in marketGate.indices" :key="index.symbol">
            <td><b>{{ index.name }}</b></td>
            <td>{{ fmt(index.close) }}</td>
            <td>{{ index.has_ma20 ? fmt(index.ma20) : '-' }}</td>
            <td>
              <i v-if="index.has_ma20" class="score" :class="{ bad: index.close < index.ma20 }">{{ index.close < index.ma20 ? '破位' : '上方' }}</i>
              <span v-else>-</span>
            </td>
            <td :class="pctClass(index.has_momentum ? index.momentum_5d_pct : null)">{{ index.has_momentum ? fmtPct(index.momentum_5d_pct) : '-' }}</td>
          </tr>
        </tbody>
      </table>
      <p v-if="marketGate.breadth?.valid" class="breadth">全市场 {{ marketGate.breadth.stock_count }} 只：上涨占比 {{ (marketGate.breadth.up_ratio * 100).toFixed(0) }}%，平均涨跌 {{ fmtPct(marketGate.breadth.avg_change_pct) }}</p>
    </div>

    <!-- 判定历史 -->
    <div class="panel">
      <header>全球风险门历史<small>用于复盘预警命中率：红灯日 vs 实际普跌日</small></header>
      <ul v-if="history.length" class="history">
        <li v-for="item in history" :key="item.trade_date">
          <span class="dot" :class="levelClass(item.level)"></span>
          <b>{{ item.trade_date }}</b>
          <i class="score" :class="{ bad: item.score <= -4, warn: item.score <= -2 && item.score > -4 }">{{ scoreText(item.score) }}</i>
          <span class="reason">{{ item.reason }}</span>
        </li>
      </ul>
      <p v-else class="empty">暂无历史判定记录。</p>
    </div>
  </section>
</template>

<style scoped>
.risk-sentinel { display:flex; flex-direction:column; gap:10px; overflow-y:auto; padding:10px 12px; }
.risk-error { padding:8px 11px; border-left:3px solid #db4b57; background:#3a2329; color:#ffb1b8; font-size:12px; }
.gate-cards { position:relative; display:grid; grid-template-columns:repeat(3,minmax(0,1fr)); gap:10px; }
.gate-card { display:flex; flex-direction:column; gap:6px; padding:12px 14px; border:1px solid #354157; border-top:3px solid #8b96a8; background:#182234; }
.gate-card header { display:flex; align-items:baseline; gap:8px; color:#94a1b5; font-size:12px; font-weight:700; }
.gate-card header small { color:#5f6d84; font-size:10px; font-weight:400; }
.gate-card .light { display:flex; align-items:baseline; gap:10px; font-size:22px; font-weight:800; }
.gate-card .light em { color:#8794a8; font-size:12px; font-style:normal; font-weight:400; }
.gate-card p { margin:0; color:#aeb8c9; font-size:12px; line-height:1.5; }
.gate-card footer { color:#5f6d84; font-size:10px; }
.gate-card.lv-green { border-top-color:#00a56f; } .gate-card.lv-green .light { color:#2fce9a; }
.gate-card.lv-yellow { border-top-color:#e0b64f; } .gate-card.lv-yellow .light { color:#f0c760; }
.gate-card.lv-red { border-top-color:#db4b57; } .gate-card.lv-red .light { color:#ff8a94; }
.gate-card.final { background:#1b2840; }
.run-btn { position:absolute; top:-2px; right:0; transform:translateY(-100%); padding:4px 12px; border:1px solid #43516a; background:#202d41; color:#d5dce7; cursor:pointer; font-size:12px; }
.run-btn:hover { border-color:#e0b64f; color:#f0c760; }
.run-btn:disabled { cursor:wait; opacity:.6; }
.panel { border:1px solid #354157; background:#182234; }
.panel > header { display:flex; align-items:baseline; gap:10px; padding:9px 12px; border-bottom:1px solid #26324a; color:#d5dce7; font-size:13px; font-weight:700; }
.panel > header small { color:#5f6d84; font-size:10px; font-weight:400; }
table { width:100%; border-collapse:collapse; font-size:12px; }
th { padding:7px 12px; color:#6c7a93; font-size:11px; font-weight:400; text-align:left; }
td { padding:7px 12px; border-top:1px solid #202c42; color:#c6cedb; vertical-align:top; }
td b { display:block; color:#edf1f7; font-size:12px; }
td small { display:block; margin-top:2px; color:#5f6d84; font-size:10px; }
td.note { max-width:340px; color:#aeb8c9; line-height:1.45; }
tr.dim td { opacity:.45; }
.score { display:inline-block; min-width:26px; padding:1px 6px; background:#22304a; color:#9fb0c8; font-size:11px; font-style:normal; text-align:center; }
.score.warn { background:#3d3517; color:#f0c760; }
.score.bad { background:#42232a; color:#ff8a94; }
.up { color:#ff7d8a; } .down { color:#2fce9a; } .dim { color:#6c7a93; }
.breadth { margin:0; padding:8px 12px; border-top:1px solid #202c42; color:#8e9cb0; font-size:11px; }
.empty { margin:0; padding:14px 12px; color:#6c7a93; font-size:12px; }
.history { margin:0; padding:4px 0; list-style:none; }
.history li { display:flex; align-items:baseline; gap:10px; padding:6px 12px; border-top:1px solid #202c42; font-size:12px; }
.history li:first-child { border-top:0; }
.history .dot { align-self:center; width:9px; height:9px; border-radius:50%; flex:0 0 auto; }
.history .dot.lv-green { background:#00a56f; } .history .dot.lv-yellow { background:#e0b64f; } .history .dot.lv-red { background:#db4b57; }
.history b { color:#edf1f7; font-weight:600; white-space:nowrap; }
.history .reason { overflow:hidden; color:#8e9cb0; text-overflow:ellipsis; white-space:nowrap; }
@media (max-width:900px) { .gate-cards { grid-template-columns:1fr; } .run-btn { position:static; transform:none; align-self:flex-end; } .history .reason { white-space:normal; } }
</style>
