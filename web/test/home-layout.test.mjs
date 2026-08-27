import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const homeUrl = new URL('../src/views/Home.vue', import.meta.url)
const source = await readFile(homeUrl, 'utf8')

function assertInOrder(text, labels) {
  let cursor = -1
  for (const label of labels) {
    const next = text.indexOf(label, cursor + 1)
    assert.notEqual(next, -1, `missing label: ${label}`)
    assert.ok(next > cursor, `${label} must appear after ${labels[labels.indexOf(label) - 1]}`)
    cursor = next
  }
}

test('home presents the requested dashboard sections in strict order', () => {
  assertInOrder(source, [
    '今日盈亏',
    '账户总盈亏',
    '恐惧贪婪指数',
    '持仓信息',
    'AI 高分推荐股',
    '热点决策线索',
    '历史推荐组合走势',
    '复盘关键信息',
  ])
})

test('home limits positions and ranks profitable active recommendations to five rows', () => {
  assert.match(source, /const actionRows[\s\S]*?\.filter\(p => p\.status === 'pending_entry' \|\| p\.status === 'holding'\)[\s\S]*?\.slice\(0, 5\)/)
  assert.match(source, /const recommendedRows[\s\S]*?\.filter\(p => \(p\.status === 'pending_entry' \|\| p\.status === 'holding'\) && p\.change_pct != null\)[\s\S]*?\.sort\(\(a, b\) => b\.change_pct! - a\.change_pct!\)[\s\S]*?\.slice\(0, 5\)/)
})

test('desktop metrics and overview use the same strict three-column grid', () => {
  assert.match(source, /\.hero,\.grid\{[^}]*grid-template-columns:repeat\(3,minmax\(0,1fr\)\)/)
})

test('history chart and review each span the complete dashboard row', () => {
  assert.match(source, /\.panel\.basket\s*\{[^}]*grid-column\s*:\s*1\s*\/\s*-1/)
  assert.match(source, /\.panel\.review\s*\{[^}]*grid-column\s*:\s*1\s*\/\s*-1/)
})
