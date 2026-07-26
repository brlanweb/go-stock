CREATE TABLE IF NOT EXISTS indicator_definition (
    indicator_id VARCHAR(64) NOT NULL,
    display_name VARCHAR(128) NOT NULL,
    description VARCHAR(512) NOT NULL DEFAULT '',
    category VARCHAR(32) NOT NULL,
    kind VARCHAR(16) NOT NULL,
    capability VARCHAR(24) NOT NULL,
    source VARCHAR(64) NOT NULL,
    enabled TINYINT(1) NOT NULL DEFAULT 1,
    default_params TEXT NOT NULL,
    current_params TEXT NOT NULL,
    sort_order INT NOT NULL DEFAULT 0,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (indicator_id),
    KEY idx_indicator_enabled_sort (enabled, sort_order)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE IF NOT EXISTS backtest_run (
    run_id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    symbol VARCHAR(24) NOT NULL,
    indicator_id VARCHAR(64) NOT NULL,
    period VARCHAR(16) NOT NULL DEFAULT 'day',
    start_date DATE NULL,
    end_date DATE NULL,
    initial_cash DECIMAL(18,2) NOT NULL,
    final_equity DECIMAL(18,2) NOT NULL,
    total_return DECIMAL(12,6) NOT NULL,
    annual_return DECIMAL(12,6) NOT NULL,
    max_drawdown DECIMAL(12,6) NOT NULL,
    sharpe_ratio DECIMAL(12,6) NOT NULL,
    win_rate DECIMAL(12,6) NOT NULL,
    profit_loss_ratio DECIMAL(12,6) NOT NULL,
    profit_factor DECIMAL(12,6) NOT NULL,
    trade_count INT NOT NULL,
    params TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (run_id),
    KEY idx_backtest_symbol_time (symbol, created_at),
    KEY idx_backtest_indicator_time (indicator_id, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE IF NOT EXISTS backtest_trade (
    trade_id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    run_id BIGINT UNSIGNED NOT NULL,
    entry_date DATE NOT NULL,
    entry_price DECIMAL(18,6) NOT NULL,
    exit_date DATE NULL,
    exit_price DECIMAL(18,6) NULL,
    shares BIGINT NOT NULL,
    pnl DECIMAL(18,2) NOT NULL DEFAULT 0,
    return_pct DECIMAL(12,6) NOT NULL DEFAULT 0,
    entry_reason VARCHAR(256) NOT NULL DEFAULT '',
    exit_reason VARCHAR(256) NOT NULL DEFAULT '',
    PRIMARY KEY (trade_id),
    KEY idx_backtest_trade_run (run_id, entry_date)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;
