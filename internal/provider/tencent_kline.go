package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/hoax/go-stock/internal/model"
)

// DailyKlines 腾讯日K备用源（web.ifzq.gtimg.cn，参考 daily_stock_analysis TencentFetcher）。
// 单请求上限约 2000 根，按日期窗口翻页；复权因子由 qfq/raw 比值推导。
func (t *Tencent) DailyKlines(ctx context.Context, symbol, beg, end string) ([]model.Kline, error) {
	raw, err := t.fetchKlinesPaged(ctx, symbol, beg, end, "")
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, nil
	}
	qfq, err := t.fetchKlinesPaged(ctx, symbol, beg, end, "qfq")
	if err != nil {
		for i := range raw {
			raw[i].AdjFactor = 1
		}
		return raw, nil
	}
	qfqClose := make(map[string]float64, len(qfq))
	for _, k := range qfq {
		qfqClose[k.Date] = k.Close
	}
	for i := range raw {
		if qc, ok := qfqClose[raw[i].Date]; ok && raw[i].Close > 0 {
			raw[i].AdjFactor = qc / raw[i].Close
		} else {
			raw[i].AdjFactor = 1
		}
	}
	return raw, nil
}

// fetchKlinesPaged 按 2000 根窗口向前翻页直至覆盖 [beg, end]。
func (t *Tencent) fetchKlinesPaged(ctx context.Context, symbol, beg, end, fq string) ([]model.Kline, error) {
	begDate := normDate(beg, "1990-01-01")
	endDate := normDate(end, time.Now().Format("2006-01-02"))

	seen := make(map[string]bool)
	var all []model.Kline
	cursor := endDate
	for i := 0; i < 30; i++ { // 最多 30 页（约 240 年，绝对安全上限）
		batch, err := t.fetchKlinesOnce(ctx, symbol, begDate, cursor, fq)
		if err != nil {
			return nil, err
		}
		if len(batch) == 0 {
			break
		}
		newCount := 0
		for _, k := range batch {
			if !seen[k.Date] {
				seen[k.Date] = true
				all = append(all, k)
				newCount++
			}
		}
		earliest := batch[0].Date
		if newCount == 0 || earliest <= begDate {
			break
		}
		// 下一窗口：最早日期前一天
		et, err := time.Parse("2006-01-02", earliest)
		if err != nil {
			break
		}
		cursor = et.AddDate(0, 0, -1).Format("2006-01-02")
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Date < all[j].Date })
	// 重算涨跌幅
	for i := 1; i < len(all); i++ {
		if prev := all[i-1].Close; prev > 0 {
			all[i].ChangePct = (all[i].Close - prev) / prev * 100
		}
	}
	return all, nil
}

type tencentKlineResp struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"` // 出错时为 []，正常为 object
}

func (t *Tencent) fetchKlinesOnce(ctx context.Context, symbol, beg, end, fq string) ([]model.Kline, error) {
	if err := t.gate.Wait(ctx); err != nil {
		return nil, err
	}
	tsym := model.TencentSymbol(symbol)
	url := fmt.Sprintf("https://web.ifzq.gtimg.cn/appstock/app/fqkline/get?param=%s,day,%s,%s,2000,%s",
		tsym, beg, end, fq)
	body, err := httpGet(ctx, url, map[string]string{"Referer": "https://gu.qq.com/"})
	if err != nil {
		return nil, fmt.Errorf("tencent kline: %w", err)
	}
	var resp tencentKlineResp
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("tencent kline 解析失败: %w", err)
	}
	if resp.Code != 0 || len(resp.Data) == 0 || resp.Data[0] != '{' {
		return nil, fmt.Errorf("tencent kline 接口错误: code=%d msg=%s", resp.Code, resp.Msg)
	}
	var dataMap map[string]json.RawMessage
	if err := json.Unmarshal(resp.Data, &dataMap); err != nil {
		return nil, err
	}
	symData, ok := dataMap[tsym]
	if !ok {
		return nil, fmt.Errorf("tencent kline 无数据: %s", symbol)
	}
	var inner map[string]json.RawMessage
	if err := json.Unmarshal(symData, &inner); err != nil {
		return nil, err
	}
	kind := "day"
	if fq == "qfq" {
		kind = "qfqday"
	}
	var rows [][]json.RawMessage
	if raw, ok := inner[kind]; ok {
		_ = json.Unmarshal(raw, &rows)
	} else if raw, ok := inner["day"]; ok {
		_ = json.Unmarshal(raw, &rows)
	}
	klines := make([]model.Kline, 0, len(rows))
	for _, r := range rows {
		// [date, open, close, high, low, volume(手), ...]
		if len(r) < 6 {
			continue
		}
		var date string
		_ = json.Unmarshal(r[0], &date)
		klines = append(klines, model.Kline{
			Symbol:    symbol,
			Date:      date,
			Open:      jsonToF(r[1]),
			Close:     jsonToF(r[2]),
			High:      jsonToF(r[3]),
			Low:       jsonToF(r[4]),
			Volume:    int64(jsonToF(r[5])) * 100,
			AdjFactor: 1,
		})
	}
	return klines, nil
}

// normDate 支持 YYYYMMDD / YYYY-MM-DD / 空值。
func normDate(s, def string) string {
	switch len(s) {
	case 8:
		return s[:4] + "-" + s[4:6] + "-" + s[6:]
	case 10:
		return s
	default:
		if s == "0" || s == "" {
			return def
		}
		return def
	}
}

func jsonToF(raw json.RawMessage) float64 {
	var f float64
	if err := json.Unmarshal(raw, &f); err == nil {
		return f
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return parseF(s)
	}
	return 0
}
