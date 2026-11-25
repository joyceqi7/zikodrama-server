package mysqlstore

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// readDeploymentSchema 按实际部署顺序读取冻结的初始化基线及所有有序增量。
// 路径从当前测试源文件计算，不依赖执行 go test 时的工作目录。
func readDeploymentSchema(t *testing.T) []byte {
	t.Helper()
	parts := [][]byte{readDeploymentSQL(t, "init.sql")}
	files, err := filepath.Glob(filepath.Join(deploymentSQLDirectory(t), "migrations", "*.sql"))
	if err != nil {
		t.Fatalf("读取增量脚本清单: %v", err)
	}
	for _, file := range files {
		parts = append(parts, readDeploymentSQL(t, filepath.Join("migrations", filepath.Base(file))))
	}
	return bytes.Join(parts, []byte("\n"))
}

// deploymentSQLDirectory 返回数据库初始化脚本目录，不依赖测试进程的工作目录。
func deploymentSQLDirectory(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("无法定位 schema_test.go")
	}
	return filepath.Join(filepath.Dir(filename), "..", "..", "..", "..", "sql")
}

// readSuperAdminInitializationSQL 从统一初始化脚本中提取可选管理员段落。
func readSuperAdminInitializationSQL(t *testing.T) []byte {
	t.Helper()
	contents := readDeploymentSchema(t)
	const startMarker = "-- 可选超级管理员初始化开始。"
	const endMarker = "-- 可选超级管理员初始化结束。"
	start := bytes.Index(contents, []byte(startMarker))
	end := bytes.Index(contents, []byte(endMarker))
	if start < 0 || end < start {
		t.Fatal("统一初始化脚本缺少超级管理员初始化段落")
	}
	return contents[start : end+len(endMarker)]
}

// readDeploymentTableSQL 从统一初始化脚本提取一条建表语句，供隔离测试重建单表。
func readDeploymentTableSQL(t *testing.T, table string) string {
	t.Helper()
	schema := string(readDeploymentSchema(t))
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

// readDeploymentSQL 读取 sql 下的部署 SQL，不依赖 go test 的工作目录。
func readDeploymentSQL(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join(deploymentSQLDirectory(t), name)
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取部署 Schema %s: %v", path, err)
	}
	return contents
}
