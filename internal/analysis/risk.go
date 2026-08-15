package analysis

import (
	"fmt"
	"math"

	"github.com/hoax/go-stock/internal/model"
	"github.com/hoax/go-stock/internal/store"
)

// riskAction 是确定性风控引擎的输出动作。
type riskAction string

const (
	riskActionNone   riskAction = ""       // 无风控触发，交由 AI 判断
	riskActionExit   riskAction = "exit"   // 立即清仓
	riskActionReduce riskAction = "reduce" // 分批减仓保护利润
)

// riskDecision 是一次风控评估结论。Kind 用于退出归因，便于复盘各条规则贡献。
type riskDecision struct {
	Action riskAction
	Kind   string
	Reason string
}

// riskInput 是风控评估所需的全部事实，全部来自实时行情与本地日 K，
// 不含任何 AI 输出，因此结果完全确定、可回测、可单测。
type riskInput struct {
	Price        float64
	EntryPrice   float64
	HighestPrice float64
	HoldDays     int
	PositionPct  float64
	ATRPct       float64 // ATR14 相对价格的百分比，用于自适应止损
	MA10         float64
	MA20         float64
	SectorWeak   bool
	MarketAvgPct float64
	IndexTotal   int
	IndexFalling int
	IsTailSlot   bool // 是否处于尾盘档（14:52），趋势破位仅在此档确认
}

// stopLossDistancePct 计算本笔的止损距离：在固定基准与 ATR 自适应之间取较大者，
// 并受上限约束。高波动标的给更宽的止损，避免被正常波动扫出；
// 低波动标的收紧止损，减少无谓亏损。
func stopLossDistancePct(atrPct float64) float64 {
	distance := store.PositionStopLossPct
	if atrPct > 0 {
		if adaptive := atrPct * store.PositionStopLossATRMult; adaptive > distance {
			distance = adaptive
		}
	}
	if distance > store.PositionStopLossMaxPct {
		distance = store.PositionStopLossMaxPct
	}
	return distance
}

// evaluateRisk 是确定性风控引擎，按优先级返回第一条命中的规则。
//
// 优先级设计（从高到低）：
//  1. 硬止损     —— 截断亏损是盈利公式中权重最大的一项，必须最先判断
//  2. 系统性风险 —— 大盘整体崩塌时个股结构无意义
//  3. 移动止盈   —— 保护已有浮盈，避免利润回吐成亏损
//  4. 硬止盈     —— 动量充分兑现后落袋
//  5. 时间止损   —— 入场 edge 半衰期内未兑现，优势已衰减
//  6. 趋势破位   —— 仅在尾盘档以收盘价近似确认，避免盘中插针误杀
//  7. 最长持有   —— 兜底，防止长期占用仓位
//
// 返回 riskActionNone 时表示无确定性风险，交由 AI 结合三层上下文判断。
func evaluateRisk(in riskInput) riskDecision {
	if in.Price <= 0 || in.EntryPrice <= 0 {
		return riskDecision{Action: riskActionNone}
	}
	profitPct := (in.Price/in.EntryPrice - 1) * 100

	// 1. 硬止损：相对建仓成本的最大可接受回撤。
	stopDistance := stopLossDistancePct(in.ATRPct)
	if profitPct <= -stopDistance {
		return riskDecision{
			Action: riskActionExit,
			Kind:   store.ExitKindStopLoss,
			Reason: fmt.Sprintf("现价%.2f较建仓价%.2f亏损%.2f%%，触发硬止损（阈值%.1f%%）", in.Price, in.EntryPrice, profitPct, stopDistance),
		}
	}

	// 2. 系统性风险：按下跌指数占比判定，避免要求「全部下跌」而永不触发。
	if in.IndexTotal >= 3 {
		fallingRatio := float64(in.IndexFalling) / float64(in.IndexTotal)
		if fallingRatio >= 2.0/3.0 && in.MarketAvgPct <= -1.5 {
			return riskDecision{
				Action: riskActionExit,
				Kind:   store.ExitKindSystemic,
				Reason: fmt.Sprintf("%d/%d指数下跌且平均跌幅%.2f%%，系统性风险放大，退出规避隔夜风险", in.IndexFalling, in.IndexTotal, in.MarketAvgPct),
			}
		}
	}

	// 3. 移动止盈：浮盈冲过激活线后，自最高点回撤超阈值即保护离场。
	if in.HighestPrice > in.EntryPrice {
		peakPct := (in.HighestPrice/in.EntryPrice - 1) * 100
		if peakPct >= store.PositionTrailingArmPct {
			giveback := peakPct - profitPct
			if giveback >= store.PositionTrailingGivebackPct {
				return riskDecision{
					Action: riskActionExit,
					Kind:   store.ExitKindTrailingStop,
					Reason: fmt.Sprintf("最高浮盈%.2f%%回落至%.2f%%，回撤%.2f%%触发移动止盈，锁定利润", peakPct, profitPct, giveback),
				}
			}
		}
	}

	// 4. 硬止盈：达到目标收益，先减仓落袋，剩余仓位继续由移动止盈保护。
	//    仓位已降到最小阈值以下时直接清仓，避免碎仓长期占位。
	if profitPct >= store.PositionTakeProfitPct {
		if in.PositionPct > store.PositionMinPositionPct {
			return riskDecision{
				Action: riskActionReduce,
				Kind:   store.ExitKindTakeProfit,
				Reason: fmt.Sprintf("浮盈%.2f%%达到目标，减仓%.0f%%落袋，剩余仓位由移动止盈保护", profitPct, store.PositionReducePct),
			}
		}
		return riskDecision{
			Action: riskActionExit,
			Kind:   store.ExitKindTakeProfit,
			Reason: fmt.Sprintf("浮盈%.2f%%达到目标且仓位已减至%.0f%%，全部落袋", profitPct, in.PositionPct),
		}
	}

	// 5. 时间止损：入场 edge 是 1-5 日尺度，N 日内未兑现说明动量已衰减。
	if in.HoldDays >= store.PositionTimeStopDays && profitPct < store.PositionTimeStopMinPct {
		return riskDecision{
			Action: riskActionExit,
			Kind:   store.ExitKindTimeStop,
			Reason: fmt.Sprintf("持有%d个交易日浮盈仅%.2f%%，未达%.1f%%动量兑现线，优势衰减退出腾位", in.HoldDays, profitPct, store.PositionTimeStopMinPct),
		}
	}

	// 6. 趋势破位：只在尾盘档确认，且要求跌破缓冲带，避免盘中插针误杀。
	//    退出尺度改用 MA10，与 1-5 日入场 edge 对齐（原 MA20 尺度过慢）。
	if in.IsTailSlot && in.MA10 > 0 {
		buffer := in.MA10 * 0.99
		if in.Price < buffer && (in.SectorWeak || in.MarketAvgPct < 0) {
			return riskDecision{
				Action: riskActionExit,
				Kind:   store.ExitKindTrendBreak,
				Reason: fmt.Sprintf("尾盘现价%.2f有效跌破MA10 %.2f且板块或大盘转弱，短线趋势结构破坏", in.Price, in.MA10),
			}
		}
	}

	// 7. 最长持有兜底。
	if in.HoldDays >= store.PositionMaxHoldDays {
		return riskDecision{
			Action: riskActionExit,
			Kind:   store.ExitKindTimeStop,
			Reason: fmt.Sprintf("已持有%d个交易日达到上限，释放仓位给新机会", in.HoldDays),
		}
	}

	return riskDecision{Action: riskActionNone}
}

// atrPercent 用最近 14 根日 K 计算 ATR 相对最新收盘价的百分比。
// 数据不足时返回 0，调用方回退到固定止损距离。
func atrPercent(klines []model.Kline) float64 {
	const period = 14
	if len(klines) < period+1 {
		return 0
	}
	var sum float64
	start := len(klines) - period
	for i := start; i < len(klines); i++ {
		high, low, prevClose := klines[i].High, klines[i].Low, klines[i-1].Close
		if high <= 0 || low <= 0 || prevClose <= 0 {
			return 0
		}
		tr := math.Max(high-low, math.Max(math.Abs(high-prevClose), math.Abs(low-prevClose)))
		sum += tr
	}
	atr := sum / period
	last := klines[len(klines)-1].Close
	if last <= 0 {
		return 0
	}
	return atr / last * 100
}
