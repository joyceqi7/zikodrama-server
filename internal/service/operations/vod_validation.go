package operations

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"drama-server/internal/biz/operations"
)

// vodEventEnvelope 对应验签后的官方回调 JSON 字段。
type vodEventEnvelope struct {
	// RequestID 是官方回调请求标识。
	RequestID string `json:"RequestId"`
	// Version 是官方回调协议版本。
	Version string `json:"Version"`
	// EventType 决定事件所属的业务处理分支。
	EventType string `json:"EventType"`
	// EventTime 是携带时区的 RFC3339 事件时间。
	EventTime string `json:"EventTime"`
	// Data 保存官方事件数据，仅在验签后解析。
	Data struct {
		// Code 是官方事件处理结果编码。
		Code string `json:"Code"`
		// Message 是官方结果说明，不随整段载荷写日志。
		Message string `json:"Message"`
		// VID 是视频资源的官方字符串标识。
		VID string `json:"Vid"`
		// MID 是图片或字幕素材的官方字符串标识。
		MID string `json:"Mid"`
		// SpaceName 标识事件所属的 VOD 空间。
		SpaceName string `json:"SpaceName"`
		// CallbackArgs 用于关联上传时传递的托管媒资标识。
		CallbackArgs string `json:"CallbackArgs"`
		// PosterURI 是官方回调报告的封面资源标识。
		PosterURI string `json:"PosterUri"`
	} `json:"Data"`
}

// parseVODCallback 在验签后解析官方载荷，保留原始字节供业务层生成去重指纹。
func parseVODCallback(ctx context.Context, rawBody []byte, keyVersion string) (operations.VODEvent, error) {
	var envelope vodEventEnvelope
	if err := json.Unmarshal(rawBody, &envelope); err != nil {
		return operations.VODEvent{}, err
	}
	eventTime, err := time.Parse(time.RFC3339, strings.TrimSpace(envelope.EventTime))
	if err != nil {
		return operations.VODEvent{}, err
	}
	if strings.TrimSpace(envelope.RequestID) == "" || strings.TrimSpace(envelope.Version) == "" {
		return operations.VODEvent{}, operations.ErrInvalidInput
	}
	eventType := strings.TrimSpace(envelope.EventType)
	resourceClass := operations.MediaResourceVideo
	providerUploadID := strings.TrimSpace(envelope.Data.VID)
	// 同一回调地址可能收到未订阅事件；保留类型返回给 Service 正常忽略，避免无意义重试。
	switch eventType {
	case operations.VODEventFileUploadComplete, operations.VODEventDeleteMediaComplete, operations.VODEventExecutionComplete, operations.VODEventTranscodeComplete:
	case operations.VODEventMaterialUploadComplete:
		resourceClass = operations.MediaResourceMaterial
		providerUploadID = strings.TrimSpace(envelope.Data.MID)
	default:
		return operations.VODEvent{EventType: eventType}, nil
	}
	if providerUploadID == "" {
		return operations.VODEvent{}, operations.ErrInvalidInput
	}
	assetID := ""
	// 外部控制台创建的资源可能没有合法 CallbackArgs；解析失败有日志，后续仍可按 VID/MID 匹配已有账本。
	if callbackArgs := strings.TrimSpace(envelope.Data.CallbackArgs); callbackArgs != "" {
		var args struct {
			UploadID string `json:"uploadId"`
		}
		if err := json.Unmarshal([]byte(callbackArgs), &args); err != nil {
			slog.WarnContext(ctx, "decode VOD callback arguments failed", "error", err)
		} else {
			assetID = strings.TrimSpace(args.UploadID)
		}
	}

	return operations.VODEvent{RequestID: strings.TrimSpace(envelope.RequestID),
		Version: strings.TrimSpace(envelope.Version), EventType: eventType, EventTime: eventTime.UTC(),
		ResourceClass: resourceClass, ProviderAssetID: providerUploadID, SpaceName: strings.TrimSpace(envelope.Data.SpaceName),
		UploadID: assetID, CallbackArgs: strings.TrimSpace(envelope.Data.CallbackArgs), Code: strings.TrimSpace(envelope.Data.Code),
		Message: strings.TrimSpace(envelope.Data.Message), PosterURI: strings.TrimSpace(envelope.Data.PosterURI),
	}, nil
}
