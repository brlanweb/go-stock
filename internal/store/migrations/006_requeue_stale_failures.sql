-- 006_requeue_stale_failures.sql
-- 引入 BaoStock/AKShare 历史降级源后，为此前因单一上游(腾讯 HTTP 501/东财熔断)
-- 失败的历史回填断点提供一次受控重排机会。仅执行一次（版本化迁移保证幂等）。
UPDATE sync_checkpoint
SET status='pending', retry_count=0
WHERE task='backfill_kline' AND status='failed';
