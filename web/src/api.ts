// REST API 客户端与类型定义（对齐后端 model）

export interface Quote {
  symbol: string
  code: string
  name: string
  market: string
  source: string
  fetched_at: string
  provider_timestamp?: string
  fallback_from?: string
  currency: string
  price: number | null
  change_pct: number | null
  change_amount: number | null
  volume: number | null
  amount: number | null
  volume_ratio: number | null
  turnover_rate: number | null
  amplitude: number | null
  open: number | null
  high: number | null
  low: number | null
  pre_close: number | null
  pe_ratio: number | null
  pb_ratio: number | null
  total_mv: number | null
  circ_mv: number | null
  change_60d: number | null
  high_52w: number | null
  low_52w: number | null
  bids?: { price: number; volume: number }[]
  asks?: { price: number; volume: number }[]
}

export interface Kline {
  symbol: string
  date: string
  open: number
  high: number
  low: number
  close: number
  volume: number
  amount: number
  change_pct: number
  turnover_rate: number
  adj_factor: number
}

export interface TimesharePoint {
  time: string
  price: number
  avg_price: number
  volume: number
}

export interface IndexQuote {
  symbol: string
  name: string
  price: number | null
  change_pct: number | null
  amount: number | null
  volume: number | null
}

export interface Security {
  symbol: string
  code: string
  name: string
  type: string
  exchange: string
  industry?: string
}

export interface SyncStatus {
  backfill: {
    task: string
    total: number
    done: number
    pending: number
    running: number
    failed: number
    latest_date?: string
    complete: number
    partial: number
    empty: number
  }
  backfill_running: boolean
}

export interface HeatmapItem {
  symbol: string
  code: string
  name: string
  industry: string
  market: string
  change_pct: number
  period_change: number
  pe_ratio: number
  total_mv: number
  main_net_inflow?: number
}

export interface HeatmapGroup {
  name: string
  change_pct: number
  main_net_inflow?: number
  items: HeatmapItem[]
}

export interface Recommendation {
  date: string
  rank: number
  symbol: string
  code: string
  name: string
  sector: string
  probability: number
  reason: string
  model: string
}

export interface HeatmapResponse {
  market: string
  group_by: string
  metric: string
  period: string
  notice: string
  groups: HeatmapGroup[]
}

async function req<T>(path: string, init?: RequestInit): Promise<T> {
  const resp = await fetch(`/api/v1${path}`, init)
  if (!resp.ok) {
    const body = await resp.json().catch(() => ({ error: resp.statusText }))
    throw new Error(body.error || `HTTP ${resp.status}`)
  }
  return resp.json()
}

export const api = {
  quote: (code: string) => req<Quote>(`/quote/${code}`),
  quotes: (codes: string[]) => req<Quote[]>(`/quotes?codes=${codes.join(',')}`),
  kline: (code: string, period = 'day', adjust = 'qfq', limit = 250) =>
    req<Kline[]>(`/kline/${code}?period=${period}&adjust=${adjust}&limit=${limit}`),
  timeshare: (code: string) => req<TimesharePoint[]>(`/timeshare/${code}`),
  search: (q: string) => req<Security[]>(`/search?q=${encodeURIComponent(q)}`),
  indices: () => req<IndexQuote[]>('/indices'),
  watchlist: () => req<Quote[] | string[]>('/watchlist'),
  recommendations: () => req<Recommendation[]>('/recommendations'),
  addWatch: (code: string) => req(`/watchlist/${code}`, { method: 'POST' }),
  delWatch: (code: string) => req(`/watchlist/${code}`, { method: 'DELETE' }),
  syncStatus: () => req<SyncStatus>('/sync/status'),
  heatmap: (market = 'all', groupBy = 'industry', metric = 'change_pct', period = '1d') =>
    req<HeatmapResponse>(`/market/heatmap?market=${market}&group_by=${groupBy}&metric=${metric}&period=${period}`),
  syncStock: (code: string, mode = 'latest') => req(`/sync/stock/${code}?mode=${mode}`, { method: 'POST' })
}

// 格式化工具
export function fmt(v: number | null | undefined, digits = 2): string {
  if (v === null || v === undefined) return '-'
  return v.toFixed(digits)
}

export function fmtPct(v: number | null | undefined): string {
  if (v === null || v === undefined) return '-'
  return `${v > 0 ? '+' : ''}${v.toFixed(2)}%`
}

export function fmtBig(v: number | null | undefined): string {
  if (v === null || v === undefined) return '-'
  if (Math.abs(v) >= 1e12) return (v / 1e12).toFixed(2) + '万亿'
  if (Math.abs(v) >= 1e8) return (v / 1e8).toFixed(2) + '亿'
  if (Math.abs(v) >= 1e4) return (v / 1e4).toFixed(2) + '万'
  return v.toFixed(0)
}

export function pctClass(v: number | null | undefined): string {
  if (v === null || v === undefined) return 'dim'
  return v > 0 ? 'up' : v < 0 ? 'down' : 'dim'
}
