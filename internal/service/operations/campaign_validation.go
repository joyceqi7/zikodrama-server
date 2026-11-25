package operations

import (
	"drama-server/internal/pkg/identifier"
	"strings"
	"unicode/utf8"

	"drama-server/internal/biz/campaign"
	"drama-server/internal/biz/operations"
)

// normalizeCampaignInput 校验活动字段，留空编码由业务层创建或沿用。
func normalizeCampaignInput(input operations.CampaignInput) (operations.CampaignInput, error) {
	candidate := operations.CampaignInput{
		Code: strings.ToLower(strings.TrimSpace(input.Code)), Name: strings.TrimSpace(input.Name),
		ExternalCampaignID: strings.TrimSpace(input.ExternalCampaignID), ExternalAdGroupID: strings.TrimSpace(input.ExternalAdGroupID),
		ExternalAdID: strings.TrimSpace(input.ExternalAdID), ExternalCreativeID: strings.TrimSpace(input.ExternalCreativeID),
		DramaID: strings.TrimSpace(input.DramaID), EpisodeID: strings.TrimSpace(input.EpisodeID),
		ExperimentKey: strings.TrimSpace(input.ExperimentKey), Status: strings.ToLower(strings.TrimSpace(input.Status)),
	}
	if (candidate.Code != "" && !validApplicationCode(candidate.Code)) || candidate.Name == "" || utf8.RuneCountInString(candidate.Name) > 128 ||
		(candidate.Status != campaign.StatusActive && candidate.Status != campaign.StatusDisabled) ||
		len(candidate.ExternalCampaignID) > 255 || len(candidate.ExternalAdGroupID) > 255 ||
		len(candidate.ExternalAdID) > 255 || len(candidate.ExternalCreativeID) > 255 || len(candidate.ExperimentKey) > 128 ||
		!identifier.ValidDatabaseID(candidate.DramaID) || !identifier.ValidDatabaseID(candidate.EpisodeID) {
		return input, operations.ErrInvalidInput
	}

	return candidate, nil
}
