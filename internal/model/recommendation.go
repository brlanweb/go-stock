package model

// StockRecommendation 是 AI 对趋势延续概率的结构化结果。
//
// 收益展示分为真实手动交易与固定参考窗口：
//  1. 每日唯一最强推荐建立 position 记录。只有用户手动确认建仓/平仓才推进
//     holding/exited，并按确认时的行情参考价计算和冻结收益。
//  2. pending_entry / expired 从未建仓，不产生真实收益样本；holding 只展示按最新
//     市场快照计算的浮动收益，不进入胜率和已实现累计收益。
//  3. 其余两只无 position 的推荐标记为 ReferenceOnly：按推荐后首个交易日开盘
//     至第 10 个交易日收盘计算，窗口未满时暂随最新收盘更新；不进入真实交易统计。
type StockRecommendation struct {
	Date        string  `json:"date"`
	Rank        int     `json:"rank"`
	Symbol      string  `json:"symbol"`
	Code        string  `json:"code"`
	Name        string  `json:"name"`
	Sector      string  `json:"sector"`
	Probability float64 `json:"probability"`
	// RiskScore 是本地确定性计算的 0-100 风险分（波动率/回撤/短期过热）。
	// 只供页面展示和用户自行判断，不参与候选过滤、排序或 AI 选举。
	RiskScore   *float64 `json:"risk_score"`
	Reason      string   `json:"reason"`
	Model       string   `json:"model"`
	EntryPrice  *float64 `json:"entry_price"`
	LatestPrice *float64 `json:"latest_price"`
	ChangePct   *float64 `json:"change_pct"`
	TrackedDays int      `json:"tracked_days"`
	// Exited 表示真实 position 已由用户手动确认平仓；ExitReason 为平仓原因。
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
