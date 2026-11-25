package operations

import (
	"strings"
	"unicode/utf8"

	"drama-server/internal/biz/operations"
)

// normalizeApplicationMonetizationInput 校验广告阈值和文案格式，关闭 VIP 时清空旧阈值。
func normalizeApplicationMonetizationInput(input operations.ApplicationMonetizationInput) (operations.ApplicationMonetizationInput, error) {
	input.EpisodeUnlockAdUnitCode = strings.TrimSpace(input.EpisodeUnlockAdUnitCode)
	input.VIPStrategy.AdUnitCode = strings.TrimSpace(input.VIPStrategy.AdUnitCode)
	input.VIPStrategy.Title = strings.TrimSpace(input.VIPStrategy.Title)
	input.VIPStrategy.Content = strings.TrimSpace(input.VIPStrategy.Content)
	input.VIPStrategy.FailureMessage = strings.TrimSpace(input.VIPStrategy.FailureMessage)
	if input.FreeEpisodeCount < 0 || input.FreeEpisodeCount > 10_000 ||
		input.EpisodeUnlockAdUnitCode == "" ||
		(input.UnlockEpisodeCount != nil && (*input.UnlockEpisodeCount < 1 || *input.UnlockEpisodeCount > 10_000)) ||
		(input.DailyRewardLimit != nil && (*input.DailyRewardLimit < 0 || *input.DailyRewardLimit > 10_000)) {
		return operations.ApplicationMonetizationInput{}, operations.ErrInvalidInput
	}
	// VIP 开启时才检查达成阈值。ForceWatchCount=0 表示不强制观看，不能替换成 RequiredAdCount。
	if input.VIPStrategy.Enabled &&
		(input.VIPStrategy.AdUnitCode == "" ||
			input.VIPStrategy.RequiredAdCount < 1 || input.VIPStrategy.RequiredAdCount > 10 ||
			input.VIPStrategy.ForceWatchCount < 0 ||
			input.VIPStrategy.ForceWatchCount > input.VIPStrategy.RequiredAdCount ||
			input.VIPStrategy.DelaySeconds < 0 || input.VIPStrategy.DelaySeconds > 300 ||
			utf8.RuneCountInString(input.VIPStrategy.Title) > 255 ||
			utf8.RuneCountInString(input.VIPStrategy.Content) > 500 ||
			utf8.RuneCountInString(input.VIPStrategy.FailureMessage) > 500) {
		return operations.ApplicationMonetizationInput{}, operations.ErrInvalidInput
	}
	if !input.VIPStrategy.Enabled {
		// 关闭策略时清空强看配置，避免重新启用后继承历史门禁。
		input.VIPStrategy.AdUnitCode = ""
		input.VIPStrategy.ForceWatchCount = 0
		input.VIPStrategy.DelaySeconds = 0
		input.VIPStrategy.RequiredAdCount = 0
	}
	return input, nil
}
