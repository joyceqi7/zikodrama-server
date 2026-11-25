package consumer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
)

// FreezeContent 清除渠道与协作字段并复制标签标识和剧集，供各发布渠道冻结独立快照。
func FreezeContent(d Drama) Drama {
	d.Tags = nil
	d.TikTokTags = nil
	d.TagIDs = slices.Clone(d.TagIDs)
	d.Published = nil
	d.PlaybackProvider = ""
	d.OwnerApplicationID = ""
	d.AuthorizedApplicationIDs = nil
	d.PlatformAlbumID = ""
	d.PublishStatus = "ready"
	d.Assignee = ""
	d.OperationNote = ""
	d.OperationDueAt = nil
	d.OnlineVersion = 0
	d.PlatformVersion = 0
	d.OnlineEpisodeNumbers = nil
	d.Episodes = append([]Episode{}, d.Episodes...)
	for i := range d.Episodes {
		d.Episodes[i].PlatformEpisodeID = ""
	}
	return d
}

// ContentDigest 计算不包含渠道与协作字段的确定性内容摘要。
func ContentDigest(d Drama) string {
	raw, _ := json.Marshal(FreezeContent(d))
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// OfficialContentDigest 只比较实际提交到 TikTok Album 的内容及分集身份。
// 首帧图、时长和本地媒资状态不属于 Album 参数，不能据此生成新官方版本。
func OfficialContentDigest(d Drama) string {
	// 对文本使用与官方适配器一致的裁剪，历史快照的末尾空白不代表平台内容变化。
	content := Drama{Title: strings.TrimSpace(d.Title), Description: strings.TrimSpace(d.Description), CoverURL: strings.TrimSpace(d.CoverURL),
		Language: strings.TrimSpace(d.Language), Year: d.Year, Status: d.Status, DramaType: d.DramaType, TagIDs: append([]string{}, d.TagIDs...)}
	slices.Sort(content.TagIDs)
	for _, ep := range d.Episodes {
		content.Episodes = append(content.Episodes, Episode{ID: ep.ID, Number: ep.Number, Title: strings.TrimSpace(ep.Title), VID: strings.TrimSpace(ep.VID)})
	}
	slices.SortFunc(content.Episodes, func(a, b Episode) int { return a.Number - b.Number })
	raw, _ := json.Marshal(content)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
