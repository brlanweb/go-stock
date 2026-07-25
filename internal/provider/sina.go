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

// Sina 新浪行情源（hq.sinajs.cn）：最后兜底。字段较少（无量比/换手/PE/市值）。
type Sina struct {
	gate    *RateGate
	breaker *CircuitBreaker
}

// NewSina 新浪需要 Referer 反盗链头。
func NewSina() *Sina {
	return &Sina{
		gate:    NewRateGate(8, 100*time.Millisecond),
		breaker: NewCircuitBreaker(5, 60*time.Second),
	}
}

func (s *Sina) Name() string { return "sina" }

// Breaker 暴露熔断器。
func (s *Sina) Breaker() *CircuitBreaker { return s.breaker }

// Quote 单只行情。
func (s *Sina) Quote(ctx context.Context, symbol string) (*model.Quote, error) {
	quotes, err := s.BatchQuotes(ctx, []string{symbol})
	if err != nil {
		return nil, err
	}
	if len(quotes) == 0 {
		return nil, fmt.Errorf("sina 无数据: %s", symbol)
	}
	return quotes[0], nil
}

// BatchQuotes 批量（新浪一次可查多只）。
func (s *Sina) BatchQuotes(ctx context.Context, symbols []string) ([]*model.Quote, error) {
	if err := s.gate.Wait(ctx); err != nil {
		return nil, err
	}
	codes := make([]string, 0, len(symbols))
	symBySina := make(map[string]string, len(symbols))
	for _, sym := range symbols {
		sc := model.SinaSymbol(sym)
		codes = append(codes, sc)
		symBySina[sc] = sym
	}
	url := "https://hq.sinajs.cn/list=" + strings.Join(codes, ",")
	body, err := httpGet(ctx, url, map[string]string{"Referer": "https://finance.sina.com.cn"})
	if err != nil {
		return nil, fmt.Errorf("sina quote: %w", err)
	}
	utf8Body, _, err := transform.Bytes(simplifiedchinese.GBK.NewDecoder(), body)
	if err != nil {
		utf8Body = body
	}

	now := time.Now()
	var out []*model.Quote
	for _, line := range strings.Split(string(utf8Body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// var hq_str_sh600519="贵州茅台,open,preclose,price,high,low,bid,ask,volume(股),amount,b1v,b1p,...,date,time,...";
		lhs, rhs, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		scode := strings.TrimPrefix(strings.TrimSpace(lhs), "var hq_str_")
		rhs = strings.Trim(strings.TrimSuffix(strings.TrimSpace(rhs), ";"), `"`)
		f := strings.Split(rhs, ",")
		if len(f) < 32 {
			continue
		}
		symbol, okm := symBySina[scode]
		if !okm {
			symbol = model.NormalizeSymbol(scode)
		}
		pf := func(i int) *float64 {
			if i >= len(f) {
				return nil
			}
			v := parseF(f[i])
			return &v
		}
		price := pf(3)
		if price == nil || *price <= 0 {
			continue
		}
		q := &model.Quote{
			Symbol:    symbol,
			Code:      strings.TrimLeft(scode, "shszbj"),
			Name:      f[0],
			Market:    model.MarketCN,
			Source:    s.Name(),
			FetchedAt: now,
			Currency:  "CNY",

			Open:     pf(1),
			PreClose: pf(2),
			Price:    price,
			High:     pf(4),
			Low:      pf(5),
			Amount:   pf(9),
		}
		if v := pf(8); v != nil { // 新浪成交量单位已是股
			vol := int64(*v)
			q.Volume = &vol
		}
		// 推算涨跌
		if q.PreClose != nil && *q.PreClose > 0 {
			chg := *price - *q.PreClose
			pct := chg / *q.PreClose * 100
			q.ChangeAmount = &chg
			q.ChangePct = &pct
			amp := 0.0
			if q.High != nil && q.Low != nil {
				amp = (*q.High - *q.Low) / *q.PreClose * 100
			}
			q.Amplitude = &amp
		}
		// 买卖五档：f[10]买一量 f[11]买一价 ... f[20]卖一量 f[21]卖一价 ...
		for i := 0; i < 5; i++ {
			bv, bp := pf(10+i*2), pf(11+i*2)
			if bp != nil && *bp > 0 && bv != nil {
				q.Bids = append(q.Bids, model.PriceLevel{Price: *bp, Volume: int64(*bv)})
			}
			av, ap := pf(20+i*2), pf(21+i*2)
			if ap != nil && *ap > 0 && av != nil {
				q.Asks = append(q.Asks, model.PriceLevel{Price: *ap, Volume: int64(*av)})
			}
		}
		// 行情时间 f[30] 日期 f[31] 时间
		if len(f) > 31 {
			if ts, err := time.ParseInLocation("2006-01-02 15:04:05", f[30]+" "+f[31], time.Local); err == nil {
				q.ProviderTimestamp = ts.Format(time.RFC3339)
			}
		}
		out = append(out, q)
	}
	return out, nil
}
