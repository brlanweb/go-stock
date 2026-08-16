-- 受约束的策略参数闭环。
-- AI/复盘只能提出目标值；数据库边界、单步幅度、样本门槛和冻结期由确定性代码执行。
CREATE TABLE IF NOT EXISTS strategy_param (
    param_key       VARCHAR(40)   NOT NULL,
    value_num       DECIMAL(12,4) NOT NULL,
    default_num     DECIMAL(12,4) NOT NULL,
    min_num         DECIMAL(12,4) NOT NULL,
    max_num         DECIMAL(12,4) NOT NULL,
    step_num        DECIMAL(12,4) NOT NULL,
    frozen_until    DATE          NULL,
    updated_source  VARCHAR(32)   NOT NULL DEFAULT 'default',
    updated_at      TIMESTAMP     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (param_key)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci COMMENT='受约束策略参数';

INSERT INTO strategy_param(param_key,value_num,default_num,min_num,max_num,step_num) VALUES
('stop_loss_pct',6.0,6.0,3.0,10.0,0.5),
('stop_loss_atr_mult',1.8,1.8,1.0,3.0,0.1),
('stop_loss_max_pct',10.0,10.0,6.0,15.0,0.5),
('trailing_arm_pct',5.0,5.0,3.0,10.0,0.5),
('trailing_giveback_pct',4.0,4.0,2.0,8.0,0.5),
('take_profit_pct',12.0,12.0,6.0,20.0,1.0),
('time_stop_days',3.0,3.0,2.0,8.0,1.0),
('time_stop_min_pct',3.0,3.0,0.0,8.0,0.5),
('max_hold_days',15.0,15.0,8.0,30.0,1.0)
ON DUPLICATE KEY UPDATE param_key=VALUES(param_key);

CREATE TABLE IF NOT EXISTS strategy_param_change (
    id              BIGINT        NOT NULL AUTO_INCREMENT,
    param_key       VARCHAR(40)   NOT NULL,
    previous_num    DECIMAL(12,4) NOT NULL,
    proposed_num    DECIMAL(12,4) NOT NULL,
    applied_num     DECIMAL(12,4) NOT NULL,
    baseline_score  DECIMAL(8,2)  NULL,
    evaluation_score DECIMAL(8,2) NULL,
    sample_count    INT           NOT NULL DEFAULT 0,
    source          VARCHAR(32)   NOT NULL,
    rationale       VARCHAR(255)  NOT NULL DEFAULT '',
    status          VARCHAR(16)   NOT NULL COMMENT 'active/kept/reverted/rejected',
    effective_date  DATE          NOT NULL,
    evaluate_after  DATE          NOT NULL,
    created_at      TIMESTAMP     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    evaluated_at    TIMESTAMP     NULL,
    PRIMARY KEY (id),
    KEY idx_status_date (status,evaluate_after),
    KEY idx_param_date (param_key,effective_date)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci COMMENT='策略参数调整与回滚审计';

-- 每日保存考核快照，参数调整前后必须基于快照比较，不能由 AI 自评“有效”。
CREATE TABLE IF NOT EXISTS strategy_scorecard (
    score_date      DATE         NOT NULL,
    market_phase   VARCHAR(12)  NOT NULL DEFAULT 'all',
    window_days    INT          NOT NULL,
    overall_score  DECIMAL(8,2) NOT NULL,
    sample_count   INT          NOT NULL,
    payload        MEDIUMTEXT   NOT NULL,
    created_at     TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (score_date,market_phase,window_days),
    KEY idx_phase_date (market_phase,score_date)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci COMMENT='策略考核历史快照';
