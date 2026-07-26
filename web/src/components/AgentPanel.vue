<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { api, type StockDetailPayload } from '../api'

const props = defineProps<{ detail: StockDetailPayload | null }>()
const emit = defineEmits<{ close: [] }>()
const messages = ref<{ role: 'user' | 'assistant'; text: string }[]>([])
const input = ref('')
const sending = ref(false)
const error = ref('')
const chatHost = ref<HTMLElement | null>(null)
const inputHost = ref<HTMLTextAreaElement | null>(null)
const includeStock = ref(true)
const historyDays = ref<0 | 10 | 30 | 60>(30)
const loadingHistory = ref(false)
const settingsOpen = ref(false)
let controller: AbortController | null = null

const contextSummary = computed(() => {
  const parts: string[] = []
  if (includeStock.value) parts.push('个股快照')
  if (historyDays.value) parts.push(`${historyDays.value}日K线`)
  return parts.length ? parts.join(' · ') : '不携带行情数据'
})

const quickQuestions = [
  '结合量价和趋势，分析当前所处阶段',
  '关键支撑位、压力位和风险点是什么？',
  '未来 10 个交易日有哪些观察条件？'
]

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
  error.value = ''
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

function useQuickQuestion(question: string) {
  input.value = question
  nextTick(() => inputHost.value?.focus())
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
      messages.value[assistantIndex].text = `请求失败：${error.value}`
    } else {
      messages.value.splice(assistantIndex - 1, 2)
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
</script>

<template>
  <div v-if="detail" class="agent-layer" @mousedown.self="close">
    <aside class="agent-panel" aria-label="AI 行情助理">
      <header class="agent-header">
        <div class="agent-identity">
          <span class="agent-mark">AI</span>
          <div><strong>行情分析助理</strong><span>{{ detail.name }} <i>{{ detail.code }}</i></span></div>
        </div>
        <div class="header-actions">
          <button class="text-action" title="清除当前个股对话" :disabled="sending || !messages.length" @click="clearHistory">清空对话</button>
          <button class="close-button" title="关闭" aria-label="关闭" @click="close">×</button>
        </div>
      </header>

      <section class="context-bar">
        <button class="context-trigger" :class="{ active: settingsOpen }" @click="settingsOpen = !settingsOpen">
          <span><b>分析上下文</b><small>{{ contextSummary }}</small></span>
          <i>{{ settingsOpen ? '收起' : '设置' }}</i>
        </button>
        <div v-if="settingsOpen" class="context-settings">
          <label class="stock-switch"><input v-model="includeStock" type="checkbox"><span>携带当前个股基础信息与最新快照</span></label>
          <div class="history-setting"><span>历史日 K</span><div class="segments"><label v-for="days in [0,10,30,60]" :key="days"><input v-model="historyDays" type="radio" :value="days"><b>{{ days === 0 ? '不带' : days + '日' }}</b></label></div></div>
          <p>数据来自本地 MySQL，仅作为分析上下文。</p>
        </div>
      </section>

      <div ref="chatHost" class="chat">
        <div v-if="loadingHistory" class="loading-state"><i></i><span>正在读取历史对话</span></div>
        <section v-else-if="!messages.length" class="welcome-state">
          <span class="welcome-mark">AI</span>
          <h2>从哪里开始分析？</h2>
          <p>我会结合 {{ detail.name }} 的本地行情快照和所选历史 K 线回答。对话会按证券保存。</p>
          <div class="quick-questions"><button v-for="question in quickQuestions" :key="question" @click="useQuickQuestion(question)"><span>{{ question }}</span><i>›</i></button></div>
        </section>
        <div v-for="(m, i) in messages" :key="i" :class="['message-row', m.role]">
          <span class="role-mark">{{ m.role === 'assistant' ? 'AI' : '我' }}</span>
          <div class="message-body"><span class="role-name">{{ m.role === 'assistant' ? '行情助理' : '你' }}</span><div class="message-text">{{ m.text }}<i v-if="sending && i === messages.length - 1" class="cursor"></i></div></div>
        </div>
      </div>

      <footer class="composer-area">
        <div v-if="error" class="error-line"><span>{{ error }}</span><button @click="error=''">×</button></div>
        <div class="composer">
          <textarea ref="inputHost" v-model="input" rows="3" :placeholder="`询问 ${detail.name} 的趋势、风险或交易观察点`" :disabled="sending" @keydown="onInputKeydown"></textarea>
          <div class="composer-meta"><span>Enter 发送 · Shift + Enter 换行</span><button v-if="sending" class="stop-button" title="停止生成" @click="stop">■ 停止</button><button v-else class="send-button" title="发送" :disabled="!input.trim()" @click="send">发送 <b>↑</b></button></div>
        </div>
        <p class="disclaimer">AI 分析仅供研究，不构成投资建议</p>
      </footer>
    </aside>
  </div>
</template>

<style scoped>
.agent-layer { position:fixed; z-index:60; inset:0; background:rgba(15,23,34,.26); backdrop-filter:blur(1px); }
.agent-panel { position:absolute; top:0; right:0; bottom:0; display:grid; width:min(500px,96vw); grid-template-rows:auto auto minmax(0,1fr) auto; border-left:1px solid #c7ced7; background:#f7f8fa; color:#17202b; box-shadow:-18px 0 48px rgba(19,31,45,.24); }
.agent-header { display:flex; min-height:68px; align-items:center; justify-content:space-between; gap:12px; padding:11px 14px 10px 16px; border-bottom:1px solid #d8dde3; background:#fff; }
.agent-identity { display:flex; min-width:0; align-items:center; gap:10px; }.agent-mark,.welcome-mark { display:flex; flex:0 0 auto; width:34px; height:34px; align-items:center; justify-content:center; border:1px solid #26374c; border-radius:4px; background:#1d2b3e; color:#fff; font-size:11px; font-weight:750; }.agent-identity>div { display:grid; min-width:0; gap:2px; }.agent-identity strong { font-size:14px; font-weight:680; }.agent-identity span { overflow:hidden; color:#687280; font-size:11px; text-overflow:ellipsis; white-space:nowrap; }.agent-identity i { margin-left:4px; color:#929ba6; font-style:normal; }
.header-actions { display:flex; align-items:center; gap:4px; }.header-actions button { border-radius:3px; background:transparent; color:#5c6673; }.text-action { padding:5px 8px; font-size:11px; }.text-action:disabled { opacity:.35; cursor:default; }.close-button { width:32px; height:32px; padding:0; font-size:21px; line-height:1; }.header-actions button:hover { background:#edf0f3; color:#17202b; opacity:1; }
.context-bar { position:relative; border-bottom:1px solid #d8dde3; background:#f3f5f7; }.context-trigger { display:flex; width:100%; min-height:48px; align-items:center; justify-content:space-between; gap:10px; padding:8px 16px; border:0; border-radius:0; background:transparent; color:#354150; text-align:left; }.context-trigger:hover,.context-trigger.active { background:#eaedf1; opacity:1; }.context-trigger>span { display:grid; gap:2px; }.context-trigger b { font-size:11px; font-weight:650; }.context-trigger small { color:#77818d; font-size:10px; }.context-trigger i { color:#64707d; font-size:10px; font-style:normal; }
.context-settings { display:grid; gap:11px; padding:12px 16px 13px; border-top:1px solid #d8dde3; background:#fff; }.stock-switch { display:flex; align-items:center; gap:8px; color:#354150; font-size:11px; }.stock-switch input { width:auto; accent-color:#26374c; }.history-setting { display:flex; align-items:center; justify-content:space-between; gap:10px; color:#5d6875; font-size:11px; }.segments { display:flex; border:1px solid #c7ced7; background:#f7f8fa; }.segments label { cursor:pointer; }.segments input { position:absolute; opacity:0; pointer-events:none; }.segments b { display:block; min-width:48px; padding:5px 8px; border-right:1px solid #c7ced7; color:#67727f; text-align:center; font-size:10px; font-weight:550; }.segments label:last-child b { border-right:0; }.segments input:checked+b { background:#26374c; color:#fff; }.context-settings p { color:#929ba6; font-size:9px; }
.chat { min-height:0; overflow-y:auto; padding:20px 18px 28px; scroll-behavior:smooth; }.loading-state { display:flex; height:100%; align-items:center; justify-content:center; gap:8px; color:#7b8591; font-size:11px; }.loading-state i { width:13px; height:13px; border:2px solid #d2d7dd; border-top-color:#26374c; border-radius:50%; animation:spin .8s linear infinite; }
.welcome-state { display:flex; max-width:390px; min-height:100%; margin:0 auto; flex-direction:column; align-items:center; justify-content:center; padding:24px 0 50px; text-align:center; }.welcome-mark { width:42px; height:42px; margin-bottom:13px; }.welcome-state h2 { font-size:18px; font-weight:660; }.welcome-state>p { max-width:350px; margin-top:7px; color:#747e8a; font-size:11px; line-height:1.65; }.quick-questions { display:grid; width:100%; gap:6px; margin-top:20px; }.quick-questions button { display:flex; width:100%; align-items:center; justify-content:space-between; gap:12px; padding:10px 12px; border:1px solid #d0d6dd; border-radius:3px; background:#fff; color:#354150; text-align:left; }.quick-questions button:hover { border-color:#98a4b1; background:#f4f6f8; opacity:1; }.quick-questions span { font-size:11px; }.quick-questions i { color:#8a949f; font-size:16px; font-style:normal; }
.message-row { display:grid; grid-template-columns:30px minmax(0,1fr); gap:10px; margin-bottom:20px; }.role-mark { display:flex; width:28px; height:28px; align-items:center; justify-content:center; border:1px solid #c5ccd4; border-radius:3px; background:#fff; color:#44505e; font-size:9px; font-weight:700; }.message-row.assistant .role-mark { border-color:#26374c; background:#26374c; color:#fff; }.message-body { min-width:0; }.role-name { display:block; margin:1px 0 5px; color:#67727f; font-size:10px; font-weight:650; }.message-text { color:#26313e; font-size:13px; line-height:1.7; white-space:pre-wrap; word-break:break-word; }.message-row.user .message-text { display:inline-block; padding:8px 10px; border:1px solid #d3d8de; border-radius:3px; background:#fff; }.cursor { display:inline-block; width:5px; height:13px; margin-left:3px; background:#26374c; vertical-align:middle; animation:blink .8s infinite; }
.composer-area { padding:10px 14px 9px; border-top:1px solid #d8dde3; background:#fff; }.error-line { display:flex; align-items:center; justify-content:space-between; gap:8px; margin-bottom:7px; padding:6px 8px; border-left:2px solid #a92f3a; background:#fff1f2; color:#8a2730; font-size:10px; }.error-line button { width:20px; height:20px; padding:0; background:transparent; color:#8a2730; }.composer { border:1px solid #b9c1ca; border-radius:4px; background:#fff; transition:border-color .15s,box-shadow .15s; }.composer:focus-within { border-color:#687688; box-shadow:0 0 0 2px rgba(38,55,76,.08); }.composer textarea { display:block; width:100%; min-height:68px; max-height:160px; resize:none; padding:10px 11px 5px; border:0; outline:none; background:transparent; color:#1d2733; font:13px/1.5 inherit; }.composer textarea::placeholder { color:#9aa3ad; }.composer textarea:disabled { opacity:.65; }.composer-meta { display:flex; min-height:35px; align-items:center; justify-content:space-between; gap:8px; padding:4px 5px 5px 10px; }.composer-meta>span { color:#9aa3ad; font-size:9px; }.composer-meta button { height:27px; border-radius:3px; padding:0 10px; font-size:11px; font-weight:650; }.send-button { background:#26374c; color:#fff; }.send-button b { margin-left:4px; font-size:13px; }.send-button:disabled { background:#c8ced5; cursor:default; }.stop-button { border:1px solid #9b555b; background:#fff; color:#8c3038; }.disclaimer { padding-top:6px; color:#a0a8b1; font-size:9px; text-align:center; }
@keyframes blink { 50% { opacity:0; } } @keyframes spin { to { transform:rotate(360deg); } }
@media (max-width:600px) { .agent-layer { background:rgba(15,23,34,.36); }.agent-panel { top:8vh; width:100vw; border-top:1px solid #c7ced7; border-left:0; box-shadow:0 -12px 36px rgba(19,31,45,.24); }.agent-header { min-height:60px; padding-left:12px; }.chat { padding:16px 12px 22px; }.context-trigger { padding-inline:12px; }.context-settings { padding-inline:12px; }.composer-area { padding-inline:8px; }.text-action { display:none; }.message-row { grid-template-columns:27px minmax(0,1fr); gap:8px; }.role-mark { width:26px; height:26px; }.composer-meta>span { display:none; } }
</style>
