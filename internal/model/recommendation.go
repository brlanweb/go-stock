package model

// StockRecommendation 是 AI 对未来十个交易日趋势延续概率的结构化结果。
type StockRecommendation struct {
	Date        string  `json:"date"`
	Rank        int     `json:"rank"`
	Symbol      string  `json:"symbol"`
	Code        string  `json:"code"`
	Name        string  `json:"name"`
	Sector      string  `json:"sector"`
	Probability float64 `json:"probability"`
	Reason      string  `json:"reason"`
	Model       string  `json:"model"`
}
