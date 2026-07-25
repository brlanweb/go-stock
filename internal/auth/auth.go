// Package auth 提供可选的页面访问密码保护。
// 启用后，除放行清单外的页面与查询接口需要先登录换取签名 Cookie。
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	cookieName = "gostock_access"
	ttl        = 7 * 24 * time.Hour
)

// Guard 密码保护中间件工厂。password 为空时完全放行（不启用）。
type Guard struct {
	password string
	secret   []byte
}

// New 创建 Guard。secret 用于签名，基于密码派生，重启后仍可校验已发放 Cookie。
func New(password string) *Guard {
	sum := sha256.Sum256([]byte("gostock-access-secret:" + password))
	return &Guard{password: password, secret: sum[:]}
}

// Enabled 是否启用了密码保护。
func (g *Guard) Enabled() bool { return g.password != "" }

func (g *Guard) sign(expires int64) string {
	msg := strconv.FormatInt(expires, 10)
	mac := hmac.New(sha256.New, g.secret)
	mac.Write([]byte(msg))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return msg + "." + sig
}

func (g *Guard) valid(token string) bool {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return false
	}
	expires, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || time.Now().Unix() > expires {
		return false
	}
	expected := g.sign(expires)
	return subtle.ConstantTimeCompare([]byte(token), []byte(expected)) == 1
}

func (g *Guard) authorized(r *http.Request) bool {
	if c, err := r.Cookie(cookieName); err == nil && g.valid(c.Value) {
		return true
	}
	// 也支持 Header，便于脚本访问
	if h := r.Header.Get("X-Access-Token"); h != "" && g.valid(h) {
		return true
	}
	return false
}

// bypass 放行清单：MCP 自带鉴权、健康检查、登录相关接口、以及登录页所需的前端静态资源。
func bypass(r *http.Request) bool {
	p := r.URL.Path
	switch {
	case p == "/api/v1/health":
		return true
	case p == "/api/v1/auth/login" || p == "/api/v1/auth/status":
		return true
	case p == "/mcp" || strings.HasPrefix(p, "/mcp/"):
		return true
	// 前端资源与 SPA 入口需可加载，登录判定在前端发起 /auth/status 后处理
	case p == "/" || p == "/index.html" || p == "/favicon.ico":
		return true
	case strings.HasPrefix(p, "/assets/"):
		return true
	}
	return false
}

// Wrap 包裹处理器。未启用时原样返回。
func (g *Guard) Wrap(next http.Handler) http.Handler {
	if !g.Enabled() {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if bypass(r) || g.authorized(r) {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "需要访问密码"})
	})
}

// RegisterRoutes 注册登录与状态接口。
func (g *Guard) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/auth/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{
			"required":      g.Enabled(),
			"authenticated": !g.Enabled() || g.authorized(r),
		})
	})
	mux.HandleFunc("POST /api/v1/auth/login", func(w http.ResponseWriter, r *http.Request) {
		if !g.Enabled() {
			writeJSON(w, http.StatusOK, map[string]bool{"authenticated": true})
			return
		}
		var body struct {
			Password string `json:"password"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
			return
		}
		if subtle.ConstantTimeCompare([]byte(body.Password), []byte(g.password)) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "密码错误"})
			return
		}
		expires := time.Now().Add(ttl).Unix()
		token := g.sign(expires)
		http.SetCookie(w, &http.Cookie{
			Name:     cookieName,
			Value:    token,
			Path:     "/",
			Expires:  time.Unix(expires, 0),
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
		writeJSON(w, http.StatusOK, map[string]interface{}{"authenticated": true, "token": token})
	})
	mux.HandleFunc("POST /api/v1/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
		writeJSON(w, http.StatusOK, map[string]bool{"authenticated": false})
	})
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
