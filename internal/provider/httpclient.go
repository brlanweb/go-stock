package provider

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// 共享 HTTP 客户端：连接复用（keep-alive）降低握手开销与被封概率。
var httpClient = &http.Client{
	Timeout: 10 * time.Second,
	Transport: &http.Transport{
		MaxIdleConns:        16,
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     60 * time.Second,
		ForceAttemptHTTP2:   true,
	},
}

const browserUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"

// httpGet 发起 GET 请求并返回 body 字节。
func httpGet(ctx context.Context, url string, headers map[string]string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Accept", "*/*")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, req.URL.Host)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20)) // 32MB 上限
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("空响应 from %s（可能被限流）", req.URL.Host)
	}
	return body, nil
}
