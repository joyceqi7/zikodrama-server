package consumer

import (
	"drama-server/internal/pkg/identifier"
	"encoding/base64"
	"strconv"
	"strings"

	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/monetization"
)

// normalizeDiscoveryQuery 在入口校验查询条件并解析分页游标，服务只接收已验证的位置。
func normalizeDiscoveryQuery(input domain.DiscoveryQuery, cursor string) (domain.DiscoveryQuery, error) {
	input.Query = strings.TrimSpace(input.Query)
	input.Tag = strings.TrimSpace(input.Tag)
	input.Status = strings.ToLower(strings.TrimSpace(input.Status))
	cursor = strings.TrimSpace(cursor)
	if len(input.Query) > 200 || len(input.Tag) > 80 ||
		(input.Status != "" && input.Status != "ongoing" && input.Status != "completed") ||
		input.Limit < 0 || input.Limit > 50 {
		return input, domain.ErrInvalidDiscovery
	}
	if input.Limit == 0 {
		input.Limit = 20
	}
	offset, err := decodeDiscoveryCursor(cursor)
	if err != nil {
		return input, domain.ErrInvalidDiscovery
	}
	input.Offset = offset
	// Service 接收筛选字段和数字偏移量；原始游标仅属于 HTTP 协议。
	return input, nil
}

// decodeDiscoveryCursor 将无填充的 URL Base64 游标解码为 v1:非负偏移量。
// 空游标表示第一页；偏移量是否超出当前筛选结果，需要 Service 在读取可见内容后判断。
func decodeDiscoveryCursor(cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || !strings.HasPrefix(string(decoded), "v1:") {
		return 0, domain.ErrInvalidDiscovery
	}
	offset, err := strconv.Atoi(strings.TrimPrefix(string(decoded), "v1:"))
	if err != nil || offset < 0 {
		return 0, domain.ErrInvalidDiscovery
	}
	return offset, nil
}

// validClaimID 限制客户端奖励幂等键为 16～96 字节的英数及 -_. 字符。
// 允许 UUID、ULID 或业务前缀；不在服务端重新生成，以便客户端重试恢复同一次领取。
func validClaimID(value string) bool {
	if len(value) < 16 || len(value) > 96 {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '-' || char == '_' || char == '.' {
			continue
		}
		return false
	}
	return true
}

// normalizeRewardClaim 校验领取字段；Offer 与已发奖励的绑定、过期和幂等恢复仍由业务层检查。
func normalizeRewardClaim(input monetization.ClaimRewardInput) (monetization.ClaimRewardInput, error) {
	input.DramaID = strings.TrimSpace(input.DramaID)
	input.EpisodeID = strings.TrimSpace(input.EpisodeID)
	input.ClaimID = strings.TrimSpace(input.ClaimID)
	input.CompletedAdUnitID = strings.TrimSpace(input.CompletedAdUnitID)
	if (input.DramaID != "" && !identifier.ValidDatabaseID(input.DramaID)) || (input.EpisodeID != "" && !identifier.ValidDatabaseID(input.EpisodeID)) || !input.IsEnded || !validClaimID(input.ClaimID) || input.CompletedAdUnitID == "" {
		return input, monetization.ErrInvalidRewardClaim
	}
	return input, nil
}
