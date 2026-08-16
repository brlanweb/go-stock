-- 单笔离场复盘：每个 position 退出时立即生成一条确定性复盘底稿。
-- AI 只能在后续基于这些事实补充说明，不得改写收益、MFE/MAE、捕获率与归责枚举。
CREATE TABLE IF NOT EXISTS position_review (
    id                  BIGINT        NOT NULL AUTO_INCREMENT,
    position_id         BIGINT        NOT NULL,
    symbol              VARCHAR(24)   NOT NULL,
    review_date         DATE          NOT NULL,
    verdict             VARCHAR(12)   NOT NULL COMMENT 'success/failure/neutral',
    blame_stage         VARCHAR(16)   NOT NULL COMMENT 'selection/opportunity/entry/exit/market',
    net_change_pct      DECIMAL(10,4) NOT NULL,
    mfe_pct             DECIMAL(10,4) NULL COMMENT '持仓期最大有利偏移',
    mae_pct             DECIMAL(10,4) NULL COMMENT '持仓期最大不利偏移',
    capture_rate_pct    DECIMAL(10,4) NULL COMMENT '净收益/MFE，衡量盈利捕获率',
    post_exit_5d_pct    DECIMAL(10,4) NULL COMMENT '退出后5交易日续涨，正值表示可能过早退出',
    exit_kind           VARCHAR(16)   NOT NULL DEFAULT '',
    reason              VARCHAR(255)  NOT NULL DEFAULT '',
    data_quality        VARCHAR(16)   NOT NULL DEFAULT '',
    generated_by        VARCHAR(16)   NOT NULL DEFAULT 'rule' COMMENT 'rule/ai',
    created_at          TIMESTAMP     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at          TIMESTAMP     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uk_position (position_id),
    KEY idx_review_date (review_date),
    KEY idx_blame_verdict (blame_stage, verdict),
    KEY idx_exit_kind (exit_kind)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci COMMENT='单笔离场确定性复盘';

-- 为历史已退出记录生成底稿。失真样本仍保留复盘记录，但 data_quality 会阻止其进入考核。
INSERT INTO position_review
    (position_id,symbol,review_date,verdict,blame_stage,net_change_pct,mfe_pct,mae_pct,capture_rate_pct,exit_kind,reason,data_quality,generated_by)
SELECT p.id,p.symbol,p.exit_date,
       CASE WHEN (((p.exit_price/p.entry_price-1)*100*p.position_pct/100)+p.realized_pct-0.25)>0 THEN 'success'
            WHEN (((p.exit_price/p.entry_price-1)*100*p.position_pct/100)+p.realized_pct-0.25)<0 THEN 'failure'
            ELSE 'neutral' END,
       CASE
         WHEN p.exit_kind='systemic' THEN 'market'
         WHEN p.exit_kind='stop_loss' THEN 'entry'
         WHEN p.exit_kind='time_stop' THEN 'opportunity'
         WHEN p.highest_price IS NOT NULL AND (p.highest_price/p.entry_price-1)*100>=5
              AND (((p.exit_price/p.entry_price-1)*100*p.position_pct/100)+p.realized_pct-0.25)<=0 THEN 'exit'
         WHEN (((p.exit_price/p.entry_price-1)*100*p.position_pct/100)+p.realized_pct-0.25)<=0 THEN 'selection'
         ELSE 'exit' END,
       (((p.exit_price/p.entry_price-1)*100*p.position_pct/100)+p.realized_pct-0.25),
       CASE WHEN p.highest_price IS NULL THEN NULL ELSE (p.highest_price/p.entry_price-1)*100 END,
       CASE WHEN p.lowest_price IS NULL THEN NULL ELSE (p.lowest_price/p.entry_price-1)*100 END,
       CASE WHEN p.highest_price IS NULL OR p.highest_price<=p.entry_price THEN NULL
            ELSE ((((p.exit_price/p.entry_price-1)*100*p.position_pct/100)+p.realized_pct-0.25)/((p.highest_price/p.entry_price-1)*100))*100 END,
       p.exit_kind,
       CASE
         WHEN p.exit_kind='systemic' THEN '系统性风险触发退出，主要归因市场环境'
         WHEN p.exit_kind='stop_loss' THEN '建仓后触发硬止损，优先检查建仓位置与波动适配'
         WHEN p.exit_kind='time_stop' THEN '动量在有效期内未兑现，机会判断未形成正向优势'
         WHEN p.highest_price IS NOT NULL AND (p.highest_price/p.entry_price-1)*100>=5
              AND (((p.exit_price/p.entry_price-1)*100*p.position_pct/100)+p.realized_pct-0.25)<=0 THEN '持仓曾有明显浮盈但最终亏损，离场未保护已有优势'
         WHEN (((p.exit_price/p.entry_price-1)*100*p.position_pct/100)+p.realized_pct-0.25)<=0 THEN '持仓期未形成有效正收益，选股优势不足'
         ELSE '交易实现正收益，继续评估MFE捕获率与离场后续涨' END,
       p.data_quality,'rule'
  FROM position p
 WHERE p.status='exited' AND p.entry_price>0 AND p.exit_price>0
ON DUPLICATE KEY UPDATE position_id=VALUES(position_id);
