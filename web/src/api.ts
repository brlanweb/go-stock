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
  risk_score: number | null
  reason: string
  model: string
  entry_price: number | null
  latest_price: number | null
  change_pct: number | null
  tracked_days: number
  // AI 实际退出优先；历史无生命周期记录时才使用 MA10/最大追踪天数模拟退出
  exited: boolean
  exit_reason?: string
  position_status?: 'pending_entry' | 'holding' | 'exited' | 'expired'
  settled: boolean
}

// 蒙特卡洛模拟：基于最近 250 个真实日收益率有放回抽样的价格路径分布（确定性种子）。
export interface MonteCarloResult {
  symbol: string
  days: number
  paths: number
  sample_days: number
  base_price: number
  win_rate: number
  avg_return_pct: number
  median_pct: number
  p5_pct: number
  p25_pct: number
  p75_pct: number
  p95_pct: number
  prob_gain_5_pct: number
  prob_loss_5_pct: number
}

// 趋势建议：daily_pick=盘前首选；hourly_ai=盘中30分钟分析；rule=本地硬风控。
export interface EntryAdvice {
  id: number
  trade_date: string
  symbol: string
  name: string
  code: string
  source: 'daily_pick' | 'hourly_ai' | 'rule'
  stage: 'entry' | 'exit'
  action: 'pick' | 'entry' | 'wait' | 'hold' | 'reduce' | 'exit' | 'expired'
  reason: string
  price_low: number | null
  price_high: number | null
  urgency: 'normal' | 'warn' | 'urgent'
  ref_price: number | null
  model: string
  created_at: string
}

export interface Position {
  id: number
  symbol: string
  code: string
  name: string
  pick_date: string
  analysis_date: string
  status: 'pending_entry' | 'holding' | 'exited' | 'expired'
  entry_date?: string
  entry_price: number | null
  exit_date?: string
  exit_price: number | null
  exit_reason?: string
  hold_days: number
  reference_price: number | null
  change_pct: number | null
  created_at: string
  updated_at: string
}

export interface EntryAdviceResponse {
  date: string
  paused: boolean
  items: EntryAdvice[]
}

export interface RecommendationStats {
  total_days: number
  lifecycle_picks: number
  pending_picks: number
  holding_picks: number
  exited_picks: number
  expired_picks: number
  frozen_picks: number
  tracking_picks: number
  wins: number
  losses: number
  breakeven: number
  win_rate: number | null
  avg_change_pct: number | null
  sum_change_pct: number | null
  median_pct: number | null
  avg_win_pct: number | null
  avg_loss_pct: number | null
  gross_profit_pct: number | null
  gross_loss_pct: number | null
  profit_factor: number | null
  unrealized_sum_pct: number | null
  unrealized_avg_pct: number | null
  avg_hold_days: number | null
  best_pct: number | null
  best_name: string
  worst_pct: number | null
  worst_name: string
  day_wins: number
  day_frozen: number
  day_win_rate: number | null
}

// 影子基线对照：ai 与确定性规则（trend=趋势分前3 / low_risk=低风险前3）
// 在共同分析日期集上的趋势退出冻结口径统计，用于度量 AI 相对基线的超额。
export interface RecommendationShadowStats {
  strategy: string
  total_days: number
  frozen_picks: number
  tracking_picks: number
  wins: number
  win_rate: number | null
  avg_change_pct: number | null
  sum_change_pct: number | null
  day_wins: number
  day_frozen: number
  day_win_rate: number | null
}

// 下一次盘前推荐的候选风险上限：由最近一次 AI 复盘的市场阶段自动决定，仅展示。
export interface RecommendationRiskPolicy {
  review_date: string
  market_phase: string
  max_risk_score: number
}

export interface RecommendationPerformance {
  date: string
  stocks: number
  tracked_days: number
  finished: boolean
  sum_change_pct: number | null
  avg_change_pct: number | null
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

export interface HotspotSectorStat {
  sector_code: string
  sector_name: string
  stock_count: number
  avg_change: number
  avg_change_5d: number
  avg_change_20d: number
  up_ratio: number
  limit_up_count: number
  total_amount: number
  amount_ratio: number
  avg_turnover: number
  heat_score: number
}

export interface HotspotRelation {
  from_code: string
  from_name: string
  to_code: string
  to_name: string
  common_count: number
  jaccard: number
}

export interface HotspotAIRelation {
  from_code: string
  to_code: string
  type: string
  reason: string
}

export interface HotspotMainline {
  name: string
  thesis: string
  concept_codes: string[]
  relations: HotspotAIRelation[]
  chokepoints: Array<{ sector_code: string; status: string; confidence: number; reason: string; invalidation: string }>
}

export interface HotspotConcept {
  sector_code: string
  sector_name: string
  status: 'accelerating' | 'latent' | 'overheated'
  confidence: number
  reason: string
  invalidation: string
  stats: HotspotSectorStat
  stocks: Array<{ symbol: string; code: string; name: string; change_pct: number; circ_mv: number; amount: number }>
}

export interface HotspotRunSummary {
  id: number
  report_date: string
  model: string
  created_at: string
}

export interface HotspotReport {
  available?: boolean
  report_date?: string
  model?: string
  generated_at?: string
  screened?: HotspotSectorStat[]
  data_relations?: HotspotRelation[]
  mainlines?: HotspotMainline[]
  concepts?: HotspotConcept[]
}

export interface DailyReviewRunSummary {
  id: number
  review_date: string
  market_phase: 'up' | 'range' | 'down'
  model: string
  created_at: string
}

export interface DailyReviewSectorFact {
  sector_code: string
  sector_name: string
  sector_type: string
  stock_count: number
  avg_change: number
  avg_change_5d: number
  up_ratio: number
  amount_ratio: number
  heat_score: number
}

export interface DailyReviewRecommendationFact {
  date: string
  symbol: string
  code: string
  name: string
  sector: string
  probability: number
  risk_score: number | null
  reason: string
  entry_price: number | null
  latest_price: number | null
  change_pct: number | null
  tracked_days: number
  frozen: boolean
  day_change_pct: number | null
  benchmark_change_pct: number | null
  excess_change_pct: number | null
}

export interface DailyReviewHotspotFact {
  report_date: string
  sector_code: string
  sector_name: string
  status: string
  confidence: number
  avg_change: number
  up_ratio: number
  amount_ratio: number
  heat_score: number
}

export interface DailyReviewReport {
  available?: boolean
  review_date?: string
  generated_at?: string
  model?: string
  market_phase?: 'up' | 'range' | 'down'
  confidence?: number
  market_summary?: string
  index_review?: string
  breadth_review?: string
  sector_assessments?: Array<{ sector_code: string; sector_name: string; strength: 'strong' | 'neutral' | 'weak'; outlook: string; risk: string }>
  hotspot_reviews?: Array<{ sector_code: string; verdict: 'hit' | 'miss' | 'mixed'; assessment: string }>
  recommendation_reviews?: Array<{ recommendation_date: string; symbol: string; name: string; verdict: 'hit' | 'miss' | 'watching'; performance: string; attribution: string; next_action: string }>
  previous_directive_reviews?: Array<{ action: string; verdict: 'effective' | 'ineffective' | 'unclear'; comment: string }>
  what_worked?: string[]
  what_failed?: string[]
  directives?: Array<{ action: string; rationale: string }>
  risk_controls?: { position_mode: 'aggressive' | 'balanced' | 'defensive'; max_position_pct: number; max_single_stock_pct: number; stop_loss_pct: number; avoid_conditions: string[] }
  facts?: {
    trade_date: string
    indices: Array<{ symbol: string; name: string; price: number; change_pct: number; amount: number }>
    breadth: { stock_count: number; up_count: number; flat_count: number; down_count: number; limit_up_count: number; limit_down_count: number; up_ratio: number; avg_change_pct: number; total_amount: number }
    // 操作姿态：本地按近 20 日等权大盘历史数据确定性推演，不经 AI（旧记录可能缺失）
    market_stance?: { stance: 'take_profit' | 'hold' | 'accumulate'; lookback_days: number; momentum_5d_pct: number; drawdown_pct: number; rebound_pct: number; up_ratio_today: number; up_ratio_5d: number; reason: string }
    strong_sectors: DailyReviewSectorFact[]
    weak_sectors: DailyReviewSectorFact[]
    hotspot_checks: DailyReviewHotspotFact[]
    latest_recommendations: DailyReviewRecommendationFact[]
    previous_review: { review_date: string; market_phase: 'up' | 'range' | 'down' | ''; directives: Array<{ action: string; rationale: string }> }
    recent_recommendation_stats: RecommendationStats
  }
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
  recommendationPerformance: (days = 5) => req<RecommendationPerformance[]>(`/recommendations/performance?days=${days}`),
  recommendationStats: (days = 60) => req<RecommendationStats>(`/recommendations/stats?days=${days}`),
  recommendationRiskPolicy: () => req<RecommendationRiskPolicy>('/recommendations/risk-policy'),
  recommendationShadowStats: (days = 60) => req<RecommendationShadowStats[]>(`/recommendations/shadow-stats?days=${days}`),
  recommendationStatus: () => req<{ enabled: boolean; running: boolean; last_error: string }>('/recommendations/status'),
  runRecommendations: () => req<{ status: string }>('/recommendations/run', { method: 'POST' }),
  recommendationMonteCarlo: (code: string, days = 10) => req<MonteCarloResult>(`/recommendations/montecarlo/${encodeURIComponent(code)}?days=${days}`),
  entryAdvice: (date = '') => req<EntryAdviceResponse>(`/entry/advice${date ? `?date=${encodeURIComponent(date)}` : ''}`),
  positions: (limit = 30) => req<{ items: Position[] }>(`/positions?limit=${limit}`),
  entryStatus: () => req<{ enabled: boolean; running: boolean; last_error: string }>('/entry/status'),
  runEntryAnalysis: () => req<{ status: string }>('/entry/run', { method: 'POST' }),
  hotspot: (id?: number) => req<HotspotReport>(`/hotspot${id ? `?id=${id}` : ''}`),
  hotspotHistory: (limit = 30) => req<HotspotRunSummary[]>(`/hotspot/history?limit=${limit}`),
  hotspotStatus: () => req<{ enabled: boolean; running: boolean }>('/hotspot/status'),
  runHotspot: () => req<{ status: string }>('/hotspot/run', { method: 'POST' }),
  dailyReview: (id?: number) => req<DailyReviewReport>(`/review${id ? `?id=${id}` : ''}`),
  dailyReviewHistory: (limit = 30) => req<DailyReviewRunSummary[]>(`/review/history?limit=${limit}`),
  dailyReviewStatus: () => req<{ enabled: boolean; running: boolean; last_error: string }>('/review/status'),
  runDailyReview: () => req<{ status: string }>('/review/run', { method: 'POST' }),
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
