-- 退市和转板旧代码以库内最后一根日 K 作为覆盖终点，避免继续追赶全市场最近交易日。
UPDATE stock_basic b
INNER JOIN (
    SELECT symbol, MAX(trade_date) AS last_date
    FROM kline_daily
    GROUP BY symbol
) k ON k.symbol=b.symbol
SET b.last_trade_date=k.last_date,
    b.updated_at=NOW()
WHERE b.status<>'listed'
  AND (b.last_trade_date IS NULL OR b.last_trade_date<>k.last_date);

-- 非在市股票由协调阶段直接收敛，不再进入 Provider 队列。退市证券的早期
-- 交易日历和上市日期可能存在历史口径差异，不再用 14 天头部容差阻塞收敛。
UPDATE sync_checkpoint cp
INNER JOIN stock_basic b ON b.symbol=cp.symbol
LEFT JOIN (
    SELECT symbol,MIN(trade_date) first_date,MAX(trade_date) last_date,COUNT(*) kline_count
    FROM kline_daily
    GROUP BY symbol
) k ON k.symbol=cp.symbol
SET cp.first_synced_date=k.first_date,
    cp.last_synced_date=k.last_date,
    cp.kline_count=IFNULL(k.kline_count,0),
    cp.status='done',
    cp.last_error=''
WHERE cp.task='backfill_kline'
  AND b.sec_type='stock'
  AND b.status<>'listed'
  AND (k.last_date IS NULL OR b.last_trade_date IS NULL OR k.last_date>=b.last_trade_date);
