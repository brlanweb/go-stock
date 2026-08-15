-- 持仓生命周期：把「每日 AI 推荐选出的最佳建仓股」从一次性建议升级为可追踪的持仓状态机。
--
-- 状态流转（position.status）：
--   pending_entry  盘前入池，等待盘中出现合适建仓点；宽限 2 个交易日
--   holding        已建仓，进入盘中退出机会分析（趋势跟踪的核心阶段）
--   exited         AI 或硬规则判定趋势不可持续/风险放大，已择机退出并冻结收益
--   expired        宽限期内始终未等到建仓点，放弃该标的并腾出自选位
--
-- 收益结算口径：exited 后以 exit_price 冻结，不再随后续行情波动统计；
-- 未建仓（expired）不产生任何收益样本。
CREATE TABLE IF NOT EXISTS position (
    id            BIGINT       NOT NULL AUTO_INCREMENT,
    symbol        VARCHAR(24)  NOT NULL,
    pick_date     DATE         NOT NULL COMMENT '入池日：AI 趋势推荐选中并加入自选的交易日',
    analysis_date DATE         NOT NULL COMMENT '对应 stock_recommendation.analysis_date',
    status        VARCHAR(16)  NOT NULL DEFAULT 'pending_entry' COMMENT 'pending_entry/holding/exited/expired',
    entry_date    DATE         NULL COMMENT '实际建仓交易日',
    entry_price   DECIMAL(12,3) NULL COMMENT 'AI 给出建仓建议时的参考价',
    exit_date     DATE         NULL COMMENT '实际退出交易日',
    exit_price    DECIMAL(12,3) NULL COMMENT 'AI/硬规则判定退出时的参考价',
    exit_reason   VARCHAR(255) NOT NULL DEFAULT '',
    hold_days     INT          NOT NULL DEFAULT 0 COMMENT '已持有交易日数，随盘中分析递增',
    created_at    TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at    TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uk_symbol_pick (symbol, pick_date),
    KEY idx_status (status),
    KEY idx_pick_date (pick_date)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci COMMENT='AI趋势交易持仓生命周期';

-- entry_advice 扩展为「建仓 + 退出」双阶段建议记录。
-- stage=entry 时 price_low/price_high 是建议建仓价区间；
-- stage=exit  时为建议退出价区间；urgency 表达风险紧迫度，urgent 需当日处理。
ALTER TABLE entry_advice
    ADD COLUMN stage      VARCHAR(8)    NOT NULL DEFAULT 'entry' COMMENT 'entry/exit' AFTER source,
    ADD COLUMN price_low  DECIMAL(12,3) NULL COMMENT '建议价格区间下沿' AFTER reason,
    ADD COLUMN price_high DECIMAL(12,3) NULL COMMENT '建议价格区间上沿' AFTER price_low,
    ADD COLUMN urgency    VARCHAR(8)    NOT NULL DEFAULT 'normal' COMMENT 'normal/warn/urgent' AFTER price_high,
    ADD COLUMN ref_price  DECIMAL(12,3) NULL COMMENT '产生该建议时的现价快照' AFTER urgency;

ALTER TABLE entry_advice ADD KEY idx_symbol_date (symbol, trade_date);
