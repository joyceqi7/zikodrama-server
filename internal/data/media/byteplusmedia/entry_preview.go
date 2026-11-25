package byteplusmedia

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"

	"drama-server/internal/biz/operations"

	"github.com/byteplus-sdk/byteplus-sdk-golang/service/vod/models/business"
	"github.com/byteplus-sdk/byteplus-sdk-golang/service/vod/models/response"
)

type editClient interface {
	postEdit(context.Context, string, []byte) ([]byte, int, error)
}

// SubmitEntryPreview 提交当前空间内片源的开头剪辑，画布沿用原片尺寸。
func (p *Provider) SubmitEntryPreview(ctx context.Context, vid string, seconds int) (string, error) {
	source, err := p.entryPreviewSource(ctx, vid)
	if err != nil {
		return "", err
	}
	duration := int(math.Floor(float64(source.GetDuration()) * 1000))
	end := min(seconds*1000, duration)
	if end <= 0 || source.GetWidth() <= 0 || source.GetHeight() <= 0 {
		return "", operations.ErrInvalidInput
	}
	track := map[string]any{"Type": "video", "Source": "vid://" + vid, "TargetTime": []int{0, end}}
	body := map[string]any{
		"Uploader": p.spaceName, "Application": "VideoTrackToB",
		"EditParam": map[string]any{
			"Canvas": map[string]int32{"Width": source.GetWidth(), "Height": source.GetHeight()},
			"Track":  [][]map[string]any{{track}},
			"Upload": map[string]string{"SpaceName": p.spaceName},
		},
	}
	var result struct {
		ReqID string `json:"ReqId"`
	}
	if err := p.requestEdit(ctx, "SubmitDirectEditTaskAsync", body, &result); err != nil {
		return "", err
	}
	if strings.TrimSpace(result.ReqID) == "" {
		return "", fmt.Errorf("byteplus first episode edit response omitted request ID")
	}
	return result.ReqID, nil
}

// EntryPreviewResult 在确认剪辑成功、独立 VID、原空间和时长后发布产物。
func (p *Provider) EntryPreviewResult(ctx context.Context, requestID, originalVID string, seconds int) (string, error) {
	var result []struct {
		ReqID     string `json:"ReqId"`
		Status    string `json:"Status"`
		OutputVID string `json:"OutputVid"`
	}
	if err := p.requestEdit(ctx, "GetDirectEditResult", map[string][]string{"ReqIds": {requestID}}, &result); err != nil {
		return "", err
	}
	for _, item := range result {
		if item.ReqID != requestID {
			continue
		}
		switch item.Status {
		case "pending", "start":
			return "", operations.ErrMediaProcessing
		case "failed_run", "user_canceled":
			return "", fmt.Errorf("byteplus first episode edit ended with status %s", item.Status)
		case "success":
			vid := strings.TrimSpace(item.OutputVID)
			if vid == "" || vid == originalVID {
				return "", fmt.Errorf("byteplus first episode edit did not return a distinct output")
			}
			source, err := p.entryPreviewSource(ctx, vid)
			if err != nil {
				return "", err
			}
			// 编码尾帧按帧边界对齐，拒绝任何明显超过授权范围的产物。
			if float64(source.GetDuration()) > float64(seconds)+0.05 {
				return vid, fmt.Errorf("byteplus first episode clip exceeds requested duration")
			}
			if err := p.publishAsset(ctx, vid); err != nil {
				return vid, err
			}
			return vid, nil
		default:
			return "", fmt.Errorf("byteplus first episode edit returned an unexpected status")
		}
	}
	return "", operations.ErrMediaProcessing
}

func (p *Provider) entryPreviewSource(ctx context.Context, vid string) (*business.VodSourceInfo, error) {
	result := &response.VodGetMediaInfosResponse{}
	status, err := contextVODQuery(ctx, p.client, "GetMediaInfos", url.Values{"Vids": {vid}}, result)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK || result.GetResponseMetadata().GetError().GetCode() != "" || result.GetResult() == nil {
		return nil, fmt.Errorf("byteplus first episode media metadata is unavailable")
	}
	for _, info := range result.GetResult().GetMediaInfoList() {
		basic, source := info.GetBasicInfo(), info.GetSourceInfo()
		if basic.GetVid() != vid {
			continue
		}
		if basic.GetSpaceName() != p.spaceName {
			return nil, fmt.Errorf("byteplus first episode media belongs to a different space")
		}
		if source.GetFileId() != "" && source.GetDuration() > 0 {
			return source, nil
		}
	}
	return nil, operations.ErrMediaProcessing
}

func (p *Provider) requestEdit(ctx context.Context, action string, input, result any) error {
	client, ok := p.client.(editClient)
	if !ok {
		return operations.ErrMediaNotConfigured
	}
	body, err := json.Marshal(input)
	if err != nil {
		return err
	}
	raw, _, err := client.postEdit(ctx, action, body)
	if err != nil {
		return err
	}
	output := struct {
		Metadata struct {
			Error *struct{ Code string } `json:"Error"`
		} `json:"ResponseMetadata"`
		Result any `json:"Result"`
	}{Result: result}
	if err := json.Unmarshal(raw, &output); err != nil {
		return err
	}
	if output.Metadata.Error != nil {
		return fmt.Errorf("byteplus %s rejected: %s", action, output.Metadata.Error.Code)
	}
	return nil
}
