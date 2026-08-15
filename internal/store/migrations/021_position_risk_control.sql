-- 风险控制升级：把「只按趋势结构退出」升级为「成本止损 + 移动止盈 + 时间止损 + 分批减仓」。
--
-- 背景：原实现的退出规则全部是趋势结构类（跌破 MA20 / MA10+板块转弱 / 指数系统性下跌），
-- 没有任何一条锚定建仓成本，导致单笔亏损没有上限；同时没有止盈与回撤保护，
-- 盈利单会一路回吐到均线破位才退出。入场 edge 是 1-5 日尺度的短线动量，
-- 退出却用 20 日尺度的均线，时间尺度错配会把方向正确的交易变成亏损交易。
--
-- 幂等性说明：迁移执行器逐条执行且不包事务，中途失败不会写入 schema_migrations。
-- 若直接用 ALTER TABLE ADD COLUMN，一旦第 N 条失败，重启后第 1 条会因「列已存在」
-- 报错，导致服务永久无法启动、必须人工修数据库。因此统一先查 information_schema
-- 再决定是否执行，保证整个文件可安全重复运行。
--
-- 新增字段：
--   highest_price   持仓期内最高价，移动止盈的基准（每轮盘中分析刷新）
--   lowest_price    持仓期内最低价，用于复盘最大不利偏移（MAE）
--   position_pct    当前仓位比例 0-100，reduce 分批减仓后下降
--   realized_pct    已通过减仓落袋的加权收益贡献（百分点）
--   exit_kind       退出归因：ai/stop_loss/trailing_stop/take_profit/time_stop/trend_break/systemic

SET @ddl := (SELECT IF(COUNT(*) = 0, 'ALTER TABLE position ADD COLUMN highest_price DECIMAL(12,3) NULL COMMENT ''持仓期内最高价，移动止盈基准'' AFTER entry_price', 'SELECT 1') FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'position' AND COLUMN_NAME = 'highest_price');
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @ddl := (SELECT IF(COUNT(*) = 0, 'ALTER TABLE position ADD COLUMN lowest_price DECIMAL(12,3) NULL COMMENT ''持仓期内最低价，用于MAE复盘'' AFTER highest_price', 'SELECT 1') FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'position' AND COLUMN_NAME = 'lowest_price');
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @ddl := (SELECT IF(COUNT(*) = 0, 'ALTER TABLE position ADD COLUMN position_pct DECIMAL(6,2) NOT NULL DEFAULT 100.00 COMMENT ''当前仓位比例0-100，减仓后下降'' AFTER hold_days', 'SELECT 1') FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'position' AND COLUMN_NAME = 'position_pct');
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @ddl := (SELECT IF(COUNT(*) = 0, 'ALTER TABLE position ADD COLUMN realized_pct DECIMAL(10,4) NOT NULL DEFAULT 0 COMMENT ''已减仓落袋的加权收益百分点'' AFTER position_pct', 'SELECT 1') FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'position' AND COLUMN_NAME = 'realized_pct');
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @ddl := (SELECT IF(COUNT(*) = 0, 'ALTER TABLE position ADD COLUMN exit_kind VARCHAR(16) NOT NULL DEFAULT '''' COMMENT ''ai/stop_loss/trailing_stop/take_profit/time_stop/trend_break/systemic'' AFTER exit_reason', 'SELECT 1') FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'position' AND COLUMN_NAME = 'exit_kind');
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

-- 已建仓的历史记录用建仓价初始化极值，避免移动止盈在旧数据上误触发。
UPDATE position SET highest_price = entry_price, lowest_price = entry_price WHERE entry_price IS NOT NULL AND highest_price IS NULL;

-- 已退出的历史记录按原因归类，便于新旧口径统一复盘。
UPDATE position SET exit_kind = CASE WHEN exit_reason LIKE '%系统性风险%' THEN 'systemic' WHEN exit_reason LIKE '%跌破%' THEN 'trend_break' ELSE 'ai' END WHERE status = 'exited' AND exit_kind = '';

-- reduce 减仓明细：一次减仓一条，用于审计与收益归因。
CREATE TABLE IF NOT EXISTS position_reduction (
    id          BIGINT        NOT NULL AUTO_INCREMENT,
    position_id BIGINT        NOT NULL,
    trade_date  DATE          NOT NULL,
    price       DECIMAL(12,3) NOT NULL COMMENT '减仓参考价',
    reduce_pct  DECIMAL(6,2)  NOT NULL COMMENT '本次减掉的仓位比例',
    change_pct  DECIMAL(10,4) NOT NULL COMMENT '本次减仓相对建仓价的收益率',
    reason      VARCHAR(255)  NOT NULL DEFAULT '',
    created_at  TIMESTAMP     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    KEY idx_position (position_id),
    KEY idx_trade_date (trade_date)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci COMMENT='持仓分批减仓明细';
