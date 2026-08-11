package model

// StockRecommendation 是 AI 对未来十个交易日趋势延续概率的结构化结果。
// 收益展示口径：推荐在交易日盘前 08:10 生成，最早次日开盘建仓；以推荐日后
// 首个交易日开盘价为买入基准，只追踪其后 5 个交易日窗口内的最后收盘价；
// 窗口结束后数据冻结，不再随最新行情变化。
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
}
