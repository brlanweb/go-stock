// Package web 前端静态资源 embed（Vue3 构建产物 dist/）。
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

// Handler 返回 SPA 静态服务 handler（未命中路径回退 index.html）。
func Handler() http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		// dist 未构建时兜底提示
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "前端未构建：请在 web/ 目录执行 npm run build", http.StatusNotFound)
		})
	}
	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path != "" {
			if f, err := sub.Open(path); err == nil {
				f.Close()
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		// SPA 回退
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})
}
