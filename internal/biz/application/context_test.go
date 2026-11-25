package application

import (
	"context"
	"testing"
)

func TestApplicationContextDoesNotFallBackToProcessDefault(t *testing.T) {
	t.Parallel()

	if applicationID := IDFromContext(context.Background()); applicationID != "" {
		t.Fatalf("缺少可信应用上下文时不得回退到默认应用，实际为 %q", applicationID)
	}
	if applicationID := IDFromContext(WithContext(context.Background(), "  app-jp  ")); applicationID != "app-jp" {
		t.Fatalf("应用上下文未规范化：%q", applicationID)
	}
}
