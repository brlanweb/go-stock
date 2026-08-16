package model

// StockRecommendation 是 AI 对趋势延续概率的结构化结果。
//
// 收益展示口径只认真实持仓生命周期：
//  1. 每日推荐中被选为最佳建仓股的一只会建立 position 记录。该股以盘中 AI
//     实际给出的建仓参考价为成本；AI 或确定性硬风控判定退出后，收益按退出
//     参考价冻结（Exited=true），后续行情不再改变结果。
//  2. pending_entry / expired 从未建仓，不产生收益样本；holding 只展示按最新
//     市场快照计算的浮动收益，不进入胜率和已实现累计收益。
//  3. 没有 position 记录的历史推荐仅补充“参考走势”（ReferenceOnly=true）：
//     按推荐日后首个交易日开盘与趋势退出规则展示涨跌，供复盘参考，
//     Settled 恒为 false，不进入胜率、已实现收益或浮盈统计。
type StockRecommendation struct {
	Date        string  `json:"date"`
	Rank        int     `json:"rank"`
	Symbol      string  `json:"symbol"`
	Code        string  `json:"code"`
	Name        string  `json:"name"`
	Sector      string  `json:"sector"`
	Probability float64 `json:"probability"`
	// RiskScore 是本地确定性计算的 0-100 风险分（波动率/回撤/短期过热），
	// 非 AI 输出；超过阈值的候选在进入 AI 评审前已被剔除。
	RiskScore   *float64 `json:"risk_score"`
	Reason      string   `json:"reason"`
	Model       string   `json:"model"`
	EntryPrice  *float64 `json:"entry_price"`
	LatestPrice *float64 `json:"latest_price"`
	ChangePct   *float64 `json:"change_pct"`
	TrackedDays int      `json:"tracked_days"`
	// Exited 只表示真实 position 已按 AI/硬风控信号退出；ExitReason 为退出原因。
	Exited     bool   `json:"exited"`
	ExitReason string `json:"exit_reason,omitempty"`
	// PositionStatus 是该股的真实持仓状态；为空表示只存在推荐历史，未发生交易。
	PositionStatus string `json:"position_status,omitempty"`
	// DataQuality 非空表示该样本存在已知失真（如 t0_violation：当日进出违反 T+1），
	// 保留展示与审计价值，但一律不进入胜率、收益与考核统计。
	DataQuality string `json:"data_quality,omitempty"`
	// Settled 表示该股产生了有效收益样本并计入统计。
	// 未建仓（pending_entry/expired）时为 false，收益字段不参与胜率与合计。
	Settled bool `json:"settled"`
	// ReferenceOnly 表示收益字段来自无生命周期记录的“参考走势”口径，
	// 仅供历史复盘展示，不代表实际交易结果。
	ReferenceOnly bool `json:"reference_only,omitempty"`
}
