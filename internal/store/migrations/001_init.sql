-- go-stock 初始建表（幂等：IF NOT EXISTS）
-- 设计要点：
-- 1. 所有表带 market 字段，预留 us/crypto 扩展
-- 2. kline_daily 存不复权原始价 + 累积复权因子，前复权可随时重算
-- 3. sync_checkpoint 支撑断点续传

CREATE TABLE IF NOT EXISTS stock_basic (
    symbol       VARCHAR(24)  NOT NULL COMMENT '统一代码 SH600519',
    market       VARCHAR(8)   NOT NULL DEFAULT 'cn',
    code         VARCHAR(16)  NOT NULL COMMENT '原始代码 600519',
    name         VARCHAR(64)  NOT NULL DEFAULT '',
    sec_type     VARCHAR(8)   NOT NULL DEFAULT 'stock' COMMENT 'stock/etf/index',
    exchange     VARCHAR(8)   NOT NULL DEFAULT '' COMMENT 'SH/SZ/BJ',
    industry     VARCHAR(32)  NOT NULL DEFAULT '',
    list_date    DATE         NULL,
    total_share  DOUBLE       NOT NULL DEFAULT 0 COMMENT '总股本(股)',
    float_share  DOUBLE       NOT NULL DEFAULT 0 COMMENT '流通股本(股)',
    status       VARCHAR(12)  NOT NULL DEFAULT 'listed',
    updated_at   TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (symbol),
    KEY idx_market_type (market, sec_type),
    KEY idx_name (name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci COMMENT='证券基础信息';

CREATE TABLE IF NOT EXISTS kline_daily (
    symbol        VARCHAR(24) NOT NULL,
    trade_date    DATE        NOT NULL,
    open          DOUBLE      NOT NULL DEFAULT 0 COMMENT '不复权开盘',
    high          DOUBLE      NOT NULL DEFAULT 0,
    low           DOUBLE      NOT NULL DEFAULT 0,
    close         DOUBLE      NOT NULL DEFAULT 0,
    volume        BIGINT      NOT NULL DEFAULT 0 COMMENT '成交量(股)',
    amount        DOUBLE      NOT NULL DEFAULT 0 COMMENT '成交额(元)',
    change_pct    DOUBLE      NOT NULL DEFAULT 0 COMMENT '涨跌幅%',
    turnover_rate DOUBLE      NOT NULL DEFAULT 0 COMMENT '换手率%',
    adj_factor    DOUBLE      NOT NULL DEFAULT 1 COMMENT '累积复权因子',
    PRIMARY KEY (symbol, trade_date),
    KEY idx_date (trade_date)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci COMMENT='日K线(不复权+复权因子)';

CREATE TABLE IF NOT EXISTS daily_indicator (
    symbol        VARCHAR(24) NOT NULL,
    trade_date    DATE        NOT NULL,
    close         DOUBLE      NOT NULL DEFAULT 0,
    pe_ratio      DOUBLE      NOT NULL DEFAULT 0 COMMENT '市盈率(动)',
    pb_ratio      DOUBLE      NOT NULL DEFAULT 0,
    total_mv      DOUBLE      NOT NULL DEFAULT 0 COMMENT '总市值(元)',
    circ_mv       DOUBLE      NOT NULL DEFAULT 0 COMMENT '流通市值(元)',
    turnover_rate DOUBLE      NOT NULL DEFAULT 0,
    volume_ratio  DOUBLE      NOT NULL DEFAULT 0 COMMENT '量比',
    PRIMARY KEY (symbol, trade_date),
    KEY idx_date (trade_date)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci COMMENT='每日指标快照';

CREATE TABLE IF NOT EXISTS sync_checkpoint (
    symbol           VARCHAR(24) NOT NULL,
    task             VARCHAR(24) NOT NULL COMMENT 'backfill_kline/daily_sync',
    status           VARCHAR(12) NOT NULL DEFAULT 'pending' COMMENT 'pending/running/done/failed',
    last_synced_date DATE        NULL,
    retry_count      INT         NOT NULL DEFAULT 0,
    last_error       VARCHAR(512) NOT NULL DEFAULT '',
    updated_at       TIMESTAMP   NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (symbol, task),
    KEY idx_task_status (task, status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci COMMENT='同步断点';

CREATE TABLE IF NOT EXISTS watchlist (
    symbol     VARCHAR(24) NOT NULL,
    sort_order INT         NOT NULL DEFAULT 0,
    created_at TIMESTAMP   NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (symbol)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci COMMENT='自选股';

CREATE TABLE IF NOT EXISTS trade_calendar (
    cal_date DATE NOT NULL,
    is_open  TINYINT NOT NULL DEFAULT 1,
    PRIMARY KEY (cal_date)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci COMMENT='交易日历(A股)';
