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

export interface WatchlistResponse {
  status: 'live' | 'unavailable' | 'closed' | 'empty'
  synced_at?: string
  symbols: string[]
  quotes: Quote[]
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

export interface MinuteKline {
  symbol: string
  time: string
  open: number
  high: number
  low: number
  close: number
  volume: number
  amount: number
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
  circ_mv: number
  main_net_inflow?: number
}

export interface HeatmapGroup {
  name: string
  sector_code: string
  sector_type: 'industry' | 'concept'
  change_pct: number
  heat: number
  area_weight: number
  stock_count: number
  total_mv: number
  circ_mv: number
  amount: number
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

export interface SectorListItem {
  sector_code: string
  sector_name: string
  sector_type: string
  stock_count: number
}

export interface SectorConstituentItem {
  symbol: string
  code: string
  name: string
  industry: string
  price: number
  change_pct: number
  is_trading: boolean
  snapshot_at?: string
}

export interface AgentChatMessage {
  id: number
  symbol: string
  role: 'user' | 'assistant'
  content: string
  created_at: string
}

export interface StockDetailPayload {
  symbol: string
  code: string
  name: string
  industry: string
  industry_code: string
  list_date?: string
  concepts: SectorListItem[]
  quote?: Quote
  klines_60: Kline[]
}

export interface IndicatorDefinition {
  id: string
  display_name: string
  description: string
  category: string
  kind: 'indicator' | 'strategy'
  capability: 'executable' | 'experimental' | 'context_required'
  source: string
  enabled: boolean
  default_params: Record<string, any>
  current_params: Record<string, any>
  sort_order: number
}

export interface BacktestSignal {
  date: string
  action: 'buy' | 'sell'
  price: number
  reason: string
}

export interface BacktestResult {
  run_id: number
  symbol: string
  indicator_id: string
  period: string
  start: string
  end: string
  initial_cash: number
  final_equity: number
  total_return: number
  annual_return: number
  max_drawdown: number
  sharpe_ratio: number
  win_rate: number
  profit_loss_ratio: number
  profit_factor: number
  trade_count: number
  signals: BacktestSignal[]
  params: Record<string, any>
}

export interface HeatmapResponse {
  market: string
  group_by: string
  metric: string
  period: string
  limit: number
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
  intraday: (code: string) => req<MinuteKline[]>(`/intraday/${code}`),
  search: (q: string) => req<Security[]>(`/search?q=${encodeURIComponent(q)}`),
  indices: () => req<IndexQuote[]>('/indices'),
  watchlist: () => req<WatchlistResponse>('/watchlist'),
  recommendations: (date = '') => req<Recommendation[]>(`/recommendations${date ? `?date=${encodeURIComponent(date)}` : ''}`),
  recommendationHistory: (limit = 90) => req<string[]>(`/recommendations/history?limit=${limit}`),
  runRecommendations: () => req<{ status: string }>('/recommendations/run', { method: 'POST' }),
  addWatch: (code: string) => req(`/watchlist/${code}`, { method: 'POST' }),
  delWatch: (code: string) => req(`/watchlist/${code}`, { method: 'DELETE' }),
  syncStatus: () => req<SyncStatus>('/sync/status'),
  startBackfill: () => req<{ status: string }>('/sync/backfill', { method: 'POST' }),
  retryFailedBackfill: () => req<{ status: string; requeued: number }>('/sync/backfill/retry-failed', { method: 'POST' }),
  heatmap: (market = 'all', groupBy = 'industry', metric = 'change_pct', period = '1d', limit = 100) =>
    req<HeatmapResponse>(`/market/heatmap?market=${market}&group_by=${groupBy}&metric=${metric}&period=${period}&limit=${limit}`),
  syncStock: (code: string, mode = 'latest') => req(`/sync/stock/${code}?mode=${mode}`, { method: 'POST' }),
  sectors: (groupBy: 'industry' | 'concept' = 'industry') => req<{ sector_type: string; sectors: SectorListItem[] }>(`/sectors?group_by=${groupBy}`),
  sectorConstituents: (code: string, limit = 100) => req<{ sector_code: string; constituents: SectorConstituentItem[] }>(`/sectors/${encodeURIComponent(code)}/constituents?limit=${limit}`),
  stockDetail: (code: string) => req<StockDetailPayload>(`/stock/${encodeURIComponent(code)}/detail`),
  indicators: () => req<IndicatorDefinition[]>('/indicators'),
  indicator: (id: string) => req<IndicatorDefinition>(`/indicators/${encodeURIComponent(id)}`),
  updateIndicator: (id: string, enabled: boolean, params: Record<string, any>) => req<IndicatorDefinition>(`/indicators/${encodeURIComponent(id)}`, {
    method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ enabled, params })
  }),
  resetIndicator: (id: string) => req<IndicatorDefinition>(`/indicators/${encodeURIComponent(id)}/reset`, { method: 'POST' }),
  backtest: (payload: { symbol: string; indicator_id: string; period: string; initial_cash: number; params?: Record<string, any> }) => req<BacktestResult>('/backtest', {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(payload)
  }),
  backtestHistory: (code: string, limit = 20) => req<BacktestResult[]>(`/backtest/history/${encodeURIComponent(code)}?limit=${limit}`),
  agentHistory: (code: string) => req<AgentChatMessage[]>(`/agent/chat/history/${encodeURIComponent(code)}`),
  clearAgentHistory: (code: string) => req<{ cleared: string }>(`/agent/chat/history/${encodeURIComponent(code)}`, { method: 'DELETE' }),
  authStatus: () => req<{ required: boolean; authenticated: boolean }>('/auth/status'),
  authLogin: (password: string) => req<{ authenticated: boolean }>('/auth/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ password })
  })
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
