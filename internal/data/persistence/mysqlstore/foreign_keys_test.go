package mysqlstore

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestDeploymentSQLDoesNotCreateForeignKeys 防止建表或后续升级重新引入外键与关闭约束检查的操作。
func TestDeploymentSQLDoesNotCreateForeignKeys(t *testing.T) {
	files, err := filepath.Glob("../../../../sql/*.sql")
	if err != nil || len(files) == 0 {
		t.Fatalf("读取部署 SQL 列表失败：%v", err)
	}
	foreignKey := regexp.MustCompile(`(?i)\bFOREIGN\s+KEY\s*\(`)
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if foreignKey.Match(data) || strings.Contains(strings.ToUpper(string(data)), "FOREIGN_KEY_CHECKS") {
			t.Errorf("%s 不得创建外键或切换外键检查", filepath.Base(path))
		}
	}
}
