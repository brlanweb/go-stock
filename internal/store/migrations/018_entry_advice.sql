-- 盘中 AI 建仓建议与每日最佳建仓股记录：
-- 初始版本仅记录盘中建仓；019_position_lifecycle.sql 会扩展为建仓/退出双阶段，
-- 并把调度调整为交易时段每 30 分钟分析全部活跃生命周期标的。
CREATE TABLE IF NOT EXISTS entry_advice (
    id          BIGINT       NOT NULL AUTO_INCREMENT,
    trade_date  DATE         NOT NULL,
    symbol      VARCHAR(24)  NOT NULL DEFAULT '',
    source      VARCHAR(16)  NOT NULL DEFAULT 'hourly_ai' COMMENT 'daily_pick/hourly_ai',
    action      VARCHAR(16)  NOT NULL DEFAULT 'wait' COMMENT 'pick/entry/wait',
    reason      VARCHAR(512) NOT NULL DEFAULT '',
    model_name  VARCHAR(64)  NOT NULL DEFAULT '',
    created_at  TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    KEY idx_date_action (trade_date, action),
    KEY idx_date_source (trade_date, source)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci COMMENT='AI建仓建议';
