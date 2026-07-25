package provider

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hoax/go-stock/internal/model"
)

type Sector struct {
	Code string
	Name string
	Type string
}

type SectorConstituent struct {
	SectorCode string
	Symbol     string
}

func (e *Eastmoney) Sectors(ctx context.Context, sectorType string) ([]Sector, error) {
	fs := "m:90+t:2"
	if sectorType == "concept" {
		fs = "m:90+t:3"
	} else {
		sectorType = "industry"
	}
	var out []Sector
	page := 1
	const pageSize = 100
	for {
		if err := e.gate.Wait(ctx); err != nil {
			return out, err
		}
		url := fmt.Sprintf("https://push2delay.eastmoney.com/api/qt/clist/get?pn=%d&pz=%d&po=1&np=1&fltt=2&invt=2&fid=f12&fs=%s&fields=f12,f14", page, pageSize, fs)
		body, err := httpGet(ctx, url, map[string]string{"Referer": "https://quote.eastmoney.com/"})
		if err != nil {
			return out, fmt.Errorf("eastmoney %s sectors p%d: %w", sectorType, page, err)
		}
		var resp emClistResp
		if err := json.Unmarshal(body, &resp); err != nil {
			return out, err
		}
		for _, row := range resp.Data.Diff {
			var code, name string
			_ = json.Unmarshal(row["f12"], &code)
			_ = json.Unmarshal(row["f14"], &name)
			if code != "" && name != "" {
				out = append(out, Sector{Code: code, Name: name, Type: sectorType})
			}
		}
		if page*pageSize >= resp.Data.Total || len(resp.Data.Diff) == 0 {
			break
		}
		page++
	}
	return out, nil
}

func (e *Eastmoney) SectorConstituents(ctx context.Context, sectorCode string) ([]SectorConstituent, error) {
	var out []SectorConstituent
	page := 1
	const pageSize = 100
	for {
		if err := e.gate.Wait(ctx); err != nil {
			return out, err
		}
		url := fmt.Sprintf("https://push2delay.eastmoney.com/api/qt/clist/get?pn=%d&pz=%d&po=1&np=1&fltt=2&invt=2&fid=f12&fs=b:%s&fields=f12,f13", page, pageSize, sectorCode)
		body, err := httpGet(ctx, url, map[string]string{"Referer": "https://quote.eastmoney.com/"})
		if err != nil {
			return out, fmt.Errorf("eastmoney sector constituents %s p%d: %w", sectorCode, page, err)
		}
		var resp emClistResp
		if err := json.Unmarshal(body, &resp); err != nil {
			return out, err
		}
		for _, row := range resp.Data.Diff {
			var code string
			_ = json.Unmarshal(row["f12"], &code)
			if symbol := normalizeEastmoneyConstituent(code); symbol != "" {
				out = append(out, SectorConstituent{SectorCode: sectorCode, Symbol: symbol})
			}
		}
		if page*pageSize >= resp.Data.Total || len(resp.Data.Diff) == 0 {
			break
		}
		page++
	}
	return out, nil
}

func normalizeEastmoneyConstituent(code string) string {
	return model.NormalizeSymbol(code)
}
