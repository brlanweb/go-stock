<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { api, type RiskGateOverview, type TradeAccount } from '../api'

const router = useRouter()
const account = ref<TradeAccount | null>(null)
const riskGate = ref<RiskGateOverview | null>(null)
const accountLoading = ref(true)
const accountError = ref('')

const sentimentScore = computed(() => riskGate.value?.market_sentiment.score ?? 50)
const sentimentLabel = computed(() => riskGate.value?.market_sentiment.label ?? '数据不足')
const sentimentClass = computed(() => {
  const score = sentimentScore.value
  if (score <= 24) return 'extreme-fear'
  if (score <= 44) return 'fear'
  if (score <= 55) return 'neutral'
  if (score <= 74) return 'greed'
  return 'extreme-greed'
})

function money(value: number | null | undefined) {
  if (value == null) return '—'
  return new Intl.NumberFormat('zh-CN', {
    style: 'currency',
    currency: 'CNY',
    signDisplay: 'exceptZero',
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  }).format(value)
}

function pnlClass(value: number | null | undefined) {
  if (value == null || value === 0) return 'flat'
  return value > 0 ? 'profit' : 'loss'
}

async function loadRisk() {
  try {
    riskGate.value = await api.riskGate()
  } catch {
    riskGate.value = null
  }
}

async function load() {
  accountLoading.value = true
  accountError.value = ''
  void loadRisk()
  try {
    account.value = await api.tradeAccount()
  } catch (error: any) {
    account.value = null
    accountError.value = error?.message || '账户数据不可用'
  } finally {
    accountLoading.value = false
  }
}

function openTrades() {
  router.push({ path: '/', query: { view: 'reco' } })
}

function openRisk() {
  router.push({ path: '/', query: { view: 'risk' } })
}

onMounted(load)
</script>

<template>
  <main class="home-core">
    <header class="core-header">
      <div>
        <strong>账户核心概览</strong>
        <small>人民币金额</small>
      </div>
      <button type="button" class="refresh" :disabled="accountLoading" title="刷新" aria-label="刷新账户概览" @click="load">↻</button>
    </header>

    <div v-if="accountLoading" class="state">正在更新账户盈亏…</div>
    <div v-else-if="accountError" class="state error">{{ accountError }}</div>
    <template v-else>
      <section class="pnl-grid" aria-label="账户盈亏">
        <article class="pnl-card total">
          <span>当前总盈亏</span>
          <strong :class="pnlClass(account?.total_pnl)">{{ money(account?.total_pnl) }}</strong>
        </article>
        <article class="pnl-card today">
          <span>今日盈亏</span>
          <strong :class="pnlClass(account?.today_pnl)">{{ money(account?.today_pnl) }}</strong>
        </article>
      </section>

      <div class="detail-row">
        <button type="button" @click="openTrades">交易明细</button>
      </div>
    </template>

    <button type="button" class="sentiment" :class="sentimentClass" title="查看市场风险情绪明细" @click="openRisk">
      <span class="sentiment-title">市场风险情绪</span>
      <strong><b>{{ sentimentScore }}</b><em>{{ sentimentLabel }}</em></strong>
      <span class="sentiment-scale" aria-hidden="true">
        <i class="extreme-fear" /><i class="fear" /><i class="neutral" /><i class="greed" /><i class="extreme-greed" />
        <u :style="{ left: `${sentimentScore}%` }" />
      </span>
      <small><i>0 恐惧</i><i>100 贪婪</i></small>
    </button>
  </main>
</template>

<style scoped>
.home-core{min-width:0;min-height:100%;overflow:auto;padding:18px;background:#0f1826;color:#e7ecf4;letter-spacing:0}
.core-header{display:flex;align-items:center;justify-content:space-between;margin-bottom:14px}
.core-header>div{display:flex;align-items:baseline;gap:10px}
.core-header strong{font-size:17px}
.core-header small{color:#8895ab;font-size:11px}
.refresh{display:grid;width:32px;height:32px;place-items:center;border:1px solid #3a496a;border-radius:3px;background:#18243a;color:#c4cddc;cursor:pointer;font-size:19px;line-height:1}
.refresh:hover{background:#21304b}.refresh:disabled{cursor:wait;opacity:.55}
.state{display:grid;min-height:220px;place-items:center;border:1px solid #26324a;background:#131e33;color:#8895ab;font-size:13px}
.state.error{color:#ef7d84}
.pnl-grid{display:grid;grid-template-columns:minmax(0,1.45fr) minmax(0,1fr);gap:12px}
.pnl-card{display:flex;min-width:0;min-height:190px;flex-direction:column;justify-content:center;gap:14px;padding:24px;border:1px solid #2d3b54;border-radius:6px;background:#131e33}
.pnl-card.total{border-top:4px solid #67a9d8}
.pnl-card.today{border-top:4px solid #e9c16c}
.pnl-card span{color:#98a5b9;font-size:13px}
.pnl-card strong{max-width:100%;font-size:clamp(30px,4.5vw,58px);font-variant-numeric:tabular-nums;line-height:1.05;overflow-wrap:anywhere}
.profit{color:#ef6a72}.loss{color:#55b996}.flat{color:#d6dce6}
.detail-row{display:flex;justify-content:flex-end;padding:10px 0 14px}
.detail-row button{padding:7px 13px;border:1px solid #3d5680;border-radius:3px;background:#16233b;color:#9abde8;cursor:pointer;font-size:12px}
.detail-row button:hover{background:#1d3050;color:#d3e4f8}
.sentiment{display:grid;width:100%;min-height:132px;grid-template-columns:1fr auto;gap:8px 20px;align-items:center;padding:18px 22px;border:1px solid #2d3b54;border-radius:6px;background:#131e33;color:#e7ecf4;cursor:pointer;text-align:left}
.sentiment:hover{background:#182338}
.sentiment-title{color:#98a5b9;font-size:13px}
.sentiment>strong{display:flex;grid-column:2;grid-row:1/3;align-items:baseline;gap:10px;font-variant-numeric:tabular-nums}
.sentiment>strong b{font-size:48px;line-height:1}
.sentiment>strong em{font-size:18px;font-style:normal}
.sentiment-scale{position:relative;display:grid;height:12px;grid-template-columns:25fr 20fr 11fr 19fr 25fr;gap:2px}
.sentiment-scale>i{display:block}.sentiment-scale>.extreme-fear{background:#28755d}.sentiment-scale>.fear{background:#55b996}.sentiment-scale>.neutral{background:#e9c16c}.sentiment-scale>.greed{background:#df9462}.sentiment-scale>.extreme-greed{background:#ef6a72}
.sentiment-scale>u{position:absolute;top:-5px;width:3px;height:22px;transform:translateX(-2px);background:#f5f7fb;box-shadow:0 0 0 2px #131e33;text-decoration:none}
.sentiment>small{display:flex;justify-content:space-between;color:#75839a;font-size:10px}.sentiment>small i{font-style:normal}
.sentiment.extreme-fear>strong{color:#55b996}.sentiment.fear>strong{color:#78c8a9}.sentiment.neutral>strong{color:#e9c16c}.sentiment.greed>strong{color:#df9462}.sentiment.extreme-greed>strong{color:#ef6a72}
@media(max-width:720px){
  .home-core{padding:12px}
  .pnl-grid{grid-template-columns:1fr}
  .pnl-card{min-height:145px;padding:20px}
  .pnl-card strong{font-size:clamp(28px,10vw,44px)}
  .sentiment{min-height:126px;grid-template-columns:1fr auto;padding:16px}
  .sentiment>strong b{font-size:38px}
  .sentiment>strong em{font-size:15px}
}
</style>
