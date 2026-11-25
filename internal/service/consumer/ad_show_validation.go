package consumer

import (
	"drama-server/internal/biz/monetization"
	"drama-server/internal/pkg/identifier"
	"regexp"
	"strings"
)

var adShowScenePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// validAdShowTarget 校验可选字符串内容标识；剧集来源必须同时包含剧目。
func validAdShowTarget(dramaID, episodeID string) bool {
	return (dramaID == "" || identifier.ValidDatabaseID(dramaID)) &&
		(episodeID == "" || (dramaID != "" && identifier.ValidDatabaseID(episodeID)))
}

// normalizeAdShow 在 HTTP 入口规范化通用事实字段，场景编码不绑定入剧或奖励枚举。
func normalizeAdShow(input monetization.AdShowInput) (monetization.AdShowInput, error) {
	input.EventID, input.Scene = strings.TrimSpace(input.EventID), strings.TrimSpace(input.Scene)
	input.AdUnitID = strings.TrimSpace(input.AdUnitID)
	input.DramaID, input.EpisodeID = strings.TrimSpace(input.DramaID), strings.TrimSpace(input.EpisodeID)
	if !adShowScenePattern.MatchString(input.Scene) || !validClaimID(input.EventID) ||
		input.AdUnitID == "" || len(input.AdUnitID) > 255 || !validAdShowTarget(input.DramaID, input.EpisodeID) {
		return input, monetization.ErrInvalidAdShow
	}
	input.OccurredAt = input.OccurredAt.UTC()
	return input, nil
}
