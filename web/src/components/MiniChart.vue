<script setup lang="ts">
/**
 * 轻量指标可视化组件：把首页指标卡从「三行文字」变成「一眼可读的图形」。
 *
 * 四种形态覆盖全部指标语义，不引入任何图表库（纯 SVG，随主题色走）：
 *   - bar    单值占比条，用于胜率类 0~100 指标
 *   - split  正负对比条，用于盈利/亏损、胜/负这类双向构成
 *   - stack  多段堆叠条，用于生命周期分布这类多状态构成
 *   - gauge  中心零轴的双向标尺，用于收益率这类有正负方向的值
 *
 * 设计约束：所有形态高度一致（6px），保证指标卡在网格中对齐；
 * 无数据时渲染灰色空槽而不是隐藏，避免卡片高度跳动。
 */
interface Segment {
  value: number
  label: string
  tone: 'up' | 'down' | 'flat' | 'warn' | 'info'
}

const props = withDefaults(defineProps<{
  kind: 'bar' | 'split' | 'stack' | 'gauge'
  /** bar/gauge 使用的单值 */
  value?: number | null
  /** bar 的满量程；gauge 的单侧量程（绝对值） */
  max?: number
  /** split/stack 使用的分段 */
  segments?: Segment[]
}>(), { value: null, max: 100, segments: () => [] })

const toneColor: Record<Segment['tone'], string> = {
  up: '#ef6a72', down: '#55b996', flat: '#7b879a', warn: '#e9c16c', info: '#67a9d8',
}

// bar：单值占比，负值按 0 处理（胜率不可能为负）
function barWidth() {
  const v = props.value
  if (v == null || !Number.isFinite(v) || props.max <= 0) return 0
  return Math.max(0, Math.min(100, v / props.max * 100))
}

// gauge：以中线为零点向两侧延伸，超出量程截断
function gaugeGeometry() {
  const v = props.value
  if (v == null || !Number.isFinite(v) || props.max <= 0) return null
  const ratio = Math.max(-1, Math.min(1, v / props.max))
  const half = Math.abs(ratio) * 50
  return { left: ratio >= 0 ? 50 : 50 - half, width: half, positive: ratio >= 0 }
}

// split/stack：按占比归一化；总量为 0 时返回空表示无数据
function normalized() {
  const list = props.segments.filter(s => Number.isFinite(s.value) && s.value > 0)
  const total = list.reduce((sum, s) => sum + s.value, 0)
  if (total <= 0) return []
  return list.map(s => ({ ...s, pct: s.value / total * 100 }))
}
</script>

<template>
  <div class="mini" :class="kind">
    <!-- 单值占比 -->
    <div v-if="kind === 'bar'" class="track">
      <i class="fill" :class="(value ?? 0) >= 50 ? 'up' : 'down'" :style="{ width: `${barWidth()}%` }" />
      <u v-if="max === 100" class="mid" />
    </div>

    <!-- 双向标尺：中线为零 -->
    <div v-else-if="kind === 'gauge'" class="track">
      <template v-if="gaugeGeometry()">
        <i
          class="fill"
          :class="gaugeGeometry()!.positive ? 'up' : 'down'"
          :style="{ left: `${gaugeGeometry()!.left}%`, width: `${gaugeGeometry()!.width}%` }"
        />
      </template>
      <u class="mid" />
    </div>

    <!-- 分段构成 -->
    <div v-else class="track">
      <template v-if="normalized().length">
        <i
          v-for="seg in normalized()"
          :key="seg.label"
          class="seg"
          :style="{ width: `${seg.pct}%`, background: toneColor[seg.tone] }"
          :title="`${seg.label} ${seg.value}`"
        />
      </template>
    </div>
  </div>
</template>

<style scoped>
.mini { width: 100%; }
.track {
  position: relative; display: flex; width: 100%; height: 6px;
  overflow: hidden; background: #25334b;
}
.fill { position: absolute; top: 0; height: 100%; transition: width .18s ease; }
.gauge .fill { position: absolute; }
.bar .fill { left: 0; }
.fill.up { background: #ef6a72; }
.fill.down { background: #55b996; }
.seg { height: 100%; flex: 0 0 auto; }
/* 零轴/中位参考线 */
.mid {
  position: absolute; top: -1px; left: 50%; width: 1px; height: 8px;
  background: #8895ab; opacity: .75;
}
</style>
