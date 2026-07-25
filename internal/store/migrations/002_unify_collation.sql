-- kline_daily、daily_indicator、sync_checkpoint、watchlist、trade_calendar 已是
-- utf8mb4_general_ci；存量库仅 stock_basic 保留 unicode_ci，导致关联 symbol 时 MySQL 1267。
-- 只转换该 7 千行基础表，避免对 58 万行 kline_daily 执行无必要表重建。
ALTER TABLE stock_basic CONVERT TO CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci;
