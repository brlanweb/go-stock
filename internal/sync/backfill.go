// Package sync 历史K线回填与每日增量同步（断点续传）。
package sync

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
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
	st          *store.Store
	mgr         *provider.Manager
	tencent     *provider.Tencent // K线降级源（与东财使用同一保守 QPS）
	baostock    provider.KlineProvider
	akshare     provider.KlineProvider
	workers     int
	syncSectors bool
	baseCtx     context.Context

	mu      sync.Mutex
	running atomic.Bool
	cancel  context.CancelFunc
}

// NewEngine 创建同步引擎。
func NewEngine(st *store.Store, mgr *provider.Manager, workers int, qps float64, syncSectors bool, pythonCommand, pythonScript string) *Engine {
	if workers < 1 {
		workers = 1
	}
	return &Engine{
		st:          st,
		mgr:         mgr,
		tencent:     provider.NewTencentWithQPS(qps),
		baostock:    provider.NewPythonKline("baostock", pythonCommand, pythonScript),
		akshare:     provider.NewPythonKline("akshare", pythonCommand, pythonScript),
		workers:     workers,
		syncSectors: syncSectors,
		baseCtx:     context.Background(),
	}
}

// BaseContext 返回后台任务应使用的根上下文，避免手动触发时被请求上下文取消。
func (e *Engine) BaseContext() context.Context {
	if e.baseCtx != nil {
		return e.baseCtx
	}
	return context.Background()
}

// SetBaseContext 设置后台任务根上下文（服务启动时注入）。
func (e *Engine) SetBaseContext(ctx context.Context) {
	if ctx != nil {
		e.baseCtx = ctx
	}
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
	// 这些北交所旧代码已经转板到沪深市场。保留旧历史，但不再请求旧代码补到当前日。
	if _, err := e.st.MarkSecuritiesMigrated(ctx, []string{"BJ832317", "BJ833874", "BJ833994"}); err != nil {
		return 0, err
	}
	symbols := make([]string, 0, len(snaps))
	for _, sn := range snaps {
		symbols = append(symbols, sn.Symbol)
	}
	if len(symbols) >= 7000 {
		if count, err := e.st.MarkMissingListedSecuritiesDelisted(ctx, symbols); err != nil {
			return 0, err
		} else if count > 0 {
			slog.Info("已更新退市证券状态", "count", count)
		}
	}
	if err := e.st.InitCheckpoints(ctx, TaskBackfill, symbols); err != nil {
		return 0, err
	}
	slog.Info("证券列表同步完成", "count", len(snaps))
	return len(snaps), nil
}

// SyncSectors 同步行业/概念板块及成分关系。失败不会影响证券日线任务。
func (e *Engine) SyncSectors(ctx context.Context) error {
	for _, sectorType := range []string{"industry", "concept"} {
		sectors, err := e.mgr.Eastmoney().Sectors(ctx, sectorType)
		if err != nil {
			return err
		}
		constituents := make([]provider.SectorConstituent, 0)
		for _, sector := range sectors {
			items, err := e.mgr.Eastmoney().SectorConstituents(ctx, sector.Code)
			if err != nil {
				return err
			}
			constituents = append(constituents, items...)
		}
		if err := e.st.ReplaceSectors(ctx, sectorType, sectors, constituents); err != nil {
			return err
		}
		slog.Info("板块成分同步完成", "type", sectorType, "sectors", len(sectors), "constituents", len(constituents))
	}
	return nil
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
	klines, err := e.fetchKlinesWithFallback(ctx, symbol, beg, nil)
	if err != nil {
		return err
	}
	if len(klines) == 0 {
		return nil
	}
	if err := e.st.UpsertKlines(ctx, klines); err != nil {
		return err
	}
	if err := e.st.InitCheckpoints(ctx, TaskBackfill, []string{symbol}); err != nil {
		return err
	}
	return e.st.MarkDone(ctx, TaskBackfill, symbol, latestExpectedTradeDate(time.Now()))
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
	// failed 断点仅允许通过显式“重试失败项”接口重排，启动时不自动恢复，
	// 包括已具备北交所旧代码映射的证券，避免每次重启重复撞击上游。

	// 2. 刷新证券列表（幂等 upsert，新股自动加入断点）。
	if _, err := e.SyncSecurities(ctx); err != nil {
		status, serr := e.st.SyncStatus(ctx, TaskBackfill)
		if serr != nil || status.Total == 0 {
			return err
		}
		slog.Warn("证券列表刷新失败，使用既有断点继续", "err", err)
	} else if e.syncSectors {
		if err := e.SyncSectors(ctx); err != nil {
			slog.Warn("板块成分同步失败，继续历史回填", "err", err)
		}
	}

	// 3. 按目标交易日重新判定缺失。这样历史完整且已经更新的证券不会重复请求上游。
	targetDate := latestExpectedTradeDate(time.Now())
	if n, err := e.st.MarkSecuritiesWithStaleTradeDateDelisted(ctx, targetDate); err != nil {
		return err
	} else if n > 0 {
		slog.Info("已识别停止交易证券", "count", n)
	}
	if n, err := e.st.CompleteInactiveCheckpoints(ctx, TaskBackfill, targetDate); err != nil {
		return err
	} else if n > 0 {
		slog.Info("已收敛非活跃证券断点", "count", n)
	}
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
	coverage, err := e.st.KlineCoverage(ctx, cp.Symbol)
	if err != nil {
		return err
	}
	if coverage.Complete {
		return e.st.MarkDone(ctx, TaskBackfill, cp.Symbol, targetDate)
	}

	// 历史头部不完整时必须从上市日起重拉；只有头部完整、尾部落后时才增量追加。
	beg := "0"
	if coverage.HistoryStartComplete && coverage.LastDate != "" {
		t, err := time.Parse("2006-01-02", coverage.LastDate)
		if err != nil {
			return fmt.Errorf("解析最新日线日期: %w", err)
		}
		beg = t.AddDate(0, 0, 1).Format("20060102")
	}
	klines, err := e.fetchKlinesWithFallback(ctx, cp.Symbol, beg, coverage)
	if err != nil {
		return err
	}
	if len(klines) == 0 {
		return fmt.Errorf("上游未返回可验证的历史K线")
	}
	if err := e.st.UpsertKlines(ctx, klines); err != nil {
		return err
	}
	if err := e.st.MarkDone(ctx, TaskBackfill, cp.Symbol, targetDate); err != nil {
		return err
	}
	updated, err := e.st.KlineCoverage(ctx, cp.Symbol)
	if err != nil {
		return err
	}
	if !updated.Complete {
		return fmt.Errorf("历史覆盖仍不完整 first=%s last=%s count=%d", updated.FirstDate, updated.LastDate, updated.Count)
	}
	return nil
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

// fetchKlinesWithFallback 按东财、BaoStock、AKShare 依次尝试；腾讯保留为
// AKShare 之后的末级兼容源。北交所 920 代码由 AKShare 直接支持，仍保留官方旧代码映射尝试。
func (e *Engine) fetchKlinesWithFallback(ctx context.Context, symbol, beg string, coverage *store.KlineCoverageInfo) ([]model.Kline, error) {
	type attempt struct {
		name   string
		symbol string
		fetch  func(context.Context, string, string, string) ([]model.Kline, error)
	}
	em := e.mgr.Eastmoney()
	attempts := []attempt{
		{name: "eastmoney", symbol: symbol, fetch: em.DailyKlines},
		{name: "baostock", symbol: symbol, fetch: e.baostock.DailyKlines},
		{name: "akshare", symbol: symbol, fetch: e.akshare.DailyKlines},
		{name: "tencent", symbol: symbol, fetch: e.tencent.DailyKlines},
	}
	if legacy, ok := provider.BSELegacySymbol[symbol]; ok {
		attempts = append(attempts,
			attempt{name: "akshare-legacy", symbol: legacy, fetch: e.akshare.DailyKlines},
			attempt{name: "eastmoney-legacy", symbol: legacy, fetch: em.DailyKlines},
			attempt{name: "tencent-legacy", symbol: legacy, fetch: e.tencent.DailyKlines},
		)
	}

	var best []model.Kline
	var lastErr error
	for _, candidate := range attempts {
		isEastmoney := candidate.name == "eastmoney" || candidate.name == "eastmoney-legacy"
		if isEastmoney && !em.Breaker().Allow() {
			continue
		}
		klines, err := candidate.fetch(ctx, candidate.symbol, beg, "")
		if err != nil {
			lastErr = err
			if isEastmoney {
				em.Breaker().Failure(em.Name())
			}
			slog.Debug("历史K线源失败，继续降级", "source", candidate.name, "symbol", symbol, "query_symbol", candidate.symbol, "err", err)
			continue
		}
		if isEastmoney {
			em.Breaker().Success()
		}
		for i := range klines {
			klines[i].Symbol = symbol
		}
		best = mergeKlines(best, klines)
		if fetchedCoverageComplete(best, coverage, beg, latestExpectedTradeDate(time.Now())) {
			return best, nil
		}
	}
	if len(best) > 0 {
		first, last := klineDateRange(best)
		return nil, fmt.Errorf("历史数据源均未达到覆盖要求 first=%s last=%s count=%d", first, last, len(best))
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("所有历史K线源均不可用或处于熔断冷却")
	}
	return nil, lastErr
}

// fetchedCoverageComplete 判断本轮数据是否足以补齐证券历史。不能使用返回条数
// 判断成功，否则东财对 ETF 返回最近一小段数据时会阻止完整来源继续执行。
func fetchedCoverageComplete(klines []model.Kline, coverage *store.KlineCoverageInfo, beg, marketTarget string) bool {
	if len(klines) == 0 {
		return false
	}
	first, last := klineDateRange(klines)
	if first == "" || last == "" {
		return false
	}
	target := marketTarget
	if coverage != nil {
		if coverage.Status != "listed" && coverage.LastTradeDate != "" {
			target = coverage.LastTradeDate
		}
		if coverage.ListDate != "" && coverage.ListDate > marketTarget {
			return true
		}
	}
	if last < target {
		return false
	}
	// 增量补尾时库内头部已验证，本轮结果无需再次包含上市日。
	if beg != "" && beg != "0" && coverage != nil && coverage.HistoryStartComplete {
		return true
	}
	if coverage == nil || coverage.ListDate == "" {
		return true
	}
	listDate, err := time.Parse("2006-01-02", coverage.ListDate)
	if err != nil {
		return false
	}
	return first <= listDate.AddDate(0, 0, 14).Format("2006-01-02")
}

func klineDateRange(klines []model.Kline) (string, string) {
	var first, last string
	for _, k := range klines {
		date := k.Date
		if len(date) > 10 {
			date = date[:10]
		}
		if date == "" {
			continue
		}
		if first == "" || date < first {
			first = date
		}
		if last == "" || date > last {
			last = date
		}
	}
	return first, last
}

// mergeKlines 合并各来源的覆盖区间；先返回的数据源优先，后续来源仅补缺失日期。
func mergeKlines(base, extra []model.Kline) []model.Kline {
	byDate := make(map[string]model.Kline, len(base)+len(extra))
	for _, k := range base {
		byDate[k.Date] = k
	}
	for _, k := range extra {
		if _, exists := byDate[k.Date]; !exists {
			byDate[k.Date] = k
		}
	}
	out := make([]model.Kline, 0, len(byDate))
	for _, k := range byDate {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out
}
