package sync

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/hoax/go-stock/internal/model"
	"github.com/hoax/go-stock/internal/provider"
)

const (
	morningSnapshotHour = 12
	closeSnapshotHour   = 16
)

// StartDailyScheduler persists market data at 12:00 and 16:00 Asia/Shanghai time.
// The noon run captures the morning close. The 16:00 run captures the full session
// and updates the daily indicator and daily K-line tables used by historical analysis.
func (e *Engine) StartDailyScheduler(ctx context.Context) {
	go func() {
		slog.Info("市场快照调度已启动", "runs", "12:00,16:00 Asia/Shanghai")
		for {
			next := nextRunTime(time.Now())
			select {
			case <-time.After(time.Until(next)):
				isClose := next.Hour() == closeSnapshotHour
				if err := e.runMarketSnapshot(ctx, isClose, false); err != nil {
					slog.Error("市场快照同步失败", "scheduled_at", next.Format(time.RFC3339), "err", err)
				}
				if isClose && !e.IsRunning() {
					if err := e.StartBackfill(ctx); err != nil {
						slog.Warn("启动历史缺失补齐失败", "err", err)
					}
				}
			case <-ctx.Done():
				return
			}
		}
	}()
}

// nextRunTime calculates the next 12:00 or 16:00 weekday schedule in Shanghai time.
func nextRunTime(now time.Time) time.Time {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err == nil {
		now = now.In(loc)
	}
	candidates := []time.Time{
		time.Date(now.Year(), now.Month(), now.Day(), morningSnapshotHour, 0, 0, 0, now.Location()),
		time.Date(now.Year(), now.Month(), now.Day(), closeSnapshotHour, 0, 0, 0, now.Location()),
	}
	for _, candidate := range candidates {
		if candidate.After(now) && isWeekday(candidate) {
			return candidate
		}
	}
	day := now.AddDate(0, 0, 1)
	for !isWeekday(day) {
		day = day.AddDate(0, 0, 1)
	}
	return time.Date(day.Year(), day.Month(), day.Day(), morningSnapshotHour, 0, 0, 0, day.Location())
}

func isWeekday(t time.Time) bool {
	return t.Weekday() != time.Saturday && t.Weekday() != time.Sunday
}

// RunScheduledSnapshot is retained for manual collection and bypasses the calendar guard.
// Scheduled runs call runMarketSnapshot with calendar validation enabled.
func (e *Engine) RunScheduledSnapshot(ctx context.Context, closeSession bool) error {
	return e.runMarketSnapshot(ctx, closeSession, true)
}

func (e *Engine) runMarketSnapshot(ctx context.Context, closeSession, manual bool) error {
	if !manual && !isWeekday(time.Now().In(shanghaiLocation())) {
		slog.Info("非工作日，跳过市场快照")
		return nil
	}

	snaps, err := e.mgr.Eastmoney().AllSecurities(ctx)
	if err != nil {
		return fmt.Errorf("全市场快照失败: %w", err)
	}
	if len(snaps) == 0 {
		return fmt.Errorf("全市场快照为空")
	}

	activeCount := 0
	for _, snapshot := range snaps {
		if snapshot.Amount > 0 {
			activeCount++
		}
	}
	if activeCount < len(snaps)/10 {
		slog.Info("疑似非交易日，跳过市场快照", "active", activeCount, "total", len(snaps))
		return nil
	}

	capturedAt := time.Now().In(shanghaiLocation()).Truncate(time.Second)
	if err := e.st.UpsertSecurities(ctx, snaps); err != nil {
		return err
	}
	if err := e.st.UpsertMarketSnapshots(ctx, capturedAt, snaps); err != nil {
		return err
	}

	// Index data is useful but a temporary upstream failure must not discard the full market snapshot.
	if indices, err := e.mgr.Indices(ctx); err != nil {
		slog.Warn("指数快照采集失败", "err", err)
	} else if err := e.st.UpsertIndexSnapshots(ctx, capturedAt, indices); err != nil {
		return err
	}

	if !closeSession {
		slog.Info("上午收盘市场快照完成", "securities", len(snaps), "captured_at", capturedAt.Format(time.RFC3339))
		return nil
	}
	return e.persistCloseSession(ctx, capturedAt, snaps)
}

// RunDailySync is retained for the manual REST endpoint and always creates a full-session snapshot.
func (e *Engine) RunDailySync(ctx context.Context) error {
	return e.RunScheduledSnapshot(ctx, true)
}

func (e *Engine) persistCloseSession(ctx context.Context, capturedAt time.Time, snaps []provider.SecuritySnapshot) error {
	today := capturedAt.Format("2006-01-02")
	if err := e.st.UpsertDailyIndicators(ctx, today, snaps); err != nil {
		return err
	}

	klines := make([]model.Kline, 0, len(snaps))
	for _, snapshot := range snaps {
		if snapshot.Amount <= 0 || snapshot.Price <= 0 {
			continue
		}
		open, high, low := snapshot.Open, snapshot.High, snapshot.Low
		if open <= 0 {
			open = snapshot.Price
		}
		if high <= 0 {
			high = snapshot.Price
		}
		if low <= 0 {
			low = snapshot.Price
		}
		klines = append(klines, model.Kline{
			Symbol:       snapshot.Symbol,
			Date:         today,
			Open:         open,
			High:         high,
			Low:          low,
			Close:        snapshot.Price,
			ChangePct:    snapshot.ChangePct,
			Volume:       snapshot.Volume,
			Amount:       snapshot.Amount,
			TurnoverRate: snapshot.TurnoverRate,
			AdjFactor:    1,
		})
	}
	if err := e.fillAdjFactors(ctx, klines); err != nil {
		slog.Warn("延续复权因子失败", "err", err)
	}
	if err := e.st.UpsertKlines(ctx, klines); err != nil {
		return err
	}

	symbols := make([]string, 0, len(snaps))
	for _, snapshot := range snaps {
		symbols = append(symbols, snapshot.Symbol)
	}
	if err := e.st.InitCheckpoints(ctx, TaskBackfill, symbols); err != nil {
		return err
	}
	slog.Info("下午收盘市场快照完成", "securities", len(snaps), "klines", len(klines), "date", today)
	return nil
}

func (e *Engine) fillAdjFactors(ctx context.Context, klines []model.Kline) error {
	idxBySymbol := make(map[string]int, len(klines))
	for i, kline := range klines {
		idxBySymbol[kline.Symbol] = i
	}
	rows, err := e.st.DB.QueryContext(ctx, `
		SELECT k.symbol, k.adj_factor FROM kline_daily k
		INNER JOIN (SELECT symbol, MAX(trade_date) AS max_date FROM kline_daily WHERE adj_factor>0 GROUP BY symbol) latest
		ON latest.symbol=k.symbol AND latest.max_date=k.trade_date`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var symbol string
		var factor float64
		if err := rows.Scan(&symbol, &factor); err != nil {
			return err
		}
		if i, ok := idxBySymbol[symbol]; ok && factor > 0 {
			klines[i].AdjFactor = factor
		}
	}
	for i := range klines {
		if klines[i].AdjFactor <= 0 {
			klines[i].AdjFactor = 1
		}
	}
	return rows.Err()
}

func shanghaiLocation() *time.Location {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.FixedZone("CST", 8*3600)
	}
	return location
}
