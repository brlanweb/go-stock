-- 影子基线：每次盘前推荐运行时，除 AI 结果外同步落两组确定性规则选股
-- （trend=候选池趋势分前3、low_risk=候选池风险分最低3），与 AI 推荐共用
-- “建仓机会确认 → 趋势不再可持续时退出”的动态口径统计，用于度量 AI 相对基线的超额。
-- 仅内部观测，不在前端作为投资建议展示。
CREATE TABLE IF NOT EXISTS recommendation_shadow (
    analysis_date DATE NOT NULL,
    strategy      VARCHAR(16) NOT NULL COMMENT 'trend/low_risk',
    rank_no       TINYINT NOT NULL,
    symbol        VARCHAR(24) NOT NULL,
    sector_name   VARCHAR(64) NOT NULL DEFAULT '',
    trend_score   DOUBLE NULL,
    risk_score    DOUBLE NULL,
    created_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (analysis_date, strategy, rank_no),
    KEY idx_symbol_date (symbol, analysis_date)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci COMMENT='推荐影子基线（确定性规则对照组）';
