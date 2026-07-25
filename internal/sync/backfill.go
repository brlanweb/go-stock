// Package sync 历史K线回填与每日增量同步（断点续传）。
package sync

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hoax/go-stock/internal/model"
	"github.com/hoax/go-stock/internal/provider"
	"github.com/hoax/go-stock/internal/store"
)

const (
	TaskBackfill = "backfill_kline"
	TaskDaily    = "daily_sync"
)

// Engine 同步引擎。
type Engine struct {
	st      *store.Store
	mgr     *provider.Manager
	tencent *provider.Tencent // K线降级源（共享限流器）
	workers int

	mu      sync.Mutex
	running atomic.Bool
	cancel  context.CancelFunc
}

// NewEngine 创建同步引擎。
func NewEngine(st *store.Store, mgr *provider.Manager, workers int) *Engine {
	if workers < 1 {
		workers = 1
	}
	return &Engine{st: st, mgr: mgr, tencent: provider.NewTencent(), workers: workers}
}

// IsRunning 回填是否进行中。
func (e *Engine) IsRunning() bool { return e.running.Load() }

// SyncSecurities 同步全市场证券列表到 stock_basic，并初始化回填断点。
func (e *Engine) SyncSecurities(ctx context.Context) (int, error) {
	snaps, err := e.mgr.Eastmoney().AllSecurities(ctx)
	if err != nil {
		return 0, fmt.Errorf("拉取全市场列表失败: %w", err)
	}
	if len(snaps) == 0 {
		return 0, fmt.Errorf("全市场列表为空")
	}
	if err := e.st.UpsertSecurities(ctx, snaps); err != nil {
		return 0, err
	}
	symbols := make([]string, 0, len(snaps))
	for _, sn := range snaps {
		symbols = append(symbols, sn.Symbol)
	}
	if err := e.st.InitCheckpoints(ctx, TaskBackfill, symbols); err != nil {
		return 0, err
	}
	slog.Info("证券列表同步完成", "count", len(snaps))
	return len(snaps), nil
}

// SyncStock 按需同步单个证券。latest 只补当前日，missing 从库内最后日期补齐，
// full 显式重拉上市以来历史；不再将全市场历史任务作为常驻后台工作。
func (e *Engine) SyncStock(parent context.Context, symbol, mode string) error {
	symbol = model.NormalizeSymbol(symbol)
	if symbol == "" {
		return fmt.Errorf("无法识别的代码")
	}
	e.mu.Lock()
	if e.running.Load() {
		e.mu.Unlock()
		return fmt.Errorf("已有同步任务运行中")
	}
	ctx, cancel := context.WithCancel(parent)
	e.cancel = cancel
	e.running.Store(true)
	e.mu.Unlock()

	go func() {
		defer func() {
			e.running.Store(false)
			cancel()
		}()
		if err := e.syncStock(ctx, symbol, mode); err != nil && ctx.Err() == nil {
			slog.Error("单股同步失败", "symbol", symbol, "mode", mode, "err", err)
		}
	}()
	return nil
}

func (e *Engine) syncStock(ctx context.Context, symbol, mode string) error {
	beg := "0"
	if mode != "full" {
		var err error
		beg, err = e.st.NextKlineDate(ctx, symbol)
		if err != nil {
			return err
		}
		if mode == "latest" {
			beg = time.Now().Format("20060102")
		}
	}
	klines, err := e.fetchKlinesWithFallback(ctx, symbol, beg)
	if err != nil {
		return err
	}
	if len(klines) == 0 {
		return nil
	}
	if err := e.st.UpsertKlines(ctx, klines); err != nil {
		return err
	}
	last := klines[len(klines)-1].Date
	if err := e.st.InitCheckpoints(ctx, TaskBackfill, []string{symbol}); err != nil {
		return err
	}
	return e.st.MarkDone(ctx, TaskBackfill, symbol, last)
}

// StartBackfill 启动受控缺失补齐：只处理空历史或最新日线落后目标交易日的证券。
// 已完整证券会直接标记 done，不向上游请求历史数据。
func (e *Engine) StartBackfill(parent context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.running.Load() {
		return fmt.Errorf("回填已在运行中")
	}
	ctx, cancel := context.WithCancel(parent)
	e.cancel = cancel
	e.running.Store(true)

	go func() {
		defer func() {
			e.running.Store(false)
			cancel()
		}()
		if err := e.runBackfill(ctx); err != nil && ctx.Err() == nil {
			slog.Error("回填异常结束", "err", err)
		}
	}()
	return nil
}

// StopBackfill 停止回填（断点保留，可续跑）。
func (e *Engine) StopBackfill() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cancel != nil {
		e.cancel()
	}
}

// runBackfill 主循环：领取断点 -> 并发拉取 -> 落库 -> 标记。
func (e *Engine) runBackfill(ctx context.Context) error {
	// 1. 残留 running 重置（上次进程退出导致）；failed 重置重试计数（新一轮机会）
	if n, err := e.st.ResetRunning(ctx, TaskBackfill); err != nil {
		return err
	} else if n > 0 {
		slog.Info("重置残留 running 断点", "count", n)
	}
	if n, err := e.st.ResetFailed(ctx, TaskBackfill); err != nil {
		return err
	} else if n > 0 {
		slog.Info("重置 failed 断点重试", "count", n)
	}

	// 2. 刷新证券列表（幂等 upsert，新股自动加入断点）。
	if _, err := e.SyncSecurities(ctx); err != nil {
		status, serr := e.st.SyncStatus(ctx, TaskBackfill)
		if serr != nil || status.Total == 0 {
			return err
		}
		slog.Warn("证券列表刷新失败，使用既有断点继续", "err", err)
	}

	// 3. 按目标交易日重新判定缺失。这样历史完整且已经更新的证券不会重复请求上游。
	targetDate := latestExpectedTradeDate(time.Now())
	if n, err := e.st.ReconcileCheckpoints(ctx, TaskBackfill, targetDate); err != nil {
		return err
	} else {
		slog.Info("历史缺失检查完成", "target_date", targetDate, "checked", n)
	}

	slog.Info("缺失历史补齐启动", "workers", e.workers, "target_date", targetDate)
	start := time.Now()
	var doneCount, failCount atomic.Int64

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		batch, err := e.st.ClaimPending(ctx, TaskBackfill, e.workers*4)
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			break // 全部完成
		}

		var wg sync.WaitGroup
		jobs := make(chan model.SyncCheckpoint)
		for w := 0; w < e.workers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for cp := range jobs {
					if err := e.backfillOne(ctx, cp); err != nil {
						if ctx.Err() != nil {
							return
						}
						failCount.Add(1)
						_ = e.st.MarkFailed(ctx, TaskBackfill, cp.Symbol, err.Error())
						slog.Warn("回填失败", "symbol", cp.Symbol, "err", err)
					} else {
						doneCount.Add(1)
					}
				}
			}()
		}
		for _, cp := range batch {
			select {
			case jobs <- cp:
			case <-ctx.Done():
			}
		}
		close(jobs)
		wg.Wait()

		if d := doneCount.Load(); d > 0 && d%200 == 0 {
			slog.Info("回填进度", "done", d, "failed", failCount.Load(), "elapsed", time.Since(start).Round(time.Second))
		}
	}
	slog.Info("历史回填结束", "done", doneCount.Load(), "failed", failCount.Load(), "elapsed", time.Since(start).Round(time.Second))
	return nil
}

// backfillOne 拉取单只股票的历史K线（断点后增量）。
func (e *Engine) backfillOne(ctx context.Context, cp model.SyncCheckpoint) error {
	targetDate := latestExpectedTradeDate(time.Now())
	latestDate, err := e.st.LatestKlineDateForSymbol(ctx, cp.Symbol)
	if err != nil {
		return err
	}
	if latestDate >= targetDate {
		return e.st.MarkDone(ctx, TaskBackfill, cp.Symbol, latestDate)
	}

	beg := "0"
	if latestDate != "" {
		t, err := time.Parse("2006-01-02", latestDate)
		if err != nil {
			return fmt.Errorf("解析最新日线日期: %w", err)
		}
		beg = t.AddDate(0, 0, 1).Format("20060102")
	}
	klines, err := e.fetchKlinesWithFallback(ctx, cp.Symbol, beg)
	if err != nil {
		return err
	}
	if len(klines) == 0 {
		// 无新数据也标记为当前库内日期。停牌、未上市或数据源无返回不会形成热循环。
		return e.st.MarkDone(ctx, TaskBackfill, cp.Symbol, latestDate)
	}
	if err := e.st.UpsertKlines(ctx, klines); err != nil {
		return err
	}
	last := klines[len(klines)-1].Date
	return e.st.MarkDone(ctx, TaskBackfill, cp.Symbol, last)
}

func latestExpectedTradeDate(now time.Time) string {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err == nil {
		now = now.In(loc)
	}
	// 收盘前还不应把当天作为缺失；收盘后当天的日线应可用。
	if now.Hour() < 16 {
		now = now.AddDate(0, 0, -1)
	}
	for now.Weekday() == time.Saturday || now.Weekday() == time.Sunday {
		now = now.AddDate(0, 0, -1)
	}
	return now.Format("2006-01-02")
}

// fetchKlinesWithFallback 东财优先；熔断时等待冷却（保证数据完整性），
// 个股可降级腾讯，ETF 腾讯不支持（HTTP 501）故仅东财。
func (e *Engine) fetchKlinesWithFallback(ctx context.Context, symbol, beg string) ([]model.Kline, error) {
	em := e.mgr.Eastmoney()
	// 熔断中：等待冷却而非跳过（回填以完整性优先）
	for !em.Breaker().Allow() {
		select {
		case <-time.After(5 * time.Second):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	klines, err := em.DailyKlines(ctx, symbol, beg, "")
	if err == nil {
		em.Breaker().Success()
		return klines, nil
	}
	em.Breaker().Failure(em.Name())
	slog.Debug("东财K线失败，降级腾讯", "symbol", symbol, "err", err)
	// 腾讯降级（共享实例限流；ETF 会 501，重试机制会再回东财）
	return e.tencent.DailyKlines(ctx, symbol, beg, "")
}
