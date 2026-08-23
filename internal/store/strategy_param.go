package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"time"
)

const (
	// StrategyMinSamples 是「整步」调参所需的真实已退出交易样本数。
	//
	// 原值 30 在实盘中被证明不可达：每交易日最多产生 1 笔生命周期，且需用户
	// 手动确认建仓/平仓，上线一个月仅积累到 1 笔已退出样本，导致复盘连续 8 天
	// 建议 stop_loss_pct=7~8 却始终无法写入，strategy_param 长期停留在默认值，
	// 反哺闭环事实上从未运转。下调到 12 使其在合理周期内可达。
	StrategyMinSamples = 12
	// StrategyMinSelectionSamples 是「半步」调参的兜底门槛：真实交易样本不足
	// 时，改用选股阶段的机械基线样本（每条推荐都会产生，积累快得多）。
	// 机械基线不含建仓与退出择时，只反映选股质量，因此单次仅允许移动半个
	// step，用更慢的速度换取更早开始学习。
	StrategyMinSelectionSamples  = 20
	StrategyEvaluationMinSamples = 10
	StrategyFreezeDays           = 10
	StrategyRollbackDrop         = 2.0
)

// 参数提案未生效的原因，用于日志与告警（均非错误，属于正常约束拦截）。
const (
	StrategySkipInsufficientSamples = "样本不足：真实交易与机械基线样本均未达门槛"
	StrategySkipFrozen              = "参数处于冻结期，等待上一次调整完成评估"
	StrategySkipDeltaTooSmall       = "建议值与当前值差异小于半个步长，无需调整"
	StrategySkipAtBoundary          = "已处于数据库允许的边界值，无法继续移动"
)

// StrategyRiskPolicy 是确定性风控引擎的动态参数快照。数据库不可用或参数异常时
// 使用 DefaultStrategyRiskPolicy，保证风险引擎不会因配置故障失效。
type StrategyRiskPolicy struct {
	StopLossPct         float64 `json:"stop_loss_pct"`
	StopLossATRMult     float64 `json:"stop_loss_atr_mult"`
	StopLossMaxPct      float64 `json:"stop_loss_max_pct"`
	TrailingArmPct      float64 `json:"trailing_arm_pct"`
	TrailingGivebackPct float64 `json:"trailing_giveback_pct"`
	TakeProfitPct       float64 `json:"take_profit_pct"`
	TimeStopDays        int     `json:"time_stop_days"`
	TimeStopMinPct      float64 `json:"time_stop_min_pct"`
	MaxHoldDays         int     `json:"max_hold_days"`
}

func DefaultStrategyRiskPolicy() StrategyRiskPolicy {
	return StrategyRiskPolicy{
		StopLossPct: PositionStopLossPct, StopLossATRMult: PositionStopLossATRMult,
		StopLossMaxPct: PositionStopLossMaxPct, TrailingArmPct: PositionTrailingArmPct,
		TrailingGivebackPct: PositionTrailingGivebackPct, TakeProfitPct: PositionTakeProfitPct,
		TimeStopDays: PositionTimeStopDays, TimeStopMinPct: PositionTimeStopMinPct,
		MaxHoldDays: PositionMaxHoldDays,
	}
}

// StrategyRiskPolicySnapshot 一次性加载全部参数，盘中一轮分析共用同一快照，
// 避免同一批标的因参数在处理中变化而采用不同纪律。
func (s *Store) StrategyRiskPolicySnapshot(ctx context.Context) (StrategyRiskPolicy, error) {
	policy := DefaultStrategyRiskPolicy()
	rows, err := s.DB.QueryContext(ctx, `SELECT param_key,value_num FROM strategy_param`)
	if err != nil {
		return policy, err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var value float64
		if err := rows.Scan(&key, &value); err != nil {
			return policy, err
		}
		switch key {
		case "stop_loss_pct":
			policy.StopLossPct = value
		case "stop_loss_atr_mult":
			policy.StopLossATRMult = value
		case "stop_loss_max_pct":
			policy.StopLossMaxPct = value
		case "trailing_arm_pct":
			policy.TrailingArmPct = value
		case "trailing_giveback_pct":
			policy.TrailingGivebackPct = value
		case "take_profit_pct":
			policy.TakeProfitPct = value
		case "time_stop_days":
			policy.TimeStopDays = int(math.Round(value))
		case "time_stop_min_pct":
			policy.TimeStopMinPct = value
		case "max_hold_days":
			policy.MaxHoldDays = int(math.Round(value))
		}
	}
	return policy, rows.Err()
}

type StrategyParam struct {
	Key           string  `json:"key"`
	Value         float64 `json:"value"`
	Default       float64 `json:"default"`
	Min           float64 `json:"min"`
	Max           float64 `json:"max"`
	Step          float64 `json:"step"`
	FrozenUntil   *string `json:"frozen_until,omitempty"`
	UpdatedSource string  `json:"updated_source"`
	UpdatedAt     string  `json:"updated_at"`
}

type StrategyParamChange struct {
	ID              int64    `json:"id"`
	ParamKey        string   `json:"param_key"`
	Previous        float64  `json:"previous"`
	Proposed        float64  `json:"proposed"`
	Applied         float64  `json:"applied"`
	BaselineScore   *float64 `json:"baseline_score,omitempty"`
	EvaluationScore *float64 `json:"evaluation_score,omitempty"`
	SampleCount     int      `json:"sample_count"`
	Source          string   `json:"source"`
	Rationale       string   `json:"rationale"`
	Status          string   `json:"status"`
	EffectiveDate   string   `json:"effective_date"`
	EvaluateAfter   string   `json:"evaluate_after"`
	CreatedAt       string   `json:"created_at"`
}

func (s *Store) StrategyParams(ctx context.Context) ([]StrategyParam, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT param_key,value_num,default_num,min_num,max_num,step_num,
		NULLIF(DATE_FORMAT(frozen_until,'%Y-%m-%d'),''),updated_source,DATE_FORMAT(updated_at,'%Y-%m-%d %H:%i')
		FROM strategy_param ORDER BY param_key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []StrategyParam{}
	for rows.Next() {
		var item StrategyParam
		var frozen sql.NullString
		if err := rows.Scan(&item.Key, &item.Value, &item.Default, &item.Min, &item.Max, &item.Step, &frozen, &item.UpdatedSource, &item.UpdatedAt); err != nil {
			return nil, err
		}
		if frozen.Valid {
			item.FrozenUntil = &frozen.String
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) RecentStrategyParamChanges(ctx context.Context, limit int) ([]StrategyParamChange, error) {
	if limit <= 0 || limit > 200 {
		limit = 30
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id,param_key,previous_num,proposed_num,applied_num,baseline_score,evaluation_score,
		sample_count,source,rationale,status,DATE_FORMAT(effective_date,'%Y-%m-%d'),DATE_FORMAT(evaluate_after,'%Y-%m-%d'),DATE_FORMAT(created_at,'%Y-%m-%d %H:%i')
		FROM strategy_param_change ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []StrategyParamChange{}
	for rows.Next() {
		var item StrategyParamChange
		var baseline, evaluation sql.NullFloat64
		if err := rows.Scan(&item.ID, &item.ParamKey, &item.Previous, &item.Proposed, &item.Applied, &baseline, &evaluation, &item.SampleCount, &item.Source, &item.Rationale, &item.Status, &item.EffectiveDate, &item.EvaluateAfter, &item.CreatedAt); err != nil {
			return nil, err
		}
		if baseline.Valid {
			item.BaselineScore = &baseline.Float64
		}
		if evaluation.Valid {
			item.EvaluationScore = &evaluation.Float64
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// ApplyStrategyParamProposal 应用一个受约束参数提案：样本量达标；冻结期内拒绝；
// 单次最多移动一个 step（样本仅机械基线达标时为半个 step）；数据库 min/max 是
// 最终边界。AI 无权绕过这些约束。
//
// 返回值：applied 表示是否真的写入；skipped 非空表示被约束拦截的原因（非错误，
// 供调用方告警留痕，避免像此前那样静默失败导致闭环长期空转）。
func (s *Store) ApplyStrategyParamProposal(ctx context.Context, key string, proposed float64, score StrategyScorecard, effectiveDate, source, rationale string) (applied bool, skipped string, err error) {
	// 优先用真实交易样本走整步；不足时回退到选股机械基线样本走半步。
	stepScale := 1.0
	if score.Overall.Samples < StrategyMinSamples {
		if score.Stages.Selection.Samples < StrategyMinSelectionSamples {
			return false, StrategySkipInsufficientSamples, nil
		}
		stepScale = 0.5
	}
	if effectiveDate == "" {
		effectiveDate = time.Now().Format("2006-01-02")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, "", err
	}
	defer tx.Rollback()
	var current, minValue, maxValue, step float64
	var frozen sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT value_num,min_num,max_num,step_num,DATE_FORMAT(frozen_until,'%Y-%m-%d') FROM strategy_param WHERE param_key=? FOR UPDATE`, key).Scan(&current, &minValue, &maxValue, &step, &frozen)
	if err == sql.ErrNoRows {
		return false, "", fmt.Errorf("未知策略参数: %s", key)
	}
	if err != nil {
		return false, "", err
	}
	if frozen.Valid && frozen.String >= effectiveDate {
		return false, StrategySkipFrozen, nil
	}
	moveStep := step * stepScale
	delta := proposed - current
	if math.Abs(delta) < moveStep/2 {
		return false, StrategySkipDeltaTooSmall, nil
	}
	appliedValue := current + math.Copysign(moveStep, delta)
	appliedValue = math.Max(minValue, math.Min(maxValue, appliedValue))
	if appliedValue == current {
		return false, StrategySkipAtBoundary, nil
	}
	effective, err := time.Parse("2006-01-02", effectiveDate)
	if err != nil {
		return false, "", err
	}
	evaluateAfter := effective.AddDate(0, 0, StrategyFreezeDays).Format("2006-01-02")
	if _, err = tx.ExecContext(ctx, `UPDATE strategy_param SET value_num=?,frozen_until=?,updated_source=? WHERE param_key=?`, appliedValue, evaluateAfter, source, key); err != nil {
		return false, "", err
	}
	// sample_count 记录本次决策依据的样本数：整步用真实交易样本，半步用选股
	// 机械样本，便于事后审计「这次调整到底基于多少证据」。
	sampleCount := score.Overall.Samples
	if stepScale < 1 {
		sampleCount = score.Stages.Selection.Samples
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO strategy_param_change(param_key,previous_num,proposed_num,applied_num,baseline_score,sample_count,source,rationale,status,effective_date,evaluate_after) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, key, current, proposed, appliedValue, score.Overall.Score, sampleCount, source, rationale, "active", effectiveDate, evaluateAfter); err != nil {
		return false, "", err
	}
	if err := tx.Commit(); err != nil {
		return false, "", err
	}
	return true, "", nil
}

func strategyEvaluationReady(samples int) bool {
	return samples >= StrategyEvaluationMinSamples
}

func strategyShouldRollback(evaluationScore, baselineScore float64) bool {
	return evaluationScore < baselineScore-StrategyRollbackDrop
}

// EvaluateStrategyParamChanges 在冻结期结束后用统一总分做确定性验收：下降超过2分自动回滚，
// 否则保留。一次只允许同参数一条 active 变更，因此回滚不会覆盖后续人工变更。
func (s *Store) EvaluateStrategyParamChanges(ctx context.Context, date string, score StrategyScorecard) error {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,param_key,previous_num,applied_num,baseline_score,DATE_FORMAT(effective_date,'%Y-%m-%d') FROM strategy_param_change WHERE status='active' AND evaluate_after<=? ORDER BY id`, date)
	if err != nil {
		return err
	}
	type pending struct {
		id                          int64
		key                         string
		previous, applied, baseline float64
		effectiveDate               string
	}
	items := []pending{}
	for rows.Next() {
		var item pending
		if err := rows.Scan(&item.id, &item.key, &item.previous, &item.applied, &item.baseline, &item.effectiveDate); err != nil {
			rows.Close()
			return err
		}
		items = append(items, item)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, item := range items {
		var evaluationSamples int
		if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM position
			WHERE status=? AND data_quality='' AND exit_date>? AND exit_date<=?`,
			PositionExited, item.effectiveDate, date).Scan(&evaluationSamples); err != nil {
			return err
		}
		if !strategyEvaluationReady(evaluationSamples) {
			// 冻结期只是最早验收时间。新增结算样本不足时顺延，避免用调整前的旧样本
			// 判断参数优劣，也避免无交易期间自动判定“保留”。
			if _, err := s.DB.ExecContext(ctx, `UPDATE strategy_param_change
				SET evaluate_after=DATE_ADD(?, INTERVAL 1 DAY)
				WHERE id=? AND status='active'`, date, item.id); err != nil {
				return err
			}
			continue
		}
		tx, err := s.DB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		status := "kept"
		if strategyShouldRollback(score.Overall.Score, item.baseline) {
			result, err := tx.ExecContext(ctx, `UPDATE strategy_param SET value_num=?,frozen_until=NULL,updated_source='auto_rollback' WHERE param_key=? AND value_num=?`, item.previous, item.key, item.applied)
			if err != nil {
				tx.Rollback()
				return err
			}
			if affected, _ := result.RowsAffected(); affected > 0 {
				status = "reverted"
			}
		} else {
			if _, err := tx.ExecContext(ctx, `UPDATE strategy_param SET frozen_until=NULL WHERE param_key=? AND value_num=?`, item.key, item.applied); err != nil {
				tx.Rollback()
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE strategy_param_change SET status=?,evaluation_score=?,evaluated_at=CURRENT_TIMESTAMP WHERE id=? AND status='active'`, status, score.Overall.Score, item.id); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) SaveStrategyScorecard(ctx context.Context, date string, report StrategyScorecard) error {
	payload, err := json.Marshal(report)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO strategy_scorecard(score_date,market_phase,window_days,overall_score,sample_count,payload) VALUES(?,?,?,?,?,?) ON DUPLICATE KEY UPDATE overall_score=VALUES(overall_score),sample_count=VALUES(sample_count),payload=VALUES(payload),created_at=CURRENT_TIMESTAMP`, date, report.Phase, report.WindowDays, report.Overall.Score, report.Overall.Samples, payload)
	return err
}
