package store

import (
	"context"
	"database/sql"
	"fmt"
	"math"
)

const (
	DefaultTradeAmount = 1_000_000.0
	BuyCommissionRate  = 0.0003
	SellFeeRate        = 0.0008
)

type TradeAccount struct {
	InitialCash   float64 `json:"initial_cash"`
	Cash          float64 `json:"cash"`
	MarketValue   float64 `json:"market_value"`
	TotalAssets   float64 `json:"total_assets"`
	RealizedPnl   float64 `json:"realized_pnl"`
	UnrealizedPnl float64 `json:"unrealized_pnl"`
	TotalPnl      float64 `json:"total_pnl"`
	TotalFee      float64 `json:"total_fee"`
	BuyCount      int     `json:"buy_count"`
	SellCount     int     `json:"sell_count"`
}

type TradeResult struct {
	PositionID  int64   `json:"position_id"`
	Symbol      string  `json:"symbol"`
	Side        string  `json:"side"`
	Price       float64 `json:"price"`
	Shares      int     `json:"shares"`
	Amount      float64 `json:"amount"`
	Fee         float64 `json:"fee"`
	Cash        float64 `json:"cash"`
	RealizedPnl float64 `json:"realized_pnl"`
	Status      string  `json:"status"`
}

type TradeOrder struct {
	ID          int64   `json:"id"`
	PositionID  int64   `json:"position_id"`
	Symbol      string  `json:"symbol"`
	Code        string  `json:"code"`
	Name        string  `json:"name"`
	Side        string  `json:"side"`
	TradeDate   string  `json:"trade_date"`
	Price       float64 `json:"price"`
	Shares      int     `json:"shares"`
	Amount      float64 `json:"amount"`
	Fee         float64 `json:"fee"`
	CashDelta   float64 `json:"cash_delta"`
	RealizedPnl float64 `json:"realized_pnl"`
	Note        string  `json:"note"`
	CreatedAt   string  `json:"created_at"`
}

func minimumCommission(amount, rate float64) float64 { return math.Max(5, amount*rate) }

func roundLotShares(amount, price float64) int {
	if amount <= 0 || price <= 0 {
		return 0
	}
	return int(math.Floor(amount/price/100)) * 100
}

func (s *Store) IsWatchlisted(ctx context.Context, symbol string) (bool, error) {
	var exists int
	err := s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM watchlist WHERE symbol=?)`, symbol).Scan(&exists)
	return exists == 1, err
}

// ManuallyBuySymbol 仅接受已加入自选的标的。AI 建议不调用此方法，确保 AI 永远不能改变仓位。
func (s *Store) ManuallyBuySymbol(ctx context.Context, symbol, tradeDate string, price float64) (TradeResult, error) {
	if price <= 0 {
		return TradeResult{}, fmt.Errorf("建仓价格必须大于 0")
	}
	shares := roundLotShares(DefaultTradeAmount, price)
	if shares <= 0 {
		return TradeResult{}, fmt.Errorf("当前价格过高，100万元不足以买入一手")
	}
	amount := float64(shares) * price
	fee := minimumCommission(amount, BuyCommissionRate)
	cost := amount + fee

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return TradeResult{}, err
	}
	defer tx.Rollback()
	var watchlisted int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM watchlist WHERE symbol=?)`, symbol).Scan(&watchlisted); err != nil {
		return TradeResult{}, err
	}
	if watchlisted != 1 {
		return TradeResult{}, fmt.Errorf("请先将 %s 加入自选后再建仓", symbol)
	}
	var activeID int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM position WHERE symbol=? AND status=? LIMIT 1 FOR UPDATE`, symbol, PositionHolding).Scan(&activeID)
	if err == nil {
		return TradeResult{}, fmt.Errorf("%s 已有持仓，不能重复建仓", symbol)
	}
	if err != sql.ErrNoRows {
		return TradeResult{}, err
	}
	var cash float64
	if err := tx.QueryRowContext(ctx, `SELECT cash FROM trade_account WHERE id=1 FOR UPDATE`).Scan(&cash); err != nil {
		return TradeResult{}, err
	}
	if cash < cost {
		return TradeResult{}, fmt.Errorf("可用余额不足：需要 %.2f 元，当前 %.2f 元", cost, cash)
	}
	// 手动建仓必须衔接 AI 推荐生命周期，否则首页统计（按 symbol+analysis_date
	// 匹配 stock_recommendation）永远看不到这笔真实交易：
	//   1. 已有 pending_entry（盘前推荐入池）时直接在原记录上确认建仓，既避免
	//      同一 symbol 出现重复生命周期，也避免入池记录白白过期；
	//   2. 否则回溯最近 7 个自然日内该标的的推荐日作为 analysis_date，覆盖
	//      「推荐次日甚至隔周末才买入」的常见节奏；同一推荐日已有生命周期时
	//      不重复挂靠（卖出后再买属于用户自主的新交易，不能算推荐的第二次机会）；
	//   3. 从未被推荐过的标的保持原行为：analysis_date=建仓日，不进推荐统计。
	var positionID int64
	var pendingID int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM position WHERE symbol=? AND status=? ORDER BY id DESC LIMIT 1 FOR UPDATE`,
		symbol, PositionPendingEntry).Scan(&pendingID)
	switch {
	case err == nil:
		if _, err := tx.ExecContext(ctx, `UPDATE position SET status=?,entry_date=?,entry_price=?,highest_price=?,lowest_price=?,
			hold_days=1,position_pct=100,realized_pct=0,shares=?,buy_shares=?,buy_amount=?,fee_amount=? WHERE id=?`,
			PositionHolding, tradeDate, price, price, price, shares, shares, cost, fee, pendingID); err != nil {
			return TradeResult{}, err
		}
		positionID = pendingID
	case err == sql.ErrNoRows:
		analysisDate := tradeDate
		var recoDate sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT DATE_FORMAT(MAX(r.analysis_date),'%Y-%m-%d') FROM stock_recommendation r
			WHERE r.symbol=? AND r.analysis_date BETWEEN DATE_SUB(?, INTERVAL 7 DAY) AND ?
			AND NOT EXISTS(SELECT 1 FROM position p WHERE p.symbol=r.symbol AND p.analysis_date=r.analysis_date)`,
			symbol, tradeDate, tradeDate).Scan(&recoDate); err != nil {
			return TradeResult{}, err
		}
		if recoDate.Valid && recoDate.String != "" {
			analysisDate = recoDate.String
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO position
			(symbol,pick_date,analysis_date,status,entry_date,entry_price,highest_price,lowest_price,hold_days,
			 position_pct,realized_pct,shares,buy_shares,buy_amount,fee_amount)
			 VALUES (?,?,?,?,?,?,?,?,1,100,0,?,?,?,?)`,
			symbol, tradeDate, analysisDate, PositionHolding, tradeDate, price, price, price,
			shares, shares, cost, fee)
		if err != nil {
			return TradeResult{}, err
		}
		if positionID, err = result.LastInsertId(); err != nil {
			return TradeResult{}, err
		}
	default:
		return TradeResult{}, err
	}
	note := fmt.Sprintf("用户手动建仓，默认买入100万元，实际成交%d股", shares)
	if _, err := tx.ExecContext(ctx, `INSERT INTO trade_order
		(position_id,symbol,side,trade_date,price,shares,amount,fee,cash_delta,note)
		VALUES (?,?,?,?,?,?,?,?,?,?)`, positionID, symbol, "buy", tradeDate, price, shares, amount, fee, -cost, note); err != nil {
		return TradeResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE trade_account SET cash=cash-?,total_fee=total_fee+?,buy_count=buy_count+1 WHERE id=1`, cost, fee); err != nil {
		return TradeResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO entry_advice
		(trade_date,symbol,source,stage,action,reason,urgency,ref_price,model_name)
		VALUES (?,?,?,?,?,?,?,?,?)`, tradeDate, symbol, EntrySourceManual, EntryStageEntry, EntryActionEntry, note, EntryUrgencyNormal, price, "manual"); err != nil {
		return TradeResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return TradeResult{}, err
	}
	return TradeResult{PositionID: positionID, Symbol: symbol, Side: "buy", Price: price, Shares: shares, Amount: amount, Fee: fee, Cash: cash - cost, Status: PositionHolding}, nil
}

// ManuallySellSymbol 按现价最多卖出 100 万元市值；不足 100 万时卖出全部剩余股数。
func (s *Store) ManuallySellSymbol(ctx context.Context, symbol, tradeDate string, price float64) (TradeResult, error) {
	if price <= 0 {
		return TradeResult{}, fmt.Errorf("平仓价格必须大于 0")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return TradeResult{}, err
	}
	defer tx.Rollback()
	var id int64
	var entryDate string
	var heldShares, buyShares int
	var buyAmount, realized float64
	if err := tx.QueryRowContext(ctx, `SELECT id,DATE_FORMAT(entry_date,'%Y-%m-%d'),shares,buy_shares,buy_amount,realized_pnl
		FROM position WHERE symbol=? AND status=? ORDER BY id DESC LIMIT 1 FOR UPDATE`, symbol, PositionHolding).
		Scan(&id, &entryDate, &heldShares, &buyShares, &buyAmount, &realized); err != nil {
		if err == sql.ErrNoRows {
			return TradeResult{}, fmt.Errorf("%s 当前没有可卖持仓", symbol)
		}
		return TradeResult{}, err
	}
	if entryDate == tradeDate {
		return TradeResult{}, fmt.Errorf("A股 T+1 限制：建仓当日不能平仓")
	}
	shares := heldShares
	if float64(heldShares)*price > DefaultTradeAmount {
		shares = roundLotShares(DefaultTradeAmount, price)
	}
	if shares <= 0 {
		return TradeResult{}, fmt.Errorf("没有可卖股数")
	}
	amount := float64(shares) * price
	fee := minimumCommission(amount, SellFeeRate)
	proceeds := amount - fee
	avgCost := buyAmount / float64(buyShares)
	tradePnl := proceeds - avgCost*float64(shares)
	remaining := heldShares - shares
	status := PositionHolding
	var exitDate any
	var exitPrice any
	exitReason := ""
	if remaining == 0 {
		status = PositionExited
		exitDate, exitPrice = tradeDate, price
		exitReason = "用户手动卖出全部剩余持仓"
	}
	positionPct := float64(remaining) / float64(buyShares) * 100
	newRealized := realized + tradePnl
	realizedPct := newRealized / buyAmount * 100
	if _, err := tx.ExecContext(ctx, `UPDATE position SET shares=?,position_pct=?,realized_pnl=?,realized_pct=?,
		sell_amount=sell_amount+?,fee_amount=fee_amount+?,status=?,exit_date=?,exit_price=?,exit_reason=?,exit_kind=? WHERE id=?`,
		remaining, positionPct, newRealized, realizedPct, proceeds, fee, status, exitDate, exitPrice, exitReason, ExitKindManual, id); err != nil {
		return TradeResult{}, err
	}
	note := fmt.Sprintf("用户手动平仓，默认卖出100万元市值，实际成交%d股", shares)
	if _, err := tx.ExecContext(ctx, `INSERT INTO trade_order
		(position_id,symbol,side,trade_date,price,shares,amount,fee,cash_delta,realized_pnl,note)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`, id, symbol, "sell", tradeDate, price, shares, amount, fee, proceeds, tradePnl, note); err != nil {
		return TradeResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE trade_account SET cash=cash+?,realized_pnl=realized_pnl+?,total_fee=total_fee+?,sell_count=sell_count+1 WHERE id=1`, proceeds, tradePnl, fee); err != nil {
		return TradeResult{}, err
	}
	var cash float64
	if err := tx.QueryRowContext(ctx, `SELECT cash FROM trade_account WHERE id=1`).Scan(&cash); err != nil {
		return TradeResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO entry_advice
		(trade_date,symbol,source,stage,action,reason,urgency,ref_price,model_name)
		VALUES (?,?,?,?,?,?,?,?,?)`, tradeDate, symbol, EntrySourceManual, EntryStageExit, EntryActionExit, note, EntryUrgencyNormal, price, "manual"); err != nil {
		return TradeResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return TradeResult{}, err
	}
	return TradeResult{PositionID: id, Symbol: symbol, Side: "sell", Price: price, Shares: shares, Amount: amount, Fee: fee, Cash: cash, RealizedPnl: tradePnl, Status: status}, nil
}

func (s *Store) TradeAccountOverview(ctx context.Context) (TradeAccount, error) {
	var out TradeAccount
	if err := s.DB.QueryRowContext(ctx, `SELECT initial_cash,cash,realized_pnl,total_fee,buy_count,sell_count FROM trade_account WHERE id=1`).
		Scan(&out.InitialCash, &out.Cash, &out.RealizedPnl, &out.TotalFee, &out.BuyCount, &out.SellCount); err != nil {
		return out, err
	}
	positions, err := s.ActivePositions(ctx)
	if err != nil {
		return out, err
	}
	for _, position := range positions {
		if position.Status != PositionHolding || position.Shares <= 0 {
			continue
		}
		if position.MarketValue != nil {
			out.MarketValue += *position.MarketValue
		}
		if position.UnrealizedPnl != nil {
			out.UnrealizedPnl += *position.UnrealizedPnl
		}
	}
	out.TotalAssets = out.Cash + out.MarketValue
	out.TotalPnl = out.TotalAssets - out.InitialCash
	return out, nil
}

func (s *Store) RecentTradeOrders(ctx context.Context, limit int) ([]TradeOrder, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT o.id,o.position_id,o.symbol,COALESCE(b.code,''),COALESCE(b.name,''),o.side,
		DATE_FORMAT(o.trade_date,'%Y-%m-%d'),o.price,o.shares,o.amount,o.fee,o.cash_delta,o.realized_pnl,o.note,
		DATE_FORMAT(o.created_at,'%Y-%m-%d %H:%i')
		FROM trade_order o LEFT JOIN stock_basic b ON b.symbol=o.symbol ORDER BY o.id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TradeOrder{}
	for rows.Next() {
		var item TradeOrder
		if err := rows.Scan(&item.ID, &item.PositionID, &item.Symbol, &item.Code, &item.Name, &item.Side,
			&item.TradeDate, &item.Price, &item.Shares, &item.Amount, &item.Fee, &item.CashDelta,
			&item.RealizedPnl, &item.Note, &item.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
