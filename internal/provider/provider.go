// Package provider 数据源层：东财(全字段) -> 腾讯 -> 新浪 多源降级 + 熔断 + 限流。
// 设计参考 daily_stock_analysis 的 BaseFetcher/CircuitBreaker（策略模式）。
package provider

import (
	"context"

	"github.com/hoax/go-stock/internal/model"
)

// QuoteProvider 实时行情源接口（预留 US/CRYPTO 市场各自实现）。
type QuoteProvider interface {
	Name() string
	// Quote 单只实时行情。
	Quote(ctx context.Context, symbol string) (*model.Quote, error)
	// BatchQuotes 批量实时行情（腾讯/新浪原生支持一次多只；东财循环单只或用 ulist）。
	BatchQuotes(ctx context.Context, symbols []string) ([]*model.Quote, error)
}

// KlineProvider 历史K线源接口。
type KlineProvider interface {
	Name() string
	// DailyKlines 拉取日K线（不复权 + 前复权，用于推导复权因子）。
	// beg 格式 YYYYMMDD，"0" 表示从上市日开始。
	DailyKlines(ctx context.Context, symbol, beg, end string) ([]model.Kline, error)
}

// ListProvider 全市场列表/快照源接口。
type ListProvider interface {
	// AllSecurities 全市场证券列表（含实时快照字段，用于每日指标）。
	AllSecurities(ctx context.Context) ([]SecuritySnapshot, error)
}

// TimeshareProvider 分时数据源接口。
type TimeshareProvider interface {
	Timeshare(ctx context.Context, symbol string) ([]model.TimesharePoint, error)
}

// SecuritySnapshot 全市场快照行（列表同步 + 每日指标共用）。
type SecuritySnapshot struct {
	Symbol       string
	Code         string
	Name         string
	SecType      model.SecurityType
	Exchange     string
	Price        float64
	ChangePct    float64
	ChangeAmount float64
	Volume       int64
	Amount       float64
	TurnoverRate float64
	VolumeRatio  float64
	Amplitude    float64
	Open         float64
	High         float64
	Low          float64
	PreClose     float64
	PERatio      float64
	PBRatio      float64
	TotalMV      float64
	CircMV       float64
	TotalShare   float64
	FloatShare   float64
	ListDate     string // YYYY-MM-DD，可能为空
	TradeDate    string // 上游行情所属交易日 YYYY-MM-DD，可能为空
	Industry     string
}
