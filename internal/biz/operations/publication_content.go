package operations

import domain "drama-server/internal/biz/consumer"

// PublicationContentSummary 为运营说明本次处理范围，平台版本仍由官方维护。
type PublicationContentSummary struct {
	// EpisodeNumbers 是本次同步固定的完整集序号，不随后续编辑变化。
	EpisodeNumbers []int `json:"episodeNumbers"`
	// PreviousEpisodeNumbers 是同步前线上集序号；空数组表示首次发布，nil 表示历史数据未知。
	PreviousEpisodeNumbers []int `json:"previousEpisodeNumbers"`
}

// NewPublicationContentSummary 从同步时的内容和线上快照提取安全摘要，不公开视频或封面地址。
func NewPublicationContentSummary(drama domain.Drama) *PublicationContentSummary {
	summary := &PublicationContentSummary{EpisodeNumbers: []int{}, PreviousEpisodeNumbers: []int{}}
	for _, episode := range drama.Episodes {
		summary.EpisodeNumbers = append(summary.EpisodeNumbers, episode.Number)
	}
	if drama.Published != nil {
		for _, episode := range drama.Published.Episodes {
			summary.PreviousEpisodeNumbers = append(summary.PreviousEpisodeNumbers, episode.Number)
		}
	}
	return summary
}
