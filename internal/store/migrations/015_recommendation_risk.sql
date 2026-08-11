-- AI 趋势推荐增加风险评分：0-100，由本地波动率/回撤/短期过热确定性计算，
-- 风险过高的候选在进入 AI 评审前已被剔除，该列用于展示与复盘。
ALTER TABLE stock_recommendation
    ADD COLUMN risk_score DOUBLE NULL DEFAULT NULL;
