package model

// StockRecommendation 是 AI 对未来十个交易日趋势延续概率的结构化结果。
// 收益展示口径：以加入日开盘价为买入基准，只追踪加入日起 5 个交易日窗口内的
// 最后收盘价；窗口结束后数据冻结，不再随最新行情变化。
type StockRecommendation struct {
	Date        string   `json:"date"`
	Rank        int      `json:"rank"`
	Symbol      string   `json:"symbol"`
	Code        string   `json:"code"`
	Name        string   `json:"name"`
	Sector      string   `json:"sector"`
	Probability float64  `json:"probability"`
	Reason      string   `json:"reason"`
	Model       string   `json:"model"`
	EntryPrice  *float64 `json:"entry_price"`
	LatestPrice *float64 `json:"latest_price"`
	ChangePct   *float64 `json:"change_pct"`
	TrackedDays int      `json:"tracked_days"`
}
