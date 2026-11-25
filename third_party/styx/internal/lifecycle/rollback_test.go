package lifecycle

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRollbackUsesFreshContextAndKeepsErrorsSafe(t *testing.T) {
	cause := errors.New("create failed")
	err := Rollback("database", func(ctx context.Context) error {
		if ctx == nil || ctx.Err() != nil {
			t.Fatal("回滚必须使用可用的独立 Context")
		}
		return errors.New("credential-secret")
	}, cause)
	if !errors.Is(err, cause) {
		t.Fatal("回滚结果必须保留原始创建错误")
	}
	if strings.Contains(err.Error(), "credential-secret") {
		t.Fatalf("回滚结果泄露底层错误：%q", err.Error())
	}
}
