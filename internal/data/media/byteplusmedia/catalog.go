package byteplusmedia

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/byteplus-sdk/byteplus-sdk-golang/service/vod/models/response"

	"drama-server/internal/biz/operations"
)

// SyncVODEpisodeMetadata 同步运营后台的简介和标签到 VOD，并保留上传时由原文件名生成的视频标题。
func (p *Provider) SyncVODEpisodeMetadata(
	ctx context.Context,
	metadata operations.VODEpisodeMetadata,
	observation operations.MediaObservation,
) error {
	if err := p.validateVODEpisodeMetadata(metadata, true); err != nil {
		return err
	}
	if !observation.Exists || strings.TrimSpace(observation.VID) != metadata.VID {
		return operations.ErrInvalidInput
	}
	// 直传完成时 VOD 以原始文件名创建标题；后台同步不得覆盖它，否则运营无法从媒资列表识别文件来源。
	title := strings.TrimSpace(observation.Title)
	if title == "" {
		title = metadata.Title
	}
	if strings.TrimSpace(observation.Title) == title &&
		strings.TrimSpace(observation.Description) == metadata.Description &&
		equalVODTags(observation.Tags, metadata.Tags) {
		return nil
	}

	spanContext, span := p.startBytePlusSpan(ctx, "UpdateMediaInfo")
	output := &response.VodUpdateMediaInfoResponse{}
	status, requestErr := contextVODQuery(spanContext, p.client, "UpdateMediaInfo", url.Values{
		"Vid": {metadata.VID}, "Title": {title}, "Description": {metadata.Description},
		"Tags": {strings.Join(metadata.Tags, ",")},
	}, output)
	failed := bytePlusResponseFailed(status, requestErr, responseMetadata(output))
	finishBytePlusSpan(span, status, failed)
	if requestErr != nil {
		return fmt.Errorf("sync byteplus media metadata: %w", requestErr)
	}
	if status != http.StatusOK {
		return fmt.Errorf("sync byteplus media metadata: unexpected status %d", status)
	}
	if metadata := output.GetResponseMetadata(); metadata != nil && metadata.GetError().GetCode() != "" {
		return fmt.Errorf("sync byteplus media metadata: %s", metadata.GetError().GetCode())
	}
	return nil
}

func (p *Provider) validateVODEpisodeMetadata(metadata operations.VODEpisodeMetadata, requireVID bool) error {
	if p == nil || p.client == nil || strings.TrimSpace(p.spaceName) == "" ||
		strings.TrimSpace(metadata.Title) == "" || len(metadata.Tags) == 0 ||
		len([]rune(metadata.Title)) > 128 || len([]rune(metadata.Description)) > 1024 ||
		(requireVID && (strings.TrimSpace(metadata.VID) == "" || strings.Contains(metadata.VID, ","))) {
		return operations.ErrInvalidInput
	}
	for _, tag := range metadata.Tags {
		if tag = strings.TrimSpace(tag); tag == "" || strings.Contains(tag, ",") || len([]rune(tag)) > 36 {
			return operations.ErrInvalidInput
		}
	}
	return nil
}

func equalVODTags(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	counts := make(map[string]int, len(left))
	for _, tag := range left {
		counts[strings.TrimSpace(tag)]++
	}
	for _, tag := range right {
		tag = strings.TrimSpace(tag)
		if counts[tag] == 0 {
			return false
		}
		counts[tag]--
	}
	return true
}
