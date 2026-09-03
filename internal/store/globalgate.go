package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/hoax/go-stock/internal/model"
)

// 全球风险门（Global Risk Gate）：风险感知板块的确定性核心。
// 在盘前（08:10 推荐链路启动时）用隔夜外盘因子提前识别系统性风险——
// 千股跌停类的普跌日，前一晚的 A50 夜盘、金龙指数、美股、VIX 几乎总有前兆。
//
// 与指数风向门（marketgate.go）的关系：
//   - MarketGate 基于境内 T-1 收盘结构（滞后确认）；
//   - GlobalGate 基于隔夜外盘（提前预警）；
//   - 融合规则取最严档位，GlobalGate 永远只能收紧、不能放宽 MarketGate。
//
// 全部输入为行情数值，打分完全确定性、可单测、可回测；AI 不参与判定。
//
// 因子分档（0 / -1 / -2，A50 因子最高 -3，因其直接定价 A 股开盘预期）：
//   - A50期指     ≤ -2.0% → -3；≤ -1.2% → -2；≤ -0.6% → -1
//   - 金龙指数    ≤ -3.0% → -2；≤ -1.5% → -1
//   - 美股(标普+纳指均值) ≤ -1.5% → -2；≤ -0.8% → -1
//   - VIX        ≥ 30 或单日涨 ≥ 20% → -2；≥ 25 或涨 ≥ 10% → -1
//   - 离岸人民币  隔夜贬值 ≥ 1.0% → -2；≥ 0.5% → -1
//
// 档位判定：total ≤ -4 → red；total ≤ -2 → yellow；核心数据缺失（A50 与美股
// 同时缺失）→ yellow 保守降档；其余 → green。
const (
	globalGateA50SevereChgPct = -2.0
	globalGateA50HeavyChgPct  = -1.2
	globalGateA50LightChgPct  = -0.6
	globalGateADRHeavyChgPct  = -3.0
	globalGateADRLightChgPct  = -1.5
	globalGateUSHeavyChgPct   = -1.5
	globalGateUSLightChgPct   = -0.8
	globalGateVIXHeavyLevel   = 30.0
	globalGateVIXLightLevel   = 25.0
	globalGateVIXHeavyChgPct  = 20.0
	globalGateVIXLightChgPct  = 10.0
	globalGateFXHeavyChgPct   = 1.0
	globalGateFXLightChgPct   = 0.5
	globalGateRedScore        = -4
	globalGateYellowScore     = -2

	// globalGateMinFactors 是可感知风险所需的最少有效因子数。
	//
	// 2026-09 生产数据显示：vix 与 china_adr 长期 has_data=false，5 个因子
	// 实际只有 3 个在工作。因子缺失会静默削弱负分累积能力——VIX 与金龙合计
	// 可贡献 -4 分，恰好等于红灯阈值，两者失效意味着红灯几乎不可能触发。
	// 旧守卫只在「A50 与美股同时缺失」时降档，掩盖了这类部分失效：
	// 近 10 个交易日全部判定为 green，风险门形同虚设。
	//
	// 因此改为按有效因子数量守卫：少于 3 个有效因子时无法可靠感知隔夜风险，
	// 一律保守降档，并在 Reason 中点名缺失因子，便于定位数据源故障。
	globalGateMinFactors = 3
)

// GlobalRiskSignal 是单一外盘因子的判定事实。
type GlobalRiskSignal struct {
	Factor    string  `json:"factor"`     // a50/china_adr/us_equity/vix/fx
	Name      string  `json:"name"`       // 中文名
	Price     float64 `json:"price"`      // 最新值
	ChangePct float64 `json:"change_pct"` // 涨跌幅%
	HasData   bool    `json:"has_data"`
	Score     int     `json:"score"` // 0/-1/-2/-3
	Note      string  `json:"note"`  // 判定说明
}

// GlobalRiskGate 是一次全球风险判定的完整结论与依据。
type GlobalRiskGate struct {
	TradeDate string             `json:"trade_date"` // 保护的交易日（当日）
	Level     string             `json:"level"`      // 复用 MarketGate 三档常量
	Score     int                `json:"score"`
	Reason    string             `json:"reason"`
	Signals   []GlobalRiskSignal `json:"signals"`
	CreatedAt string             `json:"created_at,omitempty"`
}

// Defensive 表示是否应进入持仓防御模式（红灯）。
func (g GlobalRiskGate) Defensive() bool { return g.Level == MarketGateRed }

// quoteBySymbol 提取指定 symbol 的有效行情。
func globalQuoteBySymbol(quotes []model.GlobalQuote, symbol string) (model.GlobalQuote, bool) {
	for _, q := range quotes {
		if q.Symbol == symbol && q.Price != nil && q.ChangePct != nil {
			return q, true
		}
	}
	return model.GlobalQuote{}, false
}

// ClassifyGlobalRiskGate 是纯函数分类器：按隔夜外盘因子输出全球风险档位。
// 不做任何 IO，结果完全由输入行情决定。
func ClassifyGlobalRiskGate(tradeDate string, quotes []model.GlobalQuote) GlobalRiskGate {
	gate := GlobalRiskGate{TradeDate: tradeDate}
	signals := make([]GlobalRiskSignal, 0, 5)
	total := 0
	reasons := make([]string, 0, 5)

	// 1. A50 期指：最高权重因子。
	a50 := GlobalRiskSignal{Factor: "a50", Name: "A50期指"}
	if q, ok := globalQuoteBySymbol(quotes, "CN00Y"); ok {
		a50.HasData, a50.Price, a50.ChangePct = true, *q.Price, *q.ChangePct
		switch {
		case a50.ChangePct <= globalGateA50SevereChgPct:
			a50.Score, a50.Note = -3, fmt.Sprintf("A50期指夜盘暴跌%.2f%%，A股开盘预期严重承压", a50.ChangePct)
		case a50.ChangePct <= globalGateA50HeavyChgPct:
			a50.Score, a50.Note = -2, fmt.Sprintf("A50期指夜盘大跌%.2f%%，开盘预期显著转弱", a50.ChangePct)
		case a50.ChangePct <= globalGateA50LightChgPct:
			a50.Score, a50.Note = -1, fmt.Sprintf("A50期指夜盘走弱%.2f%%", a50.ChangePct)
		}
	}
	signals = append(signals, a50)

	// 2. 纳斯达克金龙中国指数：中概股情绪传导。
	adr := GlobalRiskSignal{Factor: "china_adr", Name: "金龙指数"}
	if q, ok := globalQuoteBySymbol(quotes, "HXC"); ok {
		adr.HasData, adr.Price, adr.ChangePct = true, *q.Price, *q.ChangePct
		switch {
		case adr.ChangePct <= globalGateADRHeavyChgPct:
			adr.Score, adr.Note = -2, fmt.Sprintf("金龙指数隔夜重挫%.2f%%，中概情绪恶化", adr.ChangePct)
		case adr.ChangePct <= globalGateADRLightChgPct:
			adr.Score, adr.Note = -1, fmt.Sprintf("金龙指数隔夜下跌%.2f%%", adr.ChangePct)
		}
	}
	signals = append(signals, adr)

	// 3. 美股：标普 500 与纳斯达克均值（去除单指数噪音）。
	us := GlobalRiskSignal{Factor: "us_equity", Name: "美股(标普+纳指)"}
	{
		count, sum := 0, 0.0
		for _, symbol := range []string{"SPX", "NDX"} {
			if q, ok := globalQuoteBySymbol(quotes, symbol); ok {
				count++
				sum += *q.ChangePct
			}
		}
		if count > 0 {
			us.HasData, us.ChangePct = true, sum/float64(count)
			switch {
			case us.ChangePct <= globalGateUSHeavyChgPct:
				us.Score, us.Note = -2, fmt.Sprintf("美股隔夜大跌%.2f%%，全球风险偏好收缩", us.ChangePct)
			case us.ChangePct <= globalGateUSLightChgPct:
				us.Score, us.Note = -1, fmt.Sprintf("美股隔夜下跌%.2f%%", us.ChangePct)
			}
		}
	}
	signals = append(signals, us)

	// 4. VIX：恐慌水平（绝对值）与单日跳升（变化率）双口径。
	vix := GlobalRiskSignal{Factor: "vix", Name: "VIX恐慌指数"}
	if q, ok := globalQuoteBySymbol(quotes, "VIX"); ok {
		vix.HasData, vix.Price, vix.ChangePct = true, *q.Price, *q.ChangePct
		switch {
		case vix.Price >= globalGateVIXHeavyLevel || vix.ChangePct >= globalGateVIXHeavyChgPct:
			vix.Score, vix.Note = -2, fmt.Sprintf("VIX达%.1f（单日%+.1f%%），恐慌情绪显著", vix.Price, vix.ChangePct)
		case vix.Price >= globalGateVIXLightLevel || vix.ChangePct >= globalGateVIXLightChgPct:
			vix.Score, vix.Note = -1, fmt.Sprintf("VIX升至%.1f（单日%+.1f%%）", vix.Price, vix.ChangePct)
		}
	}
	signals = append(signals, vix)

	// 5. 离岸人民币：USDCNH 上涨 = 人民币贬值 = 资金外流压力。
	fx := GlobalRiskSignal{Factor: "fx", Name: "离岸人民币"}
	if q, ok := globalQuoteBySymbol(quotes, "USDCNH"); ok {
		fx.HasData, fx.Price, fx.ChangePct = true, *q.Price, *q.ChangePct
		switch {
		case fx.ChangePct >= globalGateFXHeavyChgPct:
			fx.Score, fx.Note = -2, fmt.Sprintf("离岸人民币隔夜贬值%.2f%%，资金外流压力大", fx.ChangePct)
		case fx.ChangePct >= globalGateFXLightChgPct:
			fx.Score, fx.Note = -1, fmt.Sprintf("离岸人民币隔夜贬值%.2f%%", fx.ChangePct)
		}
	}
	signals = append(signals, fx)

	for _, signal := range signals {
		total += signal.Score
		if signal.Score < 0 {
			reasons = append(reasons, signal.Note)
		}
	}
	gate.Signals, gate.Score = signals, total

	// 数据完备性守卫（先于档位判定）：因子缺失会削弱负分累积能力，
	// 让「无风险信号」与「感知不到风险」在结果上无法区分。
	missing := make([]string, 0, len(signals))
	available := 0
	for _, signal := range signals {
		if signal.HasData {
			available++
		} else {
			missing = append(missing, signal.Name)
		}
	}
	// 核心因子缺失：A50 与美股同时缺失时完全无法感知隔夜风险。
	if !a50.HasData && !us.HasData {
		gate.Level = MarketGateYellow
		gate.Reason = "A50期指与美股行情均缺失，隔夜风险不可感知，保守降档"
		return gate
	}
	// 有效因子不足：即便核心因子在，样本太少也不足以支撑绿灯结论。
	if available < globalGateMinFactors {
		gate.Level = MarketGateYellow
		gate.Reason = fmt.Sprintf("仅%d/%d个外盘因子有数据（缺失：%s），隔夜风险感知不完整，保守降档",
			available, len(signals), strings.Join(missing, "、"))
		return gate
	}

	switch {
	case total <= globalGateRedScore:
		gate.Level = MarketGateRed
		gate.Reason = "隔夜外盘系统性风险信号共振：" + strings.Join(reasons, "；")
	case total <= globalGateYellowScore:
		gate.Level = MarketGateYellow
		gate.Reason = "隔夜外盘偏弱：" + strings.Join(reasons, "；")
	default:
		gate.Level = MarketGateGreen
		if len(reasons) > 0 {
			gate.Reason = "隔夜外盘整体平稳（" + strings.Join(reasons, "；") + "）"
		} else {
			gate.Reason = "隔夜外盘无风险信号"
		}
		// 绿灯但存在缺失因子时如实披露：避免「感知范围不全」被读成「确认安全」。
		if len(missing) > 0 {
			gate.Reason += fmt.Sprintf("；注意：%s无数据，风险感知范围不完整", strings.Join(missing, "、"))
		}
	}
	return gate
}

// StricterGateLevel 返回两个风向档位中更严的一档（red > yellow > green）。
// 用于 MarketGate 与 GlobalGate 融合：只能收紧，不能放宽。
func StricterGateLevel(a, b string) string {
	rank := func(level string) int {
		switch level {
		case MarketGateRed:
			return 2
		case MarketGateYellow:
			return 1
		default:
			return 0
		}
	}
	if rank(b) > rank(a) {
		return b
	}
	return a
}

// UpsertGlobalSnapshots 保存一轮外盘因子快照。
func (s *Store) UpsertGlobalSnapshots(ctx context.Context, capturedAt time.Time, quotes []model.GlobalQuote) error {
	if len(quotes) == 0 {
		return nil
	}
	var b strings.Builder
	b.WriteString("INSERT INTO global_snapshot (snapshot_at,symbol,name,category,price,change_pct,source) VALUES ")
	args := make([]interface{}, 0, len(quotes)*7)
	count := 0
	for _, q := range quotes {
		if q.Price == nil {
			continue
		}
		if count > 0 {
			b.WriteByte(',')
		}
		count++
		chg := 0.0
		if q.ChangePct != nil {
			chg = *q.ChangePct
		}
		b.WriteString("(?,?,?,?,?,?,?)")
		args = append(args, capturedAt, q.Symbol, q.Name, q.Category, *q.Price, chg, q.Source)
	}
	if count == 0 {
		return nil
	}
	b.WriteString(" ON DUPLICATE KEY UPDATE name=VALUES(name),category=VALUES(category),price=VALUES(price),change_pct=VALUES(change_pct),source=VALUES(source)")
	if _, err := s.DB.ExecContext(ctx, b.String(), args...); err != nil {
		return fmt.Errorf("upsert global snapshots: %w", err)
	}
	return nil
}

// SaveGlobalRiskGate 落库当日全球风险门（同日重复判定覆盖更新）。
func (s *Store) SaveGlobalRiskGate(ctx context.Context, gate GlobalRiskGate) error {
	payload, err := json.Marshal(gate.Signals)
	if err != nil {
		return fmt.Errorf("marshal global gate signals: %w", err)
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO global_risk_gate (trade_date,level,score,reason,payload) VALUES (?,?,?,?,?)
		ON DUPLICATE KEY UPDATE level=VALUES(level),score=VALUES(score),reason=VALUES(reason),payload=VALUES(payload)`,
		gate.TradeDate, gate.Level, gate.Score, gate.Reason, string(payload)); err != nil {
		return fmt.Errorf("save global risk gate: %w", err)
	}
	return nil
}

// GlobalRiskGateForDate 读取指定交易日的全球风险门；无记录返回 nil。
func (s *Store) GlobalRiskGateForDate(ctx context.Context, tradeDate string) (*GlobalRiskGate, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT DATE_FORMAT(trade_date,'%Y-%m-%d'),level,score,reason,payload,DATE_FORMAT(created_at,'%Y-%m-%d %H:%i') FROM global_risk_gate WHERE trade_date=?`, tradeDate)
	return scanGlobalRiskGate(row)
}

// LatestGlobalRiskGate 读取最近一条全球风险门；无记录返回 nil。
func (s *Store) LatestGlobalRiskGate(ctx context.Context) (*GlobalRiskGate, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT DATE_FORMAT(trade_date,'%Y-%m-%d'),level,score,reason,payload,DATE_FORMAT(created_at,'%Y-%m-%d %H:%i') FROM global_risk_gate ORDER BY trade_date DESC LIMIT 1`)
	return scanGlobalRiskGate(row)
}

// GlobalRiskGateHistory 按日期倒序返回最近 limit 条判定历史。
func (s *Store) GlobalRiskGateHistory(ctx context.Context, limit int) ([]GlobalRiskGate, error) {
	if limit <= 0 || limit > 180 {
		limit = 30
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT DATE_FORMAT(trade_date,'%Y-%m-%d'),level,score,reason,payload,DATE_FORMAT(created_at,'%Y-%m-%d %H:%i') FROM global_risk_gate ORDER BY trade_date DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("query global risk gate history: %w", err)
	}
	defer rows.Close()
	out := make([]GlobalRiskGate, 0, limit)
	for rows.Next() {
		gate, err := scanGlobalRiskGateRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *gate)
	}
	return out, rows.Err()
}

type globalGateScanner interface {
	Scan(dest ...interface{}) error
}

func scanGlobalRiskGateRow(row globalGateScanner) (*GlobalRiskGate, error) {
	var gate GlobalRiskGate
	var payload string
	if err := row.Scan(&gate.TradeDate, &gate.Level, &gate.Score, &gate.Reason, &payload, &gate.CreatedAt); err != nil {
		return nil, err
	}
	if payload != "" {
		_ = json.Unmarshal([]byte(payload), &gate.Signals)
	}
	return &gate, nil
}

func scanGlobalRiskGate(row *sql.Row) (*GlobalRiskGate, error) {
	gate, err := scanGlobalRiskGateRow(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return gate, nil
}
