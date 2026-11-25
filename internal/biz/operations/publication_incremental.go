package operations

import (
	"strings"

	domain "drama-server/internal/biz/consumer"
)

// publicationMediaPlan 保存新发布任务可复用的官方媒资事实和首个待执行阶段。
type publicationMediaPlan struct {
	Videos          []PublicationVideo
	Transcodes      []PublicationTranscode
	PlatformCoverID string
	Stage           string
}

// reusablePublicationEpisodeIDs 从同一官方归属的已确认任务按本地集标识恢复身份。
// 序号移动或替换视频仍更新原分集；新增集即使占用旧序号，也不能继承另一集的官方标识。
func reusablePublicationEpisodeIDs(drama domain.Drama, previous TikTokPublication, reusable bool) map[int]string {
	result := make(map[int]string)
	if !reusable || previous.ContentSnapshot == nil || previous.PlatformAlbumID == "" || previous.PlatformVersion < 1 {
		return result
	}
	byEpisode := make(map[string]string, len(previous.ContentSnapshot.Episodes))
	for _, episode := range previous.ContentSnapshot.Episodes {
		if id := strings.TrimSpace(previous.EpisodeIDsBySequence[episode.Number]); episode.ID != "" && id != "" {
			byEpisode[episode.ID] = id
		}
	}
	for _, episode := range drama.Episodes {
		if id := byEpisode[episode.ID]; id != "" {
			result[episode.Number] = id
		}
	}
	return result
}

// planPublicationMedia 比较当前内容与上一任务的冻结快照，只复用同一官方账号下已经确认的媒资事实。
// 标题、简介、标签和分集标题等元数据变化不会重新登记或转码；封面和视频按各自身份独立判断。
func planPublicationMedia(drama domain.Drama, previous TikTokPublication, reusable bool, transcode bool) publicationMediaPlan {
	plan := publicationMediaPlan{Stage: PublicationStageUploadCover}
	if reusable && previous.ContentSnapshot != nil && previous.ContentSnapshot.CoverURL == drama.CoverURL {
		plan.PlatformCoverID = strings.TrimSpace(previous.PlatformCoverID)
	}

	registered := map[string]PublicationVideo{}
	transcoded := map[string]bool{}
	if reusable {
		for _, video := range previous.Videos {
			if video.Status == "ready" && strings.TrimSpace(video.VID) != "" {
				registered[publicationVideoKey(video.EpisodeID, video.VID)] = video
			}
		}
		for _, batch := range previous.Transcodes {
			if !batch.Ready || !sameVideoSet(batch.VIDs, batch.CompletedVIDs) {
				continue
			}
			for _, vid := range batch.VIDs {
				transcoded[vid] = true
			}
		}
	}

	reusedVIDs := make([]string, 0, len(drama.Episodes))
	pendingVIDs := make([]string, 0, len(drama.Episodes))
	reusedSeen := map[string]bool{}
	pendingSeen := map[string]bool{}
	for _, episode := range drama.Episodes {
		video := PublicationVideo{EpisodeID: episode.ID, VID: episode.VID, Status: "pending"}
		if prior, ok := registered[publicationVideoKey(episode.ID, episode.VID)]; ok {
			video = prior
			video.EpisodeID, video.VID = episode.ID, episode.VID
		}
		plan.Videos = append(plan.Videos, video)
		if transcoded[episode.VID] {
			if !reusedSeen[episode.VID] {
				reusedVIDs = append(reusedVIDs, episode.VID)
				reusedSeen[episode.VID] = true
			}
		} else if !pendingSeen[episode.VID] {
			pendingVIDs = append(pendingVIDs, episode.VID)
			pendingSeen[episode.VID] = true
		}
	}
	// 即使本次使用源视频，也保留已经确认的转码事实，后续选择多清晰度时可复用。
	plan.Transcodes = appendReadyTranscodeBatches(plan.Transcodes, reusedVIDs)
	if transcode {
		plan.Transcodes = appendPendingTranscodeBatches(plan.Transcodes, pendingVIDs)
	}
	plan.Stage = nextPublicationMediaStage(TikTokPublication{
		SkipTranscode:   !transcode,
		PlatformCoverID: plan.PlatformCoverID,
		PlatformAlbumID: previous.PlatformAlbumID,
		Videos:          plan.Videos,
		Transcodes:      plan.Transcodes,
	})
	return plan
}

// publicationVideoKey 以内部集标识和 BytePlus VID 共同标识可复用的登记结果。
func publicationVideoKey(episodeID, vid string) string {
	return episodeID + "\x00" + vid
}

// appendReadyTranscodeBatches 把已确认转码的 VID 固化为完成批次，后续 Worker 不再调用转码接口。
func appendReadyTranscodeBatches(target []PublicationTranscode, vids []string) []PublicationTranscode {
	for start := 0; start < len(vids); start += 1000 {
		batchVIDs := append([]string(nil), vids[start:min(start+1000, len(vids))]...)
		target = append(target, PublicationTranscode{VIDs: batchVIDs, CompletedVIDs: append([]string(nil), batchVIDs...), Ready: true})
	}
	return target
}

// appendPendingTranscodeBatches 为新增或替换的视频创建稳定批次，只转码发生变化的 VID。
func appendPendingTranscodeBatches(target []PublicationTranscode, vids []string) []PublicationTranscode {
	for start := 0; start < len(vids); start += 1000 {
		target = append(target, PublicationTranscode{VIDs: append([]string(nil), vids[start:min(start+1000, len(vids))]...)})
	}
	return target
}

// nextPublicationMediaStage 返回当前媒资计划首个未完成阶段；全部复用时直接更新 Album 元数据。
func nextPublicationMediaStage(publication TikTokPublication) string {
	if strings.TrimSpace(publication.PlatformCoverID) == "" {
		return PublicationStageUploadCover
	}
	for _, video := range publication.Videos {
		if video.Status != "ready" {
			return PublicationStageRegisterVideos
		}
	}
	if len(publication.Transcodes) == 0 && !publication.SkipTranscode {
		return PublicationStageTranscodeVideos
	}
	if !publication.SkipTranscode {
		for _, batch := range publication.Transcodes {
			if !batch.Ready {
				return PublicationStageTranscodeVideos
			}
		}
	}
	if strings.TrimSpace(publication.PlatformAlbumID) == "" {
		return PublicationStageCreateAlbum
	}
	return PublicationStageUpdateAlbum
}
