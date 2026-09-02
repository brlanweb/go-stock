package mcpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// okHandler 记录是否放行到下游。
type okHandler struct{ called bool }

func (h *okHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	h.called = true
	w.WriteHeader(http.StatusOK)
}

func TestAuthMiddlewareRejectsBadToken(t *testing.T) {
	cases := []struct {
		name   string
		header string
	}{
		{"缺失Authorization", ""},
		{"Token错误", "Bearer wrong-token"},
		{"缺少Bearer前缀", "secret"},
		{"仅前缀匹配", "Bearer secret-extra"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			next := &okHandler{}
			h := authMiddleware(next, "secret")
			req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("期望 401，实际 %d", rec.Code)
			}
			if next.called {
				t.Fatal("鉴权失败时不应放行到下游")
			}
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				t.Fatalf("401 响应必须声明 application/json，实际 %q", ct)
			}
			if rec.Header().Get("WWW-Authenticate") == "" {
				t.Fatal("401 响应应包含 WWW-Authenticate 头")
			}
			// 客户端需要能解析出结构化错误，而不是拿到无法解析的纯文本
			var body struct {
				JSONRPC string `json:"jsonrpc"`
				Error   struct {
					Code    int    `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("401 响应体必须是合法 JSON: %v", err)
			}
			if body.JSONRPC != "2.0" || body.Error.Code == 0 || body.Error.Message == "" {
				t.Fatalf("401 响应体缺少 JSON-RPC 错误信息: %s", rec.Body.String())
			}
		})
	}
}

func TestAuthMiddlewareAcceptsValidToken(t *testing.T) {
	next := &okHandler{}
	h := authMiddleware(next, "secret")
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("期望 200，实际 %d", rec.Code)
	}
	if !next.called {
		t.Fatal("Token 正确时应放行到下游")
	}
}

func TestNewHandlerWithoutTokenSkipsAuth(t *testing.T) {
	// token 为空表示不鉴权，此时不应返回 401。
	// 必须用 POST：GET 会打开 SSE 长连接并阻塞。
	h := NewHandler(Deps{}, "")
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code == http.StatusUnauthorized {
		t.Fatal("未配置 Token 时不应触发鉴权")
	}
}

func TestNewHandlerWithTokenRejectsUnauthenticated(t *testing.T) {
	h := NewHandler(Deps{}, "secret")
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("配置 Token 后未鉴权请求应返回 401，实际 %d", rec.Code)
	}
}
