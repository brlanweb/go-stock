-- 修复历史完整性统计，并保存每日 AI 推荐结果。
ALTER TABLE sync_checkpoint
    ADD COLUMN first_synced_date DATE NULL AFTER last_synced_date,
    ADD COLUMN kline_count INT NOT NULL DEFAULT 0 AFTER first_synced_date,
    ADD KEY idx_task_coverage (task, status, kline_count);

CREATE TABLE IF NOT EXISTS stock_recommendation (
    analysis_date DATE NOT NULL,
    rank_no TINYINT NOT NULL,
    symbol VARCHAR(24) NOT NULL,
    sector_name VARCHAR(64) NOT NULL DEFAULT '',
    probability DECIMAL(5,2) NOT NULL DEFAULT 0,
    reason VARCHAR(512) NOT NULL DEFAULT '',
    model_name VARCHAR(128) NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (analysis_date, rank_no),
    KEY idx_symbol_date (symbol, analysis_date)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci COMMENT='AI 趋势延续推荐';
