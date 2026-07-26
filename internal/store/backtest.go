package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hoax/go-stock/internal/backtest"
)

func (s *Store) SaveBacktest(ctx context.Context, result *backtest.Result) error {
	params, _ := json.Marshal(result.Params)
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	row, err := tx.ExecContext(ctx, `INSERT INTO backtest_run
		(symbol,indicator_id,period,start_date,end_date,initial_cash,final_equity,total_return,annual_return,max_drawdown,sharpe_ratio,win_rate,profit_loss_ratio,profit_factor,trade_count,params)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, result.Symbol, result.IndicatorID, result.Period, result.Start, result.End, result.InitialCash, result.FinalEquity, result.TotalReturn, result.AnnualReturn, result.MaxDrawdown, result.SharpeRatio, result.WinRate, result.ProfitLossRatio, result.ProfitFactor, result.TradeCount, string(params))
	if err != nil {
		return fmt.Errorf("保存回测结果失败: %w", err)
	}
	result.RunID, err = row.LastInsertId()
	if err != nil {
		return err
	}
	for _, trade := range result.Trades {
		var exitDate, exitPrice any
		if trade.ExitDate != "" {
			exitDate, exitPrice = trade.ExitDate, trade.ExitPrice
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO backtest_trade
			(run_id,entry_date,entry_price,exit_date,exit_price,shares,pnl,return_pct,entry_reason,exit_reason)
			VALUES (?,?,?,?,?,?,?,?,?,?)`, result.RunID, trade.EntryDate, trade.EntryPrice, exitDate, exitPrice, trade.Shares, trade.PnL, trade.ReturnPct, trade.EntryReason, trade.ExitReason); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) BacktestHistory(ctx context.Context, symbol string, limit int) ([]backtest.Result, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.DB.QueryContext(ctx, fmt.Sprintf(`SELECT run_id,symbol,indicator_id,period,DATE_FORMAT(start_date,'%%Y-%%m-%%d'),DATE_FORMAT(end_date,'%%Y-%%m-%%d'),initial_cash,final_equity,total_return,annual_return,max_drawdown,sharpe_ratio,win_rate,profit_loss_ratio,profit_factor,trade_count,params FROM backtest_run WHERE symbol=? ORDER BY run_id DESC LIMIT %d`, limit), symbol)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]backtest.Result, 0)
	for rows.Next() {
		var item backtest.Result
		var params []byte
		if err := rows.Scan(&item.RunID, &item.Symbol, &item.IndicatorID, &item.Period, &item.Start, &item.End, &item.InitialCash, &item.FinalEquity, &item.TotalReturn, &item.AnnualReturn, &item.MaxDrawdown, &item.SharpeRatio, &item.WinRate, &item.ProfitLossRatio, &item.ProfitFactor, &item.TradeCount, &params); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(params, &item.Params)
		out = append(out, item)
	}
	return out, rows.Err()
}
