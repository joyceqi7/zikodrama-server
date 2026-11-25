package mysqlstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBusinessRepositoriesUseGORM 防止业务仓储重新引入“GORM 外面再包装一层
// database/sql”的伪 ORM 写法。Data 可以持有 GORM 暴露的 *sql.DB 做 Ping，但业务
// 文件的查询、事务和更新必须使用 Model、Where、Clauses 和 Transaction 等 GORM API。
func TestBusinessRepositoriesUseGORM(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") {
			continue
		}
		contents, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		source := string(contents)
		for _, forbidden := range []string{
			".Raw(", ".Exec(", "QueryContext(", "QueryRowContext(", "ExecContext(",
			"BeginTx(", "sqlDatabase", "sqlTransaction",
		} {
			// MySQL 的 CREATE TABLE LIKE 没有 Model API，仅日期分表 DDL 适配器可用 GORM Exec；业务查询和迁移 DML 仍受约束。
			if name == "metric_partition_ddl.go" && forbidden == ".Exec(" {
				continue
			}
			if strings.Contains(source, forbidden) {
				t.Errorf("%s 不得包含 %q，业务数据库访问必须使用 GORM", name, forbidden)
			}
		}
		if name != "store.go" && strings.Contains(source, `"database/sql"`) {
			t.Errorf("%s 不得直接依赖 database/sql", name)
		}
	}
}
