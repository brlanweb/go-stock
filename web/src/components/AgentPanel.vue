<script setup lang="ts">
import { ref } from 'vue'
import { api, type StockDetailPayload, type Kline } from '../api'

const props = defineProps<{ detail: StockDetailPayload | null }>()
const open = ref(false)
const messages = ref<{ role: 'user' | 'assistant'; text: string }[]>([])
const input = ref('')
const sending = ref(false)
const error = ref('')

function fmtClose(k: Kline) { return k.close.toFixed(2) }
function pctChange(k: Kline) {
  if (!k.change_pct) return ''
  return (k.change_pct > 0 ? '+' : '') + k.change_pct.toFixed(2) + '%'
}
function fmtMV(v: number | null | undefined) {
  if (v == null) return '-'
  if (Math.abs(v) >= 1e12) return (v / 1e12).toFixed(2) + '万亿'
  if (Math.abs(v) >= 1e8) return (v / 1e8).toFixed(2) + '亿'
  return (v / 1e4).toFixed(0) + '万'
}
function buildPrompt(userText: string): string {
  const d = props.detail
  if (!d) return userText
  const q = d.quote
  const k = d.klines_60 || []
  const recent = k.slice(-30)
  const lines = [
    `你正在分析一只 A 股个股，请结合下面提供的本地数据库字段回答用户问题。`,
    `用户问题：${userText}`,
    '',
    '--- 个股基础信息 ---',
    `证券代码：${d.symbol}（${d.code}）`,
    `名称：${d.name}`,
    `所属行业：${d.industry}`,
    `所属概念：${d.concepts.map(c => c.sector_name).join('、') || '无'}`,
    `上市日期：${d.list_date || '未知'}`,
    '',
    '--- 最新快照（来自本地 MySQL，非实时） ---',
    q ? `价格=${q.price?.toFixed(2)} 涨跌幅=${q.change_pct?.toFixed(2)}% 成交额=${fmtMV(q.amount)} 成交量=${fmtMV(q.volume)} PE=${q.pe_ratio?.toFixed(2)} 总市值=${fmtMV(q.total_mv)} 流通市值=${fmtMV(q.circ_mv)}` : '（尚无快照）',
    '',
    `--- 最近 ${recent.length} 个交易日日 K（前复权） ---`,
    recent.map(item => `${item.date} 开=${item.open.toFixed(2)} 收=${fmtClose(item)} ${pctChange(item)} 额=${fmtMV(item.amount)}`).join('\n')
  ]
  return lines.join('\n')
}

async function send() {
  if (!input.value.trim()) return
  const userText = input.value.trim()
  input.value = ''
  messages.value.push({ role: 'user', text: userText })
  sending.value = true
  error.value = ''
  try {
    // 走 MCP 服务（推荐分析后端），让 AI 直接分析本地数据。
    // 客户端不内置模型配置，仅构造请求并发送给后端 /api/v1/agent/chat。
    const resp = await fetch('/api/v1/agent/chat', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ symbol: props.detail?.symbol, question: userText, context: buildPrompt(userText) })
    })
    if (!resp.ok) {
      const data = await resp.json().catch(() => ({ error: '请求失败' }))
      throw new Error(data.error || `HTTP ${resp.status}`)
    }
    const data = await resp.json()
    messages.value.push({ role: 'assistant', text: data.reply || '（无回复）' })
  } catch (e: any) {
    error.value = e?.message || '请求失败'
    messages.value.push({ role: 'assistant', text: '【错误】' + error.value })
  } finally {
    sending.value = false
  }
}

function toggle() {
  open.value = !open.value
}

defineExpose({ open, toggle })
</script>

<template>
  <div class="agent-panel" v-if="open && detail">
    <header>
      <strong>AI 行情助理 · {{ detail.name }}</strong>
      <button class="ghost" @click="open=false">关闭</button>
    </header>
    <p class="hint">已携带该股的快照、60 日 K 线、行业和全部所属概念，作为对话上下文。</p>
    <div class="chat">
      <div v-for="(m, i) in messages" :key="i" :class="['msg', m.role]">{{ m.text }}</div>
      <div v-if="!messages.length" class="empty">输入你的问题，例如"分析最近一周的走势"或"该公司所属概念里今日涨跌幅最高是哪几个？"</div>
    </div>
    <form class="input-row" @submit.prevent="send">
      <textarea v-model="input" rows="2" placeholder="向 AI 行情助理提问…" :disabled="sending"></textarea>
      <button type="submit" :disabled="sending || !input.trim()">{{ sending ? '发送中…' : '发送' }}</button>
    </form>
  </div>
</template>

<style scoped>
.agent-panel { position:fixed; left:0; right:0; bottom:0; z-index:60; padding:14px 18px 16px; border-top:1px solid #26324a; background:#101a2b; color:#e7ecf4; box-shadow:0 -16px 40px rgba(0,0,0,.5); }
.agent-panel header { display:flex; align-items:center; justify-content:space-between; margin-bottom:6px; }
.agent-panel strong { font-size:14px; }
.agent-panel .ghost { padding:4px 10px; border:1px solid #3a496a; background:#233150; color:#e7ecf4; font-size:12px; cursor:pointer; }
.hint { margin:0 0 8px; color:#8895ab; font-size:12px; }
.chat { display:flex; flex-direction:column; gap:6px; max-height:38vh; min-height:120px; overflow-y:auto; padding:6px 2px; }
.msg { padding:8px 10px; border-radius:6px; font-size:13px; line-height:1.5; white-space:pre-wrap; word-break:break-word; }
.msg.user { align-self:flex-end; max-width:70%; background:#233150; color:#e7ecf4; }
.msg.assistant { align-self:flex-start; max-width:88%; background:#0d2138; border:1px solid #1d3a5c; color:#cfe3ff; }
.empty { padding:14px; color:#6f7c92; font-size:12px; }
.input-row { display:flex; gap:8px; margin-top:8px; }
.input-row textarea { flex:1; resize:none; padding:8px 10px; border:1px solid #3a496a; background:#0d1525; color:#e7ecf4; font-size:13px; outline:none; }
.input-row textarea:focus { border-color:#e9c16c; }
.input-row button { padding:0 16px; border:0; background:#e9c16c; color:#1a1206; font-size:13px; font-weight:700; cursor:pointer; }
.input-row button:disabled { opacity:.6; cursor:wait; }
</style>