package operations

import (
	"strings"

	"drama-server/internal/biz/operations"
)

// normalizePublicationInput 校验发布主体、目标标识和优先级，平台授权关系在业务层检查。
func normalizePublicationInput(input operations.TikTokPublicationInput) (operations.TikTokPublicationInput, error) {
	input.Priority = strings.ToLower(strings.TrimSpace(input.Priority))
	if input.Priority == "" {
		input.Priority = operations.PublicationPriorityNormal
	}
	if input.Priority != operations.PublicationPriorityNormal && input.Priority != operations.PublicationPriorityUrgent {
		return input, operations.ErrInvalidInput
	}
	return input, nil
}
