package provider

import (
	"bytes"
	"encoding/json"
	"strconv"
)

// EmFloat 东财 JSON 数值字段：可能是 number，也可能是 "-"（停牌/无数据）。
type EmFloat struct {
	Value float64
	Valid bool
}

// UnmarshalJSON 实现容错解析。
func (e *EmFloat) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	if data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return nil
		}
		if s == "" || s == "-" || s == "--" {
			return nil
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil
		}
		e.Value, e.Valid = f, true
		return nil
	}
	var f float64
	if err := json.Unmarshal(data, &f); err != nil {
		return nil
	}
	e.Value, e.Valid = f, true
	return nil
}

// Ptr 返回 *float64（无效时 nil）。
func (e EmFloat) Ptr() *float64 {
	if !e.Valid {
		return nil
	}
	v := e.Value
	return &v
}

// PtrInt64 返回 *int64。
func (e EmFloat) PtrInt64() *int64 {
	if !e.Valid {
		return nil
	}
	v := int64(e.Value)
	return &v
}

// Or 返回值或默认值。
func (e EmFloat) Or(def float64) float64 {
	if !e.Valid {
		return def
	}
	return e.Value
}
