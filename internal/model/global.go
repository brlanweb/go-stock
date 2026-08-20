package model

// GlobalQuote 隔夜外盘风险因子行情（全球指数/期货/汇率/波动率）。
// 供风险感知层（Risk Sentinel）在盘前采集，用于提前识别系统性风险。
type GlobalQuote struct {
	Symbol            string   `json:"symbol"`   // 内部标识：CN00Y/HXC/SPX/NDX/DJIA/HSI/USDCNH/VIX
	Name              string   `json:"name"`     // 中文名称
	Category          string   `json:"category"` // a50_future/china_adr/us_equity/hk_equity/fx/volatility
	Price             *float64 `json:"price"`
	ChangePct         *float64 `json:"change_pct"`
	Source            string   `json:"source"`
	ProviderTimestamp string   `json:"provider_timestamp,omitempty"`
}

// 全球风险因子类别常量。
const (
	GlobalCategoryA50Future  = "a50_future" // 富时中国A50期指：A股开盘预期最直接的定价品种
	GlobalCategoryChinaADR   = "china_adr"  // 纳斯达克金龙中国指数：中概股情绪传导
	GlobalCategoryUSEquity   = "us_equity"  // 美股三大指数
	GlobalCategoryHKEquity   = "hk_equity"  // 恒生指数
	GlobalCategoryFX         = "fx"         // 离岸人民币：资金外流压力
	GlobalCategoryVolatility = "volatility" // VIX 恐慌指数
)
