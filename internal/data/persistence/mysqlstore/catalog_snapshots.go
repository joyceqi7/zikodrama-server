package mysqlstore

import (
	"context"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/consumer"
)

// catalogSource 指定渠道目录的真实列，避免为复用查询而重写或包装整份 JSON。
type catalogSource struct {
	id, snapshot, createdAt string
}

// publishedCatalogSource 只按服务端已解析的播放通道选择固定列名。
func publishedCatalogSource(ctx context.Context) catalogSource {
	if application.IsDirect(ctx) {
		return catalogSource{id: "p.drama_id", snapshot: "v.snapshot_json", createdAt: "v.created_at"}
	}
	return catalogSource{id: "official_state.drama_id", snapshot: "official_state.published_snapshot", createdAt: "d.created_at"}
}

// snapshotFields 仅返回短剧标识和已发布快照，列表展示字段由 Go 提取。
func (s catalogSource) snapshotFields() string {
	return s.id + " AS id, " + s.snapshot + " AS snapshot_json"
}

// catalogSnapshotRow 保存数据库筛选、分页后选中的线上快照。
type catalogSnapshotRow struct {
	// ID 使用关联记录中的短剧字符串标识，不依赖快照内冗余标识。
	ID string
	// SnapshotJSON 只在服务端解码，不直接返回消费者或写入日志。
	SnapshotJSON []byte
}

// catalogSnapshot 只解码列表所需字段，跳过媒资详情、凭据与其他非展示字段。
type catalogSnapshot struct {
	// Title 保存冻结版本的标题。
	Title string `json:"title"`
	// Description 保存冻结版本的简介。
	Description string `json:"description"`
	// CoverURL 保存冻结版本的封面。
	CoverURL string `json:"coverUrl"`
	// Status 保存内容连载状态，不代表渠道上架状态。
	Status string `json:"status"`
	// TagIDs 保存官方标签字符串标识及原始顺序。
	TagIDs []string `json:"tagIds"`
	// Episodes 只解码摘要所需的分集标识与海报匹配字段。
	Episodes []catalogEpisode `json:"episodes"`
}

// catalogEpisode 不保留列表不会使用的分集详情。
type catalogEpisode struct {
	// ID 保存分集字符串标识。
	ID string `json:"id"`
	// VID 仅用于校验发布后生成的首帧图是否仍属于线上视频。
	VID string `json:"vid"`
	// PosterURL 保存发布时已有的首帧图。
	PosterURL string `json:"posterUrl"`
}

// summary 保持列表状态归一化和标签展示合同；首页首帧图由调用方单独补齐。
func (s catalogSnapshot) summary(id string, catalog map[string]consumer.TikTokTag) consumer.DramaSummary {
	tags := []string{}
	for _, tagID := range s.TagIDs {
		if tag, ok := catalog[tagID]; ok {
			tags = append(tags, tag.DisplayName())
		}
	}
	status := s.Status
	if status == consumer.DramaStatusUpdating || status == consumer.DramaStatusUnpublished {
		status = "ongoing"
	}
	firstEpisodeID := ""
	if len(s.Episodes) > 0 {
		firstEpisodeID = s.Episodes[0].ID
	}
	return consumer.DramaSummary{
		ID: id, Title: s.Title, Description: s.Description, CoverURL: s.CoverURL,
		FirstEpisodeID: firstEpisodeID, EpisodeCount: len(s.Episodes),
		Tags: tags, TagIDs: s.TagIDs, Status: status,
	}
}
