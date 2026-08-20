package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"

	"github.com/hoax/go-stock/internal/model"
)

// 隔夜外盘风险因子品种表。secid 均已通过东财 push2 实测核验（2026-08）：
//   - 批量 ulist 接口支持：104.CN00Y（新加坡 A50 期指）、100.SPX/NDX/DJIA、
//     100.HSI、133.USDCNH；
//   - 251.HXC（纳斯达克金龙中国指数）ulist 不返回，需走单只 stock/get 接口；
//   - VIX 东财无数据，由新浪 znb_VIX 提供。
type GlobalInstrument struct {
	Symbol   string
	Name     string
	Category string
	EmSecID  string
}

// GlobalInstruments 参与风险感知的外盘品种（批量接口部分）。
var GlobalInstruments = []GlobalInstrument{
	{"CN00Y", "A50期指当月连续", model.GlobalCategoryA50Future, "104.CN00Y"},
	{"SPX", "标普500", model.GlobalCategoryUSEquity, "100.SPX"},
	{"NDX", "纳斯达克", model.GlobalCategoryUSEquity, "100.NDX"},
	{"DJIA", "道琼斯", model.GlobalCategoryUSEquity, "100.DJIA"},
	{"HSI", "恒生指数", model.GlobalCategoryHKEquity, "100.HSI"},
	{"USDCNH", "美元兑离岸人民币", model.GlobalCategoryFX, "133.USDCNH"},
}

// hxcInstrument 金龙指数走东财单只接口。
var hxcInstrument = GlobalInstrument{"HXC", "纳斯达克金龙中国", model.GlobalCategoryChinaADR, "251.HXC"}

// GlobalQuotes 拉取东财外盘行情：一次 ulist 批量 + 一次金龙指数单只。
// 单只失败只损失金龙因子，不阻断整批。
func (e *Eastmoney) GlobalQuotes(ctx context.Context) ([]model.GlobalQuote, error) {
	if err := e.gate.Wait(ctx); err != nil {
		return nil, err
	}
	secids := make([]string, 0, len(GlobalInstruments))
	instByCode := make(map[string]GlobalInstrument, len(GlobalInstruments))
	for _, inst := range GlobalInstruments {
		secids = append(secids, inst.EmSecID)
		instByCode[inst.Symbol] = inst
	}
	url := fmt.Sprintf("https://push2.eastmoney.com/api/qt/ulist.np/get?fltt=2&invt=2&fields=f2,f3,f12,f13,f14,f124&secids=%s", strings.Join(secids, ","))
	body, err := httpGet(ctx, url, map[string]string{"Referer": "https://quote.eastmoney.com/"})
	if err != nil {
		return nil, fmt.Errorf("eastmoney global: %w", err)
	}
	var resp emUlistResp
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("eastmoney global 解析失败: %w", err)
	}
	out := make([]model.GlobalQuote, 0, len(GlobalInstruments)+1)
	for _, row := range resp.Data.Diff {
		var code string
		if raw, ok := row["f12"]; ok {
			_ = json.Unmarshal(raw, &code)
		}
		inst, ok := instByCode[code]
		if !ok {
			continue
		}
		f := func(key string) EmFloat {
			var v EmFloat
			if raw, ok := row[key]; ok {
				_ = v.UnmarshalJSON(raw)
			}
			return v
		}
		q := model.GlobalQuote{
			Symbol: inst.Symbol, Name: inst.Name, Category: inst.Category,
			Source: e.Name(), Price: f("f2").Ptr(), ChangePct: f("f3").Ptr(),
		}
		if ts := f("f124"); ts.Valid && ts.Value > 0 {
			q.ProviderTimestamp = time.Unix(int64(ts.Value), 0).Format(time.RFC3339)
		}
		if q.Price != nil {
			out = append(out, q)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("eastmoney global 无有效数据")
	}
	if hxc, err := e.globalHXC(ctx); err == nil {
		out = append(out, *hxc)
	}
	return out, nil
}

// globalHXC 单只接口取纳斯达克金龙中国指数（251.HXC 不被 ulist 支持）。
func (e *Eastmoney) globalHXC(ctx context.Context) (*model.GlobalQuote, error) {
	if err := e.gate.Wait(ctx); err != nil {
		return nil, err
	}
	url := fmt.Sprintf("https://push2.eastmoney.com/api/qt/stock/get?secid=%s&invt=2&fltt=2&fields=f43,f58,f60,f169,f170,f86", hxcInstrument.EmSecID)
	body, err := httpGet(ctx, url, map[string]string{"Referer": "https://quote.eastmoney.com/"})
	if err != nil {
		return nil, fmt.Errorf("eastmoney hxc: %w", err)
	}
	var resp emStockGetResp
	if err := json.Unmarshal(body, &resp); err != nil || resp.Data == nil {
		return nil, fmt.Errorf("eastmoney hxc 解析失败")
	}
	f := func(key string) EmFloat {
		var v EmFloat
		if raw, ok := resp.Data[key]; ok {
			_ = v.UnmarshalJSON(raw)
		}
		return v
	}
	q := &model.GlobalQuote{
		Symbol: hxcInstrument.Symbol, Name: hxcInstrument.Name, Category: hxcInstrument.Category,
		Source: e.Name(), Price: f("f43").Ptr(), ChangePct: f("f170").Ptr(),
	}
	if ts := f("f86"); ts.Valid && ts.Value > 0 {
		q.ProviderTimestamp = time.Unix(int64(ts.Value), 0).Format(time.RFC3339)
	}
	if q.Price == nil {
		return nil, fmt.Errorf("eastmoney hxc 无有效价格")
	}
	return q, nil
}

// GlobalVIX 新浪 znb_VIX 行情。返回格式（GBK）：
// var hq_str_znb_VIX="VIX恐慌指数,14.8800,-0.97,-6.12,,,2026-08-20,04:13:01,15.9200,...";
// 字段：0 名称，1 最新价，2 涨跌额，3 涨跌幅%，6 日期，7 时间。
func (s *Sina) GlobalVIX(ctx context.Context) (*model.GlobalQuote, error) {
	if err := s.gate.Wait(ctx); err != nil {
		return nil, err
	}
	body, err := httpGet(ctx, "https://hq.sinajs.cn/list=znb_VIX", map[string]string{"Referer": "https://finance.sina.com.cn"})
	if err != nil {
		return nil, fmt.Errorf("sina vix: %w", err)
	}
	utf8Body, _, err := transform.Bytes(simplifiedchinese.GBK.NewDecoder(), body)
	if err != nil {
		utf8Body = body
	}
	_, rhs, ok := strings.Cut(string(utf8Body), "=")
	if !ok {
		return nil, fmt.Errorf("sina vix 响应格式异常")
	}
	rhs = strings.Trim(strings.TrimSuffix(strings.TrimSpace(rhs), ";"), `"`)
	f := strings.Split(rhs, ",")
	if len(f) < 8 {
		return nil, fmt.Errorf("sina vix 无数据")
	}
	price := parseF(f[1])
	if price <= 0 {
		return nil, fmt.Errorf("sina vix 无有效价格")
	}
	chgPct := parseF(f[3])
	q := &model.GlobalQuote{
		Symbol: "VIX", Name: "VIX恐慌指数", Category: model.GlobalCategoryVolatility,
		Source: s.Name(), Price: &price, ChangePct: &chgPct,
	}
	if len(f) > 7 && f[6] != "" && f[7] != "" {
		q.ProviderTimestamp = f[6] + "T" + f[7]
	}
	return q, nil
}
