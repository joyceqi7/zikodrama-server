package operations

import (
	"drama-server/internal/biz/application"
	biz "drama-server/internal/biz/operations"
	"errors"
	"testing"
)

// TestApplicationPlaybackValidation 验证入口只接收播放模式，直连不再要求应用级引用，旧更新请求保留原通道。
func TestApplicationPlaybackValidation(t *testing.T) {
	base := biz.ApplicationInput{SubjectID: "1", Name: "播放配置测试", TikTokAppID: "000123", ClientKey: "test-client", ClientSecret: "test-secret", MonetizationMode: application.MonetizationModeIAP, Status: application.StatusDisabled}
	for _, test := range []struct {
		name, provider, wantProvider string
		creating, invalid            bool
	}{
		{name: "旧创建默认官方", creating: true, wantProvider: "tiktok_minis"},
		{name: "旧更新保留通道"},
		{name: "直连创建无需引用", creating: true, provider: " byteplus_web ", wantProvider: "byteplus_web"},
		{name: "直连完整更新无需引用", provider: "byteplus_web", wantProvider: "byteplus_web"},
		{name: "未知创建通道拒绝", creating: true, provider: "custom", invalid: true},
		{name: "未知更新通道拒绝", provider: "custom", invalid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := base
			input.PlaybackProvider = test.provider
			normalize := normalizeApplicationInput
			if test.creating {
				normalize = normalizeCreateApplicationInput
			}
			got, err := normalize(input)
			if test.invalid {
				if !errors.Is(err, biz.ErrInvalidInput) {
					t.Fatalf("应拒绝无效播放模式：%v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.PlaybackProvider != test.wantProvider {
				t.Fatalf("播放模式丢失：%q", got.PlaybackProvider)
			}
		})
	}
}
