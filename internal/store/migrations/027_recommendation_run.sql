-- 推荐运行留痕：把「当日主动空仓」与「当日尚未运行」区分开。
--
-- 背景（2026-09 复盘结论）：
--   旧链路强制每个交易日必须产出恰好 3 只推荐，因此「stock_recommendation 里
--   analysis_date 的最大值」天然等价于「最近一次推荐日」。放开数量下限后
--   （允许 0..3 只，红灯默认空仓），空仓日在 stock_recommendation 中不留任何行，
--   MAX(analysis_date) 会静默回退到上一个有推荐的交易日，导致前端把昨天的
--   推荐当作今天的展示——这比不给空仓权更危险。
--
--   本表为每个完成的推荐运行留一行（无论产出几只），作为「最近推荐日」的
--   权威来源，同时记录风向档位与候选规模，供空仓率与策略有效性复盘。
--
-- 幂等性：沿用既有 CREATE TABLE IF NOT EXISTS 模式，可安全重复执行。

CREATE TABLE IF NOT EXISTS recommendation_run (
    analysis_date   DATE        NOT NULL COMMENT '推荐基准交易日（分析日）',
    gate_level      VARCHAR(8)  NOT NULL DEFAULT '' COMMENT '融合后风向档位 green/yellow/red',
    gate_reason     VARCHAR(512) NOT NULL DEFAULT '' COMMENT '风向判定依据',
    max_picks       TINYINT     NOT NULL DEFAULT 0 COMMENT '当日允许的推荐数量上限',
    pick_count      TINYINT     NOT NULL DEFAULT 0 COMMENT '实际产出推荐数，0 表示主动空仓',
    candidate_count SMALLINT    NOT NULL DEFAULT 0 COMMENT '通过全部硬过滤的候选数',
    model_name      VARCHAR(128) NOT NULL DEFAULT '',
    created_at      TIMESTAMP   NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMP   NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (analysis_date),
    KEY idx_pick_count (pick_count, analysis_date)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci COMMENT='AI 推荐运行留痕（含空仓日）';

-- 历史回填：已有推荐记录的交易日补齐运行留痕，保证 MAX(analysis_date)
-- 在切换数据源后仍连续。旧链路固定 3 只、风向档位未留痕，故留空。
INSERT IGNORE INTO recommendation_run (analysis_date, gate_level, gate_reason, max_picks, pick_count, candidate_count, model_name)
SELECT analysis_date, '', '历史回填：旧链路未留痕风向档位', 3, COUNT(*), 0, MAX(model_name)
FROM stock_recommendation
GROUP BY analysis_date;
