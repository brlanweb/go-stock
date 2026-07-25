package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/hoax/go-stock/internal/model"
)

// Eastmoney 东方财富数据源：字段最全（PE/PB/市值/量比/换手/52周高低/盘口）。
// 接口来自 efinance/akshare 底层（push2.eastmoney.com），fltt=2 返回已除权浮点。
type Eastmoney struct {
	gate    *RateGate
	breaker *CircuitBreaker
}

// NewEastmoney 保守限流：默认由配置控制 QPS，并附加 300-1200ms 随机抖动；
// 连续失败后冷却 10 分钟，避免固定出口 IP 在短时间内反复撞限流。
func NewEastmoney(qps float64) *Eastmoney {
	return &Eastmoney{
		gate:    NewRateGate(qps, 1200*time.Millisecond),
		breaker: NewCircuitBreaker(3, 10*time.Minute),
	}
}

func (e *Eastmoney) Name() string { return "eastmoney" }

// Breaker 暴露给管理器判断熔断状态。
func (e *Eastmoney) Breaker() *CircuitBreaker { return e.breaker }

// Gate 暴露给回填任务复用限流。
func (e *Eastmoney) Gate() *RateGate { return e.gate }

// ---------- 实时行情（单只，全字段） ----------

// 东财单股接口字段映射（fltt=2 浮点模式）：
// f43 最新价 f44 最高 f45 最低 f46 今开 f47 成交量(手) f48 成交额(元)
// f50 量比 f57 代码 f58 名称 f60 昨收 f116 总市值 f117 流通市值
// f162 PE动 f167 PB f168 换手率% f169 涨跌额 f170 涨跌幅% f171 振幅%
// f174 52周最高? (实际 f174/f175 为52周高低) f127 行业
// 盘口: f19/f39 买一价量... 使用 f11~f20(买五档) f31~f40(卖五档)
const emQuoteFields = "f43,f44,f45,f46,f47,f48,f50,f57,f58,f60,f116,f117,f162,f167,f168,f169,f170,f171,f174,f175,f127," +
	"f11,f12,f13,f14,f15,f16,f17,f18,f19,f20,f31,f32,f33,f34,f35,f36,f37,f38,f39,f40,f86"

type emStockGetResp struct {
	Data map[string]json.RawMessage `json:"data"`
}

// Quote 单只实时行情。
func (e *Eastmoney) Quote(ctx context.Context, symbol string) (*model.Quote, error) {
	if err := e.gate.Wait(ctx); err != nil {
		return nil, err
	}
	secid := model.SecIDForEastmoney(symbol)
	url := fmt.Sprintf("https://push2.eastmoney.com/api/qt/stock/get?secid=%s&invt=2&fltt=2&fields=%s", secid, emQuoteFields)
	body, err := httpGet(ctx, url, map[string]string{"Referer": "https://quote.eastmoney.com/"})
	if err != nil {
		return nil, fmt.Errorf("eastmoney quote: %w", err)
	}
	var resp emStockGetResp
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("eastmoney quote 解析失败: %w", err)
	}
	if resp.Data == nil {
		return nil, fmt.Errorf("eastmoney quote 无数据: %s", symbol)
	}

	f := func(key string) EmFloat {
		var v EmFloat
		if raw, ok := resp.Data[key]; ok {
			_ = v.UnmarshalJSON(raw)
		}
		return v
	}
	name := ""
	if raw, ok := resp.Data["f58"]; ok {
		_ = json.Unmarshal(raw, &name)
	}

	q := &model.Quote{
		Symbol:    symbol,
		Code:      strings.TrimLeft(symbol, "SHZBJ"),
		Name:      name,
		Market:    model.MarketCN,
		Source:    e.Name(),
		FetchedAt: time.Now(),
		Currency:  "CNY",

		Price:        f("f43").Ptr(),
		High:         f("f44").Ptr(),
		Low:          f("f45").Ptr(),
		Open:         f("f46").Ptr(),
		PreClose:     f("f60").Ptr(),
		ChangeAmount: f("f169").Ptr(),
		ChangePct:    f("f170").Ptr(),
		Amplitude:    f("f171").Ptr(),

		Amount:       f("f48").Ptr(),
		VolumeRatio:  f("f50").Ptr(),
		TurnoverRate: f("f168").Ptr(),

		PERatio: f("f162").Ptr(),
		PBRatio: f("f167").Ptr(),
		TotalMV: f("f116").Ptr(),
		CircMV:  f("f117").Ptr(),

		High52W: f("f174").Ptr(),
		Low52W:  f("f175").Ptr(),
	}
	// 成交量：手 -> 股
	if v := f("f47"); v.Valid {
		vol := int64(v.Value) * 100
		q.Volume = &vol
	}
	// 行情时间戳 f86（unix 秒）
	if ts := f("f86"); ts.Valid {
		q.ProviderTimestamp = time.Unix(int64(ts.Value), 0).Format(time.RFC3339)
	}
	// 买五档 f19/f20=买一价/量 f17/f18=买二 ... f11/f12=买五
	bidKeys := [][2]string{{"f19", "f20"}, {"f17", "f18"}, {"f15", "f16"}, {"f13", "f14"}, {"f11", "f12"}}
	askKeys := [][2]string{{"f39", "f40"}, {"f37", "f38"}, {"f35", "f36"}, {"f33", "f34"}, {"f31", "f32"}}
	for _, k := range bidKeys {
		p, v := f(k[0]), f(k[1])
		if p.Valid && p.Value > 0 {
			q.Bids = append(q.Bids, model.PriceLevel{Price: p.Value, Volume: int64(v.Or(0)) * 100})
		}
	}
	for _, k := range askKeys {
		p, v := f(k[0]), f(k[1])
		if p.Valid && p.Value > 0 {
			q.Asks = append(q.Asks, model.PriceLevel{Price: p.Value, Volume: int64(v.Or(0)) * 100})
		}
	}
	if q.Price == nil {
		return nil, fmt.Errorf("eastmoney quote 无有效价格: %s", symbol)
	}
	return q, nil
}

// BatchQuotes 东财 ulist 批量接口（一次最多约 50 只，字段少于单只接口）。
func (e *Eastmoney) BatchQuotes(ctx context.Context, symbols []string) ([]*model.Quote, error) {
	var out []*model.Quote
	for i := 0; i < len(symbols); i += 50 {
		end := i + 50
		if end > len(symbols) {
			end = len(symbols)
		}
		batch, err := e.batchQuotes50(ctx, symbols[i:end])
		if err != nil {
			return out, err
		}
		out = append(out, batch...)
	}
	return out, nil
}

type emUlistResp struct {
	Data struct {
		Diff []map[string]json.RawMessage `json:"diff"`
	} `json:"data"`
}

func (e *Eastmoney) batchQuotes50(ctx context.Context, symbols []string) ([]*model.Quote, error) {
	secids := make([]string, 0, len(symbols))
	for _, s := range symbols {
		secids = append(secids, model.SecIDForEastmoney(s))
	}
	return e.batchQuotesBySecids(ctx, strings.Join(secids, ","))
}

// batchQuotesBySecids 按东财 secid 串批量查询（指数等特殊 secid 复用此入口）。
func (e *Eastmoney) batchQuotesBySecids(ctx context.Context, secids string) ([]*model.Quote, error) {
	if err := e.gate.Wait(ctx); err != nil {
		return nil, err
	}
	// ulist.np/get：f1-f31 常用字段
	fields := "f2,f3,f4,f5,f6,f8,f10,f12,f13,f14,f15,f16,f17,f18,f9,f23,f20,f21"
	url := fmt.Sprintf("https://push2.eastmoney.com/api/qt/ulist.np/get?fltt=2&invt=2&fields=%s&secids=%s",
		fields, secids)
	body, err := httpGet(ctx, url, map[string]string{"Referer": "https://quote.eastmoney.com/"})
	if err != nil {
		return nil, fmt.Errorf("eastmoney batch: %w", err)
	}
	var resp emUlistResp
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("eastmoney batch 解析失败: %w", err)
	}
	now := time.Now()
	var out []*model.Quote
	for _, row := range resp.Data.Diff {
		f := func(key string) EmFloat {
			var v EmFloat
			if raw, ok := row[key]; ok {
				_ = v.UnmarshalJSON(raw)
			}
			return v
		}
		var code, name string
		if raw, ok := row["f12"]; ok {
			_ = json.Unmarshal(raw, &code)
		}
		if raw, ok := row["f14"]; ok {
			_ = json.Unmarshal(raw, &name)
		}
		var mkt EmFloat // f13: 1=SH 0=SZ/BJ
		if raw, ok := row["f13"]; ok {
			_ = mkt.UnmarshalJSON(raw)
		}
		ex := "SZ"
		if mkt.Or(0) == 1 {
			ex = "SH"
		} else if model.NormalizeSymbol(code) != "" {
			// BJ 号段修正（东财 m=0 含深北两市）
			if strings.HasPrefix(model.NormalizeSymbol(code), "BJ") {
				ex = "BJ"
			}
		}
		q := &model.Quote{
			Symbol:    ex + code,
			Code:      code,
			Name:      name,
			Market:    model.MarketCN,
			Source:    e.Name(),
			FetchedAt: now,
			Currency:  "CNY",

			Price:        f("f2").Ptr(),
			ChangePct:    f("f3").Ptr(),
			ChangeAmount: f("f4").Ptr(),
			Amount:       f("f6").Ptr(),
			TurnoverRate: f("f8").Ptr(),
			VolumeRatio:  f("f10").Ptr(),
			High:         f("f15").Ptr(),
			Low:          f("f16").Ptr(),
			Open:         f("f17").Ptr(),
			PreClose:     f("f18").Ptr(),
			PERatio:      f("f9").Ptr(),
			PBRatio:      f("f23").Ptr(),
			TotalMV:      f("f20").Ptr(),
			CircMV:       f("f21").Ptr(),
		}
		if v := f("f5"); v.Valid {
			vol := int64(v.Value) * 100
			q.Volume = &vol
		}
		if q.Price != nil {
			out = append(out, q)
		}
	}
	return out, nil
}
