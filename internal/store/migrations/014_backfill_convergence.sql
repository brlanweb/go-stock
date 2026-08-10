-- 让"上游本身给不出数据"的证券收敛，不再永远挂在待处理/失败：
-- 1) head_exhausted：头部历史各数据源已尽力仍不可得时的收敛标记，
--    协调阶段视为头部达标，避免每轮重新全量拉取。
ALTER TABLE sync_checkpoint
    ADD COLUMN head_exhausted TINYINT(1) NOT NULL DEFAULT 0;

-- 2) 回填仅覆盖股票；清理分类调整后遗留的非股票断点（如 ETF 残留）。
DELETE cp FROM sync_checkpoint cp
INNER JOIN stock_basic b ON b.symbol=cp.symbol
WHERE cp.task='backfill_kline' AND b.sec_type<>'stock';

-- 3) 名称带退市整理期标记（沪市"退市XX"、深市/北交所"XX退"）但状态仍为
--    listed 的股票转为退市。上游列表在摘牌后一段时间内仍包含这些代码，
--    且 180 天兜底阈值尚未触发，导致它们反复请求上游并失败。
UPDATE stock_basic b
LEFT JOIN (SELECT symbol,MAX(trade_date) last_date FROM kline_daily GROUP BY symbol) k ON k.symbol=b.symbol
SET b.status='delisted',
    b.last_trade_date=COALESCE(k.last_date,b.last_trade_date),
    b.updated_at=NOW()
WHERE b.status='listed' AND b.sec_type='stock'
  AND (b.name LIKE '退市%' OR b.name LIKE '%退');
