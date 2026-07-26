package indicator

import "encoding/json"

const (
	Executable      = "executable"
	Experimental    = "experimental"
	ContextRequired = "context_required"
)

type Definition struct {
	ID            string         `json:"id"`
	DisplayName   string         `json:"display_name"`
	Description   string         `json:"description"`
	Category      string         `json:"category"`
	Kind          string         `json:"kind"`
	Capability    string         `json:"capability"`
	Source        string         `json:"source"`
	Enabled       bool           `json:"enabled"`
	DefaultParams map[string]any `json:"default_params"`
	CurrentParams map[string]any `json:"current_params"`
	SortOrder     int            `json:"sort_order"`
}

func params(values ...any) map[string]any {
	out := make(map[string]any, len(values)/2)
	for i := 0; i+1 < len(values); i += 2 {
		out[values[i].(string)] = values[i+1]
	}
	return out
}

func Catalog() []Definition {
	traditional := []Definition{
		{ID: "ma", DisplayName: "移动平均线 MA", Description: "简单移动平均线组。", Category: "trend", Capability: Executable, Source: "Vibe-Trading", DefaultParams: params("periods", []any{5, 10, 20, 60})},
		{ID: "ema", DisplayName: "指数移动平均线 EMA", Description: "指数移动平均线组。", Category: "trend", Capability: Executable, Source: "Vibe-Trading", DefaultParams: params("periods", []any{12, 26})},
		{ID: "vegas", DisplayName: "Vegas 隧道", Description: "EMA144/169 过滤隧道与 EMA576/676 趋势隧道。", Category: "trend", Capability: Executable, Source: "go-stock", DefaultParams: params("periods", []any{144, 169, 576, 676})},
		{ID: "boll", DisplayName: "布林带 BOLL", Description: "中轨、上轨和下轨。", Category: "volatility", Capability: Executable, Source: "Vibe-Trading", DefaultParams: params("period", 20, "multiplier", 2)},
		{ID: "macd", DisplayName: "MACD", Description: "DIF、DEA 与柱状动量。", Category: "momentum", Capability: Executable, Source: "Vibe-Trading/daily_stock_analysis", DefaultParams: params("fast", 12, "slow", 26, "signal", 9)},
		{ID: "rsi", DisplayName: "RSI", Description: "Wilder 相对强弱指标。", Category: "momentum", Capability: Executable, Source: "Vibe-Trading/daily_stock_analysis", DefaultParams: params("periods", []any{6, 12, 24})},
		{ID: "kdj", DisplayName: "KDJ", Description: "随机指标 K、D、J。", Category: "momentum", Capability: Executable, Source: "Vibe-Trading", DefaultParams: params("period", 9, "k_smooth", 3, "d_smooth", 3)},
		{ID: "atr", DisplayName: "ATR", Description: "平均真实波幅。", Category: "volatility", Capability: Executable, Source: "daily_stock_analysis", DefaultParams: params("period", 14)},
		{ID: "bias", DisplayName: "乖离率 BIAS", Description: "价格相对均线偏离程度。", Category: "momentum", Capability: Executable, Source: "daily_stock_analysis", DefaultParams: params("periods", []any{6, 12, 24})},
		{ID: "wr", DisplayName: "Williams %R", Description: "超买超卖动量指标。", Category: "momentum", Capability: Executable, Source: "go-stock", DefaultParams: params("period", 14)},
		{ID: "cci", DisplayName: "CCI", Description: "商品通道指标。", Category: "momentum", Capability: Executable, Source: "go-stock", DefaultParams: params("period", 14)},
		{ID: "roc", DisplayName: "ROC", Description: "价格变化率。", Category: "momentum", Capability: Executable, Source: "go-stock", DefaultParams: params("period", 12)},
		{ID: "mom", DisplayName: "MOM", Description: "价格动量。", Category: "momentum", Capability: Executable, Source: "go-stock", DefaultParams: params("period", 10)},
		{ID: "obv", DisplayName: "OBV", Description: "能量潮量价指标。", Category: "volume", Capability: Executable, Source: "go-stock", DefaultParams: params()},
		{ID: "vwap", DisplayName: "VWAP", Description: "成交量加权平均价。", Category: "volume", Capability: Executable, Source: "go-stock", DefaultParams: params("period", 20)},
		{ID: "vol_ma", DisplayName: "成交量均线", Description: "成交量移动平均线。", Category: "volume", Capability: Executable, Source: "go-stock", DefaultParams: params("periods", []any{5, 10})},
		{ID: "donchian", DisplayName: "Donchian 通道", Description: "指定窗口的最高价与最低价通道。", Category: "volatility", Capability: Executable, Source: "go-stock", DefaultParams: params("period", 20)},
		{ID: "support_resistance", DisplayName: "支撑与压力", Description: "滚动高低点形成的支撑压力区。", Category: "structure", Capability: Experimental, Source: "daily_stock_analysis", DefaultParams: params("period", 20)},
	}
	strategies := []Definition{
		{ID: "bottom_volume", DisplayName: "底部放量", Description: "长期下跌后底部放量企稳。", Category: "reversal", Capability: Executable},
		{ID: "box_oscillation", DisplayName: "箱体震荡", Description: "箱底买入、箱顶卖出。", Category: "framework", Capability: Executable},
		{ID: "bull_trend", DisplayName: "默认多头趋势", Description: "多头排列与趋势延续。", Category: "trend", Capability: Executable},
		{ID: "chan_theory", DisplayName: "缠论", Description: "笔、线段、中枢与背驰。", Category: "framework", Capability: Experimental},
		{ID: "dragon_head", DisplayName: "龙头策略", Description: "板块轮动中识别龙头股。", Category: "trend", Capability: ContextRequired},
		{ID: "emotion_cycle", DisplayName: "情绪周期", Description: "市场情绪周期与量价结构。", Category: "framework", Capability: ContextRequired},
		{ID: "event_driven", DisplayName: "事件驱动", Description: "围绕公告、政策和订单催化。", Category: "framework", Capability: ContextRequired},
		{ID: "expectation_repricing", DisplayName: "预期重估", Description: "业绩、政策和估值预期变化。", Category: "framework", Capability: ContextRequired},
		{ID: "growth_quality", DisplayName: "成长质量", Description: "收入、利润、ROE 和现金流质量。", Category: "framework", Capability: ContextRequired},
		{ID: "hot_theme", DisplayName: "热点题材", Description: "板块热度与个股相对强弱。", Category: "framework", Capability: ContextRequired},
		{ID: "ma_golden_cross", DisplayName: "均线金叉", Description: "MA5 上穿 MA10 并结合量能。", Category: "trend", Capability: Executable, DefaultParams: params("fast", 5, "slow", 10, "volume_period", 5, "volume_ratio", 1.0)},
		{ID: "one_yang_three_yin", DisplayName: "一阳夹三阴", Description: "五日整理后突破形态。", Category: "pattern", Capability: Executable},
		{ID: "shrink_pullback", DisplayName: "缩量回踩", Description: "多头趋势中缩量回踩均线。", Category: "trend", Capability: Executable, DefaultParams: params("volume_ratio", 0.7)},
		{ID: "volume_breakout", DisplayName: "放量突破", Description: "放量突破近期阻力位。", Category: "trend", Capability: Executable, DefaultParams: params("period", 20, "volume_period", 5, "volume_ratio", 2.0)},
		{ID: "wave_theory", DisplayName: "波浪理论", Description: "推动浪与调整浪结构。", Category: "framework", Capability: Experimental},
	}
	for i := range traditional {
		traditional[i].Kind = "indicator"
		traditional[i].Enabled = true
		traditional[i].SortOrder = i + 1
		traditional[i].CurrentParams = cloneParams(traditional[i].DefaultParams)
	}
	for i := range strategies {
		strategies[i].Kind = "strategy"
		strategies[i].Source = "daily_stock_analysis"
		strategies[i].Enabled = true
		strategies[i].SortOrder = 100 + i
		if strategies[i].DefaultParams == nil {
			strategies[i].DefaultParams = params()
		}
		strategies[i].CurrentParams = cloneParams(strategies[i].DefaultParams)
	}
	return append(traditional, strategies...)
}

func cloneParams(in map[string]any) map[string]any {
	raw, _ := json.Marshal(in)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out
}
