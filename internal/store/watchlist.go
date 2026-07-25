package store

import "context"

// WatchlistSymbols 自选股列表（按排序）。
func (s *Store) WatchlistSymbols(ctx context.Context) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT symbol FROM watchlist ORDER BY created_at DESC, sort_order DESC LIMIT 10")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var sym string
		if err := rows.Scan(&sym); err != nil {
			return nil, err
		}
		out = append(out, sym)
	}
	return out, rows.Err()
}

// AddWatchlist 添加自选股，并只保留最近加入的 10 只。
func (s *Store) AddWatchlist(ctx context.Context, symbol string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM watchlist WHERE symbol=?", symbol); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO watchlist (symbol, sort_order, created_at) SELECT ?, IFNULL(MAX(sort_order),0)+1, NOW() FROM watchlist", symbol); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM watchlist WHERE symbol IN (
		SELECT symbol FROM (SELECT symbol FROM watchlist ORDER BY created_at DESC,sort_order DESC LIMIT 18446744073709551615 OFFSET 10) old_items
	)`); err != nil {
		return err
	}
	return tx.Commit()
}

// RemoveWatchlist 移除自选股。
func (s *Store) RemoveWatchlist(ctx context.Context, symbol string) error {
	_, err := s.DB.ExecContext(ctx, "DELETE FROM watchlist WHERE symbol=?", symbol)
	return err
}
