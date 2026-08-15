-- 019 上线前已经存在的最新推荐批次没有 position，导致首页真实生命周期为空。
-- 仅当该批次紧邻数据库最新交易日、整批尚无生命周期且自选仍有容量时，
-- 补入概率最高（同概率取风险更低、再取排名靠前）的 1 只为 pending_entry。
-- 不补建仓价和收益；后续仍必须经过盘中 AI entry 才能进入 holding。
INSERT INTO watchlist (symbol, sort_order, created_at)
SELECT candidate.symbol, COALESCE((SELECT MAX(w.sort_order) FROM watchlist w), 0) + 1, NOW()
FROM (
    SELECT r.symbol
    FROM stock_recommendation r
    JOIN (
        SELECT MIN(k.trade_date) AS pick_date
        FROM kline_daily k
        WHERE k.trade_date > (SELECT MAX(sr.analysis_date) FROM stock_recommendation sr)
    ) next_trade ON next_trade.pick_date IS NOT NULL
    WHERE r.analysis_date = (SELECT MAX(sr.analysis_date) FROM stock_recommendation sr)
      AND next_trade.pick_date = (SELECT MAX(k.trade_date) FROM kline_daily k)
      AND NOT EXISTS (
          SELECT 1 FROM position p WHERE p.analysis_date = r.analysis_date
      )
      AND (SELECT COUNT(*) FROM watchlist w WHERE w.symbol <> r.symbol) < 10
    ORDER BY r.probability DESC,
             CASE WHEN r.risk_score IS NULL THEN 1 ELSE 0 END,
             r.risk_score ASC,
             r.rank_no ASC
    LIMIT 1
) candidate
ON DUPLICATE KEY UPDATE symbol = VALUES(symbol);

INSERT INTO position (symbol, pick_date, analysis_date, status)
SELECT r.symbol, next_trade.pick_date, r.analysis_date, 'pending_entry'
FROM stock_recommendation r
JOIN (
    SELECT MIN(k.trade_date) AS pick_date
    FROM kline_daily k
    WHERE k.trade_date > (SELECT MAX(sr.analysis_date) FROM stock_recommendation sr)
) next_trade ON next_trade.pick_date IS NOT NULL
WHERE r.analysis_date = (SELECT MAX(sr.analysis_date) FROM stock_recommendation sr)
  AND next_trade.pick_date = (SELECT MAX(k.trade_date) FROM kline_daily k)
  AND NOT EXISTS (
      SELECT 1 FROM position p WHERE p.analysis_date = r.analysis_date
  )
  AND (SELECT COUNT(*) FROM watchlist w WHERE w.symbol <> r.symbol) < 10
ORDER BY r.probability DESC,
         CASE WHEN r.risk_score IS NULL THEN 1 ELSE 0 END,
         r.risk_score ASC,
         r.rank_no ASC
LIMIT 1;

INSERT INTO entry_advice (
    trade_date, symbol, source, stage, action, reason,
    urgency, model_name, created_at
)
SELECT p.pick_date, p.symbol, 'daily_pick', 'entry', 'pick',
       CONCAT('生命周期升级补录：最新推荐中最适合建仓（概率 ',
              FORMAT(r.probability, 1), '）：', r.reason),
       'normal', r.model_name, NOW()
FROM position p
JOIN stock_recommendation r
  ON r.analysis_date = p.analysis_date AND r.symbol = p.symbol
WHERE p.analysis_date = (SELECT MAX(sr.analysis_date) FROM stock_recommendation sr)
  AND p.status = 'pending_entry'
  AND NOT EXISTS (
      SELECT 1 FROM entry_advice e
      WHERE e.trade_date = p.pick_date
        AND e.symbol = p.symbol
        AND e.source = 'daily_pick'
  );
