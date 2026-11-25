package byteplusmedia

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/byteplus-sdk/byteplus-sdk-golang/base"
	servicebase "github.com/byteplus-sdk/byteplus-sdk-golang/service/base/models/base"
	"github.com/byteplus-sdk/byteplus-sdk-golang/service/vod"
	"github.com/byteplus-sdk/byteplus-sdk-golang/service/vod/models/business"
	"github.com/byteplus-sdk/byteplus-sdk-golang/service/vod/models/request"
	"github.com/byteplus-sdk/byteplus-sdk-golang/service/vod/models/response"
	"github.com/byteplus-sdk/byteplus-sdk-golang/service/vod/upload/consts"
	"github.com/byteplus-sdk/byteplus-sdk-golang/service/vod/upload/functions"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"drama-server/internal/biz/operations"
)

var (
	ErrInvalidConfig = errors.New("invalid byteplus vod configuration")
)

const (
	bytePlusTracerName = "drama-api/byteplus"
)

// normalizedHTTPSOrigin 仅规范化封面与上传 Origin，拒绝路径、凭据和查询参数。
func normalizedHTTPSOrigin(value string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return ""
	}
	return "https://" + strings.ToLower(parsed.Host)
}

// 定义配置。
type Config struct {
	// 保存访问键标识。
	AccessKeyID string
	// 保存密钥访问键。
	SecretAccessKey string
	// 保存应用标识。
	AppID string
	// 保存地域。
	Region string
	// 保存名称。
	SpaceName string
	// 保存最大值文件。
	MaxFileBytes int64
	// 保存上传令牌有效时长。
	UploadTokenTTL time.Duration
	// ProcessingTimeout 是本次客户端使用的原片处理等待上限。
	ProcessingTimeout time.Duration
	// 保存封面域名。
	CoverDomain string
	// 保存后台任务使用的链路追踪器。
	Tracer trace.Tracer
}

type uploadClient interface {
	contextQueryClient
	// UploadStreamWithCallback 上传视频或封面素材流并写入回调透传参数，返回提交结果或平台错误。
	UploadStreamWithCallback(*materialStreamUploadRequest) (*response.VodCommitUploadInfoResponse, int, error)
}

type uploadAuthClient interface {
	// GetUploadAuthWithExpiredTime 使用官方默认上传策略，按配置有效期签发短期凭据。
	GetUploadAuthWithExpiredTime(time.Duration) (*base.SecurityToken2, error)
}

// materialStreamUploadRequest 是对 BytePlus SDK 流式素材上传入参的最小封装。
type materialStreamUploadRequest struct {
	Context                                                                             context.Context
	SpaceName, FileType, FileName, FileExtension, UploadSource, Functions, CallbackArgs string
	Content                                                                             io.Reader
	Size                                                                                int64
}

type contextQueryClient interface {
	// 执行当前组件的数据对应的业务处理。
	CtxQuery(context.Context, string, url.Values) ([]byte, int, error)
	// 执行需要表单 body 的可取消控制面请求。
	CtxPost(context.Context, string, url.Values, url.Values) ([]byte, int, error)
}

// 定义服务提供器。
type Provider struct {
	// 保存客户端。
	client uploadClient
	// 保存认证客户端。
	authClient uploadAuthClient
	// 保存应用标识。
	appID string
	// 保存地域。
	region string
	// 保存名称。
	spaceName string
	// 保存最大值文件。
	maxFileBytes int64
	// 保存上传令牌有效时长。
	uploadTokenTTL time.Duration
	// processingTimeout 用于任务创建时保存处理截止时间，不在请求时重新读取配置。
	processingTimeout time.Duration
	// 保存封面域名。
	coverDomain string
	// 保存当前组件使用的链路追踪器。
	tracer trace.Tracer
}

// 启动媒体平台并管理其运行状态。
func (p *Provider) startBytePlusSpan(ctx context.Context, operation string) (context.Context, trace.Span) {
	tracer := p.tracer
	if tracer == nil {
		tracer = tracenoop.NewTracerProvider().Tracer(bytePlusTracerName)
	}
	ctx, span := tracer.Start(ctx, "byteplus."+operation, trace.WithSpanKind(trace.SpanKindClient))
	span.SetAttributes(
		attribute.String("provider", "byteplus"),
		attribute.String("operation", operation),
	)
	return ctx, span
}

// 执行媒体平台对应的业务处理。
func finishBytePlusSpan(span trace.Span, status int, failed bool) {
	if !failed && (status == 0 || (status >= http.StatusOK && status < http.StatusMultipleChoices)) {
		span.SetAttributes(attribute.String("result", "success"))
		span.End()
		return
	}
	span.SetAttributes(
		attribute.String("result", "failure"),
		attribute.String("error.class", "dependency"),
	)
	span.SetStatus(codes.Error, "dependency")
	span.End()
}

// 执行媒体平台响应对应的业务处理。
func bytePlusResponseFailed(status int, err error, metadata *servicebase.ResponseMetadata) bool {
	return err != nil || status < http.StatusOK || status >= http.StatusMultipleChoices ||
		(metadata != nil && metadata.GetError().GetCode() != "")
}

type responseMetadataProvider interface {
	// 查询并返回指定的响应。
	GetResponseMetadata() *servicebase.ResponseMetadata
}

// 执行响应对应的业务处理。
func responseMetadata(response responseMetadataProvider) *servicebase.ResponseMetadata {
	return response.GetResponseMetadata()
}

// 校验配置并创建当前包提供的服务实例。
func New(config Config) (*Provider, error) {
	if strings.TrimSpace(config.AccessKeyID) == "" || strings.TrimSpace(config.SecretAccessKey) == "" ||
		strings.TrimSpace(config.AppID) == "" || strings.TrimSpace(config.SpaceName) == "" || config.Tracer == nil {
		return nil, ErrInvalidConfig
	}
	if strings.TrimSpace(config.Region) == "" {
		config.Region = base.RegionApSingapore
	}
	if config.Region != base.RegionApSingapore && config.Region != base.RegionApSouthEast1 {
		return nil, ErrInvalidConfig
	}
	if config.MaxFileBytes <= 0 {
		return nil, ErrInvalidConfig
	}
	if config.UploadTokenTTL <= 0 {
		config.UploadTokenTTL = time.Hour
	}
	if config.ProcessingTimeout == 0 {
		config.ProcessingTimeout = 30 * time.Minute
	}
	if config.ProcessingTimeout < time.Minute || config.ProcessingTimeout > 2*time.Hour {
		return nil, ErrInvalidConfig
	}
	config.CoverDomain = strings.TrimRight(strings.TrimSpace(config.CoverDomain), "/")
	if config.CoverDomain == "" {
		return nil, ErrInvalidConfig
	}
	if config.CoverDomain != "" && normalizedHTTPSOrigin(config.CoverDomain) == "" {
		return nil, ErrInvalidConfig
	}
	sdk := vod.NewInstanceWithRegion(strings.TrimSpace(config.Region))
	sdk.SetCredential(base.Credentials{
		AccessKeyID: strings.TrimSpace(config.AccessKeyID), SecretAccessKey: strings.TrimSpace(config.SecretAccessKey),
		Region: strings.TrimSpace(config.Region), Service: "vod",
	})
	client := &bytePlusSDKClient{Vod: sdk}
	return &Provider{
		client: client, authClient: client, appID: strings.TrimSpace(config.AppID), region: strings.TrimSpace(config.Region),
		spaceName: strings.TrimSpace(config.SpaceName), maxFileBytes: config.MaxFileBytes, uploadTokenTTL: config.UploadTokenTTL,
		coverDomain:       config.CoverDomain,
		processingTimeout: config.ProcessingTimeout,
		tracer:            config.Tracer,
	}, nil
}

// PublicationMediaScope 返回本实例本次客户端使用的真实上传区域和空间。
func (p *Provider) PublicationMediaScope(context.Context) (string, string, error) {
	return p.region, p.spaceName, nil
}

// ProcessingTimeout 返回任务应保存的固定等待上限。
func (p *Provider) ProcessingTimeout(context.Context) (time.Duration, error) {
	return p.processingTimeout, nil
}

// UploadAuthorization 沿用官方默认上传授权及配置有效期，不自行拼接资源策略；目标空间仍由上传配置指定。
func (p *Provider) UploadAuthorization(ctx context.Context) (operations.MediaUploadAuthorization, error) {
	if p == nil || p.authClient == nil || strings.TrimSpace(p.appID) == "" || strings.TrimSpace(p.region) == "" ||
		strings.TrimSpace(p.spaceName) == "" || p.maxFileBytes <= 0 || p.uploadTokenTTL <= 0 {
		return operations.MediaUploadAuthorization{}, operations.ErrInvalidInput
	}
	if err := ctx.Err(); err != nil {
		return operations.MediaUploadAuthorization{}, err
	}
	_, span := p.startBytePlusSpan(ctx, "GetUploadAuth")
	token, err := p.authClient.GetUploadAuthWithExpiredTime(p.uploadTokenTTL)
	if err != nil {
		finishBytePlusSpan(span, 0, true)
		return operations.MediaUploadAuthorization{}, fmt.Errorf("sign byteplus upload authorization: %w", err)
	}
	if token == nil || strings.TrimSpace(token.AccessKeyID) == "" || strings.TrimSpace(token.SecretAccessKey) == "" ||
		strings.TrimSpace(token.SessionToken) == "" {
		finishBytePlusSpan(span, 0, true)
		return operations.MediaUploadAuthorization{}, fmt.Errorf("sign byteplus upload authorization: empty token")
	}
	finishBytePlusSpan(span, 0, false)
	actions := []operations.MediaUploadAction{{Name: "GetMeta"}}
	return operations.MediaUploadAuthorization{
		Mode:         operations.MediaUploadModeDirect,
		AppID:        p.appID,
		SpaceName:    p.spaceName,
		Region:       p.region,
		MaxFileBytes: p.maxFileBytes,
		Token: operations.MediaUploadToken{
			AccessKeyID: token.AccessKeyID, SecretAccessKey: token.SecretAccessKey,
			SessionToken: token.SessionToken, ExpiredTime: token.ExpiredTime, CurrentTime: token.CurrentTime,
		},
		ProcessActions: actions,
	}, nil
}

// 执行上传任务封面对应的业务处理。
func (p *Provider) UploadCover(ctx context.Context, upload operations.Upload) (operations.CoverAsset, error) {
	if p == nil {
		return operations.CoverAsset{}, operations.ErrInvalidInput
	}
	limit := p.maxFileBytes
	if limit <= 0 || limit > operations.MaxCoverUploadBytes {
		limit = operations.MaxCoverUploadBytes
	}
	if p.client == nil || strings.TrimSpace(p.spaceName) == "" || upload.Reader == nil || upload.Size <= 0 ||
		upload.Size > limit || normalizedHTTPSOrigin(p.coverDomain) == "" {
		return operations.CoverAsset{}, operations.ErrInvalidInput
	}
	extension := strings.ToLower(filepath.Ext(filepath.Base(upload.FileName)))
	switch extension {
	case ".jpg", ".jpeg", ".png", ".webp":
	default:
		return operations.CoverAsset{}, operations.ErrInvalidInput
	}
	uploadFunctions := []business.VodUploadFunction{
		functions.AddOptionInfoFunc(business.VodUploadFunctionInput{
			Title: strings.TrimSuffix(filepath.Base(upload.FileName), extension), Category: consts.CategoryImage,
			RecordType: 2, Format: strings.TrimPrefix(extension, "."),
		}),
		functions.GetMetaFunc(),
	}
	functionBytes, err := json.Marshal(uploadFunctions)
	if err != nil {
		return operations.CoverAsset{}, err
	}
	spanContext, span := p.startBytePlusSpan(ctx, "UploadCover")
	result, status, err := p.client.UploadStreamWithCallback(&materialStreamUploadRequest{
		Context:   spanContext,
		SpaceName: p.spaceName, Content: &contextReader{ctx: spanContext, reader: io.LimitReader(upload.Reader, limit+1)},
		Size: upload.Size, FileType: consts.FileTypeImage, FileExtension: extension,
		UploadSource: "upload", Functions: string(functionBytes), CallbackArgs: strings.TrimSpace(upload.CallbackArgs),
	})
	if err != nil {
		finishBytePlusSpan(span, status, true)
		return operations.CoverAsset{}, fmt.Errorf("upload cover to byteplus: %w", err)
	}
	if status != http.StatusOK || result == nil || result.GetResult() == nil || result.GetResult().GetData() == nil {
		finishBytePlusSpan(span, status, true)
		return operations.CoverAsset{}, fmt.Errorf("upload cover to byteplus: unexpected status %d", status)
	}
	if metadata := result.GetResponseMetadata(); metadata != nil && metadata.GetError().GetCode() != "" {
		finishBytePlusSpan(span, status, true)
		return operations.CoverAsset{}, fmt.Errorf("upload cover to byteplus: %s", metadata.GetError().GetCode())
	}
	data := result.GetResult().GetData()
	asset := operations.CoverAsset{ID: strings.TrimSpace(data.GetMid()), MID: strings.TrimSpace(data.GetMid()), SpaceName: p.spaceName}
	if source := data.GetSourceInfo(); source != nil {
		asset.URL = coverAssetURL(p.coverDomain, source.GetStoreUri())
		asset.ProviderFileName = strings.TrimSpace(source.GetStoreUri())
	}
	if asset.ID == "" || asset.URL == "" || asset.ProviderFileName == "" {
		finishBytePlusSpan(span, status, true)
		return operations.CoverAsset{}, fmt.Errorf("upload cover to byteplus: response omitted material metadata")
	}
	finishBytePlusSpan(span, status, false)
	return asset, nil
}

// UploadEpisodeMedia 通过官方服务端流式上传视频，作为浏览器 multipart 初始化失败时的同一业务恢复路径。
func (p *Provider) UploadEpisodeMedia(ctx context.Context, upload operations.Upload) (operations.MediaAsset, error) {
	if p == nil || p.client == nil || p.maxFileBytes <= 0 || upload.Reader == nil || upload.Size <= 0 || upload.Size > p.maxFileBytes {
		return operations.MediaAsset{}, operations.ErrInvalidInput
	}
	fileName := filepath.Base(strings.TrimSpace(upload.FileName))
	extension := strings.ToLower(filepath.Ext(fileName))
	if fileName == "" || !supportedVideoExtension(extension) {
		return operations.MediaAsset{}, operations.ErrInvalidInput
	}
	functionsJSON, err := json.Marshal([]business.VodUploadFunction{
		functions.AddOptionInfoFunc(business.VodUploadFunctionInput{Title: strings.TrimSuffix(fileName, extension), Format: strings.TrimPrefix(extension, ".")}),
		functions.GetMetaFunc(),
	})
	if err != nil {
		slog.ErrorContext(ctx, "encode episode upload functions failed", "error", err)
		return operations.MediaAsset{}, err
	}
	spanContext, span := p.startBytePlusSpan(ctx, "UploadEpisodeMedia")
	result, status, err := p.client.UploadStreamWithCallback(&materialStreamUploadRequest{
		Context:   spanContext,
		SpaceName: p.spaceName, Content: &contextReader{ctx: spanContext, reader: io.LimitReader(upload.Reader, upload.Size+1)},
		// BytePlus 将 FileName 当作云端路径；只传扩展名可为不同剧集的同名文件生成独立路径。
		Size: upload.Size, FileType: "video", FileExtension: extension, UploadSource: "upload",
		Functions: string(functionsJSON), CallbackArgs: strings.TrimSpace(upload.CallbackArgs),
	})
	if err != nil {
		finishBytePlusSpan(span, status, true)
		return operations.MediaAsset{}, fmt.Errorf("upload episode to byteplus: %w", err)
	}
	if status != http.StatusOK || result == nil || result.GetResult() == nil || result.GetResult().GetData() == nil || result.GetResponseMetadata() == nil || result.GetResponseMetadata().GetError().GetCode() != "" {
		finishBytePlusSpan(span, status, true)
		return operations.MediaAsset{}, fmt.Errorf("upload episode to byteplus: unexpected response")
	}
	data := result.GetResult().GetData()
	asset := operations.MediaAsset{VID: strings.TrimSpace(data.GetVid()), UploadRequestID: strings.TrimSpace(result.GetResponseMetadata().GetRequestId())}
	if asset.VID == "" {
		finishBytePlusSpan(span, status, true)
		return operations.MediaAsset{}, fmt.Errorf("upload episode to byteplus: response omitted VID")
	}
	finishBytePlusSpan(span, status, false)
	return asset, nil
}

// supportedVideoExtension 与运营端入口允许的视频格式保持一致，避免把非视频内容交给 VOD。
func supportedVideoExtension(extension string) bool {
	switch extension {
	case ".mp4", ".flv", ".wmv", ".rm", ".rmvb", ".mpeg", ".mov", ".avi", ".mts", ".m4s", ".mkv":
		return true
	default:
		return false
	}
}

// FinalizeUpload 按 VID 确认原片所属空间与可用状态，第二个参数保留调用端上传请求合同。
func (p *Provider) FinalizeUpload(ctx context.Context, vid, _ string) (operations.MediaAsset, error) {
	vid = strings.TrimSpace(vid)
	if p == nil || p.client == nil || vid == "" || strings.Contains(vid, ",") {
		return operations.MediaAsset{}, operations.ErrInvalidInput
	}
	asset := operations.MediaAsset{VID: vid}
	// 上传完成只确认原片及空间身份。播放器适配转码由后续 TikTok 默认模板负责。
	spanContext, span := p.startBytePlusSpan(ctx, "GetMediaInfos")
	result := &response.VodGetMediaInfosResponse{}
	status, err := contextVODQuery(spanContext, p.client, "GetMediaInfos", url.Values{"Vids": {vid}}, result)
	finishBytePlusSpan(span, status, bytePlusResponseFailed(status, err, responseMetadata(result)))
	if err != nil {
		return asset, err
	}
	if status != http.StatusOK || result.GetResponseMetadata().GetError().GetCode() != "" || result.GetResult() == nil {
		return asset, fmt.Errorf("confirm byteplus upload: invalid media metadata response")
	}
	for _, missing := range result.GetResult().GetNotExistVids() {
		if missing == vid {
			return asset, operations.ErrMediaProcessing
		}
	}
	for _, info := range result.GetResult().GetMediaInfoList() {
		basic, source := info.GetBasicInfo(), info.GetSourceInfo()
		if basic.GetVid() != vid {
			continue
		}
		if p.spaceName != "" && basic.GetSpaceName() != p.spaceName {
			return asset, operations.ErrInvalidInput
		}
		if source.GetFileId() == "" || source.GetDuration() <= 0 {
			return asset, operations.ErrMediaProcessing
		}
		if err := p.publishAsset(ctx, vid); err != nil {
			return asset, err
		}
		asset.Status, asset.DurationSeconds = "ready", int(math.Ceil(float64(source.GetDuration())))
		// PosterUri 是 BytePlus 已有的官方视频封面；未生成封面不影响视频发布与播放。
		asset.PosterURL = coverAssetURL(p.coverDomain, basic.GetPosterUri())
		return asset, nil
	}
	return asset, operations.ErrMediaProcessing
}

// ObserveMedia 查询 VOD 的权威基础信息；只有 NotExistVids 才表示已确认不存在。
func (p *Provider) ObserveMedia(ctx context.Context, vid string) (operations.MediaObservation, error) {
	vid = strings.TrimSpace(vid)
	if p == nil || p.client == nil || vid == "" || strings.Contains(vid, ",") {
		return operations.MediaObservation{}, operations.ErrInvalidInput
	}
	if err := ctx.Err(); err != nil {
		return operations.MediaObservation{}, err
	}
	spanContext, span := p.startBytePlusSpan(ctx, "GetMediaInfos")
	result := &response.VodGetMediaInfosResponse{}
	status, err := contextVODQuery(spanContext, p.client, "GetMediaInfos", url.Values{"Vids": {vid}}, result)
	if err != nil {
		finishBytePlusSpan(span, status, true)
		return operations.MediaObservation{}, fmt.Errorf("observe byteplus media: %w", err)
	}
	if status != http.StatusOK || result == nil {
		finishBytePlusSpan(span, status, true)
		return operations.MediaObservation{}, fmt.Errorf("observe byteplus media: unexpected status %d", status)
	}
	if metadata := result.GetResponseMetadata(); metadata != nil && metadata.GetError().GetCode() != "" {
		finishBytePlusSpan(span, status, true)
		return operations.MediaObservation{}, fmt.Errorf("observe byteplus media: %s", metadata.GetError().GetCode())
	}
	finishBytePlusSpan(span, status, false)
	data := result.GetResult()
	if data == nil {
		return operations.MediaObservation{}, fmt.Errorf("observe byteplus media: response omitted result")
	}
	for _, missingVID := range data.GetNotExistVids() {
		if strings.TrimSpace(missingVID) == vid {
			return operations.MediaObservation{VID: vid, Exists: false, PublishStatus: operations.MediaProviderMissing}, nil
		}
	}
	for _, info := range data.GetMediaInfoList() {
		basic := info.GetBasicInfo()
		if basic == nil || strings.TrimSpace(basic.GetVid()) != vid {
			continue
		}
		return operations.MediaObservation{
			VID: vid, Exists: true, PublishStatus: strings.TrimSpace(basic.GetPublishStatus()),
			Title: strings.TrimSpace(basic.GetTitle()), Description: strings.TrimSpace(basic.GetDescription()),
			Tags:             append([]string(nil), basic.GetTags()...),
			ClassificationID: strconv.FormatInt(basic.GetClassification().GetClassificationId(), 10),
		}, nil
	}
	return operations.MediaObservation{}, fmt.Errorf("observe byteplus media: VID absent from both media and missing lists")
}

// ObserveMediaResource 查询删除任务指向的 Vid 或 Mid 是否仍存在。
// 图片/字幕素材没有按 Mid 查询的 OpenAPI，因此使用上传响应持久化的 StoreUri 调用
// ListFileMetaInfosByFileNames，并要求返回 MaterialId 与账本 Mid 完全一致。
func (p *Provider) ObserveMediaResource(ctx context.Context, asset operations.MediaResource) (operations.MediaResourceObservation, error) {
	asset.ResourceClass = strings.TrimSpace(asset.ResourceClass)
	asset.ProviderAssetID = strings.TrimSpace(asset.ProviderAssetID)
	if p == nil || p.client == nil || asset.ProviderAssetID == "" {
		return operations.MediaResourceObservation{}, operations.ErrInvalidInput
	}
	if asset.ResourceClass == operations.MediaResourceVideo {
		observation, err := p.ObserveMedia(ctx, asset.ProviderAssetID)
		if err != nil {
			return operations.MediaResourceObservation{}, err
		}
		return operations.MediaResourceObservation{
			ResourceClass: asset.ResourceClass, ProviderAssetID: asset.ProviderAssetID, Exists: observation.Exists,
		}, nil
	}
	if asset.ResourceClass != operations.MediaResourceMaterial || strings.TrimSpace(asset.SpaceName) == "" ||
		strings.TrimSpace(asset.ProviderFileName) == "" {
		return operations.MediaResourceObservation{}, operations.ErrInvalidInput
	}
	if err := ctx.Err(); err != nil {
		return operations.MediaResourceObservation{}, err
	}
	spanContext, span := p.startBytePlusSpan(ctx, "ListFileMetaInfosByFileNames")
	encodedFileName := strings.ReplaceAll(url.QueryEscape(strings.TrimSpace(asset.ProviderFileName)), "+", "%20")
	body, status, err := p.client.CtxPost(spanContext, "ListFileMetaInfosByFileNames", url.Values{}, url.Values{
		"SpaceName":        {strings.TrimSpace(asset.SpaceName)},
		"FileNameEncodeds": {encodedFileName},
	})
	result := &response.VodListFileMetaInfosByFileNamesResponse{}
	if err == nil {
		err = protojson.UnmarshalOptions{DiscardUnknown: true}.Unmarshal(body, result)
	}
	if err != nil {
		finishBytePlusSpan(span, status, true)
		return operations.MediaResourceObservation{}, fmt.Errorf("observe byteplus material: %w", err)
	}
	if status != http.StatusOK {
		finishBytePlusSpan(span, status, true)
		return operations.MediaResourceObservation{}, fmt.Errorf("observe byteplus material: unexpected status %d", status)
	}
	if metadata := result.GetResponseMetadata(); metadata != nil && metadata.GetError().GetCode() != "" {
		finishBytePlusSpan(span, status, true)
		return operations.MediaResourceObservation{}, fmt.Errorf("observe byteplus material: %s", metadata.GetError().GetCode())
	}
	finishBytePlusSpan(span, status, false)
	if result.GetResult() == nil {
		return operations.MediaResourceObservation{}, fmt.Errorf("observe byteplus material: response omitted result")
	}
	for _, item := range result.GetResult().GetVodFileMetaInfos() {
		if item == nil {
			continue
		}
		materialID := strings.TrimSpace(item.GetMaterialId())
		if materialID == asset.ProviderAssetID {
			return operations.MediaResourceObservation{
				ResourceClass: asset.ResourceClass, ProviderAssetID: asset.ProviderAssetID, Exists: true,
			}, nil
		}
		if materialID != "" {
			return operations.MediaResourceObservation{}, fmt.Errorf("observe byteplus material: file name resolved to a different material")
		}
	}
	return operations.MediaResourceObservation{
		ResourceClass: asset.ResourceClass, ProviderAssetID: asset.ProviderAssetID, Exists: false,
	}, nil
}

// 执行资源对应的业务处理。
func (p *Provider) publishAsset(ctx context.Context, vid string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	publishResponse, publishStatus, err := p.updateMediaPublishStatus(ctx, &request.VodUpdateMediaPublishStatusRequest{
		Vid: vid, Status: "Published",
	})
	if err != nil {
		return fmt.Errorf("publish byteplus media: %w", err)
	}
	if publishStatus != http.StatusOK || publishResponse == nil {
		return fmt.Errorf("publish byteplus media: unexpected status %d", publishStatus)
	}
	if metadata := publishResponse.GetResponseMetadata(); metadata != nil && metadata.GetError().GetCode() != "" {
		return fmt.Errorf("publish byteplus media: %s", metadata.GetError().GetCode())
	}
	return nil
}

// 删除指定的当前组件的数据。
func (p *Provider) Delete(ctx context.Context, resourceClass, spaceName, providerUploadID, providerFileName string) error {
	resourceClass = strings.TrimSpace(resourceClass)
	spaceName = strings.TrimSpace(spaceName)
	providerUploadID = strings.TrimSpace(providerUploadID)
	providerFileName = strings.TrimSpace(providerFileName)
	if p == nil || p.client == nil || providerUploadID == "" || strings.Contains(providerUploadID, ",") ||
		(resourceClass != operations.MediaResourceVideo && resourceClass != operations.MediaResourceMaterial) {
		return operations.ErrInvalidInput
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	action := "DeleteMedia"
	query := url.Values{"Vids": {providerUploadID}}
	var deleteResponse proto.Message = &response.VodDeleteMediaResponse{}
	if resourceClass == operations.MediaResourceMaterial {
		if spaceName == "" || providerFileName == "" {
			return operations.ErrInvalidInput
		}
		// BytePlus 没有按 Mid 删除普通素材的公开接口；官方 DeleteMediaTosFile
		// 使用上传响应中持久化的 StoreUri 删除图片或字幕文件。
		action = "DeleteMediaTosFile"
		query = url.Values{"FileNames": {mustJSONStrings([]string{providerFileName})}, "SpaceName": {spaceName}}
		deleteResponse = &response.VodDeleteMediaTosFileResponse{}
	}
	spanContext, span := p.startBytePlusSpan(ctx, action)
	var status int
	var err error
	if action == "DeleteMediaTosFile" {
		status, err = contextVODPost(spanContext, p.client, action, nil, nonEmptyQuery(query), deleteResponse)
	} else {
		status, err = contextVODQuery(spanContext, p.client, action, nonEmptyQuery(query), deleteResponse)
	}
	if err != nil {
		finishBytePlusSpan(span, status, true)
		return fmt.Errorf("delete byteplus media: %w", err)
	}
	if status != http.StatusOK {
		finishBytePlusSpan(span, status, true)
		return fmt.Errorf("delete byteplus media: unexpected status %d", status)
	}
	var providerCode string
	switch typed := deleteResponse.(type) {
	case *response.VodDeleteMediaResponse:
		providerCode = typed.GetResponseMetadata().GetError().GetCode()
	case *response.VodDeleteMediaTosFileResponse:
		providerCode = typed.GetResponseMetadata().GetError().GetCode()
		if typed.GetResult() != nil && len(typed.GetResult().GetFailedFileNames()) > 0 {
			providerCode = "DeleteMediaTosFileFailed"
		}
	}
	if providerCode != "" {
		finishBytePlusSpan(span, status, true)
		return fmt.Errorf("delete byteplus media: %s", providerCode)
	}
	if err := ctx.Err(); err != nil {
		finishBytePlusSpan(span, status, true)
		return err
	}
	finishBytePlusSpan(span, status, false)
	return nil
}

// 校验并更新媒体状态。
func (p *Provider) updateMediaPublishStatus(
	ctx context.Context,
	input *request.VodUpdateMediaPublishStatusRequest,
) (*response.VodUpdateMediaPublishStatusResponse, int, error) {
	spanContext, span := p.startBytePlusSpan(ctx, "UpdateMediaPublishStatus")
	output := &response.VodUpdateMediaPublishStatusResponse{}
	status, err := contextVODQuery(spanContext, p.client, "UpdateMediaPublishStatus", url.Values{
		"Vid": {input.GetVid()}, "Status": {input.GetStatus()},
	}, output)
	finishBytePlusSpan(span, status, bytePlusResponseFailed(status, err, responseMetadata(output)))
	return output, status, err
}

// 执行上下文对应的业务处理。
func contextVODQuery(
	ctx context.Context,
	client contextQueryClient,
	api string,
	query url.Values,
	output proto.Message,
) (int, error) {
	body, status, requestErr := client.CtxQuery(ctx, api, query)
	if len(body) == 0 {
		return status, requestErr
	}
	decodeErr := protojson.UnmarshalOptions{DiscardUnknown: true}.Unmarshal(body, output)
	if requestErr != nil || decodeErr != nil {
		return status, errors.Join(requestErr, decodeErr)
	}
	return status, nil
}

// contextVODPost 使用表单请求调用 BytePlus 控制面，并按官方 protobuf 合同解析响应。
func contextVODPost(
	ctx context.Context,
	client contextQueryClient,
	api string,
	query, form url.Values,
	output proto.Message,
) (int, error) {
	body, status, requestErr := client.CtxPost(ctx, api, query, form)
	if len(body) == 0 {
		return status, requestErr
	}
	decodeErr := protojson.UnmarshalOptions{DiscardUnknown: true}.Unmarshal(body, output)
	if requestErr != nil || decodeErr != nil {
		return status, errors.Join(requestErr, decodeErr)
	}
	return status, nil
}

// mustJSONStrings 把受服务端校验的字符串切片编码为 BytePlus 表单数组字段。
func mustJSONStrings(values []string) string {
	encoded, err := json.Marshal(values)
	if err != nil {
		return "[]"
	}
	return string(encoded)
}

// nonEmptyQuery 与 BytePlus SDK 的 protobuf 编码保持一致：未设置的可选字段不进入签名和查询串。
func nonEmptyQuery(query url.Values) url.Values {
	for key, values := range query {
		if len(values) == 0 || strings.TrimSpace(values[0]) == "" {
			query.Del(key)
		}
	}
	return query
}

// 校验并返回可公开访问的安全资源地址。
func publicHTTPSURL(value string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return ""
	}
	return parsed.String()
}

// 执行封面资源地址对应的业务处理。
func coverAssetURL(origin, storeURI string) string {
	origin = normalizedHTTPSOrigin(origin)
	storeURI = strings.TrimSpace(storeURI)
	separator := strings.IndexByte(storeURI, '/')
	if origin == "" || separator <= 0 || separator == len(storeURI)-1 {
		return ""
	}
	if strings.Contains(storeURI, "//") || strings.ContainsAny(storeURI, "\\?#\t\r\n ") {
		return ""
	}
	for _, segment := range strings.Split(storeURI, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return ""
		}
	}
	return origin + "/" + storeURI + "~tplv-vod-noop.image"
}

// 定义上下文读取流。
type contextReader struct {
	// 保存上下文。
	ctx context.Context
	// 保存读取流。
	reader io.Reader
}

// 在检查上下文取消状态后读取下一段媒体内容。
func (r *contextReader) Read(buffer []byte) (int, error) {
	select {
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	default:
		return r.reader.Read(buffer)
	}
}
