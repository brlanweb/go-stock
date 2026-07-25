package provider

import (
	"context"
	"fmt"
	"strings"
	"time"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"

	"github.com/hoax/go-stock/internal/model"
)

// Tencent 腾讯行情源（qt.gtimg.cn）：降级第一候选。
// 返回 GBK 编码的 ~ 分隔文本，字段较全（含盘口、量比、换手、市值、52周高低）。
type Tencent struct {
	gate    *RateGate
	breaker *CircuitBreaker
}

// NewTencent 腾讯实时接口默认使用 8 QPS + 100ms 抖动。
func NewTencent() *Tencent {
	return NewTencentWithQPS(8)
}

// NewTencentWithQPS 为历史回填创建独立的保守限流实例。
func NewTencentWithQPS(qps float64) *Tencent {
	if qps <= 0 {
		qps = 0.35
	}
	return &Tencent{
		gate:    NewRateGate(qps, 100*time.Millisecond),
		breaker: NewCircuitBreaker(5, 60*time.Second),
	}
}

func (t *Tencent) Name() string { return "tencent" }

// Breaker 暴露熔断器。
func (t *Tencent) Breaker() *CircuitBreaker { return t.breaker }

// Quote 单只行情。
func (t *Tencent) Quote(ctx context.Context, symbol string) (*model.Quote, error) {
	quotes, err := t.BatchQuotes(ctx, []string{symbol})
	if err != nil {
		return nil, err
	}
	if len(quotes) == 0 {
		return nil, fmt.Errorf("tencent 无数据: %s", symbol)
	}
	return quotes[0], nil
}

// BatchQuotes 批量（腾讯单请求支持多只，逗号分隔，上限约60只）。
func (t *Tencent) BatchQuotes(ctx context.Context, symbols []string) ([]*model.Quote, error) {
	var out []*model.Quote
	for i := 0; i < len(symbols); i += 60 {
		end := i + 60
		if end > len(symbols) {
			end = len(symbols)
		}
		batch, err := t.batchQuotes60(ctx, symbols[i:end])
		if err != nil {
			return out, err
		}
		out = append(out, batch...)
	}
	return out, nil
}

func (t *Tencent) batchQuotes60(ctx context.Context, symbols []string) ([]*model.Quote, error) {
	if err := t.gate.Wait(ctx); err != nil {
		return nil, err
	}
	codes := make([]string, 0, len(symbols))
	symByTencent := make(map[string]string, len(symbols))
	for _, s := range symbols {
		tc := model.TencentSymbol(s)
		codes = append(codes, tc)
		symByTencent[tc] = s
	}
	url := "https://qt.gtimg.cn/q=" + strings.Join(codes, ",")
	body, err := httpGet(ctx, url, map[string]string{"Referer": "https://gu.qq.com/"})
	if err != nil {
		return nil, fmt.Errorf("tencent quote: %w", err)
	}
	// GBK -> UTF-8
	utf8Body, _, err := transform.Bytes(simplifiedchinese.GBK.NewDecoder(), body)
	if err != nil {
		utf8Body = body
	}

	now := time.Now()
	var out []*model.Quote
	for _, line := range strings.Split(string(utf8Body), ";") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, "=") {
			continue
		}
		// v_sh600519="1~贵州茅台~600519~...";
		lhs, rhs, _ := strings.Cut(line, "=")
		tcode := strings.TrimPrefix(strings.TrimSpace(lhs), "v_")
		rhs = strings.Trim(strings.TrimSpace(rhs), `"`)
		fields := strings.Split(rhs, "~")
		if len(fields) < 50 {
			continue
		}
		symbol, ok := symByTencent[tcode]
		if !ok {
			symbol = model.NormalizeSymbol(tcode)
		}
		q := parseTencentFields(symbol, fields, now)
		if q != nil {
			out = append(out, q)
		}
	}
	return out, nil
}

// parseTencentFields 腾讯字段位序（参考 daily_stock_analysis TencentFetcher 与公开文档）：
// 1名称 2代码 3最新价 4昨收 5今开 6成交量(手) 9-18买卖五档(价1量1..) 30时间 31涨跌额 32涨跌幅%
// 33最高 34最低 36成交量(手) 37成交额(万) 38换手率 39PE 41最高 42最低 43振幅 44流通市值(亿)
// 45总市值(亿) 46PB 47涨停 48跌停 49量比
func parseTencentFields(symbol string, f []string, now time.Time) *model.Quote {
	pf := func(i int) *float64 {
		if i >= len(f) {
			return nil
		}
		s := strings.TrimSpace(f[i])
		if s == "" || s == "-" {
			return nil
		}
		v := parseF(s)
		return &v
	}
	price := pf(3)
	if price == nil || *price <= 0 {
		return nil
	}
	q := &model.Quote{
		Symbol:    symbol,
		Code:      f[2],
		Name:      f[1],
		Market:    model.MarketCN,
		Source:    "tencent",
		FetchedAt: now,
		Currency:  "CNY",

		Price:        price,
		PreClose:     pf(4),
		Open:         pf(5),
		ChangeAmount: pf(31),
		ChangePct:    pf(32),
		High:         pf(33),
		Low:          pf(34),
		TurnoverRate: pf(38),
		PERatio:      pf(39),
		Amplitude:    pf(43),
		PBRatio:      pf(46),
		VolumeRatio:  pf(49),
	}
	// 成交量 手->股
	if v := pf(36); v != nil {
		vol := int64(*v) * 100
		q.Volume = &vol
	}
	// 成交额 万->元
	if a := pf(37); a != nil {
		amt := *a * 10000
		q.Amount = &amt
	}
	// 市值 亿->元
	if cm := pf(44); cm != nil {
		v := *cm * 1e8
		q.CircMV = &v
	}
	if tm := pf(45); tm != nil {
		v := *tm * 1e8
		q.TotalMV = &v
	}
	// 行情时间 f[30]: 20260724161433
	if len(f) > 30 && len(f[30]) == 14 {
		if ts, err := time.ParseInLocation("20060102150405", f[30], time.Local); err == nil {
			q.ProviderTimestamp = ts.Format(time.RFC3339)
		}
	}
	// 买卖五档 f[9]买一价 f[10]买一量 ... f[19]卖一价 f[20]卖一量 ...
	for i := 0; i < 5; i++ {
		bp, bv := pf(9+i*2), pf(10+i*2)
		if bp != nil && *bp > 0 && bv != nil {
			q.Bids = append(q.Bids, model.PriceLevel{Price: *bp, Volume: int64(*bv) * 100})
		}
		ap, av := pf(19+i*2), pf(20+i*2)
		if ap != nil && *ap > 0 && av != nil {
			q.Asks = append(q.Asks, model.PriceLevel{Price: *ap, Volume: int64(*av) * 100})
		}
	}
	return q
}
