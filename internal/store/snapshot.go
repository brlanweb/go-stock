package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hoax/go-stock/internal/model"
	"github.com/hoax/go-stock/internal/provider"
)

// UpsertMarketSnapshots writes one consistent market snapshot produced by a scheduled job.
func (s *Store) UpsertMarketSnapshots(ctx context.Context, capturedAt time.Time, snaps []provider.SecuritySnapshot) error {
	const batchSize = 500
	for start := 0; start < len(snaps); start += batchSize {
		end := start + batchSize
		if end > len(snaps) {
			end = len(snaps)
		}
		if err := s.upsertMarketSnapshotBatch(ctx, capturedAt, snaps[start:end]); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) upsertMarketSnapshotBatch(ctx context.Context, capturedAt time.Time, snaps []provider.SecuritySnapshot) error {
	if len(snaps) == 0 {
		return nil
	}
	var b strings.Builder
	b.WriteString("INSERT INTO market_snapshot (snapshot_at,symbol,code,name,sec_type,exchange,source,price,change_pct,change_amount,volume,amount,volume_ratio,turnover_rate,amplitude,open,high,low,pre_close,pe_ratio,pb_ratio,total_mv,circ_mv) VALUES ")
	args := make([]interface{}, 0, len(snaps)*23)
	for i, snap := range snaps {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString("(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)")
		args = append(args, capturedAt, snap.Symbol, snap.Code, snap.Name, string(snap.SecType), snap.Exchange, "eastmoney", snap.Price, snap.ChangePct, snap.ChangeAmount, snap.Volume, snap.Amount, snap.VolumeRatio, snap.TurnoverRate, snap.Amplitude, snap.Open, snap.High, snap.Low, snap.PreClose, snap.PERatio, snap.PBRatio, snap.TotalMV, snap.CircMV)
	}
	b.WriteString(" ON DUPLICATE KEY UPDATE code=VALUES(code),name=VALUES(name),sec_type=VALUES(sec_type),exchange=VALUES(exchange),source=VALUES(source),price=VALUES(price),change_pct=VALUES(change_pct),change_amount=VALUES(change_amount),volume=VALUES(volume),amount=VALUES(amount),volume_ratio=VALUES(volume_ratio),turnover_rate=VALUES(turnover_rate),amplitude=VALUES(amplitude),open=VALUES(open),high=VALUES(high),low=VALUES(low),pre_close=VALUES(pre_close),pe_ratio=VALUES(pe_ratio),pb_ratio=VALUES(pb_ratio),total_mv=VALUES(total_mv),circ_mv=VALUES(circ_mv)")
	if _, err := s.DB.ExecContext(ctx, b.String(), args...); err != nil {
		return fmt.Errorf("upsert market snapshots: %w", err)
	}
	return nil
}

// UpsertIndexSnapshots writes index values from the same scheduled collection cycle.
func (s *Store) UpsertIndexSnapshots(ctx context.Context, capturedAt time.Time, indices []model.IndexQuote) error {
	if len(indices) == 0 {
		return nil
	}
	var b strings.Builder
	b.WriteString("INSERT INTO index_snapshot (snapshot_at,symbol,name,price,change_pct,amount,volume,source) VALUES ")
	args := make([]interface{}, 0, len(indices)*8)
	for i, idx := range indices {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString("(?,?,?,?,?,?,?,?)")
		args = append(args, capturedAt, idx.Symbol, idx.Name, floatValue(idx.Price), floatValue(idx.ChangePct), floatValue(idx.Amount), intValue(idx.Volume), "scheduled")
	}
	b.WriteString(" ON DUPLICATE KEY UPDATE name=VALUES(name),price=VALUES(price),change_pct=VALUES(change_pct),amount=VALUES(amount),volume=VALUES(volume),source=VALUES(source)")
	if _, err := s.DB.ExecContext(ctx, b.String(), args...); err != nil {
		return fmt.Errorf("upsert index snapshots: %w", err)
	}
	return nil
}

// LatestQuote reads the latest stored market snapshot. It never calls an upstream provider.
func (s *Store) LatestQuote(ctx context.Context, symbol string) (*model.Quote, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT symbol,code,name,source,snapshot_at,price,change_pct,change_amount,volume,amount,volume_ratio,turnover_rate,amplitude,open,high,low,pre_close,pe_ratio,pb_ratio,total_mv,circ_mv FROM market_snapshot WHERE symbol=? ORDER BY snapshot_at DESC LIMIT 1`, symbol)
	return scanSnapshotQuote(row)
}

// LatestQuotes returns the latest stored snapshot for each requested security.
func (s *Store) LatestQuotes(ctx context.Context, symbols []string) ([]*model.Quote, error) {
	if len(symbols) == 0 {
		return []*model.Quote{}, nil
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(symbols)), ",")
	args := make([]interface{}, len(symbols))
	for i := range symbols {
		args[i] = symbols[i]
	}
	query := `SELECT s.symbol,s.code,s.name,s.source,s.snapshot_at,s.price,s.change_pct,s.change_amount,s.volume,s.amount,s.volume_ratio,s.turnover_rate,s.amplitude,s.open,s.high,s.low,s.pre_close,s.pe_ratio,s.pb_ratio,s.total_mv,s.circ_mv FROM market_snapshot s INNER JOIN (SELECT symbol,MAX(snapshot_at) AS snapshot_at FROM market_snapshot WHERE symbol IN (` + placeholders + `) GROUP BY symbol) latest ON latest.symbol=s.symbol AND latest.snapshot_at=s.snapshot_at ORDER BY s.code`
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query latest market snapshots: %w", err)
	}
	defer rows.Close()
	out := make([]*model.Quote, 0, len(symbols))
	for rows.Next() {
		q, err := scanSnapshotQuote(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

// LatestIndices returns only values persisted by scheduled snapshot jobs.
func (s *Store) LatestIndices(ctx context.Context) ([]model.IndexQuote, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT s.symbol,s.name,s.price,s.change_pct,s.amount,s.volume FROM index_snapshot s INNER JOIN (SELECT symbol,MAX(snapshot_at) AS snapshot_at FROM index_snapshot GROUP BY symbol) latest ON latest.symbol=s.symbol AND latest.snapshot_at=s.snapshot_at ORDER BY FIELD(s.symbol,'SH000001','SZ399001','SZ399006','SH000688','SH000300','BJ899050')`)
	if err != nil {
		return nil, fmt.Errorf("query latest index snapshots: %w", err)
	}
	defer rows.Close()
	out := make([]model.IndexQuote, 0, 6)
	for rows.Next() {
		var idx model.IndexQuote
		var price, changePct, amount float64
		var volume int64
		if err := rows.Scan(&idx.Symbol, &idx.Name, &price, &changePct, &amount, &volume); err != nil {
			return nil, err
		}
		idx.Price, idx.ChangePct, idx.Amount, idx.Volume = &price, &changePct, &amount, &volume
		out = append(out, idx)
	}
	return out, rows.Err()
}

// LatestSnapshotTime exposes data freshness for API and MCP clients.
func (s *Store) LatestSnapshotTime(ctx context.Context) (time.Time, error) {
	var capturedAt time.Time
	if err := s.DB.QueryRowContext(ctx, "SELECT MAX(snapshot_at) FROM market_snapshot").Scan(&capturedAt); err != nil {
		return time.Time{}, err
	}
	return capturedAt, nil
}

type snapshotScanner interface {
	Scan(dest ...interface{}) error
}

func scanSnapshotQuote(row snapshotScanner) (*model.Quote, error) {
	q := &model.Quote{Market: model.MarketCN, Currency: "CNY"}
	var capturedAt time.Time
	var price, changePct, changeAmount, amount, volumeRatio, turnoverRate, amplitude, open, high, low, preClose, peRatio, pbRatio, totalMV, circMV float64
	var volume int64
	if err := row.Scan(&q.Symbol, &q.Code, &q.Name, &q.Source, &capturedAt, &price, &changePct, &changeAmount, &volume, &amount, &volumeRatio, &turnoverRate, &amplitude, &open, &high, &low, &preClose, &peRatio, &pbRatio, &totalMV, &circMV); err != nil {
		return nil, err
	}
	q.FetchedAt = capturedAt
	q.ProviderTimestamp = capturedAt.In(time.FixedZone("CST", 8*3600)).Format(time.RFC3339)
	q.Price, q.ChangePct, q.ChangeAmount = &price, &changePct, &changeAmount
	q.Volume, q.Amount, q.VolumeRatio, q.TurnoverRate, q.Amplitude = &volume, &amount, &volumeRatio, &turnoverRate, &amplitude
	q.Open, q.High, q.Low, q.PreClose = &open, &high, &low, &preClose
	q.PERatio, q.PBRatio, q.TotalMV, q.CircMV = &peRatio, &pbRatio, &totalMV, &circMV
	return q, nil
}

func floatValue(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}

func intValue(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}
