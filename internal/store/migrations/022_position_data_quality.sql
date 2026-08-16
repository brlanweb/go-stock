-- 数据质量标记：把历史上「当日建仓、当日退出」的持仓标记为 T+0 违约样本。
--
-- 背景：applyRiskControls/evaluateRisk 原本没有 T+1 守卫，10:00 建仓、13:30 触发止损
-- 会被记为当日 exited。A 股 T+1 下这笔交易现实中无法成交，真实持有者必须承担隔夜跳空。
-- 这类样本会系统性高估止损类退出的收益，进而污染考核分与参数寻优方向。
--
-- 处理方式：不删除历史记录（保留审计价值），改为打标后从考核统计中排除，
-- 并在接口中显式返回被排除的样本数，避免「悄悄改口径」。
--
-- data_quality 取值：
--   ''             正常样本，参与全部考核统计
--   't0_violation' 当日进出，违反 T+1，仅保留审计，不参与考核
--
-- 幂等性：沿用 021 的 information_schema 预检模式，保证整个文件可重复执行。

SET @ddl := (SELECT IF(COUNT(*) = 0, 'ALTER TABLE position ADD COLUMN data_quality VARCHAR(16) NOT NULL DEFAULT '''' COMMENT ''考核样本质量：空=正常，t0_violation=当日进出'' AFTER exit_kind', 'SELECT 1') FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'position' AND COLUMN_NAME = 'data_quality');
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @ddl := (SELECT IF(COUNT(*) = 0, 'ALTER TABLE position ADD KEY idx_data_quality (data_quality)', 'SELECT 1') FROM information_schema.STATISTICS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'position' AND INDEX_NAME = 'idx_data_quality');
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

-- 只回填未标记过的记录，保证重复执行不会覆盖人工修正结果。
UPDATE position SET data_quality = 't0_violation' WHERE status = 'exited' AND entry_date IS NOT NULL AND exit_date IS NOT NULL AND entry_date = exit_date AND data_quality = '';
