ALTER TABLE stock_basic
    ADD COLUMN last_trade_date DATE NULL AFTER list_date,
    ADD KEY idx_status_last_trade (status, last_trade_date);

UPDATE stock_basic b
LEFT JOIN (
    SELECT symbol, MAX(trade_date) AS last_trade_date
    FROM kline_daily
    GROUP BY symbol
) k ON k.symbol=b.symbol
SET b.last_trade_date=k.last_trade_date
WHERE b.last_trade_date IS NULL AND k.last_trade_date IS NOT NULL;
