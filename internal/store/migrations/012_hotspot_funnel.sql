-- 热点漏斗：板块每日统计、概念重叠度、漏斗报告
-- 设计要点：
-- 1. sector_daily_stats 由 16:00 快照任务在日K/指标写入后计算，页面查询只读本表
-- 2. sector_overlap 在概念成分刷新后重算，仅保留 jaccard >= 0.05 的概念对，控制行数
-- 3. hotspot_report 按阶段存 JSON，中间结果可追溯、可单独重跑

CREATE TABLE IF NOT EXISTS sector_daily_stats (
    sector_code       VARCHAR(16) NOT NULL,
    trade_date        DATE        NOT NULL,
    sector_type       VARCHAR(12) NOT NULL DEFAULT 'concept',
    stock_count       INT         NOT NULL DEFAULT 0,
    avg_change        DOUBLE      NOT NULL DEFAULT 0 COMMENT '等权平均涨跌幅%',
    avg_change_5d     DOUBLE      NOT NULL DEFAULT 0 COMMENT '等权5日累计涨跌幅%',
    avg_change_20d    DOUBLE      NOT NULL DEFAULT 0 COMMENT '等权20日累计涨跌幅%',
    up_ratio          DOUBLE      NOT NULL DEFAULT 0 COMMENT '上涨家数占比 0~1',
    limit_up_count    INT         NOT NULL DEFAULT 0 COMMENT '近似涨停家数(按板块10/20/30cm阈值)',
    total_amount      DOUBLE      NOT NULL DEFAULT 0 COMMENT '当日成交额合计(元)',
    amount_ratio      DOUBLE      NOT NULL DEFAULT 0 COMMENT '5日均额/60日均额 量能放大倍数',
    avg_turnover      DOUBLE      NOT NULL DEFAULT 0 COMMENT '等权平均换手率%',
    heat_score        DOUBLE      NOT NULL DEFAULT 0 COMMENT 'L1热度分(z-score加权)',
    PRIMARY KEY (sector_code, trade_date),
    KEY idx_date_score (trade_date, heat_score)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci COMMENT='板块每日热度统计';

CREATE TABLE IF NOT EXISTS sector_overlap (
    sector_a    VARCHAR(16) NOT NULL,
    sector_b    VARCHAR(16) NOT NULL COMMENT '约定 sector_a < sector_b',
    common_cnt  INT         NOT NULL DEFAULT 0,
    jaccard     DOUBLE      NOT NULL DEFAULT 0,
    updated_at  TIMESTAMP   NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (sector_a, sector_b),
    KEY idx_b (sector_b)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci COMMENT='概念成分股Jaccard重叠度';

CREATE TABLE IF NOT EXISTS hotspot_report (
    report_date DATE         NOT NULL,
    stage       VARCHAR(24)  NOT NULL COMMENT 'l1_screen/l2_cluster/l3_chokepoint/final',
    payload     MEDIUMTEXT   NOT NULL COMMENT '阶段结果 JSON',
    model       VARCHAR(64)  NOT NULL DEFAULT '' COMMENT 'AI 模型标识，纯数据阶段为空',
    created_at  TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (report_date, stage)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci COMMENT='热点漏斗分阶段报告';
