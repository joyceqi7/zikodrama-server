package operations_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/directplay"
	"drama-server/internal/biz/operations"
	"drama-server/internal/testsupport/memory"
)

// TestApplicationPlaybackRejectionLogs 验证业务拒绝不依赖 Handler 打印错误，保持错误传播、正常路径及凭据隔离。
func TestApplicationPlaybackRejectionLogs(t *testing.T) {
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })
	for _, test := range []struct {
		name, operation, provider, message string
		want                               error
	}{
		{"创建拒绝未知通道", "create", "custom", "create application playback provider rejected", operations.ErrInvalidInput},
		{"更新拒绝未知通道", "update", "custom", "update application playback provider rejected", operations.ErrInvalidInput},
		{"更新拒绝缺少切换修订", "update", "byteplus_web", "update application playback revision rejected", operations.ErrConflict},
		{"创建成功不误报", "create", "tiktok_minis", "", nil},
		{"更新成功不误报", "update", "tiktok_minis", "", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := memory.NewStore(memory.Seed{})
			existing := application.Application{SubjectID: "1", ID: "701", Name: "播放测试", TikTokAppID: "playback-test", ClientKey: "playback-client", RegionCode: "GLOBAL", TimeZone: "UTC", MonetizationMode: application.MonetizationModeIAP, Status: application.StatusDisabled, PlaybackProvider: "tiktok_minis"}
			if err := store.SaveApplication(t.Context(), existing); err != nil {
				t.Fatal(err)
			}
			service := operations.NewApplicationService(store, nil, store)
			input := operations.ApplicationInput{SubjectID: "1", Name: existing.Name, TikTokAppID: existing.TikTokAppID, ClientKey: existing.ClientKey, RegionCode: existing.RegionCode, TimeZone: existing.TimeZone, Status: existing.Status, MonetizationMode: existing.MonetizationMode, ClientSecret: "test-private-secret", PlaybackProvider: test.provider}
			var output bytes.Buffer
			slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
			var err error
			if test.operation == "create" {
				input.TikTokAppID, input.ClientKey = "create-playback-test", "create-playback-client"
				_, err = service.CreateApplication(t.Context(), input)
			} else {
				_, err = service.UpdateApplication(t.Context(), existing.ID, input)
			}
			if !errors.Is(err, test.want) {
				t.Fatalf("业务结果发生变化：%v", err)
			}
			if test.want == nil {
				if output.Len() != 0 {
					t.Fatal("成功路径误报错误日志")
				}
				return
			}
			raw := output.String()
			if !strings.Contains(raw, test.message) || !strings.Contains(raw, test.want.Error()) || !strings.Contains(raw, `"level":"WARN"`) {
				t.Fatal("拒绝日志缺少操作、实际错误或正确级别")
			}
			if strings.Contains(raw, input.ClientSecret) || strings.Contains(raw, "private-profile") {
				t.Fatal("业务拒绝日志泄漏敏感输入")
			}
		})
	}
}

// playbackSaveFailureRepository 仅替换写入端口，验证事务拒绝和依赖故障都保留实际错误详情。
type playbackSaveFailureRepository struct {
	operations.ApplicationRepository
	err error
}

// SaveApplication 返回注入的写入错误，不修改底层应用，以验证失败不会伪装成成功。
func (r playbackSaveFailureRepository) SaveApplication(context.Context, application.Application) error {
	return r.err
}

// TestApplicationPlaybackSaveFailureLogs 验证默认播放配置拒绝、并发冲突和存储故障的日志级别、错误传播及凭据隔离。
func TestApplicationPlaybackSaveFailureLogs(t *testing.T) {
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })
	for _, test := range []struct {
		name, message, level string
		err                  error
	}{
		{"默认配置拒绝", "save application playback profile rejected", "WARN", directplay.ErrInvalid},
		{"并发冲突", "save application failed", "WARN", operations.ErrConflict},
		{"存储故障", "save application failed", "ERROR", errors.New("fixture database write failed")},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := memory.NewStore(memory.Seed{})
			app := application.Application{SubjectID: "1", ID: "701", Name: "播放测试", TikTokAppID: "playback-test", ClientKey: "playback-client", Status: application.StatusDisabled, MonetizationMode: application.MonetizationModeIAP}
			if err := store.SaveApplication(t.Context(), app); err != nil {
				t.Fatal(err)
			}
			service := operations.NewApplicationService(playbackSaveFailureRepository{ApplicationRepository: store, err: test.err}, nil, store)
			revision := int64(0)
			input := operations.ApplicationInput{SubjectID: "1", Name: app.Name, TikTokAppID: app.TikTokAppID, ClientKey: app.ClientKey, Status: app.Status, MonetizationMode: app.MonetizationMode, ClientSecret: "private-fixture-secret", PlaybackProvider: "byteplus_web", PlaybackRevision: &revision}
			var logs bytes.Buffer
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			result, err := service.UpdateApplication(t.Context(), app.ID, input)
			if !errors.Is(err, test.err) || result != nil {
				t.Fatal("写入失败改变了原错误或返回成功结果")
			}
			persisted, err := store.FindApplication(t.Context(), app.ID)
			if err != nil || persisted.Normalized().PlaybackProvider != "tiktok_minis" || persisted.PlaybackRevision != 0 {
				t.Fatal("失败后原配置发生变化")
			}
			raw := logs.String()
			if strings.Count(strings.TrimSpace(raw), "\n") != 0 || !strings.Contains(raw, test.message) || !strings.Contains(raw, test.err.Error()) || !strings.Contains(raw, `"level":"`+test.level+`"`) {
				t.Fatal("写入失败日志缺少操作、实际错误、正确级别或重复记录")
			}
			if strings.Contains(raw, input.ClientSecret) {
				t.Fatal("写入失败日志泄漏凭据或完整配置")
			}
		})
	}
}
