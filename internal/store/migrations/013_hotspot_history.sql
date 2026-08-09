-- 热点漏斗历史化：每次运行追加记录而非覆盖
-- 原主键 (report_date, stage) 导致同日重跑互相覆盖；改为自增 id 主键，
-- (report_date, stage) 降为普通索引，同一天多次运行全部保留。
ALTER TABLE hotspot_report
    DROP PRIMARY KEY,
    ADD COLUMN id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY FIRST,
    ADD KEY idx_date_stage (report_date, stage, id);
