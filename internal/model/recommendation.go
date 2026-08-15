package model

// StockRecommendation 是 AI 对趋势延续概率的结构化结果。
//
// 收益展示口径（趋势跟踪，优先真实持仓）：
//  1. 每日推荐中被选为最佳建仓股的一只会建立持仓生命周期记录（position）。
//     该股以盘中 AI 实际给出的建仓价为成本；AI 判定趋势不可持续或大盘风险
//     放大而建议退出后，收益按退出价冻结（Exited=true），不再跟随后续行情。
//     入池后宽限期内始终未建仓（expired）或尚未建仓（pending_entry）的标的
//     不产生收益样本，不计入胜率统计。
//  2. 其余推荐股没有真实持仓记录，沿用技术规则模拟口径：推荐日后首个交易日
//     开盘价买入，收盘跌破 10 日均线视为趋势不再可持续并按当日收盘结算退出，
//     最多追踪 30 个交易日兜底冻结。不采用固定 5 日强制退出。
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
	// Exited 表示趋势跟踪已结算退出（AI 建议退出、跌破趋势线或达到追踪上限），
	// ExitReason 为退出原因；未退出时表示仍在趋势内继续追踪。
	Exited     bool   `json:"exited"`
	ExitReason string `json:"exit_reason,omitempty"`
	// PositionStatus 是该股的真实持仓状态（pending_entry/holding/exited/expired）；
	// 为空表示这只推荐股没有建立持仓记录，收益按技术规则模拟口径展示。
	PositionStatus string `json:"position_status,omitempty"`
	// Settled 表示该股产生了有效收益样本并计入统计。
	// 未建仓（pending_entry/expired）时为 false，收益字段不参与胜率与合计。
	Settled bool `json:"settled"`
}
