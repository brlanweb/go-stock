-- 风险感知板块（Risk Sentinel）：隔夜外盘快照与全球风险门。
-- 设计原则与指数风向门一致：确定性打分、可回测、只能收紧不能放宽。

-- 隔夜外盘因子快照：A50期指/金龙指数/美股三大/恒指/离岸人民币/VIX。
CREATE TABLE IF NOT EXISTS global_snapshot (
    snapshot_at TIMESTAMP     NOT NULL,
    symbol      VARCHAR(24)   NOT NULL,
    name        VARCHAR(64)   NOT NULL DEFAULT '',
    category    VARCHAR(24)   NOT NULL DEFAULT '',
    price       DECIMAL(16,4) NOT NULL DEFAULT 0,
    change_pct  DECIMAL(10,4) NOT NULL DEFAULT 0,
    source      VARCHAR(16)   NOT NULL DEFAULT '',
    PRIMARY KEY (snapshot_at, symbol),
    KEY idx_symbol_time (symbol, snapshot_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci COMMENT='隔夜外盘风险因子快照';

-- 全球风险门判定结果：每个交易日一条，payload 保存全部信号明细用于归因与回测。
CREATE TABLE IF NOT EXISTS global_risk_gate (
    trade_date DATE          NOT NULL,
    level      VARCHAR(8)    NOT NULL COMMENT 'green/yellow/red',
    score      INT           NOT NULL DEFAULT 0,
    reason     VARCHAR(500)  NOT NULL DEFAULT '',
    payload    MEDIUMTEXT    NOT NULL,
    created_at TIMESTAMP     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (trade_date)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci COMMENT='全球风险门每日判定';
