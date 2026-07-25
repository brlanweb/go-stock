package model

import "strings"

// NormalizeSymbol 将各类输入规范化为统一 symbol：
//
//	600519 / SH600519 / sh600519 / 600519.SH -> SH600519
//	000001 / SZ000001 / 000001.SZ           -> SZ000001
//	920748 / BJ920748 / 920748.BJ           -> BJ920748
//	510300（ETF）                            -> SH510300
//	US.AAPL / CRYPTO.BTCUSDT 原样保留（预留扩展）
//
// 返回空字符串表示无法识别。
func NormalizeSymbol(input string) string {
	s := strings.TrimSpace(strings.ToUpper(input))
	if s == "" {
		return ""
	}

	// 预留扩展市场：显式前缀直接保留
	if strings.HasPrefix(s, "US.") || strings.HasPrefix(s, "CRYPTO.") {
		return s
	}

	// 600519.SH 后缀形式
	if i := strings.LastIndex(s, "."); i > 0 {
		base, suffix := s[:i], s[i+1:]
		if isDigits(base) && len(base) == 6 {
			switch suffix {
			case "SH", "SS":
				return "SH" + base
			case "SZ":
				return "SZ" + base
			case "BJ":
				return "BJ" + base
			}
		}
	}

	// SH600519 前缀形式
	if len(s) == 8 {
		prefix, code := s[:2], s[2:]
		if isDigits(code) {
			switch prefix {
			case "SH", "SS":
				return "SH" + code
			case "SZ":
				return "SZ" + code
			case "BJ":
				return "BJ" + code
			}
		}
	}

	// 纯 6 位数字：按号段推断交易所
	if isDigits(s) && len(s) == 6 {
		return inferExchange(s) + s
	}

	return ""
}

// inferExchange 依据 A股号段推断交易所。
func inferExchange(code string) string {
	switch {
	// 上海：60xxxx 主板、68xxxx 科创板、90xxxx B股、51/58xxxx ETF、000xxx 指数(上证)
	case strings.HasPrefix(code, "60"), strings.HasPrefix(code, "68"),
		strings.HasPrefix(code, "90"), strings.HasPrefix(code, "51"),
		strings.HasPrefix(code, "58"), strings.HasPrefix(code, "56"):
		return "SH"
	// 北京：43/83/87/88/92 开头
	case strings.HasPrefix(code, "43"), strings.HasPrefix(code, "83"),
		strings.HasPrefix(code, "87"), strings.HasPrefix(code, "88"),
		strings.HasPrefix(code, "92"):
		return "BJ"
	// 深圳：00 主板、30 创业板、20 B股、15/16/18 ETF/LOF、39 指数
	default:
		return "SZ"
	}
}

// SplitSymbol 拆出交易所与代码：SH600519 -> (SH, 600519)。
func SplitSymbol(symbol string) (exchange, code string) {
	if len(symbol) == 8 {
		return symbol[:2], symbol[2:]
	}
	return "", symbol
}

// SecIDForEastmoney 东财 secid：SH->1.xxxxxx，SZ/BJ->0.xxxxxx。
func SecIDForEastmoney(symbol string) string {
	ex, code := SplitSymbol(symbol)
	switch ex {
	case "SH":
		return "1." + code
	default: // SZ/BJ 均为 0.
		return "0." + code
	}
}

// TencentSymbol 腾讯格式：sh600519 / sz000001 / bj920748。
func TencentSymbol(symbol string) string {
	ex, code := SplitSymbol(symbol)
	return strings.ToLower(ex) + code
}

// SinaSymbol 新浪格式：与腾讯一致。
func SinaSymbol(symbol string) string {
	return TencentSymbol(symbol)
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
