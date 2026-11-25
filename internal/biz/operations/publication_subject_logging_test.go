package operations

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/subject"
)

// TestPublicationSubjectOwnerLogging 验证未配发布方只记业务拒绝，存储故障保留实际错误，成功不记录失败日志。
func TestPublicationSubjectOwnerLogging(t *testing.T) {
	previous := slog.Default()
	t.Cleanup(func() { slog.SetDefault(previous) })
	for _, test := range []struct {
		name  string
		cause error
		level string
	}{{"未配置", subject.ErrNotConfigured, "WARN"}, {"仓储故障", errors.New("publisher storage unavailable"), "ERROR"}, {"成功", nil, ""}} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
			service := NewService(Repositories{Content: ContentPorts{OwnerApplication: func(context.Context, string) (application.Application, error) {
				return application.Application{ClientSecret: "private-owner-secret"}, test.cause
			}}}, nil)
			_, err := service.loadPublicationOwner(t.Context(), "subject-1")
			if !errors.Is(err, test.cause) {
				t.Fatal("发布方错误未原样传播", err)
			}
			if test.level == "" {
				if output.Len() != 0 {
					t.Fatal("成功解析误报日志")
				}
				return
			}
			if !strings.Contains(output.String(), `"level":"`+test.level+`"`) || !strings.Contains(output.String(), test.cause.Error()) {
				t.Fatal("日志级别或实际原因丢失")
			}
			if strings.Contains(output.String(), "private-owner-secret") {
				t.Fatal("错误日志泄露发布密钥")
			}
		})
	}
}

// TestBatchAuthorizationRejectsCrossSubject 验证业务入口拒绝错配主体，不读取其他主体的候选或调用平台。
func TestBatchAuthorizationRejectsCrossSubject(t *testing.T) {
	repo := &distributionReadRepo{}
	service := NewService(Repositories{Application: ApplicationPorts{Registry: distributionQueueApps{app: application.Application{ID: "101", SubjectID: "1", Status: "active", ClientKey: "fixture"}}}, Content: ContentPorts{Distributions: repo}}, nil)
	_, err := service.AuthorizeApplicationDramas(application.WithSubject(t.Context(), "2"), "101", []string{"201"})
	if !errors.Is(err, ErrConflict) || repo.listedAll {
		t.Fatal("跨主体批量入口读取了其他主体候选", err)
	}
}
