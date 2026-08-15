package store

import (
	"strings"
	"testing"
)

// 迁移执行器逐条执行且不包事务，中途失败不会写入 schema_migrations。
// 因此 021 必须整文件可重复执行：任何 ADD COLUMN 都要先查 information_schema，
// 否则一次部分失败就会让服务永久无法启动。
func TestPositionRiskControlMigrationIsIdempotent(t *testing.T) {
	raw, err := migrationFS.ReadFile("migrations/021_position_risk_control.sql")
	if err != nil {
		t.Fatal(err)
	}
	sqlText := string(raw)

	if strings.Contains(sqlText, "ALTER TABLE position\n") {
		t.Fatal("不得直接使用裸 ALTER TABLE，重复执行会因列已存在而卡死启动")
	}
	for _, column := range []string{"highest_price", "lowest_price", "position_pct", "realized_pct", "exit_kind"} {
		guard := "COLUMN_NAME = '" + column + "'"
		if !strings.Contains(sqlText, guard) {
			t.Fatalf("列 %s 缺少 information_schema 幂等守卫", column)
		}
	}
	if !strings.Contains(sqlText, "CREATE TABLE IF NOT EXISTS position_reduction") {
		t.Fatal("position_reduction 必须使用 IF NOT EXISTS")
	}

	// 分割器按行尾分号切分，PREPARE/EXECUTE/DEALLOCATE 必须各自独立成句，
	// 否则会被合并成一条非法语句。
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
	if prepare != 5 || execute != 5 || deallocate != 5 {
		t.Fatalf("PREPARE/EXECUTE/DEALLOCATE 应各 5 条，got %d/%d/%d", prepare, execute, deallocate)
	}
	// 回填语句必须带条件，避免重复执行时覆盖已经推进过的极值与归因。
	if !strings.Contains(sqlText, "highest_price IS NULL") {
		t.Fatal("极值回填必须限定 highest_price IS NULL，否则会覆盖已推进的峰值")
	}
	if !strings.Contains(sqlText, "exit_kind = ''") {
		t.Fatal("归因回填必须限定 exit_kind 为空，否则会覆盖新写入的归因")
	}
}
