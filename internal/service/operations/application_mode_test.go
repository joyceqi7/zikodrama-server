package operations

import (
	"errors"
	"testing"

	"drama-server/internal/biz/application"
	biz "drama-server/internal/biz/operations"
)

// TestApplicationModeValidation 验证枚举规范化以及创建、更新均要求明确模式。
func TestApplicationModeValidation(t *testing.T) {
	for _, value := range []string{"IAA", "IAP", "IAA+IAP"} {
		t.Run(value, func(t *testing.T) {
			mode := application.MonetizationMode(value)
			input, err := normalizeCreateApplicationInput(biz.ApplicationInput{SubjectID: "1", Name: "模式测试", TikTokAppID: "000123", ClientKey: "test-client", ClientSecret: "test-secret", MonetizationMode: mode})
			if err != nil || input.MonetizationMode != mode {
				t.Fatalf("合法模式被拒绝：%v", err)
			}
		})
	}
	mode := application.MonetizationMode(" iap ")
	normalized, err := normalizeCreateApplicationInput(biz.ApplicationInput{SubjectID: "1", Name: "模式测试", TikTokAppID: "000123", ClientKey: "test-client", ClientSecret: "test-secret", MonetizationMode: mode})
	if err != nil || normalized.MonetizationMode != application.MonetizationModeIAP {
		t.Fatal("未规范化模式", err)
	}
	for _, value := range []string{"", "IAA + IAP", "other"} {
		mode := application.MonetizationMode(value)
		_, err := normalizeCreateApplicationInput(biz.ApplicationInput{SubjectID: "1", Name: "模式测试", TikTokAppID: "000123", ClientKey: "test-client", ClientSecret: "test-secret", MonetizationMode: mode})
		if !errors.Is(err, biz.ErrInvalidInput) {
			t.Fatal("未拒绝无效枚举", value, err)
		}
	}
	input := biz.ApplicationInput{SubjectID: "1", Name: "模式测试", TikTokAppID: "000123", ClientKey: "test-client", ClientSecret: "test-secret", Status: application.StatusDisabled}
	for _, normalize := range []func(biz.ApplicationInput) (biz.ApplicationInput, error){normalizeCreateApplicationInput, normalizeApplicationInput} {
		if _, err := normalize(input); !errors.Is(err, biz.ErrInvalidInput) {
			t.Fatal("省略模式必须拒绝", err)
		}
	}
}
