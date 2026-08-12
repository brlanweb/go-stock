-- 每日复盘：交易日 17:00 基于 16:00 收盘快照产出的本地数据复盘报告
-- 设计要点：
-- 1. 复用 hotspot_report 的历史化模式：自增 id 主键，同日多次运行全部保留
-- 2. stage 分两层：facts（纯 SQL 确定性事实，AI 不可用时也有底稿）、review（AI 结构化复盘）
-- 3. market_phase 仅 review 阶段有值（up/range/down），供次日盘前推荐注入防御/进攻指令
CREATE TABLE IF NOT EXISTS daily_review (
    id           BIGINT       NOT NULL AUTO_INCREMENT PRIMARY KEY,
    review_date  DATE         NOT NULL,
    stage        VARCHAR(24)  NOT NULL COMMENT 'facts/review',
    market_phase VARCHAR(12)  NOT NULL DEFAULT '' COMMENT 'up/range/down，仅 review 阶段有值',
    payload      MEDIUMTEXT   NOT NULL COMMENT '阶段结果 JSON',
    model        VARCHAR(64)  NOT NULL DEFAULT '' COMMENT 'AI 模型标识，facts 阶段为空',
    created_at   TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    KEY idx_date_stage (review_date, stage, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci COMMENT='每日复盘分阶段报告';
