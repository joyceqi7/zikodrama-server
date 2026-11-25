package operations

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/directplay"
	biz "drama-server/internal/biz/operations"
	"drama-server/internal/testsupport/memory"
)

// TestApplicationPlaybackSwitchValidation 验证不依赖旧播放权限，仍拒绝过期修订及不可用服务，诊断不得泄漏凭据。
func TestApplicationPlaybackSwitchValidation(t *testing.T) {
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })
	for _, test := range []struct {
		name, message string
		ready         bool
		revision      int64
		status        int
	}{
		{"正常切换成功", "", true, 0, 200},
		{"服务不可用拒绝", "direct playback deployment unavailable", false, 0, 503},
		{"过期修订拒绝", "update application playback revision rejected", true, 1, 409},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := memory.NewStore(memory.Seed{})
			app := application.Application{SubjectID: "1", ID: "701", Name: "切换测试", TikTokAppID: "000123", ClientKey: "fixture-client", Status: "disabled", MonetizationMode: "IAP"}
			if err := store.SaveApplication(t.Context(), app); err != nil {
				t.Fatal(err)
			}
			h := &Handler{service: biz.NewService(memory.OperationsRepositories(store), nil)}
			if test.ready {
				h.config.Direct = directplay.NewService(nil, nil)
			}
			input := biz.ApplicationInput{SubjectID: "1", Name: app.Name, TikTokAppID: app.TikTokAppID, ClientKey: app.ClientKey, ClientSecret: "private-fixture-secret", Status: "disabled", MonetizationMode: "IAP", PlaybackProvider: "byteplus_web", PlaybackRevision: &test.revision}
			body, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest(http.MethodPut, "/admin/v1/applications/701", bytes.NewReader(body))
			r.SetPathValue("applicationID", "701")
			r.Header.Set("Authorization", "Bearer private-fixture-token")
			var logs bytes.Buffer
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			w := httptest.NewRecorder()
			h.updateApplication(w, r)
			if w.Code != test.status {
				t.Fatalf("状态错误：%d", w.Code)
			}
			persisted, err := store.FindApplication(t.Context(), app.ID)
			if err != nil {
				t.Fatal(err)
			}
			if test.status != 200 && persisted.Normalized().PlaybackProvider != "tiktok_minis" {
				t.Fatal("拒绝后应用被修改")
			}
			if test.status == 200 && (persisted.PlaybackProvider != "byteplus_web" || persisted.PlaybackRevision != 1) {
				t.Fatal("获准切换未保存通道和修订")
			}
			wantError := biz.ErrConflict
			if !test.ready {
				wantError = biz.ErrPublicationUnavailable
			}
			if test.message != "" && (!strings.Contains(logs.String(), test.message) || !strings.Contains(logs.String(), wantError.Error())) {
				t.Fatal("权限拒绝缺少具体错误诊断")
			}
			if strings.Contains(logs.String(), input.ClientSecret) || strings.Contains(logs.String(), "private-fixture-token") || strings.Contains(logs.String(), `"level":"ERROR"`) {
				t.Fatal("日志泄漏凭据或正常流程误报故障")
			}
		})
	}
}

// TestApplicationPlaybackRevisionValidation 验证历史修订 0 可用于第一次切换，负值和不安全整数在 Handler 入口拒绝。
func TestApplicationPlaybackRevisionValidation(t *testing.T) {
	for _, revision := range []int64{-1, 0, 1, 9007199254740991} {
		input := biz.ApplicationInput{SubjectID: "1", Name: "修订测试", TikTokAppID: "000123", ClientKey: "client", Status: "disabled", MonetizationMode: "IAP", PlaybackRevision: &revision}
		_, err := normalizeApplicationInput(input)
		if (err != nil) != (revision < 0 || revision >= 9007199254740991) {
			t.Fatalf("修订 %d 校验错误：%v", revision, err)
		}
	}
}
