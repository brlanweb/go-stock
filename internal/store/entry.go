package store

import (
	"context"
	"database/sql"
)

// 建议记录的来源、阶段、动作与紧迫度常量（与 018/019 迁移对应）。
const (
	EntrySourceDailyPick = "daily_pick"
	EntrySourceHourlyAI  = "hourly_ai"
	EntrySourceRule      = "rule" // 确定性硬规则兜底产出，不依赖 LLM

	// 阶段：入池首日找建仓机会，建仓之后全程找退出机会。
	EntryStageEntry = "entry"
	EntryStageExit  = "exit"

	EntryActionPick = "pick" // 盘前自动选出的最佳建仓候选（已加入自选）
	// 建仓阶段动作
	EntryActionEntry = "entry" // 给出建仓建议 → 持仓转入 holding
	EntryActionWait  = "wait"  // 暂无合适建仓机会，后续时段继续观察
	// 退出阶段动作
	EntryActionHold   = "hold"   // 趋势仍可持续，继续持有
	EntryActionReduce = "reduce" // 趋势转弱，建议减仓保护收益
	EntryActionExit   = "exit"   // 趋势不可持续/逆转/大盘风险放大 → 清仓退出

	// 紧迫度：urgent 表示当日必须处理，避免承担隔夜跳空风险。
	EntryUrgencyNormal = "normal"
	EntryUrgencyWarn   = "warn"
	EntryUrgencyUrgent = "urgent"
)

// EntryAdvice 是一条 AI 建仓/退出建议记录。
// stage=entry 时 PriceLow/PriceHigh 表示建议建仓价区间；
// stage=exit 时表示建议退出价区间。
type EntryAdvice struct {
	ID        int64    `json:"id"`
	TradeDate string   `json:"trade_date"`
	Symbol    string   `json:"symbol"`
	Name      string   `json:"name"`
	Code      string   `json:"code"`
	Source    string   `json:"source"`
	Stage     string   `json:"stage"`
	Action    string   `json:"action"`
	Reason    string   `json:"reason"`
	PriceLow  *float64 `json:"price_low"`
	PriceHigh *float64 `json:"price_high"`
	Urgency   string   `json:"urgency"`
	RefPrice  *float64 `json:"ref_price"`
	Model     string   `json:"model"`
	CreatedAt string   `json:"created_at"`
}

// EntryAdviceInput 是写入一条建议所需的全部字段。
type EntryAdviceInput struct {
	TradeDate string
	Symbol    string
	Source    string
	Stage     string
	Action    string
	Reason    string
	PriceLow  *float64
	PriceHigh *float64
	Urgency   string
	RefPrice  *float64
	Model     string
}

// SaveEntryAdvice 追加保存一条建议记录（wait/hold 结论也保留，供审计与前端展示）。
func (s *Store) SaveEntryAdvice(ctx context.Context, input EntryAdviceInput) error {
	if input.Stage == "" {
		input.Stage = EntryStageEntry
	}
	if input.Urgency == "" {
		input.Urgency = EntryUrgencyNormal
	}
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO entry_advice (trade_date,symbol,source,stage,action,reason,price_low,price_high,urgency,ref_price,model_name)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		input.TradeDate, input.Symbol, input.Source, input.Stage, input.Action, input.Reason,
		input.PriceLow, input.PriceHigh, input.Urgency, input.RefPrice, input.Model)
	return err
}

// EntryAdviceIssuedForSymbol 判断某只股在给定交易日是否已给出建仓建议。
// 建仓是一次性动作：同一标的当日出现 entry 后不再重复产出建仓信号；
// 退出分析不受此限制，每轮都会重新评估。
func (s *Store) EntryAdviceIssuedForSymbol(ctx context.Context, tradeDate, symbol string) (bool, error) {
	var id int64
	err := s.DB.QueryRowContext(ctx,
		`SELECT id FROM entry_advice WHERE trade_date=? AND symbol=? AND action=? LIMIT 1`,
		tradeDate, symbol, EntryActionEntry).Scan(&id)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// EntryAdviceByDate 返回某交易日的全部建议记录（含盘前最佳候选、建仓与退出结论），
// 按时间倒序；tradeDate 为空时返回最近 limit 条。
func (s *Store) EntryAdviceByDate(ctx context.Context, tradeDate string, limit int) ([]EntryAdvice, error) {
	if limit <= 0 || limit > 200 {
		limit = 60
	}
	query := `SELECT e.id,DATE_FORMAT(e.trade_date,'%Y-%m-%d'),e.symbol,COALESCE(b.name,''),COALESCE(b.code,''),
		e.source,e.stage,e.action,e.reason,e.price_low,e.price_high,e.urgency,e.ref_price,e.model_name,
		DATE_FORMAT(e.created_at,'%Y-%m-%d %H:%i')
		FROM entry_advice e LEFT JOIN stock_basic b ON b.symbol=e.symbol`
	var args []any
	if tradeDate != "" {
		query += " WHERE e.trade_date=?"
		args = append(args, tradeDate)
	}
	query += " ORDER BY e.id DESC LIMIT ?"
	args = append(args, limit)
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EntryAdvice{}
	for rows.Next() {
		var item EntryAdvice
		var priceLow, priceHigh, refPrice sql.NullFloat64
		if err := rows.Scan(&item.ID, &item.TradeDate, &item.Symbol, &item.Name, &item.Code,
			&item.Source, &item.Stage, &item.Action, &item.Reason,
			&priceLow, &priceHigh, &item.Urgency, &refPrice, &item.Model, &item.CreatedAt); err != nil {
			return nil, err
		}
		if priceLow.Valid {
			item.PriceLow = &priceLow.Float64
		}
		if priceHigh.Valid {
			item.PriceHigh = &priceHigh.Float64
		}
		if refPrice.Valid {
			item.RefPrice = &refPrice.Float64
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
