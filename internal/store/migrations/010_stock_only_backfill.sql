-- ETF 不参与自动历史回填。仅删除断点元数据，保留已入库行情、快照和手动查询能力。
DELETE cp
FROM sync_checkpoint cp
INNER JOIN stock_basic b ON b.symbol=cp.symbol
WHERE cp.task='backfill_kline' AND b.sec_type='etf';
