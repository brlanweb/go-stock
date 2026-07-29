<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import MarketSidebar from '../components/MarketSidebar.vue'
import { api, type IndicatorDefinition } from '../api'

const items = ref<IndicatorDefinition[]>([])
const keyword = ref('')
const kind = ref('all')
const capability = ref('all')
const selected = ref<IndicatorDefinition | null>(null)
const paramsText = ref('')
const message = ref('')

const filtered = computed(() => items.value.filter(item => {
  const q = keyword.value.trim().toLowerCase()
  return (!q || `${item.display_name} ${item.id} ${item.description}`.toLowerCase().includes(q)) &&
    (kind.value === 'all' || item.kind === kind.value) &&
    (capability.value === 'all' || item.capability === capability.value)
}))

function capabilityText(item: IndicatorDefinition) {
  if (item.capability === 'executable') return item.kind === 'strategy' ? '可回测' : '可计算'
  return item.capability === 'experimental' ? '实验性' : '需要额外数据'
}

async function load() { items.value = await api.indicators() }
function edit(item: IndicatorDefinition) { selected.value = item; paramsText.value = JSON.stringify(item.current_params, null, 2); message.value = '' }
async function save() {
  if (!selected.value) return
  try {
    const params = JSON.parse(paramsText.value || '{}')
    selected.value = await api.updateIndicator(selected.value.id, selected.value.enabled, params)
    message.value = '已保存'
    await load()
  } catch (e: any) { message.value = e.message || '保存失败' }
}
async function reset() {
  if (!selected.value) return
  selected.value = await api.resetIndicator(selected.value.id)
  paramsText.value = JSON.stringify(selected.value.current_params, null, 2)
  message.value = '已恢复默认参数'
  await load()
}
async function toggle(item: IndicatorDefinition) {
  await api.updateIndicator(item.id, !item.enabled, item.current_params)
  await load()
}

onMounted(load)
</script>

<template>
  <div class="indicator-shell">
    <MarketSidebar :controls="false" />
    <main class="indicator-main">
      <header class="page-header"><div><h1>指标管理</h1><p>传统技术指标与参考策略目录。仅“可回测”策略会执行本地日 K 回测。</p></div></header>
      <div class="filters">
        <input v-model="keyword" placeholder="搜索指标、策略或说明" />
        <select v-model="kind"><option value="all">全部类型</option><option value="indicator">技术指标</option><option value="strategy">交易策略</option></select>
        <select v-model="capability"><option value="all">全部能力</option><option value="executable">可计算 / 可回测</option><option value="experimental">实验性</option><option value="context_required">需要额外数据</option></select>
      </div>
      <div class="indicator-grid">
        <article v-for="item in filtered" :key="item.id" class="indicator-card" :class="{ disabled: !item.enabled }">
          <header><div><strong>{{ item.display_name }}</strong><code>{{ item.id }}</code></div><button class="icon-toggle" :title="item.enabled ? '停用' : '启用'" @click="toggle(item)">{{ item.enabled ? '✓' : '○' }}</button></header>
          <p>{{ item.description }}</p>
          <footer><span>{{ item.kind === 'strategy' ? '策略' : '指标' }}</span><span :class="`cap-${item.capability}`">{{ capabilityText(item) }}</span><span>{{ item.source }}</span><button @click="edit(item)">参数</button></footer>
        </article>
      </div>
    </main>
    <aside v-if="selected" class="editor-drawer">
      <header><div><strong>{{ selected.display_name }}</strong><small>{{ selected.id }}</small></div><button title="关闭" @click="selected=null">×</button></header>
      <label><span>状态</span><input v-model="selected.enabled" type="checkbox" /></label>
      <label class="params"><span>参数 JSON</span><textarea v-model="paramsText" spellcheck="false"></textarea></label>
      <p>{{ selected.description }}</p><small>来源：{{ selected.source }} · {{ capabilityText(selected) }}</small>
      <div class="actions"><button class="ghost" @click="reset">恢复默认</button><button @click="save">保存</button></div>
      <em v-if="message">{{ message }}</em>
    </aside>
  </div>
</template>

<style scoped>
.indicator-shell{display:grid;grid-template-columns:212px minmax(0,1fr);height:100vh;background:#eef1f4;color:#121820}.indicator-main{min-width:0;overflow:auto;padding:18px}.page-header{display:flex;justify-content:space-between;border-bottom:1px solid #cdd3db;padding-bottom:12px}.page-header h1{font-size:22px}.page-header p{margin-top:5px;color:#687280;font-size:12px}.filters{display:flex;gap:8px;padding:14px 0}.filters input{width:min(380px,100%);background:#fff;color:#121820;border-color:#bfc6cf}.filters select{height:34px;border:1px solid #bfc6cf;background:#fff;padding:0 10px}.indicator-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(270px,1fr));gap:8px}.indicator-card{display:grid;gap:9px;min-height:132px;padding:12px;border:1px solid #cdd3db;background:#fff}.indicator-card.disabled{opacity:.55}.indicator-card header,.indicator-card footer{display:flex;align-items:center;justify-content:space-between;gap:8px}.indicator-card header>div{display:grid;gap:3px}.indicator-card code{color:#788391;font-size:10px}.indicator-card p{color:#4e5967;font-size:12px;line-height:1.5}.indicator-card footer{justify-content:flex-start;flex-wrap:wrap;font-size:10px;color:#687280}.indicator-card footer span{padding:2px 5px;background:#edf1f5}.indicator-card footer button{margin-left:auto;padding:3px 8px}.cap-executable{color:#147454!important}.cap-experimental{color:#936400!important}.cap-context_required{color:#8e3740!important}.icon-toggle{width:28px;height:28px;padding:0;border:1px solid #bac2cc;background:#f5f7f9;color:#263241}.editor-drawer{position:fixed;z-index:30;top:0;right:0;display:flex;width:380px;max-width:100vw;height:100vh;flex-direction:column;gap:14px;padding:18px;border-left:1px solid #bfc6cf;background:#fff;box-shadow:-12px 0 30px rgba(16,28,42,.14);color:#121820}.editor-drawer header{display:flex;justify-content:space-between}.editor-drawer header div{display:grid}.editor-drawer header small,.editor-drawer>small,.editor-drawer p{color:#687280}.editor-drawer header button{width:30px;height:30px;padding:0}.editor-drawer label{display:flex;justify-content:space-between}.editor-drawer input[type=checkbox]{width:auto}.editor-drawer .params{display:grid;gap:6px;flex:1}.editor-drawer textarea{width:100%;min-height:260px;resize:none;border:1px solid #bfc6cf;padding:10px;font:12px/1.5 ui-monospace,SFMono-Regular,monospace}.actions{display:flex;justify-content:flex-end;gap:8px}.editor-drawer em{color:#147454;font-size:12px;font-style:normal}@media(max-width:900px){.indicator-shell{height:auto;min-height:100vh;grid-template-columns:1fr}.indicator-main{padding:12px}.filters{flex-wrap:wrap}.filters select{flex:1}.indicator-grid{grid-template-columns:1fr}}
</style>
