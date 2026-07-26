<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { type StockDetailPayload, type Kline } from '../api'

const props = defineProps<{ detail: StockDetailPayload | null }>()
const open = ref(true)
const messages = ref<{ role: 'user' | 'assistant'; text: string }[]>([])
const input = ref('')
const sending = ref(false)
const error = ref('')
const chatHost = ref<HTMLElement | null>(null)
let controller: AbortController | null = null

onMounted(() => { open.value = true })
onBeforeUnmount(() => controller?.abort())

function fmtClose(k: Kline) { return k.close.toFixed(2) }
function pctChange(k: Kline) { return !k.change_pct ? '' : `${k.change_pct > 0 ? '+' : ''}${k.change_pct.toFixed(2)}%` }
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
  const recent = (d.klines_60 || []).slice(-30)
  return [
    '你正在分析一只 A 股个股，请结合下面提供的本地数据库字段回答用户问题。',
    `用户问题：${userText}`, '', '--- 个股基础信息 ---',
    `证券代码：${d.symbol}（${d.code}）`, `名称：${d.name}`, `所属行业：${d.industry}`,
    `所属概念：${d.concepts.map(c => c.sector_name).join('、') || '无'}`, `上市日期：${d.list_date || '未知'}`, '',
    '--- 最新快照（来自本地 MySQL，非实时） ---',
    q ? `价格=${q.price?.toFixed(2)} 涨跌幅=${q.change_pct?.toFixed(2)}% 成交额=${fmtMV(q.amount)} 成交量=${fmtMV(q.volume)} PE=${q.pe_ratio?.toFixed(2)} 总市值=${fmtMV(q.total_mv)} 流通市值=${fmtMV(q.circ_mv)}` : '（尚无快照）', '',
    `--- 最近 ${recent.length} 个交易日日 K（前复权） ---`,
    recent.map(item => `${item.date} 开=${item.open.toFixed(2)} 收=${fmtClose(item)} ${pctChange(item)} 额=${fmtMV(item.amount)}`).join('\n')
  ].join('\n')
}

async function scrollBottom() {
  await nextTick()
  if (chatHost.value) chatHost.value.scrollTop = chatHost.value.scrollHeight
}

function consumeEventBlock(block: string, assistantIndex: number) {
  let event = 'message'
  const data: string[] = []
  for (const line of block.split('\n')) {
    if (line.startsWith('event:')) event = line.slice(6).trim()
    if (line.startsWith('data:')) data.push(line.slice(5).trim())
  }
  if (!data.length) return false
  const payload = JSON.parse(data.join('\n'))
  if (event === 'delta') messages.value[assistantIndex].text += payload.text || ''
  if (event === 'error') throw new Error(payload.error || '模型流式请求失败')
  return event === 'done'
}

async function send() {
  if (!input.value.trim() || sending.value) return
  const userText = input.value.trim()
  input.value = ''
  messages.value.push({ role: 'user', text: userText }, { role: 'assistant', text: '' })
  const assistantIndex = messages.value.length - 1
  sending.value = true
  error.value = ''
  controller = new AbortController()
  await scrollBottom()
  try {
    const resp = await fetch('/api/v1/agent/chat/stream', {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, signal: controller.signal,
      body: JSON.stringify({ symbol: props.detail?.symbol, question: userText, context: buildPrompt(userText) })
    })
    if (!resp.ok || !resp.body) {
      const data = await resp.json().catch(() => ({ error: `HTTP ${resp.status}` }))
      throw new Error(data.error || `HTTP ${resp.status}`)
    }
    const reader = resp.body.getReader()
    const decoder = new TextDecoder()
    let buffer = ''
    let done = false
    while (!done) {
      const chunk = await reader.read()
      if (chunk.done) break
      buffer += decoder.decode(chunk.value, { stream: true }).replace(/\r\n/g, '\n')
      let boundary = buffer.indexOf('\n\n')
      while (boundary >= 0) {
        const block = buffer.slice(0, boundary)
        buffer = buffer.slice(boundary + 2)
        done = consumeEventBlock(block, assistantIndex) || done
        await scrollBottom()
        boundary = buffer.indexOf('\n\n')
      }
    }
    if (!messages.value[assistantIndex].text) messages.value[assistantIndex].text = '（无回复）'
  } catch (e: any) {
    if (e?.name !== 'AbortError') {
      error.value = e?.message || '请求失败'
      messages.value[assistantIndex].text = `【错误】${error.value}`
    }
  } finally {
    controller = null
    sending.value = false
    await scrollBottom()
  }
}

function onInputKeydown(event: KeyboardEvent) {
  if (event.key === 'Enter' && !event.shiftKey) {
    event.preventDefault()
    send()
  }
}
function stop() { controller?.abort() }
function toggle() { open.value = !open.value }
defineExpose({ open, toggle })
</script>

<template>
  <aside v-if="open && detail" class="agent-panel">
    <header><div><strong>AI 行情助理</strong><span>{{ detail.name }} · {{ detail.code }}</span></div><button class="icon-button" title="关闭" @click="open=false">×</button></header>
    <p class="hint">上下文包含本地快照、60 日 K 线、行业和概念。</p>
    <div ref="chatHost" class="chat">
      <div v-for="(m, i) in messages" :key="i" :class="['msg', m.role]">{{ m.text }}<i v-if="sending && i === messages.length - 1" class="cursor"></i></div>
      <div v-if="!messages.length" class="empty">可询问近期走势、量价结构、行业和概念关联。</div>
    </div>
    <div class="composer">
      <textarea v-model="input" rows="3" placeholder="输入问题，Enter 发送，Shift+Enter 换行" :disabled="sending" @keydown="onInputKeydown"></textarea>
      <button v-if="sending" class="stop" title="停止生成" @click="stop">停止</button>
      <button v-else class="send" title="发送" :disabled="!input.trim()" @click="send">发送</button>
    </div>
  </aside>
</template>

<style scoped>
.agent-panel { position:fixed; z-index:60; top:0; right:0; bottom:0; display:grid; width:min(420px,92vw); grid-template-rows:auto auto minmax(0,1fr) auto; padding:14px; border-left:1px solid #354157; background:#101a2b; color:#e7ecf4; box-shadow:-18px 0 44px rgba(0,0,0,.48); }
.agent-panel header { display:flex; align-items:center; justify-content:space-between; gap:12px; padding-bottom:10px; border-bottom:1px solid #2b3850; }.agent-panel header div { display:grid; gap:3px; }.agent-panel strong { font-size:14px; }.agent-panel header span { color:#8f9caf; font-size:11px; }.icon-button { width:30px; height:30px; border:1px solid #3a496a; background:#233150; color:#e7ecf4; font-size:20px; cursor:pointer; }.hint { margin:10px 0 5px; color:#8895ab; font-size:11px; }
.chat { display:flex; min-height:0; flex-direction:column; gap:8px; overflow-y:auto; padding:8px 2px 14px; }.msg { padding:9px 11px; border-radius:6px; font-size:13px; line-height:1.55; white-space:pre-wrap; word-break:break-word; }.msg.user { align-self:flex-end; max-width:82%; background:#2b3954; }.msg.assistant { align-self:flex-start; max-width:94%; border:1px solid #264563; background:#10253b; color:#d8e8f9; }.empty { padding:20px 8px; color:#728096; font-size:12px; line-height:1.6; }.cursor { display:inline-block; width:6px; height:14px; margin-left:2px; background:#e9c16c; vertical-align:middle; animation:blink .8s infinite; }
.composer { display:grid; grid-template-columns:minmax(0,1fr) 58px; gap:8px; padding-top:10px; border-top:1px solid #2b3850; }.composer textarea { min-width:0; resize:none; padding:9px 10px; border:1px solid #3a496a; outline:none; background:#0d1525; color:#e7ecf4; font-size:13px; line-height:1.45; }.composer textarea:focus { border-color:#e9c16c; }.composer button { border:0; font-weight:700; cursor:pointer; }.send { background:#e9c16c; color:#1a1206; }.send:disabled { opacity:.45; cursor:default; }.stop { border:1px solid #93444a!important; background:#43272d; color:#ffbdc2; }
@keyframes blink { 50% { opacity:0; } }
@media (max-width:600px) { .agent-panel { top:auto; width:100vw; height:78vh; border-top:1px solid #354157; border-left:0; } }
</style>
