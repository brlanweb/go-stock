-- 市场快照由后台定时任务写入；页面、REST 和 MCP 只从本表读取，绝不在请求链路调用外部行情源。
CREATE TABLE IF NOT EXISTS market_snapshot (
    snapshot_at   DATETIME    NOT NULL,
    symbol        VARCHAR(24) NOT NULL,
    code          VARCHAR(16) NOT NULL DEFAULT '',
    name          VARCHAR(64) NOT NULL DEFAULT '',
    sec_type      VARCHAR(8)  NOT NULL DEFAULT 'stock',
    exchange      VARCHAR(8)  NOT NULL DEFAULT '',
    source        VARCHAR(24) NOT NULL DEFAULT 'eastmoney',
    price         DOUBLE      NOT NULL DEFAULT 0,
    change_pct    DOUBLE      NOT NULL DEFAULT 0,
    change_amount DOUBLE      NOT NULL DEFAULT 0,
    volume        BIGINT      NOT NULL DEFAULT 0,
    amount        DOUBLE      NOT NULL DEFAULT 0,
    volume_ratio  DOUBLE      NOT NULL DEFAULT 0,
    turnover_rate DOUBLE      NOT NULL DEFAULT 0,
    amplitude     DOUBLE      NOT NULL DEFAULT 0,
    open          DOUBLE      NOT NULL DEFAULT 0,
    high          DOUBLE      NOT NULL DEFAULT 0,
    low           DOUBLE      NOT NULL DEFAULT 0,
    pre_close     DOUBLE      NOT NULL DEFAULT 0,
    pe_ratio      DOUBLE      NOT NULL DEFAULT 0,
    pb_ratio      DOUBLE      NOT NULL DEFAULT 0,
    total_mv      DOUBLE      NOT NULL DEFAULT 0,
    circ_mv       DOUBLE      NOT NULL DEFAULT 0,
    PRIMARY KEY (snapshot_at, symbol),
    KEY idx_symbol_time (symbol, snapshot_at),
    KEY idx_time_type (snapshot_at, sec_type)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci COMMENT='全市场定时行情快照';

CREATE TABLE IF NOT EXISTS index_snapshot (
    snapshot_at DATETIME    NOT NULL,
    symbol      VARCHAR(24) NOT NULL,
    name        VARCHAR(64) NOT NULL DEFAULT '',
    price       DOUBLE      NOT NULL DEFAULT 0,
    change_pct  DOUBLE      NOT NULL DEFAULT 0,
    amount      DOUBLE      NOT NULL DEFAULT 0,
    volume      BIGINT      NOT NULL DEFAULT 0,
    source      VARCHAR(24) NOT NULL DEFAULT 'snapshot',
    PRIMARY KEY (snapshot_at, symbol),
    KEY idx_symbol_time (symbol, snapshot_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci COMMENT='大盘指数定时快照';
