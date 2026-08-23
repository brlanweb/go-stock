package store

import "strings"

// 同步错误分类。区分「上游临时故障」与「结构性不可得」，决定失败断点能否自动重试。
//
// 背景：failed 曾是终态，一次网络抖动就让证券数据永久停更（2026-08 有 6 只正常
// 股票因此缺 2~9 个交易日，被静默排除在推荐候选池外）。但也不能无脑重试——像
// 定向可转债那样上游根本不提供日 K 的标的，重试只会每天空撞上游。

// transientErrorPatterns 是可自动重试的临时故障签名：网络层错误与上游自身故障。
// 命中即认为「换个时间重试大概率能成」。
var transientErrorPatterns = []string{
	// Go http 客户端错误统一以 Get "http... 开头
	`Get "http`,
	`Post "http`,
	"timeout",
	"Timeout",
	"deadline exceeded",
	"connection reset",
	"connection refused",
	"no such host",
	"EOF",
	"i/o timeout",
	"TLS handshake",
	"temporary failure",
	// 上游自身后端故障，例如腾讯行情返回 code=11 mysql connect failed
	"mysql connect failed",
	"502 Bad Gateway",
	"503 Service",
	"504 Gateway",
	"too many requests",
	"限流",
}

// noDataErrorPatterns 是「上游明确没有该标的数据」的签名。这类错误重试无意义，
// 且是判定长期停牌/退市的依据之一（见 MarkDelistedByStaleFailure）。
var noDataErrorPatterns = []string{
	"No value to decode",
	"no data",
	"empty response",
}

// IsTransientSyncError 判断错误是否值得自动重试。
// 采用白名单：只有明确识别为临时故障才重试，未知错误一律保守视为不可重试，
// 避免结构性问题（如上游不支持该证券类型）演变成每日空转。
func IsTransientSyncError(msg string) bool {
	if msg == "" {
		return false
	}
	// 先排除明确无数据的情况：这类可能同时包含 http 字样，但重试没有意义。
	for _, pattern := range noDataErrorPatterns {
		if strings.Contains(msg, pattern) {
			return false
		}
	}
	for _, pattern := range transientErrorPatterns {
		if strings.Contains(msg, pattern) {
			return true
		}
	}
	return false
}
