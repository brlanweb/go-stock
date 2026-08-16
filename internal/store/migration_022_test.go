package store

import (
	"strings"
	"testing"
)

// 022 与 021 面临同一约束：迁移执行器逐条执行且不包事务，中途失败不会写入
// schema_migrations，因此整文件必须可安全重复执行。
func TestPositionDataQualityMigrationIsIdempotent(t *testing.T) {
	raw, err := migrationFS.ReadFile("migrations/022_position_data_quality.sql")
	if err != nil {
		t.Fatal(err)
	}
	sqlText := string(raw)

	if !strings.Contains(sqlText, "COLUMN_NAME = 'data_quality'") {
		t.Fatal("data_quality 列缺少 information_schema 幂等守卫")
	}
	if !strings.Contains(sqlText, "INDEX_NAME = 'idx_data_quality'") {
		t.Fatal("idx_data_quality 索引缺少 information_schema 幂等守卫，重复执行会因索引已存在而失败")
	}
	// 回填必须限定 data_quality 为空，否则重复执行会覆盖后续的人工修正。
	if !strings.Contains(sqlText, "data_quality = ''") {
		t.Fatal("回填语句必须限定 data_quality 为空")
	}
	if !strings.Contains(sqlText, "entry_date = exit_date") {
		t.Fatal("回填条件必须锁定当日进出样本")
	}

	statements := splitSQL(sqlText)
	var prepare, execute, deallocate int
	for _, stmt := range statements {
		switch {
		case strings.HasPrefix(stmt, "PREPARE "):
			prepare++
		case strings.HasPrefix(stmt, "EXECUTE "):
			execute++
		case strings.HasPrefix(stmt, "DEALLOCATE "):
			deallocate++
		}
		if strings.Count(stmt, ";") > 0 {
			t.Fatalf("拆分后的语句不应再包含分号: %.80s", stmt)
		}
	}
	if prepare != 2 || execute != 2 || deallocate != 2 {
		t.Fatalf("PREPARE/EXECUTE/DEALLOCATE 应各 2 条，got %d/%d/%d", prepare, execute, deallocate)
	}
}
