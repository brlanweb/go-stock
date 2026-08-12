// Package config 负责加载环境变量配置（支持 .env 文件）。
package config

import (
	"bufio"
	_ "embed"
	"fmt"
	"os"
	"strconv"
	"strings"
)

//go:embed ai_prompt.md
var embeddedAIPrompt string

//go:embed review_prompt.md
var embeddedReviewPrompt string

// Config 全局配置。
type Config struct {
	Addr string // HTTP 监听地址

	DBHost     string
	DBPort     int
	DBName     string
	DBUser     string
	DBPassword string

	MCPToken string // MCP Bearer Token，空表示不鉴权

	AccessPassword string // 页面访问密码，空表示不启用登录

	BackfillWorkers   int     // 回填并发 worker 数
	BackfillQPS       float64 // 单源全局 QPS 上限
	SyncSectors       bool    // 启动回填时是否同步板块成分（默认关闭，避免额外上游压力）
	PythonCommand     string  // BaoStock/AKShare Python 解释器
	PythonKlineScript string  // Python 历史K线桥接脚本

	QuoteTTLSeconds      int // 实时行情缓存 TTL
	WatchlistSyncSeconds int // 自选股实时行情后台同步周期

	RedisAddr       string // 可选 Redis 查询缓存地址，空表示关闭
	RedisPassword   string
	RedisDB         int
	RedisTTLSeconds int

	AIBaseURL           string // OpenAI 兼容模型地址
	AIAPIKey            string // 模型密钥
	AIModel             string // 模型名称
	AIPrompt            string // 股票分析提示词（可由 AIPromptFile 覆盖）
	AIPromptFile        string // 提示词文件路径（优先级高于 AIPrompt）
	AIHotspotPrompt     string // 热点漏斗 AI 提示词
	AIHotspotPromptFile string // 热点漏斗提示词文件路径
	AIReviewPrompt      string // 每日收盘复盘 AI 提示词
	AIReviewPromptFile  string // 每日收盘复盘提示词文件路径

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
		Addr:                 getEnv("GOSTOCK_ADDR", ":8480"),
		DBHost:               getEnv("GOSTOCK_DB_HOST", "127.0.0.1"),
		DBPort:               getEnvInt("GOSTOCK_DB_PORT", 3306),
		DBName:               getEnv("GOSTOCK_DB_NAME", "stock"),
		DBUser:               getEnv("GOSTOCK_DB_USER", "stock"),
		DBPassword:           getEnv("GOSTOCK_DB_PASSWORD", ""),
		MCPToken:             getEnv("GOSTOCK_MCP_TOKEN", ""),
		AccessPassword:       getEnv("GOSTOCK_ACCESS_PASSWORD", ""),
		BackfillWorkers:      getEnvInt("GOSTOCK_BACKFILL_WORKERS", 1),
		BackfillQPS:          getEnvFloat("GOSTOCK_BACKFILL_QPS", 0.35),
		SyncSectors:          getEnvBool("GOSTOCK_SYNC_SECTORS", false),
		PythonCommand:        getEnv("GOSTOCK_PYTHON_COMMAND", "python3"),
		PythonKlineScript:    getEnv("GOSTOCK_PYTHON_KLINE_SCRIPT", "python-provider/fetch_kline.py"),
		QuoteTTLSeconds:      getEnvInt("GOSTOCK_QUOTE_TTL", 3),
		WatchlistSyncSeconds: getEnvInt("GOSTOCK_WATCHLIST_SYNC_SECONDS", 5),
		RedisAddr:            getEnv("GOSTOCK_REDIS_ADDR", ""),
		RedisPassword:        getEnv("GOSTOCK_REDIS_PASSWORD", ""),
		RedisDB:              getEnvInt("GOSTOCK_REDIS_DB", 0),
		RedisTTLSeconds:      getEnvInt("GOSTOCK_REDIS_TTL_SECONDS", 60),
		AIBaseURL:            getEnv("GOSTOCK_AI_BASE_URL", ""),
		AIAPIKey:             getEnv("GOSTOCK_AI_API_KEY", ""),
		AIModel:              getEnv("GOSTOCK_AI_MODEL", ""),
		AIPrompt:             getEnv("GOSTOCK_AI_PROMPT", ""),
		AIPromptFile:         getEnv("GOSTOCK_AI_PROMPT_FILE", "config/ai_prompt.md"),
		AIHotspotPrompt:      getEnv("GOSTOCK_AI_HOTSPOT_PROMPT", ""),
		AIHotspotPromptFile:  getEnv("GOSTOCK_AI_HOTSPOT_PROMPT_FILE", "config/hotspot_prompt.md"),
		AIReviewPrompt:       getEnv("GOSTOCK_AI_REVIEW_PROMPT", ""),
		AIReviewPromptFile:   getEnv("GOSTOCK_AI_REVIEW_PROMPT_FILE", "config/review_prompt.md"),
		LogLevel:             getEnv("GOSTOCK_LOG_LEVEL", "info"),
	}
	if c.AIPromptFile != "" {
		if data, err := os.ReadFile(c.AIPromptFile); err == nil {
			if text := strings.TrimSpace(string(data)); text != "" {
				c.AIPrompt = text
			}
		}
	}
	if c.AIHotspotPromptFile != "" {
		if data, err := os.ReadFile(c.AIHotspotPromptFile); err == nil {
			if text := strings.TrimSpace(string(data)); text != "" {
				c.AIHotspotPrompt = text
			}
		}
	}
	if c.AIReviewPromptFile != "" {
		if data, err := os.ReadFile(c.AIReviewPromptFile); err == nil {
			if text := strings.TrimSpace(string(data)); text != "" {
				c.AIReviewPrompt = text
			}
		}
	}
	// 外部文件与内联变量都为空时，使用内嵌的默认提示词，保证部署可用。
	if strings.TrimSpace(c.AIPrompt) == "" {
		c.AIPrompt = strings.TrimSpace(embeddedAIPrompt)
	}
	if strings.TrimSpace(c.AIReviewPrompt) == "" {
		c.AIReviewPrompt = strings.TrimSpace(embeddedReviewPrompt)
	}
	if c.DBPassword == "" {
		return nil, fmt.Errorf("GOSTOCK_DB_PASSWORD 未设置（请配置 .env 或环境变量）")
	}
	if c.BackfillWorkers < 1 {
		c.BackfillWorkers = 1
	}
	if c.BackfillQPS <= 0 {
		c.BackfillQPS = 0.35
	}
	if c.WatchlistSyncSeconds < 1 {
		c.WatchlistSyncSeconds = 5
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

func getEnvBool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}
