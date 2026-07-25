// Package model 定义统一数据结构（对齐 daily_stock_analysis 的 UnifiedRealtimeQuote 维度）。
// symbol 统一规范：市场前缀+代码，如 SH600519 / SZ000001 / BJ920748 / US.AAPL / CRYPTO.BTCUSDT。
package model

import "time"

// Market 市场标识（预留 US / CRYPTO 扩展）。
type Market string

const (
	MarketCN     Market = "cn"
	MarketUS     Market = "us"
	MarketCrypto Market = "crypto"
)

// SecurityType 证券类型。
type SecurityType string

const (
	SecStock SecurityType = "stock"
	SecETF   SecurityType = "etf"
	SecIndex SecurityType = "index"
)

// Security 证券基础信息（stock_basic 表）。
type Security struct {
	Symbol     string       `json:"symbol"`   // SH600519
	Market     Market       `json:"market"`   // cn
	Code       string       `json:"code"`     // 600519
	Name       string       `json:"name"`     // 贵州茅台
	Type       SecurityType `json:"type"`     // stock/etf/index
	Exchange   string       `json:"exchange"` // SH/SZ/BJ
	Industry   string       `json:"industry,omitempty"`
	ListDate   string       `json:"list_date,omitempty"`   // YYYY-MM-DD
	TotalShare float64      `json:"total_share,omitempty"` // 总股本(股)
	FloatShare float64      `json:"float_share,omitempty"` // 流通股本(股)
	Status     string       `json:"status"`                // listed/delisted/suspended
	UpdatedAt  time.Time    `json:"updated_at"`
}

// Quote 统一实时行情（不落库，内存缓存）。
// 缺失字段使用指针，nil 表示该数据源未提供。
type Quote struct {
	Symbol string `json:"symbol"`
	Code   string `json:"code"`
	Name   string `json:"name"`
	Market Market `json:"market"`

	// 元数据
	Source            string    `json:"source"`                       // eastmoney/tencent/sina
	FetchedAt         time.Time `json:"fetched_at"`                   // 本系统抓取时间
	ProviderTimestamp string    `json:"provider_timestamp,omitempty"` // 行情源时间
	FallbackFrom      string    `json:"fallback_from,omitempty"`      // 降级来源标记
	Currency          string    `json:"currency"`                     // CNY

	// 核心价格
	Price        *float64 `json:"price"`
	ChangePct    *float64 `json:"change_pct"`
	ChangeAmount *float64 `json:"change_amount"`

	// 量价
	Volume       *int64   `json:"volume"`        // 成交量(股)
	Amount       *float64 `json:"amount"`        // 成交额(元)
	VolumeRatio  *float64 `json:"volume_ratio"`  // 量比
	TurnoverRate *float64 `json:"turnover_rate"` // 换手率(%)
	Amplitude    *float64 `json:"amplitude"`     // 振幅(%)

	// 价格区间
	Open     *float64 `json:"open"`
	High     *float64 `json:"high"`
	Low      *float64 `json:"low"`
	PreClose *float64 `json:"pre_close"`

	// 估值（东财全量接口提供）
	PERatio *float64 `json:"pe_ratio"` // 市盈率(动)
	PBRatio *float64 `json:"pb_ratio"` // 市净率
	TotalMV *float64 `json:"total_mv"` // 总市值(元)
	CircMV  *float64 `json:"circ_mv"`  // 流通市值(元)

	// 其他
	Change60D *float64 `json:"change_60d"` // 60日涨跌幅(%)
	High52W   *float64 `json:"high_52w"`
	Low52W    *float64 `json:"low_52w"`

	// 买卖五档（东财/腾讯提供）
	Bids []PriceLevel `json:"bids,omitempty"`
	Asks []PriceLevel `json:"asks,omitempty"`
}

// PriceLevel 盘口档位。
type PriceLevel struct {
	Price  float64 `json:"price"`
	Volume int64   `json:"volume"` // 股
}

// Kline 日K线（kline_daily 表，存不复权原始价 + 当日复权因子）。
type Kline struct {
	Symbol       string  `json:"symbol"`
	Date         string  `json:"date"` // YYYY-MM-DD
	Open         float64 `json:"open"`
	High         float64 `json:"high"`
	Low          float64 `json:"low"`
	Close        float64 `json:"close"`
	Volume       int64   `json:"volume"`        // 股
	Amount       float64 `json:"amount"`        // 元
	ChangePct    float64 `json:"change_pct"`    // %
	TurnoverRate float64 `json:"turnover_rate"` // %
	AdjFactor    float64 `json:"adj_factor"`    // 累积复权因子（不复权=1 起点）
}

// DailyIndicator 每日指标快照（daily_indicator 表）。
type DailyIndicator struct {
	Symbol       string  `json:"symbol"`
	Date         string  `json:"date"`
	Close        float64 `json:"close"`
	PERatio      float64 `json:"pe_ratio"`
	PBRatio      float64 `json:"pb_ratio"`
	TotalMV      float64 `json:"total_mv"`
	CircMV       float64 `json:"circ_mv"`
	TurnoverRate float64 `json:"turnover_rate"`
	VolumeRatio  float64 `json:"volume_ratio"`
}

// TimesharePoint 分时数据点（不落库）。
type TimesharePoint struct {
	Time     string  `json:"time"` // HH:MM
	Price    float64 `json:"price"`
	AvgPrice float64 `json:"avg_price"`
	Volume   int64   `json:"volume"`
}

// IndexQuote 指数实时行情。
type IndexQuote struct {
	Symbol    string   `json:"symbol"` // SH000001
	Name      string   `json:"name"`
	Price     *float64 `json:"price"`
	ChangePct *float64 `json:"change_pct"`
	Amount    *float64 `json:"amount"` // 成交额
	Volume    *int64   `json:"volume"`
}

// SyncCheckpoint 同步断点（sync_checkpoint 表）。
type SyncCheckpoint struct {
	Symbol         string    `json:"symbol"`
	Task           string    `json:"task"`             // backfill_kline / daily_sync
	Status         string    `json:"status"`           // pending/running/done/failed
	LastSyncedDate string    `json:"last_synced_date"` // YYYY-MM-DD
	RetryCount     int       `json:"retry_count"`
	LastError      string    `json:"last_error,omitempty"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// SyncStatus 同步进度汇总。
type SyncStatus struct {
	Task       string `json:"task"`
	Total      int    `json:"total"`
	Done       int    `json:"done"`
	Pending    int    `json:"pending"`
	Running    int    `json:"running"`
	Failed     int    `json:"failed"`
	LatestDate string `json:"latest_date,omitempty"` // 库内最新K线日期
	Complete   int    `json:"complete"`              // 通过首尾覆盖校验的证券数
	Partial    int    `json:"partial"`               // 有数据但历史头部/尾部不完整
	Empty      int    `json:"empty"`                 // 尚无任何日K
}

// HeatmapItem 大盘云图中的单个证券块。
type HeatmapItem struct {
	Symbol        string   `json:"symbol"`
	Code          string   `json:"code"`
	Name          string   `json:"name"`
	Industry      string   `json:"industry"`
	Market        string   `json:"market"`
	ChangePct     float64  `json:"change_pct"`
	PeriodChange  float64  `json:"period_change"`
	PERatio       float64  `json:"pe_ratio"`
	TotalMV       float64  `json:"total_mv"`
	MainNetInflow *float64 `json:"main_net_inflow,omitempty"`
}

// HeatmapGroup 按行业或概念分组的云图数据。
type HeatmapGroup struct {
	Name          string        `json:"name"`
	ChangePct     float64       `json:"change_pct"`
	MainNetInflow *float64      `json:"main_net_inflow,omitempty"`
	Items         []HeatmapItem `json:"items"`
}
