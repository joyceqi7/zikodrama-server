package operations_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"drama-server/internal/biz/monetization"
	"drama-server/internal/biz/operations"
	"drama-server/internal/testsupport/memory"
)

type applicationStrategyFailure struct {
	*memory.Store
	err error
}

// GetApplicationStrategy 注入读取错误，验证业务层记录实际依赖故障。
func (r applicationStrategyFailure) GetApplicationStrategy(context.Context) (monetization.ApplicationStrategy, error) {
	return monetization.ApplicationStrategy{}, r.err
}

// SaveApplicationStrategy 注入保存结果，验证错误传播、分级与成功不误报。
func (r applicationStrategyFailure) SaveApplicationStrategy(context.Context, monetization.ApplicationStrategyInput) (monetization.ApplicationStrategy, error) {
	return monetization.ApplicationStrategy{}, r.err
}

// TestApplicationStrategyLogs 保留操作与实际错误，不记录商品输入。
func TestApplicationStrategyLogs(t *testing.T) {
	original := slog.Default()
	defer slog.SetDefault(original)
	for _, failure := range []error{errors.New("strategy storage unavailable"), operations.ErrConflict, monetization.ErrApplicationModeConflict, nil} {
		for _, operation := range []string{"get", "save"} {
			var output bytes.Buffer
			slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
			repo := applicationStrategyFailure{Store: memory.NewStore(memory.Seed{}), err: failure}
			service := operations.NewMonetizationService(repo, repo)
			var err error
			if operation == "get" {
				_, err = service.ApplicationStrategy(t.Context())
			} else {
				_, err = service.SaveApplicationStrategy(t.Context(), monetization.ApplicationStrategyInput{Rules: map[string]monetization.RuleBinding{"paid": {ProductID: "private-product"}}})
			}
			if !errors.Is(err, failure) {
				t.Fatal("错误传播改变", err)
			}
			if failure == nil {
				if output.Len() != 0 {
					t.Fatal("成功误报错误")
				}
				continue
			}
			log := output.String()
			if !strings.Contains(log, operation+" application strategy failed") || !strings.Contains(log, failure.Error()) || strings.Contains(log, "private-") {
				t.Fatal("日志缺少诊断信息或泄漏输入", log)
			}
			level := "ERROR"
			if operation == "save" && (errors.Is(failure, operations.ErrConflict) || errors.Is(failure, monetization.ErrApplicationModeConflict)) {
				level = "WARN"
			}
			if !strings.Contains(log, `"level":"`+level+`"`) {
				t.Fatal("日志级别错误", log)
			}
		}
	}
}
