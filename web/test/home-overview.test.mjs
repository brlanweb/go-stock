import assert from 'node:assert/strict'
import test from 'node:test'
import { selectActionRows, selectRecommendedRows } from '../src/homeOverview.js'

const base = {
  symbol: 'SH600000',
  code: '600000',
  name: '测试股',
  entry_price: 10,
  reference_price: 11,
}

function position(id, status, changePct) {
  return { ...base, id, status, change_pct: changePct }
}

test('selectActionRows keeps only active positions and limits output to five', () => {
  const rows = [
    position(1, 'exited', 20),
    position(2, 'holding', 1),
    position(3, 'pending_entry', null),
    position(4, 'holding', 2),
    position(5, 'removed', 30),
    position(6, 'holding', 3),
    position(7, 'holding', 4),
    position(8, 'holding', 5),
    position(9, 'holding', 6),
  ]

  assert.deepEqual(selectActionRows(rows).map(item => item.id), [2, 3, 4, 6, 7])
})

test('selectRecommendedRows ranks measurable active returns and limits output to five', () => {
  const rows = [
    position(1, 'exited', 99),
    position(2, 'holding', -3),
    position(3, 'pending_entry', null),
    position(4, 'holding', 0),
    position(5, 'removed', 88),
    position(6, 'holding', 12),
    position(7, 'holding', 4.5),
    position(8, 'holding', 2),
    position(9, 'holding', -1),
    position(10, 'expired', 77),
    position(11, 'holding', 7),
  ]

  assert.deepEqual(selectRecommendedRows(rows).map(item => item.id), [6, 11, 7, 8, 4])
})

test('selection does not mutate the source array', () => {
  const rows = [position(1, 'holding', 1), position(2, 'holding', 2)]
  const snapshot = structuredClone(rows)

  selectActionRows(rows)
  selectRecommendedRows(rows)

  assert.deepEqual(rows, snapshot)
})
