package store

import (
	"context"
	"database/sql"
)

// PositionReview 是单笔离场后的结构化复盘。收益/MFE/MAE/捕获率全部由本地数据
// 确定性计算；blame_stage 是受限枚举，避免自由文本把所有失败都含糊归因市场。
type PositionReview struct {
	ID             int64    `json:"id"`
	PositionID     int64    `json:"position_id"`
	Symbol         string   `json:"symbol"`
	Code           string   `json:"code"`
	Name           string   `json:"name"`
	ReviewDate     string   `json:"review_date"`
	Verdict        string   `json:"verdict"`
	BlameStage     string   `json:"blame_stage"`
	NetChangePct   float64  `json:"net_change_pct"`
	MFEPct         *float64 `json:"mfe_pct,omitempty"`
	MAEPct         *float64 `json:"mae_pct,omitempty"`
	CaptureRatePct *float64 `json:"capture_rate_pct,omitempty"`
	PostExit5DPct  *float64 `json:"post_exit_5d_pct,omitempty"`
	ExitKind       string   `json:"exit_kind"`
	Reason         string   `json:"reason"`
	DataQuality    string   `json:"data_quality,omitempty"`
	GeneratedBy    string   `json:"generated_by"`
	CreatedAt      string   `json:"created_at"`
}

// savePositionReviewTx 在持仓退出事务中生成确定性复盘，保证 position exited 与复盘底稿原子一致。
func savePositionReviewTx(ctx context.Context, tx *sql.Tx, positionID int64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO position_review
		(position_id,symbol,review_date,verdict,blame_stage,net_change_pct,mfe_pct,mae_pct,capture_rate_pct,exit_kind,reason,data_quality,generated_by)
	 SELECT p.id,p.symbol,p.exit_date,
		CASE WHEN (((p.exit_price/p.entry_price-1)*100*p.position_pct/100)+p.realized_pct-? )>0 THEN 'success'
		     WHEN (((p.exit_price/p.entry_price-1)*100*p.position_pct/100)+p.realized_pct-? )<0 THEN 'failure'
		     ELSE 'neutral' END,
		CASE WHEN p.exit_kind=? THEN 'market'
		     WHEN p.exit_kind=? THEN 'entry'
		     WHEN p.exit_kind=? THEN 'opportunity'
		     WHEN p.highest_price IS NOT NULL AND (p.highest_price/p.entry_price-1)*100>=5
		          AND (((p.exit_price/p.entry_price-1)*100*p.position_pct/100)+p.realized_pct-? )<=0 THEN 'exit'
		     WHEN (((p.exit_price/p.entry_price-1)*100*p.position_pct/100)+p.realized_pct-? )<=0 THEN 'selection'
		     ELSE 'exit' END,
		(((p.exit_price/p.entry_price-1)*100*p.position_pct/100)+p.realized_pct-? ),
		CASE WHEN p.highest_price IS NULL THEN NULL ELSE (p.highest_price/p.entry_price-1)*100 END,
		CASE WHEN p.lowest_price IS NULL THEN NULL ELSE (p.lowest_price/p.entry_price-1)*100 END,
		CASE WHEN p.highest_price IS NULL OR p.highest_price<=p.entry_price THEN NULL
		     ELSE ((((p.exit_price/p.entry_price-1)*100*p.position_pct/100)+p.realized_pct-? )/((p.highest_price/p.entry_price-1)*100))*100 END,
		p.exit_kind,
		CASE WHEN p.exit_kind=? THEN '系统性风险触发退出，主要归因市场环境'
		     WHEN p.exit_kind=? THEN '建仓后触发硬止损，优先检查建仓位置与波动适配'
		     WHEN p.exit_kind=? THEN '动量在有效期内未兑现，机会判断未形成正向优势'
		     WHEN p.highest_price IS NOT NULL AND (p.highest_price/p.entry_price-1)*100>=5
		          AND (((p.exit_price/p.entry_price-1)*100*p.position_pct/100)+p.realized_pct-? )<=0 THEN '持仓曾有明显浮盈但最终亏损，离场未保护已有优势'
		     WHEN (((p.exit_price/p.entry_price-1)*100*p.position_pct/100)+p.realized_pct-? )<=0 THEN '持仓期未形成有效正收益，选股优势不足'
		     ELSE '交易实现正收益，继续评估MFE捕获率与离场后续涨' END,
		p.data_quality,'rule'
	 FROM position p WHERE p.id=? AND p.status=? AND p.entry_price>0 AND p.exit_price>0
	 ON DUPLICATE KEY UPDATE
		verdict=VALUES(verdict),blame_stage=VALUES(blame_stage),net_change_pct=VALUES(net_change_pct),
		mfe_pct=VALUES(mfe_pct),mae_pct=VALUES(mae_pct),capture_rate_pct=VALUES(capture_rate_pct),
		exit_kind=VALUES(exit_kind),reason=VALUES(reason),data_quality=VALUES(data_quality),updated_at=CURRENT_TIMESTAMP`,
		PositionRoundTripCostPct, PositionRoundTripCostPct,
		ExitKindSystemic, ExitKindStopLoss, ExitKindTimeStop,
		PositionRoundTripCostPct, PositionRoundTripCostPct, PositionRoundTripCostPct, PositionRoundTripCostPct,
		ExitKindSystemic, ExitKindStopLoss, ExitKindTimeStop,
		PositionRoundTripCostPct, PositionRoundTripCostPct,
		positionID, PositionExited)
	return err
}

// RefreshPositionReviewPostExit 回填已经走完 5 个交易日窗口的离场后表现。
// 未成熟窗口保持 NULL，不把不完整样本误当成 0。
func (s *Store) RefreshPositionReviewPostExit(ctx context.Context, limit int) error {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT r.position_id,r.symbol,DATE_FORMAT(r.review_date,'%Y-%m-%d'),p.exit_price
		FROM position_review r JOIN position p ON p.id=r.position_id
		WHERE r.post_exit_5d_pct IS NULL AND r.data_quality='' AND p.exit_price>0
		ORDER BY r.review_date ASC LIMIT ?`, limit)
	if err != nil {
		return err
	}
	type pending struct {
		id           int64
		symbol, date string
		exitPrice    float64
	}
	var items []pending
	for rows.Next() {
		var item pending
		if err := rows.Scan(&item.id, &item.symbol, &item.date, &item.exitPrice); err != nil {
			rows.Close()
			return err
		}
		items = append(items, item)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, item := range items {
		bars, err := s.DailyBarsAfter(ctx, item.symbol, item.date, MechanicalHoldDays)
		if err != nil {
			return err
		}
		if len(bars) < MechanicalHoldDays || item.exitPrice <= 0 {
			continue
		}
		pct := (bars[len(bars)-1].Close/item.exitPrice - 1) * 100
		if _, err := s.DB.ExecContext(ctx, `UPDATE position_review SET post_exit_5d_pct=? WHERE position_id=? AND post_exit_5d_pct IS NULL`, pct, item.id); err != nil {
			return err
		}
	}
	return nil
}

// RecentPositionReviews 返回最近单笔离场复盘，并排除数量无限增长。
func (s *Store) RecentPositionReviews(ctx context.Context, limit int) ([]PositionReview, error) {
	if limit <= 0 || limit > 200 {
		limit = 30
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT r.id,r.position_id,r.symbol,COALESCE(b.code,''),COALESCE(b.name,''),
		DATE_FORMAT(r.review_date,'%Y-%m-%d'),r.verdict,r.blame_stage,r.net_change_pct,
		r.mfe_pct,r.mae_pct,r.capture_rate_pct,r.post_exit_5d_pct,r.exit_kind,r.reason,
		r.data_quality,r.generated_by,DATE_FORMAT(r.created_at,'%Y-%m-%d %H:%i')
		FROM position_review r LEFT JOIN stock_basic b ON b.symbol=r.symbol
		ORDER BY r.review_date DESC,r.id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PositionReview{}
	for rows.Next() {
		var item PositionReview
		var mfe, mae, capture, post sql.NullFloat64
		if err := rows.Scan(&item.ID, &item.PositionID, &item.Symbol, &item.Code, &item.Name,
			&item.ReviewDate, &item.Verdict, &item.BlameStage, &item.NetChangePct,
			&mfe, &mae, &capture, &post, &item.ExitKind, &item.Reason,
			&item.DataQuality, &item.GeneratedBy, &item.CreatedAt); err != nil {
			return nil, err
		}
		if mfe.Valid {
			item.MFEPct = &mfe.Float64
		}
		if mae.Valid {
			item.MAEPct = &mae.Float64
		}
		if capture.Valid {
			item.CaptureRatePct = &capture.Float64
		}
		if post.Valid {
			item.PostExit5DPct = &post.Float64
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
