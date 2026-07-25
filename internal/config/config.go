// Package config 负责加载环境变量配置（支持 .env 文件）。
package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config 全局配置。
type Config struct {
	Addr string // HTTP 监听地址

	DBHost     string
	DBPort     int
	DBName     string
	DBUser     string
	DBPassword string

	MCPToken string // MCP Bearer Token，空表示不鉴权

	BackfillWorkers int     // 回填并发 worker 数
	BackfillQPS     float64 // 单源全局 QPS 上限

	QuoteTTLSeconds int // 实时行情缓存 TTL

	LogLevel string
}

// DSN 返回 MySQL 连接串。
func (c *Config) DSN() string {
	return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=true&loc=Asia%%2FShanghai&timeout=10s&readTimeout=30s&writeTimeout=30s",
		c.DBUser, c.DBPassword, c.DBHost, c.DBPort, c.DBName)
}

// Load 加载配置：先读 .env（如存在），环境变量优先。
func Load() (*Config, error) {
	loadDotEnv(".env")

	c := &Config{
		Addr:            getEnv("GOSTOCK_ADDR", ":8480"),
		DBHost:          getEnv("GOSTOCK_DB_HOST", "127.0.0.1"),
		DBPort:          getEnvInt("GOSTOCK_DB_PORT", 3306),
		DBName:          getEnv("GOSTOCK_DB_NAME", "stock"),
		DBUser:          getEnv("GOSTOCK_DB_USER", "stock"),
		DBPassword:      getEnv("GOSTOCK_DB_PASSWORD", ""),
		MCPToken:        getEnv("GOSTOCK_MCP_TOKEN", ""),
		BackfillWorkers: getEnvInt("GOSTOCK_BACKFILL_WORKERS", 2),
		BackfillQPS:     getEnvFloat("GOSTOCK_BACKFILL_QPS", 3),
		QuoteTTLSeconds: getEnvInt("GOSTOCK_QUOTE_TTL", 3),
		LogLevel:        getEnv("GOSTOCK_LOG_LEVEL", "info"),
	}
	if c.DBPassword == "" {
		return nil, fmt.Errorf("GOSTOCK_DB_PASSWORD 未设置（请配置 .env 或环境变量）")
	}
	if c.BackfillWorkers < 1 {
		c.BackfillWorkers = 1
	}
	if c.BackfillQPS <= 0 {
		c.BackfillQPS = 3
	}
	return c, nil
}

// loadDotEnv 极简 .env 解析：KEY=VALUE，# 注释，已存在的环境变量不覆盖。
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		v = strings.Trim(v, `"'`)
		if _, exists := os.LookupEnv(k); !exists {
			os.Setenv(k, v)
		}
	}
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getEnvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func getEnvFloat(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseFloat(v, 64); err == nil {
			return n
		}
	}
	return def
}
