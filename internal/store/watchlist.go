package store

import "context"

// WatchlistSymbols 自选股列表（按排序）。
func (s *Store) WatchlistSymbols(ctx context.Context) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT symbol FROM watchlist ORDER BY sort_order, created_at")
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

// AddWatchlist 添加自选股。
func (s *Store) AddWatchlist(ctx context.Context, symbol string) error {
	_, err := s.DB.ExecContext(ctx,
		"INSERT IGNORE INTO watchlist (symbol, sort_order) SELECT ?, IFNULL(MAX(sort_order),0)+1 FROM watchlist", symbol)
	return err
}

// RemoveWatchlist 移除自选股。
func (s *Store) RemoveWatchlist(ctx context.Context, symbol string) error {
	_, err := s.DB.ExecContext(ctx, "DELETE FROM watchlist WHERE symbol=?", symbol)
	return err
}
