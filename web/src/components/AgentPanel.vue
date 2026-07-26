<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { api, type StockDetailPayload } from '../api'

const props = defineProps<{ detail: StockDetailPayload | null }>()
const emit = defineEmits<{ close: [] }>()
const messages = ref<{ role: 'user' | 'assistant'; text: string }[]>([])
const input = ref('')
const sending = ref(false)
const error = ref('')
const chatHost = ref<HTMLElement | null>(null)
const includeStock = ref(true)
const historyDays = ref<0 | 10 | 30 | 60>(30)
const loadingHistory = ref(false)
let controller: AbortController | null = null

onMounted(loadHistory)
onBeforeUnmount(() => controller?.abort())
watch(() => props.detail?.symbol, () => loadHistory())

function close() {
  controller?.abort()
  emit('close')
}

async function loadHistory() {
  const symbol = props.detail?.symbol
  if (!symbol) return
  loadingHistory.value = true
  try {
    const history = await api.agentHistory(symbol)
    messages.value = history.map(item => ({ role: item.role, text: item.content }))
    await scrollBottom()
  } catch (e: any) {
    error.value = e?.message || '历史对话加载失败'
  } finally {
    loadingHistory.value = false
  }
}

async function clearHistory() {
  const symbol = props.detail?.symbol
  if (!symbol || sending.value || !window.confirm('确认清除此个股的全部 Agent 对话记录？')) return
  try {
    await api.clearAgentHistory(symbol)
    messages.value = []
    error.value = ''
  } catch (e: any) {
    error.value = e?.message || '清除失败'
  }
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
      body: JSON.stringify({
        symbol: props.detail?.symbol,
        question: userText,
        include_stock: includeStock.value,
        history_days: historyDays.value
      })
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
    } else {
      messages.value.splice(assistantIndex - 1, 2)
    }
  } finally {
    controller = null
    sending.value = false
    await scrollBottom()
  }
}
function onInputKeydown(event: KeyboardEvent) { if (event.key === 'Enter' && !event.shiftKey) { event.preventDefault(); send() } }
function stop() { controller?.abort() }
</script>

<template>
  <aside v-if="detail" class="agent-panel">
    <header><div><strong>AI 行情助理</strong><span>{{ detail.name }} · {{ detail.code }}</span></div><div class="header-actions"><button title="清除当前个股对话" :disabled="sending || !messages.length" @click="clearHistory">清除</button><button class="icon-button" title="关闭" @click="close">×</button></div></header>
    <section class="context-options">
      <label><input v-model="includeStock" type="checkbox"> 当前个股信息</label>
      <span>历史交易日</span>
      <label v-for="days in [0,10,30,60]" :key="days"><input v-model="historyDays" type="radio" :value="days"> {{ days === 0 ? '不携带' : days + '日' }}</label>
    </section>
    <div ref="chatHost" class="chat">
      <div v-for="(m, i) in messages" :key="i" :class="['msg', m.role]">{{ m.text }}<i v-if="sending && i === messages.length - 1" class="cursor"></i></div>
      <div v-if="loadingHistory" class="empty">正在读取历史对话</div>
      <div v-else-if="!messages.length" class="empty">对话会按当前个股保存在数据库中，重新打开仍可继续。</div>
    </div>
    <div class="composer"><textarea v-model="input" rows="3" placeholder="输入问题，Enter 发送，Shift+Enter 换行" :disabled="sending" @keydown="onInputKeydown"></textarea><button v-if="sending" class="stop" title="停止生成" @click="stop">停止</button><button v-else class="send" title="发送" :disabled="!input.trim()" @click="send">发送</button></div>
  </aside>
</template>

<style scoped>
.agent-panel { position:fixed; z-index:60; top:0; right:0; bottom:0; display:grid; width:min(440px,94vw); grid-template-rows:auto auto minmax(0,1fr) auto; padding:14px; border-left:1px solid #354157; background:#101a2b; color:#e7ecf4; box-shadow:-18px 0 44px rgba(0,0,0,.48); }
.agent-panel header { display:flex; align-items:center; justify-content:space-between; gap:12px; padding-bottom:10px; border-bottom:1px solid #2b3850; }.agent-panel header>div:first-child { display:grid; gap:3px; }.agent-panel strong { font-size:14px; }.agent-panel header span { color:#8f9caf; font-size:11px; }.header-actions { display:flex; align-items:center; gap:6px; }.header-actions button { height:30px; padding:0 9px; border:1px solid #3a496a; background:#233150; color:#e7ecf4; font-size:11px; cursor:pointer; }.header-actions button:disabled { opacity:.45; cursor:default; }.header-actions .icon-button { width:30px; padding:0; font-size:20px; }
.context-options { display:flex; align-items:center; flex-wrap:wrap; gap:8px 11px; padding:10px 2px; border-bottom:1px solid #2b3850; color:#aeb9ca; font-size:11px; }.context-options label { display:flex; align-items:center; gap:3px; cursor:pointer; }.context-options input { margin:0; accent-color:#e9c16c; }
.chat { display:flex; min-height:0; flex-direction:column; gap:8px; overflow-y:auto; padding:10px 2px 14px; }.msg { padding:9px 11px; border-radius:6px; font-size:13px; line-height:1.55; white-space:pre-wrap; word-break:break-word; }.msg.user { align-self:flex-end; max-width:82%; background:#2b3954; }.msg.assistant { align-self:flex-start; max-width:94%; border:1px solid #264563; background:#10253b; color:#d8e8f9; }.empty { padding:20px 8px; color:#728096; font-size:12px; line-height:1.6; }.cursor { display:inline-block; width:6px; height:14px; margin-left:2px; background:#e9c16c; vertical-align:middle; animation:blink .8s infinite; }
.composer { display:grid; grid-template-columns:minmax(0,1fr) 58px; gap:8px; padding-top:10px; border-top:1px solid #2b3850; }.composer textarea { min-width:0; resize:none; padding:9px 10px; border:1px solid #3a496a; outline:none; background:#0d1525; color:#e7ecf4; font-size:13px; line-height:1.45; }.composer textarea:focus { border-color:#e9c16c; }.composer button { border:0; font-weight:700; cursor:pointer; }.send { background:#e9c16c; color:#1a1206; }.send:disabled { opacity:.45; cursor:default; }.stop { border:1px solid #93444a!important; background:#43272d; color:#ffbdc2; }
@keyframes blink { 50% { opacity:0; } }
@media (max-width:600px) { .agent-panel { top:auto; width:100vw; height:82vh; border-top:1px solid #354157; border-left:0; } }
</style>
