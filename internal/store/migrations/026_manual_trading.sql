-- 手动模拟交易账户：把「AI 自动建仓/平仓」彻底降级为「AI 只推荐、人手动下单」。
--
-- 背景与口径变更：
--   旧模型里 AI 盘前推荐会自动 OpenPosition 建立 pending_entry 生命周期，收益全部按
--   百分比统计，既没有资金约束也没有真实盈亏金额。新模型改为：
--     1. AI 只产出推荐与建议，不再创建、改变任何持仓；
--     2. 用户先把标的加入自选，再在自选里手动点击建仓/平仓；
--     3. 建仓默认买入 100 万元，平仓默认卖出 100 万元市值，余额不足直接拒绝；
--     4. 全部现金流水记入 trade_order，账户余额与已实现盈亏记在 trade_account。
--
-- 幂等性：沿用 021/022 的 information_schema 预检模式，整个文件可安全重复执行。

-- ---- 模拟账户（单账户，id 固定为 1）----
CREATE TABLE IF NOT EXISTS trade_account (
    id           TINYINT       NOT NULL DEFAULT 1,
    initial_cash DECIMAL(18,2) NOT NULL DEFAULT 5000000.00 COMMENT '初始现金，账户总盈亏的基准',
    cash         DECIMAL(18,2) NOT NULL DEFAULT 5000000.00 COMMENT '当前可用现金余额',
    realized_pnl DECIMAL(18,2) NOT NULL DEFAULT 0 COMMENT '累计已实现盈亏（含交易费用）',
    total_fee    DECIMAL(18,2) NOT NULL DEFAULT 0 COMMENT '累计交易费用',
    buy_count    INT           NOT NULL DEFAULT 0 COMMENT '累计建仓笔数',
    sell_count   INT           NOT NULL DEFAULT 0 COMMENT '累计平仓笔数',
    updated_at   TIMESTAMP     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci COMMENT='手动模拟交易账户';

INSERT IGNORE INTO trade_account (id, initial_cash, cash) VALUES (1, 5000000.00, 5000000.00);

-- ---- 交易流水：每次手动建仓/平仓一条，账户余额的唯一真相来源 ----
CREATE TABLE IF NOT EXISTS trade_order (
    id           BIGINT        NOT NULL AUTO_INCREMENT,
    position_id  BIGINT        NOT NULL COMMENT '所属持仓 position.id',
    symbol       VARCHAR(24)   NOT NULL,
    side         VARCHAR(4)    NOT NULL COMMENT 'buy=建仓/加仓，sell=平仓/减仓',
    trade_date   DATE          NOT NULL,
    price        DECIMAL(12,3) NOT NULL COMMENT '成交参考价（下单时实时行情）',
    shares       INT           NOT NULL COMMENT '成交股数，A股按 100 股整手',
    amount       DECIMAL(18,2) NOT NULL COMMENT '成交金额 = price × shares，未含费用',
    fee          DECIMAL(18,2) NOT NULL DEFAULT 0 COMMENT '本笔佣金+印花税+过户费',
    cash_delta   DECIMAL(18,2) NOT NULL COMMENT '现金变动：买入为负，卖出为正',
    realized_pnl DECIMAL(18,2) NOT NULL DEFAULT 0 COMMENT '仅卖出有值：(卖价-均摊成本)×股数-本笔费用',
    note         VARCHAR(255)  NOT NULL DEFAULT '',
    created_at   TIMESTAMP     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    KEY idx_position (position_id),
    KEY idx_symbol_date (symbol, trade_date),
    KEY idx_trade_date (trade_date)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci COMMENT='手动交易流水';

-- ---- position 扩展为「真实金额持仓」----
-- shares       当前持有股数，0 表示已全部平仓
-- buy_shares   累计买入股数
-- buy_amount   累计买入金额（含买入费用），除以 buy_shares 即均摊成本
-- sell_amount  累计卖出净额（已扣卖出费用）
-- realized_pnl 该标的已实现盈亏金额
-- fee_amount   该标的累计费用
SET @ddl := (SELECT IF(COUNT(*) = 0, 'ALTER TABLE position ADD COLUMN shares INT NOT NULL DEFAULT 0 COMMENT ''当前持有股数'' AFTER position_pct', 'SELECT 1') FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'position' AND COLUMN_NAME = 'shares');
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @ddl := (SELECT IF(COUNT(*) = 0, 'ALTER TABLE position ADD COLUMN buy_shares INT NOT NULL DEFAULT 0 COMMENT ''累计买入股数'' AFTER shares', 'SELECT 1') FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'position' AND COLUMN_NAME = 'buy_shares');
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @ddl := (SELECT IF(COUNT(*) = 0, 'ALTER TABLE position ADD COLUMN buy_amount DECIMAL(18,2) NOT NULL DEFAULT 0 COMMENT ''累计买入金额（含买入费用）'' AFTER buy_shares', 'SELECT 1') FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'position' AND COLUMN_NAME = 'buy_amount');
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @ddl := (SELECT IF(COUNT(*) = 0, 'ALTER TABLE position ADD COLUMN sell_amount DECIMAL(18,2) NOT NULL DEFAULT 0 COMMENT ''累计卖出净额（已扣卖出费用）'' AFTER buy_amount', 'SELECT 1') FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'position' AND COLUMN_NAME = 'sell_amount');
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @ddl := (SELECT IF(COUNT(*) = 0, 'ALTER TABLE position ADD COLUMN realized_pnl DECIMAL(18,2) NOT NULL DEFAULT 0 COMMENT ''该标的已实现盈亏金额'' AFTER sell_amount', 'SELECT 1') FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'position' AND COLUMN_NAME = 'realized_pnl');
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @ddl := (SELECT IF(COUNT(*) = 0, 'ALTER TABLE position ADD COLUMN fee_amount DECIMAL(18,2) NOT NULL DEFAULT 0 COMMENT ''该标的累计交易费用'' AFTER realized_pnl', 'SELECT 1') FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'position' AND COLUMN_NAME = 'fee_amount');
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

-- 手动交易下同一只股可能「今天平仓、明天再建仓」，也可能同日加仓，
-- 旧的 (symbol, pick_date) 唯一键会直接拒绝这些合法操作，必须降级为普通索引。
SET @ddl := (SELECT IF(COUNT(*) > 0, 'ALTER TABLE position DROP INDEX uk_symbol_pick', 'SELECT 1') FROM information_schema.STATISTICS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'position' AND INDEX_NAME = 'uk_symbol_pick');
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @ddl := (SELECT IF(COUNT(*) = 0, 'ALTER TABLE position ADD KEY idx_symbol_status (symbol, status)', 'SELECT 1') FROM information_schema.STATISTICS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'position' AND INDEX_NAME = 'idx_symbol_status');
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

-- analysis_date 原本强绑定 stock_recommendation；手动建仓的标的可能从未被 AI 推荐过，
-- 此时用建仓日填充即可，放宽默认值避免插入报错。
SET @ddl := (SELECT IF(COUNT(*) = 1, 'ALTER TABLE position MODIFY COLUMN analysis_date DATE NULL COMMENT ''对应 stock_recommendation.analysis_date，手动建仓时为建仓日''', 'SELECT 1') FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'position' AND COLUMN_NAME = 'analysis_date' AND IS_NULLABLE = 'NO');
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

-- ---- 清空旧口径持仓 ----
-- 旧记录全部是「AI 自动建仓、纯百分比结算」的样本，没有股数与金额，无法并入
-- 新的资金账户；混在一起会让余额、持仓市值和盈亏三者永远对不上账。
-- 按确认口径整体清空，账户从初始现金干净开始；stock_recommendation 的推荐历史保留。
DELETE FROM position_reduction;
DELETE FROM position_review;
DELETE FROM position;
DELETE FROM trade_order;
UPDATE trade_account SET cash = initial_cash, realized_pnl = 0, total_fee = 0, buy_count = 0, sell_count = 0 WHERE id = 1;
