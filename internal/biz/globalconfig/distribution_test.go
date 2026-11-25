package globalconfig

import (
	"encoding/json"
	"errors"
	"testing"
)

// TestDistributionConfigurationRetired 保证发布方迁至主体后，旧全局键不能重新写入。
func TestDistributionConfigurationRetired(t *testing.T) {
	if err := ValidateInput("distribution", json.RawMessage(`{}`)); !errors.Is(err, ErrInvalid) {
		t.Fatal("退役的全局发布方配置仍被接受")
	}
	if err := ValidateInput("media", json.RawMessage(`{}`)); err != nil {
		t.Fatal("退役发布方不能禁用共享 VOD 配置", err)
	}
}
