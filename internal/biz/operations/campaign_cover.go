package operations

import (
	"context"
	"log/slog"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"

	"drama-server/internal/biz/campaign"
	content "drama-server/internal/biz/consumer"
)

var publicCampaignCoverKey = regexp.MustCompile("^[A-Za-z0-9][A-Za-z0-9._~/-]{0,255}$")

// campaignCoverKey 只从自有公开图片 CDN 取路径，不把短时签名或其他域写入持久深链。
func campaignCoverKey(coverURL string) string {
	cover, err := url.Parse(strings.TrimSpace(coverURL))
	if err != nil || cover.Scheme != "https" || cover.Host != "img.zikodrama-tv.com" || cover.User != nil ||
		cover.RawQuery != "" || cover.ForceQuery || cover.Fragment != "" || cover.RawPath != "" {
		return ""
	}
	key := strings.TrimPrefix(cover.Path, "/")
	if !publicCampaignCoverKey.MatchString(key) || path.Clean(key) != key {
		return ""
	}
	return key
}

// campaignMinisPath 从当前应用的线上快照选择展示信息，提示字段独立于消费者 API 的落地参数。
func campaignMinisPath(item campaign.Campaign, published *content.Drama, official bool) (string, bool) {
	query := url.Values{"campaignCode": {item.Code}}
	key := campaignCoverKey(published.CoverURL)
	if key == "" {
		return "/?" + query.Encode(), false
	}
	for _, episode := range published.Episodes {
		if episode.ID != item.EpisodeID {
			continue
		}
		query.Set("coverDramaId", item.DramaID)
		query.Set("coverEpisodeId", item.EpisodeID)
		query.Set("coverKey", key)
		query.Set("coverEpisodeNumber", strconv.Itoa(episode.Number))
		query.Set("coverEpisodeCount", strconv.Itoa(len(published.Episodes)))
		title := []rune(strings.TrimSpace(published.Title))
		if len(title) > 80 {
			title = title[:80]
		}
		if len(title) > 0 {
			query.Set("coverTitle", string(title))
		}
		// 官方标识允许展示方识别目标；播放仍使用服务端授权回包，不带 VID 或播放凭据。
		if official && published.PlatformAlbumID != "" && episode.PlatformEpisodeID != "" {
			query.Set("platformDramaId", published.PlatformAlbumID)
			query.Set("platformEpisodeId", episode.PlatformEpisodeID)
		}
		return "/?" + query.Encode(), true
	}
	return "/?" + query.Encode(), false
}

// CampaignPreviewLink 是同一活动的独立验证链接，运营可用内部路径配置版本预览二维码。
type CampaignPreviewLink struct {
	// LandingURL 是 TikTok 返回的官方短链。
	LandingURL string `json:"landingUrl"`
	// MinisPath 只包含原活动编码和公开封面参数。
	MinisPath string `json:"minisPath"`
}

// PreviewCampaignLink 为已启用活动生成独立短链，只读取已保存的活动，不改写原链接或数据库。
func (s *CampaignService) PreviewCampaignLink(ctx context.Context, id string) (*CampaignPreviewLink, error) {
	if s == nil || s.campaigns == nil || s.registry == nil || s.catalog == nil {
		slog.WarnContext(ctx, "generate campaign preview rejected", "error", ErrInvalidInput)
		return nil, ErrInvalidInput
	}
	item, err := s.campaigns.FindCampaign(ctx, id)
	if err != nil {
		slog.ErrorContext(ctx, "find campaign for preview failed", "error", err)
		return nil, err
	}
	if item.Status != campaign.StatusActive {
		slog.WarnContext(ctx, "disabled campaign preview rejected", "error", ErrInvalidInput)
		return nil, ErrInvalidInput
	}
	if err := s.generateCampaignLandingURL(ctx, &item, true); err != nil {
		slog.ErrorContext(ctx, "generate campaign preview link failed", "error", err)
		return nil, err
	}
	return &CampaignPreviewLink{LandingURL: item.LandingURL, MinisPath: item.MinisPath}, nil
}
