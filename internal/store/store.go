// Package store 封装 MySQL 访问与建表迁移。
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Store 数据库访问层。
type Store struct {
	DB *sql.DB
}

// Open 建立连接池（低内存配置）并执行迁移。
func Open(dsn string) (*Store, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}
	// 2核4G 服务器 + 远程库：连接数保守
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(30 * time.Minute)
	db.SetConnMaxIdleTime(5 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("数据库连接失败: %w", err)
	}

	s := &Store{DB: db}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	if err := s.SeedIndicatorCatalog(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// migrate 按文件名顺序执行尚未记录的迁移。迁移记录独立保存，避免 ALTER TABLE
// 在每次服务启动时重复执行。
func (s *Store) migrate() error {
	if _, err := s.DB.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version VARCHAR(128) NOT NULL,
		applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (version)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci`); err != nil {
		return fmt.Errorf("创建迁移记录表失败: %w", err)
	}
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("读取迁移目录失败: %w", err)
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		var applied string
		err := s.DB.QueryRow("SELECT version FROM schema_migrations WHERE version=?", name).Scan(&applied)
		if err == nil {
			continue
		}
		if err != sql.ErrNoRows {
			return fmt.Errorf("查询迁移记录 %s 失败: %w", name, err)
		}
		raw, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		for _, stmt := range splitSQL(string(raw)) {
			if _, err := s.DB.Exec(stmt); err != nil {
				return fmt.Errorf("迁移 %s 执行失败: %w\nSQL: %.120s", name, err, stmt)
			}
		}
		if _, err := s.DB.Exec("INSERT INTO schema_migrations (version) VALUES (?)", name); err != nil {
			return fmt.Errorf("记录迁移 %s 失败: %w", name, err)
		}
		slog.Info("迁移完成", "file", name)
	}
	return nil
}

// splitSQL 以分号拆分语句，忽略注释行与空语句。
func splitSQL(raw string) []string {
	var out []string
	var b strings.Builder
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "--") {
			continue
		}
		b.WriteString(line)
		b.WriteString("\n")
		if strings.HasSuffix(trimmed, ";") {
			stmt := strings.TrimSpace(b.String())
			stmt = strings.TrimSuffix(stmt, ";")
			if stmt != "" {
				out = append(out, stmt)
			}
			b.Reset()
		}
	}
	if rest := strings.TrimSpace(b.String()); rest != "" {
		out = append(out, rest)
	}
	return out
}

// Close 关闭连接池。
func (s *Store) Close() error { return s.DB.Close() }
