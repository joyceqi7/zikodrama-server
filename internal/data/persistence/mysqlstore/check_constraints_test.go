package mysqlstore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"drama-server/internal/biz/operations"
)

// TestDeploymentSQLDoesNotCreateChecks 防止初始化和升级重新引入数据库规则校验。
func TestDeploymentSQLDoesNotCreateChecks(t *testing.T) {
	files, err := filepath.Glob("../../../../sql/*.sql")
	if err != nil || len(files) == 0 {
		t.Fatalf("读取部署 SQL 失败：%v", err)
	}
	pattern := regexp.MustCompile(`(?i)\bCHECK\s*\(`)
	for _, path := range files {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if pattern.Match(body) {
			t.Errorf("%s 不得创建 CHECK 约束", filepath.Base(path))
		}
	}
}

// TestMediaJobRulesBeforeDatabase 验证非法任务在接触数据库前拒绝，避免依赖数据库 CHECK。
func TestMediaJobRulesBeforeDatabase(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	for _, job := range []operations.MediaJob{
		{Kind: "unknown"},
		{Kind: operations.MediaJobUpload, ProcessingStartedAt: now, ProcessingDeadlineAt: now},
		{Kind: operations.MediaJobCover, ProcessingStartedAt: now, ProcessingDeadlineAt: now.Add(-time.Second)},
		{Kind: operations.MediaJobUpload, ProcessingStartedAt: now, ProcessingDeadlineAt: now.Add(time.Nanosecond)},
	} {
		store := &Store{}
		for _, err := range []error{store.CreateMediaJob(context.Background(), &job), store.UpdateMediaJob(context.Background(), job), store.ConfirmMediaUpload(context.Background(), job)} {
			if !errors.Is(err, operations.ErrInvalidInput) {
				t.Fatalf("非法媒资任务未拒绝：%v", err)
			}
		}
	}
	for _, kind := range []string{operations.MediaJobUpload, operations.MediaJobCover, operations.MediaJobDelete} {
		for _, job := range []operations.MediaJob{{Kind: kind}, {Kind: kind, ProcessingStartedAt: now, ProcessingDeadlineAt: now.Add(time.Second)}} {
			if err := job.ValidateStorageRules(); err != nil {
				t.Fatalf("合法媒资任务被拒绝：%v", err)
			}
		}
	}
}
