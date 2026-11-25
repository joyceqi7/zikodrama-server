package data

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// readInitTableSQL 从统一初始化脚本中提取指定表的建表语句，供隔离迁移测试准备目标表。
func readInitTableSQL(t *testing.T, table string) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("无法定位 init_sql_test.go")
	}
	path := filepath.Join(filepath.Dir(filename), "..", "..", "sql", "init.sql")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取统一初始化脚本 %s: %v", path, err)
	}
	schema := string(contents)
	marker := "CREATE TABLE `" + table + "` ("
	start := strings.Index(schema, marker)
	if start < 0 {
		t.Fatalf("统一初始化脚本缺少表 %s", table)
	}
	end := strings.Index(schema[start:], ";")
	if end < 0 {
		t.Fatalf("表 %s 的建表语句未结束", table)
	}
	return schema[start : start+end+1]
}
