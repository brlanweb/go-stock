function isActive(item) {
  return item.status === 'pending_entry' || item.status === 'holding'
}

export function selectActionRows(items, limit = 5) {
  return items.filter(isActive).slice(0, limit)
}

export function selectRecommendedRows(items, limit = 5) {
  return items
    .filter(item => isActive(item) && typeof item.change_pct === 'number')
    .sort((a, b) => b.change_pct - a.change_pct)
    .slice(0, limit)
}
