package analysis

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/hoax/go-stock/internal/store"
)

// RunGlobalGate 执行一轮风险感知：采集隔夜外盘因子 → 确定性打分 → 落库。
// tradeDate 是被保护的交易日（当日）。任何环节失败都按黄灯保守降档返回，
// 保证调用方永远拿到可用的风险结论，且失败缺省值绝不是绿灯。
func (s *Service) RunGlobalGate(ctx context.Context, tradeDate string) store.GlobalRiskGate {
	fallback := store.GlobalRiskGate{
		TradeDate: tradeDate,
		Level:     store.MarketGateYellow,
		Reason:    "外盘行情不可用，风险感知降级，保守降档",
	}
	if s.globalProvider == nil {
		// 无外盘行情源（测试或旧部署）：尝试复用当日已落库的判定。
		if gate, err := s.st.GlobalRiskGateForDate(ctx, tradeDate); err == nil && gate != nil {
			return *gate
		}
		fallback.Reason = "外盘行情源未配置，风险感知不可用，保守降档"
		return fallback
	}
	quotes, err := s.globalProvider.GlobalQuotes(ctx)
	if err != nil {
		slog.Warn("外盘行情采集失败，全球风险门按黄灯保守处理", "err", err)
		if saveErr := s.st.SaveGlobalRiskGate(ctx, fallback); saveErr != nil {
			slog.Warn("保存全球风险门降级结论失败", "err", saveErr)
		}
		return fallback
	}
	if err := s.st.UpsertGlobalSnapshots(ctx, time.Now(), quotes); err != nil {
		slog.Warn("保存外盘因子快照失败", "err", err)
	}
	gate := store.ClassifyGlobalRiskGate(tradeDate, quotes)
	if err := s.st.SaveGlobalRiskGate(ctx, gate); err != nil {
		slog.Warn("保存全球风险门失败", "err", err)
	}
	slog.Info("全球风险门判定", "level", gate.Level, "score", gate.Score, "reason", gate.Reason)
	return gate
}

// globalGateForToday 读取当日（Asia/Shanghai）已落库的全球风险门。
// 盘中风控用：只读不采集，避免 30 分钟档反复请求外盘接口。
// 无记录或读取失败时返回 nil——盘中缺失时不猜测，按常规纪律执行。
func (s *Service) globalGateForToday(ctx context.Context) *store.GlobalRiskGate {
	today := time.Now().In(shanghai()).Format("2006-01-02")
	gate, err := s.st.GlobalRiskGateForDate(ctx, today)
	if err != nil {
		slog.Warn("读取当日全球风险门失败", "err", err)
		return nil
	}
	return gate
}

// nextGlobalGateRun 计算下一次盘前风险感知运行时间：交易日 08:05 Asia/Shanghai。
// 位于热点漏斗（08:00）与趋势推荐（08:10）之间：此时美股已收盘、A50 夜盘已
// 结束、日盘未开，隔夜信号完整且稳定。
func nextGlobalGateRun(now time.Time) time.Time {
	next := time.Date(now.Year(), now.Month(), now.Day(), 8, 5, 0, 0, now.Location())
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	for !isRecommendationTradingDay(next) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

// mergedGateReason 合成融合后的风向说明，便于日志与推荐提示词引用。
func mergedGateReason(market store.MarketGate, global store.GlobalRiskGate) string {
	return fmt.Sprintf("境内风向门[%s]：%s；全球风险门[%s]：%s", market.Level, market.Reason, global.Level, global.Reason)
}
