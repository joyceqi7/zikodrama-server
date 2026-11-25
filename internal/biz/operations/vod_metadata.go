package operations

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode/utf8"

	domain "drama-server/internal/biz/consumer"
)

const (
	vodTitleMaxRunes          = 128
	vodDescriptionMaxRunes    = 1024
	vodClassificationMaxRunes = 64
)

// newVODEpisodeMetadata 只使用运营后台内容生成 VOD 投影，避免 VOD 控制台内容反向覆盖业务数据。
func newVODEpisodeMetadata(drama domain.Drama, episode domain.Episode) VODEpisodeMetadata {
	primaryTagID := "46"
	if len(drama.TagIDs) > 0 {
		primaryTagID = strings.TrimSpace(drama.TagIDs[0])
	}
	primaryTag, exists := domain.FindTikTokTag(primaryTagID)
	if !exists {
		primaryTagID = "46"
		primaryTag, _ = domain.FindTikTokTag(primaryTagID)
	}

	episodeLabel := fmt.Sprintf("Episode %03d", episode.Number)
	titleParts := []string{strings.TrimSpace(drama.Title), episodeLabel}
	if episodeTitle := strings.TrimSpace(episode.Title); episodeTitle != "" {
		titleParts = append(titleParts, episodeTitle)
	}
	title := truncateRunes(strings.Join(titleParts, " - "), vodTitleMaxRunes)

	descriptionParts := make([]string, 0, 5)
	if episodeDescription := strings.TrimSpace(episode.Description); episodeDescription != "" {
		descriptionParts = append(descriptionParts, episodeDescription)
	}
	descriptionParts = append(descriptionParts,
		"短剧："+strings.TrimSpace(drama.Title),
		"剧集："+episodeLabel+" "+strings.TrimSpace(episode.Title),
	)
	if dramaDescription := strings.TrimSpace(drama.Description); dramaDescription != "" {
		descriptionParts = append(descriptionParts, "短剧简介："+dramaDescription)
	}
	descriptionParts = append(descriptionParts, "Drama ID："+drama.ID+"/"+episode.ID)

	tags := []string{
		"drama-managed",
		"tt-drama-" + shortMetadataID(drama.ID),
		"tt-episode-" + shortMetadataID(episode.ID),
		fmt.Sprintf("tt-ep-%03d", episode.Number),
	}
	for _, tagID := range drama.TagIDs {
		tagID = strings.TrimSpace(tagID)
		if tagID != "" {
			tags = append(tags, "tt-tag-"+tagID)
		}
	}

	primaryKey := "TT-TAG-" + primaryTagID
	dramaKey := "TT-DRAMA-" + shortMetadataID(drama.ID)
	return VODEpisodeMetadata{
		// 同步已有视频时必须携带绑定的 VID；上传前尚无 VID，准备分类的流程允许为空。
		VID:   strings.TrimSpace(episode.VID),
		Title: title, Description: truncateRunes(strings.Join(descriptionParts, "\n"), vodDescriptionMaxRunes), Tags: tags,
		PrimaryClassificationName: classificationName(primaryTag.OfficialName, primaryKey),
		PrimaryClassificationKey:  primaryKey,
		DramaClassificationName:   classificationName(drama.Title, dramaKey),
		DramaClassificationKey:    dramaKey,
	}
}

// classificationName 为可读分类名称保留稳定键后缀；标题过长时只截断前缀，使重命名后仍可识别业务分类。
func classificationName(title, key string) string {
	suffix := " · " + strings.TrimSpace(key)
	prefixLimit := vodClassificationMaxRunes - utf8.RuneCountInString(suffix)
	if prefixLimit <= 0 {
		return truncateRunes(strings.TrimSpace(key), vodClassificationMaxRunes)
	}
	prefix := truncateRunes(strings.TrimSpace(title), prefixLimit)
	if prefix == "" {
		return truncateRunes(strings.TrimSpace(key), vodClassificationMaxRunes)
	}
	return prefix + suffix
}

// shortMetadataID 把业务 ID 映射为固定长度的元数据标签后缀，不作为资产身份或数据库主键。
func shortMetadataID(value string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return hex.EncodeToString(sum[:8])
}

// truncateRunes 按 Unicode 字符数限制 VOD 展示文案，避免截断中文的 UTF-8 字节；非正限制表示不裁剪。
func truncateRunes(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return strings.TrimSpace(string(runes[:limit]))
}
