package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/hoax/go-stock/internal/model"
)

// ---------- 历史K线 ----------

type emKlineResp struct {
	Data struct {
		Code   string   `json:"code"`
		Name   string   `json:"name"`
		Klines []string `json:"klines"`
	} `json:"data"`
}

// DailyKlines 拉取日K线。策略：
//  1. 拉不复权（fqt=0）得到原始 OHLCV；
//  2. 拉后复权（fqt=2）收盘价，推导累积复权因子 adj_factor = close_hfq / close_raw；
//
// 前复权价可由 close_raw * adj_factor / latest_adj_factor 随时重算，除权后无需重刷全量历史。
func (e *Eastmoney) DailyKlines(ctx context.Context, symbol, beg, end string) ([]model.Kline, error) {
	raw, err := e.fetchKlines(ctx, symbol, beg, end, 0)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, nil
	}
	hfq, err := e.fetchKlines(ctx, symbol, beg, end, 2)
	if err != nil {
		return nil, err
	}
	// hfq 收盘按日期对齐推导复权因子
	hfqClose := make(map[string]float64, len(hfq))
	for _, k := range hfq {
		hfqClose[k.Date] = k.Close
	}
	for i := range raw {
		if hc, ok := hfqClose[raw[i].Date]; ok && raw[i].Close > 0 {
			raw[i].AdjFactor = hc / raw[i].Close
		} else {
			raw[i].AdjFactor = 1
		}
	}
	return raw, nil
}

// fetchKlines fqt: 0 不复权 / 1 前复权 / 2 后复权。
func (e *Eastmoney) fetchKlines(ctx context.Context, symbol, beg, end string, fqt int) ([]model.Kline, error) {
	if err := e.gate.Wait(ctx); err != nil {
		return nil, err
	}
	secid := model.SecIDForEastmoney(symbol)
	if beg == "" {
		beg = "0"
	}
	if end == "" {
		end = "20500101"
	}
	url := fmt.Sprintf("https://push2his.eastmoney.com/api/qt/stock/kline/get?secid=%s&klt=101&fqt=%d&beg=%s&end=%s&lmt=1000000&fields1=f1,f2,f3&fields2=f51,f52,f53,f54,f55,f56,f57,f58,f61",
		secid, fqt, beg, end)
	body, err := httpGet(ctx, url, map[string]string{"Referer": "https://quote.eastmoney.com/"})
	if err != nil {
		return nil, fmt.Errorf("eastmoney kline: %w", err)
	}
	var resp emKlineResp
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("eastmoney kline 解析失败: %w", err)
	}
	klines := make([]model.Kline, 0, len(resp.Data.Klines))
	// fields2 顺序: f51日期,f52开,f53收,f54高,f55低,f56量(手),f57额,f58振幅,f61换手率? 实际顺序按逗号
	for _, line := range resp.Data.Klines {
		parts := strings.Split(line, ",")
		if len(parts) < 7 {
			continue
		}
		k := model.Kline{
			Symbol:    symbol,
			Date:      parts[0],
			Open:      parseF(parts[1]),
			Close:     parseF(parts[2]),
			High:      parseF(parts[3]),
			Low:       parseF(parts[4]),
			Volume:    int64(parseF(parts[5])) * 100, // 手->股
			Amount:    parseF(parts[6]),
			AdjFactor: 1,
		}
		if len(parts) >= 9 {
			k.TurnoverRate = parseF(parts[8])
		}
		klines = append(klines, k)
	}
	// 涨跌幅由昨收推算（避免依赖字段顺序差异）
	for i := 1; i < len(klines); i++ {
		prev := klines[i-1].Close
		if prev > 0 {
			klines[i].ChangePct = (klines[i].Close - prev) / prev * 100
		}
	}
	return klines, nil
}

func eastmoneyShanghaiLocation() *time.Location {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.FixedZone("CST", 8*3600)
	}
	return loc
}

func parseF(s string) float64 {
	f, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f
}

// ---------- 分时 ----------

type emTrendsResp struct {
	Data struct {
		PreClose float64  `json:"prePrice"`
		Trends   []string `json:"trends"`
	} `json:"data"`
}

// Timeshare 当日分时（1分钟）。
func (e *Eastmoney) Timeshare(ctx context.Context, symbol string) ([]model.TimesharePoint, error) {
	if err := e.gate.Wait(ctx); err != nil {
		return nil, err
	}
	secid := model.SecIDForEastmoney(symbol)
	url := fmt.Sprintf("https://push2his.eastmoney.com/api/qt/stock/trends2/get?secid=%s&ndays=1&iscr=0&fields1=f1,f2,f3,f8&fields2=f51,f53,f56,f58", secid)
	body, err := httpGet(ctx, url, map[string]string{"Referer": "https://quote.eastmoney.com/"})
	if err != nil {
		return nil, fmt.Errorf("eastmoney timeshare: %w", err)
	}
	var resp emTrendsResp
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("eastmoney timeshare 解析失败: %w", err)
	}
	points := make([]model.TimesharePoint, 0, len(resp.Data.Trends))
	for _, line := range resp.Data.Trends {
		// "2026-07-24 09:30,1297.00,3200,1296.50"  日期时间,价,量(手),均价
		parts := strings.Split(line, ",")
		if len(parts) < 4 {
			continue
		}
		t := parts[0]
		if i := strings.IndexByte(t, ' '); i > 0 {
			t = t[i+1:]
		}
		points = append(points, model.TimesharePoint{
			Time:     t,
			Price:    parseF(parts[1]),
			Volume:   int64(parseF(parts[2])) * 100,
			AvgPrice: parseF(parts[3]),
		})
	}
	return points, nil
}

// ---------- 全市场列表/快照 ----------

// AllSecurities 全市场 A股+ETF 列表及当日快照（分页 clist 接口）。
// fs 参数：沪深京A股 + ETF。
func (e *Eastmoney) AllSecurities(ctx context.Context) ([]SecuritySnapshot, error) {
	var all []SecuritySnapshot
	// A股（沪深京）
	stocks, err := e.fetchClist(ctx, "m:0+t:6,m:0+t:80,m:1+t:2,m:1+t:23,m:0+t:81+s:2048", model.SecStock)
	if err != nil {
		return nil, err
	}
	all = append(all, stocks...)
	// ETF
	etfs, err := e.fetchClist(ctx, "b:MK0021,b:MK0022,b:MK0023,b:MK0024", model.SecETF)
	if err != nil {
		return all, err
	}
	all = append(all, etfs...)
	return all, nil
}

type emClistResp struct {
	Data struct {
		Total int                          `json:"total"`
		Diff  []map[string]json.RawMessage `json:"diff"`
	} `json:"data"`
}

// clistHosts 东财列表接口节点：主节点限流时降级延迟节点（收盘后数据等价）。
var clistHosts = []string{"push2.eastmoney.com", "push2delay.eastmoney.com"}

func (e *Eastmoney) fetchClist(ctx context.Context, fs string, secType model.SecurityType) ([]SecuritySnapshot, error) {
	var out []SecuritySnapshot
	seen := make(map[string]bool)
	page := 1
	const pageSize = 100 // 部分节点 pz 上限 100，统一用 100 保证翻页正确
	for {
		if err := e.gate.Wait(ctx); err != nil {
			return out, err
		}
		fields := "f2,f3,f4,f5,f6,f7,f8,f9,f10,f12,f13,f14,f15,f16,f17,f18,f20,f21,f23,f26,f38,f39,f100,f124"
		var body []byte
		var err error
		for _, host := range clistHosts {
			url := fmt.Sprintf("https://%s/api/qt/clist/get?pn=%d&pz=%d&po=1&np=1&fltt=2&invt=2&fid=f12&fs=%s&fields=%s",
				host, page, pageSize, fs, fields)
			body, err = httpGet(ctx, url, map[string]string{"Referer": "https://quote.eastmoney.com/"})
			if err == nil {
				break
			}
		}
		if err != nil {
			return out, fmt.Errorf("eastmoney clist p%d: %w", page, err)
		}
		var resp emClistResp
		if err := json.Unmarshal(body, &resp); err != nil {
			return out, fmt.Errorf("eastmoney clist 解析失败 p%d: %w", page, err)
		}
		if len(resp.Data.Diff) == 0 {
			break
		}
		for _, row := range resp.Data.Diff {
			f := func(key string) EmFloat {
				var v EmFloat
				if raw, ok := row[key]; ok {
					_ = v.UnmarshalJSON(raw)
				}
				return v
			}
			var code, name, industry string
			if raw, ok := row["f12"]; ok {
				_ = json.Unmarshal(raw, &code)
			}
			if raw, ok := row["f14"]; ok {
				_ = json.Unmarshal(raw, &name)
			}
			if raw, ok := row["f100"]; ok {
				_ = json.Unmarshal(raw, &industry)
			}
			ex := "SZ"
			if f("f13").Or(0) == 1 {
				ex = "SH"
			} else if norm := model.NormalizeSymbol(code); strings.HasPrefix(norm, "BJ") {
				ex = "BJ"
			}
			snap := SecuritySnapshot{
				Symbol:       ex + code,
				Code:         code,
				Name:         name,
				SecType:      secType,
				Exchange:     ex,
				Industry:     industry,
				Price:        f("f2").Or(0),
				ChangePct:    f("f3").Or(0),
				ChangeAmount: f("f4").Or(0),
				Volume:       int64(f("f5").Or(0)) * 100,
				Amount:       f("f6").Or(0),
				Amplitude:    f("f7").Or(0),
				TurnoverRate: f("f8").Or(0),
				PERatio:      f("f9").Or(0),
				VolumeRatio:  f("f10").Or(0),
				High:         f("f15").Or(0),
				Low:          f("f16").Or(0),
				Open:         f("f17").Or(0),
				PreClose:     f("f18").Or(0),
				TotalMV:      f("f20").Or(0),
				CircMV:       f("f21").Or(0),
				PBRatio:      f("f23").Or(0),
				TotalShare:   f("f38").Or(0),
				FloatShare:   f("f39").Or(0),
			}
			// f124 是行情更新时间（Unix 秒），只有存在有效成交量或成交额时才代表最后交易日。
			// 已退市证券的列表快照可能保留旧收盘价，不能把它当作当前交易状态。
			if updated := f("f124"); updated.Valid && updated.Value > 0 && (snap.Volume > 0 || snap.Amount > 0) {
				snap.TradeDate = time.Unix(int64(updated.Value), 0).In(eastmoneyShanghaiLocation()).Format("2006-01-02")
			}
			// 没有当日成交且行情时间为空的股票不应继续按在市证券回填到当前日。
			if secType == model.SecStock && snap.TradeDate == "" && snap.Volume == 0 && snap.Amount == 0 {
				snap.Status = "delisted"
			} else {
				snap.Status = "listed"
			}
			// f26 上市日期 YYYYMMDD
			if d := f("f26"); d.Valid && d.Value > 19000000 {
				s := strconv.Itoa(int(d.Value))
				if len(s) == 8 {
					snap.ListDate = s[:4] + "-" + s[4:6] + "-" + s[6:]
				}
			}
			if !seen[snap.Symbol] {
				seen[snap.Symbol] = true
				out = append(out, snap)
			}
		}
		if page*pageSize >= resp.Data.Total {
			break
		}
		page++
	}
	return out, nil
}
