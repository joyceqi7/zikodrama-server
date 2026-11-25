package visitor

import (
	"context"
	"errors"
	"testing"
)

// TestMissingServiceWiringIsNotClientFault 避免内部装配缺失被伪装为参数或登录错误。
func TestMissingServiceWiringIsNotClientFault(t *testing.T) {
	for _, service := range []*Service{nil, {}} {
		_, _, err := service.resolveApplication(context.Background(), "1")
		if !errors.Is(err, errServiceNotConfigured) || errors.Is(err, ErrInvalidRequest) || errors.Is(err, ErrUnauthorized) {
			t.Fatalf("内部装配失败不应要求用户修正参数或登录：%v", err)
		}
	}
}
