package application

import (
	"context"
	"sort"
	"strings"
)

// AdUnits 是应用在 TikTok Developer Portal 中登记的广告位编码集合。
// 激励视频允许组成候选集合，插屏仍只配置一个 code；配置随 Application 持久化。
type AdUnits struct {
	// RewardedVideo 是可以用于消费者权益结算的激励视频广告位编码。
	RewardedVideo []string `json:"rewardedVideo"`
	// Interstitial 是普通插屏广告位编码；消费者启动接口选取首个已配置广告位。
	Interstitial []string `json:"interstitial"`
}

// Normalized 返回去除空白、空值和重复编码后的稳定副本。
func (units AdUnits) Normalized() AdUnits {
	units.RewardedVideo = normalizedAdUnitCodes(units.RewardedVideo)
	units.Interstitial = normalizedAdUnitCodes(units.Interstitial)
	return units
}

// MaxRewardedAdUnits 限制单应用激励广告候选集合大小，不代表 TikTok 的平台限制。
const MaxRewardedAdUnits = 20

// Complete 判断激励候选集合与单个插屏编码是否配齐，供应用及策略启用门禁使用。
func (units AdUnits) Complete() bool {
	valid := func(values []string) bool {
		for _, value := range values {
			if strings.TrimSpace(value) == "" || len(strings.TrimSpace(value)) > 255 {
				return false
			}
		}
		return true
	}
	return len(units.RewardedVideo) > 0 && len(units.RewardedVideo) <= MaxRewardedAdUnits && len(units.Interstitial) == 1 && valid(units.RewardedVideo) && valid(units.Interstitial)
}

// HasRewardedVideo 判断编码是否属于当前应用的激励视频广告位。
func (units AdUnits) HasRewardedVideo(code string) bool {
	code = strings.TrimSpace(code)
	for _, candidate := range units.RewardedVideo {
		if candidate == code {
			return true
		}
	}
	return false
}

// normalizedAdUnitCodes 去掉空白、空项和重复广告位并排序，使配置比较与展示顺序稳定。
func normalizedAdUnitCodes(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

// AdUnitRepository 为消费设置和奖励结算提供应用广告位编码校验。
type AdUnitRepository interface {
	// HasAdUnit 检查公开广告位是否登记在当前应用的激励视频或插屏集合，不校验奖励或策略。
	HasAdUnit(context.Context, string) (bool, error)
	// HasRewardedAdUnit 检查指定公开广告位编码是否登记在上下文当前应用的激励视频列表中。
	HasRewardedAdUnit(context.Context, string) (bool, error)
}

// HasCode 判断公开广告位是否属于应用已登记的广告集合，不决定广告策略。
func (units AdUnits) HasCode(code string) bool {
	code = strings.TrimSpace(code)
	for _, codes := range [][]string{units.RewardedVideo, units.Interstitial} {
		for _, candidate := range codes {
			if candidate == code {
				return true
			}
		}
	}
	return false
}
