package consumer

import (
	"errors"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/monetization"
)

// ErrServiceUnavailable 表示消费者业务依赖暂不可用，不代表奖励被确定拒绝。
var ErrServiceUnavailable = errors.New("consumer service dependency is unavailable")

// rewardDependencyFailure 保留数据库失败因果链，但字符串只输出安全分类。
type rewardDependencyFailure struct {
	// cause 保留取消、超时及原始故障；Service 在包装前打印该错误，HTTP 仍使用稳定分类。
	cause error
}

// Error 不携带数据库连接、Token 或原始异常文本。
func (failure rewardDependencyFailure) Error() string { return ErrServiceUnavailable.Error() }

// Unwrap 让 HTTP 边界优先识别取消/超时，同时保留消费者依赖不可用分类。
func (failure rewardDependencyFailure) Unwrap() []error {
	return []error{ErrServiceUnavailable, failure.cause}
}

// rewardDependencyError 仅在奖励依赖端口边界包装故障；已有明确业务拒绝保持原语义。
func rewardDependencyError(err error) error {
	if err == nil || errors.Is(err, ErrNotFound) || errors.Is(err, ErrRateLimited) ||
		errors.Is(err, monetization.ErrInvalidRewardClaim) || errors.Is(err, application.ErrUnavailable) ||
		errors.Is(err, ErrServiceUnavailable) {
		return err
	}
	return rewardDependencyFailure{cause: err}
}
