package backtest

import "github.com/hoax/go-stock/internal/model"

type Request struct {
	Symbol      string         `json:"symbol"`
	IndicatorID string         `json:"indicator_id"`
	Period      string         `json:"period"`
	Start       string         `json:"start"`
	End         string         `json:"end"`
	InitialCash float64        `json:"initial_cash"`
	Params      map[string]any `json:"params"`
}

type Signal struct {
	Date   string  `json:"date"`
	Action string  `json:"action"`
	Price  float64 `json:"price"`
	Reason string  `json:"reason"`
}

type Trade struct {
	EntryDate   string  `json:"entry_date"`
	EntryPrice  float64 `json:"entry_price"`
	ExitDate    string  `json:"exit_date,omitempty"`
	ExitPrice   float64 `json:"exit_price,omitempty"`
	Shares      int64   `json:"shares"`
	PnL         float64 `json:"pnl"`
	ReturnPct   float64 `json:"return_pct"`
	EntryReason string  `json:"entry_reason"`
	ExitReason  string  `json:"exit_reason"`
}

type Result struct {
	RunID           int64          `json:"run_id"`
	Symbol          string         `json:"symbol"`
	IndicatorID     string         `json:"indicator_id"`
	Period          string         `json:"period"`
	Start           string         `json:"start"`
	End             string         `json:"end"`
	InitialCash     float64        `json:"initial_cash"`
	FinalEquity     float64        `json:"final_equity"`
	TotalReturn     float64        `json:"total_return"`
	AnnualReturn    float64        `json:"annual_return"`
	MaxDrawdown     float64        `json:"max_drawdown"`
	SharpeRatio     float64        `json:"sharpe_ratio"`
	WinRate         float64        `json:"win_rate"`
	ProfitLossRatio float64        `json:"profit_loss_ratio"`
	ProfitFactor    float64        `json:"profit_factor"`
	TradeCount      int            `json:"trade_count"`
	Signals         []Signal       `json:"signals"`
	Trades          []Trade        `json:"trades"`
	Params          map[string]any `json:"params"`
	Klines          []model.Kline  `json:"klines,omitempty"`
}
