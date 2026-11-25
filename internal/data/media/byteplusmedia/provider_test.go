package byteplusmedia

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	byteplusbase "github.com/byteplus-sdk/byteplus-sdk-golang/base"
	servicebase "github.com/byteplus-sdk/byteplus-sdk-golang/service/base/models/base"
	"github.com/byteplus-sdk/byteplus-sdk-golang/service/vod/models/business"
	"github.com/byteplus-sdk/byteplus-sdk-golang/service/vod/models/request"
	"github.com/byteplus-sdk/byteplus-sdk-golang/service/vod/models/response"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"drama-server/internal/biz/operations"
)

type uploadClientStub struct {
	materialRequest       *materialStreamUploadRequest
	publishRequest        *request.VodUpdateMediaPublishStatusRequest
	deleteRequest         *request.VodDeleteMediaRequest
	materialDeleteFile    string
	materialDeleteSpace   string
	publishStatus         int
	publishErrorCode      string
	deleteStatus          int
	deleteErrorCode       string
	readyRequest          *request.VodGetPlayInfoRequest
	playInfoCalls         int
	playInfoCodecs        []string
	playInfoFormats       []string
	playInfoPending       int
	playInfoMissingCodec  string
	playInfoMissingFormat string
	playInfoStatus        int
	playInfoErrorCode     string
	playURL               string
	subtitleRequest       *request.VodGetSubtitleInfoListRequest
	subtitleResponse      *response.VodGetSubtitleInfoListResponse
	subtitleStatus        int
	subtitleStatusRequest *request.VodUpdateSubtitleStatusRequest
	subtitleInfoRequest   *request.VodUpdateSubtitleInfoRequest
	mediaInfoResponse     *response.VodGetMediaInfosResponse
	mediaInfoStatus       int
	materialInfoResponse  *response.VodListFileMetaInfosByFileNamesResponse
	materialInfoStatus    int
	materialInfoForm      url.Values
	mediaInfoUpdate       url.Values
	workflowStartQuery    url.Values
	workflowQuery         url.Values
	workflowRunID         string
	workflowStatus        string
	authError             error
	authExpiry            time.Duration
}

func (s *uploadClientStub) CtxPost(ctx context.Context, api string, _ url.Values, form url.Values) ([]byte, int, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	if api == "DeleteMediaTosFile" {
		s.materialDeleteSpace = form.Get("SpaceName")
		var names []string
		if err := json.Unmarshal([]byte(form.Get("FileNames")), &names); err != nil || len(names) != 1 {
			return nil, http.StatusBadRequest, fmt.Errorf("invalid material file names")
		}
		s.materialDeleteFile = names[0]
		output := &response.VodDeleteMediaTosFileResponse{ResponseMetadata: &servicebase.ResponseMetadata{}}
		body, err := protojson.Marshal(output)
		return body, http.StatusOK, err
	}
	if api != "ListFileMetaInfosByFileNames" {
		return nil, 0, fmt.Errorf("unexpected VOD POST API %s", api)
	}
	s.materialInfoForm = form
	status := s.materialInfoStatus
	if status == 0 {
		status = http.StatusOK
	}
	output := s.materialInfoResponse
	if output == nil {
		output = &response.VodListFileMetaInfosByFileNamesResponse{
			ResponseMetadata: &servicebase.ResponseMetadata{},
			Result:           &business.VodListFileMetaInfosByFileNamesResult{},
		}
	}
	body, err := protojson.Marshal(output)
	return body, status, err
}

type contextQueryUploadClient struct {
	*uploadClientStub
	apis []string
}

func TestProviderMarksVODBusinessErrorAsFailedSpan(t *testing.T) {
	exporter, tracer := newBytePlusTraceExporter(t)
	provider := &Provider{client: &uploadClientStub{deleteErrorCode: "ProviderRejected"}, tracer: tracer}
	if err := provider.Delete(context.Background(), operations.MediaResourceVideo, "short-drama", "byteplus-vid-sensitive", ""); err == nil {
		t.Fatal("BytePlus返回业务错误时删除必须失败")
	}
	assertBytePlusFailedSpan(t, exporter.GetSpans(), "byteplus.DeleteMedia")
}

func newBytePlusTraceExporter(t *testing.T) (*tracetest.InMemoryExporter, trace.Tracer) {
	t.Helper()
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	return exporter, provider.Tracer("test")
}

func assertBytePlusFailedSpan(t *testing.T, spans tracetest.SpanStubs, expectedName string) {
	t.Helper()
	if len(spans) != 1 || spans[0].Name != expectedName {
		t.Fatalf("期望一个失败的 %s span，实际为 %+v", expectedName, spans)
	}
	if spans[0].Status.Code != codes.Error || spanAttributeValue(spans[0].Attributes, "result") != "failure" ||
		spanAttributeValue(spans[0].Attributes, "error.class") != "dependency" {
		t.Fatalf("BytePlus业务失败必须使用固定安全错误属性：%+v", spans[0])
	}
	for _, item := range spans[0].Attributes {
		if strings.Contains(item.Value.String(), "byteplus-vid-sensitive") || strings.Contains(item.Value.String(), "signed-play-auth-token") ||
			strings.Contains(item.Value.String(), "ProviderRejected") {
			t.Fatalf("BytePlus失败 span 不得暴露敏感值：%+v", spans[0].Attributes)
		}
	}
}

func spanAttributeValue(attributes []attribute.KeyValue, key string) string {
	for _, item := range attributes {
		if string(item.Key) == key {
			return item.Value.AsString()
		}
	}
	return ""
}

// CtxQuery 模拟生产 Vod 客户端的可取消控制面请求，同时复用下方 stub 的响应配置和断言字段。
func (s *uploadClientStub) CtxQuery(ctx context.Context, api string, query url.Values) ([]byte, int, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	var output proto.Message
	var status int
	var requestErr error
	switch api {
	case "UpdateMediaPublishStatus":
		output, status, requestErr = s.UpdateMediaPublishStatus(&request.VodUpdateMediaPublishStatusRequest{
			Vid: query.Get("Vid"), Status: query.Get("Status"),
		})
	case "GetPlayInfo":
		output, status, requestErr = s.GetPlayInfo(&request.VodGetPlayInfoRequest{
			Vid: query.Get("Vid"), Format: query.Get("Format"), Codec: query.Get("Codec"), Definition: query.Get("Definition"),
			FileType: query.Get("FileType"), Ssl: query.Get("Ssl"), JSPlayer: query.Get("JSPlayer"),
			UnionInfo: query.Get("UnionInfo"), DrmKEK: query.Get("DrmKEK"),
			DrmExpireTimestamp: query.Get("DrmExpireTimestamp"),
		})
	case "DeleteMedia":
		output, status, requestErr = s.DeleteMedia(&request.VodDeleteMediaRequest{
			Vids: query.Get("Vids"), CallbackArgs: query.Get("CallbackArgs"),
		})
	case "GetMediaInfos":
		output = s.mediaInfoResponse
		status = s.mediaInfoStatus
		if status == 0 {
			status = 200
		}
	case "StartWorkflow":
		s.workflowStartQuery = cloneURLValues(query)
		output = &response.VodStartWorkflowResponse{
			ResponseMetadata: &servicebase.ResponseMetadata{},
			Result:           &business.VodStartWorkflowResult{RunId: s.workflowRunID},
		}
		status = http.StatusOK
	case "GetWorkflowExecution":
		s.workflowQuery = cloneURLValues(query)
		output = &response.VodGetWorkflowExecutionStatusResponse{
			ResponseMetadata: &servicebase.ResponseMetadata{},
			Result:           &business.WorkflowExecution{RunId: s.workflowRunID, Status: s.workflowStatus},
		}
		status = http.StatusOK
	case "UpdateMediaInfo":
		s.mediaInfoUpdate = cloneURLValues(query)
		output = &response.VodUpdateMediaInfoResponse{ResponseMetadata: &servicebase.ResponseMetadata{}}
		status = http.StatusOK
	default:
		return nil, 0, fmt.Errorf("unexpected VOD API %s", api)
	}
	body, marshalErr := protojson.Marshal(output)
	return body, status, errors.Join(requestErr, marshalErr)
}

func cloneURLValues(values url.Values) url.Values {
	cloned := make(url.Values, len(values))
	for key, items := range values {
		cloned[key] = append([]string(nil), items...)
	}
	return cloned
}

func TestProviderRestoresAuthoritativeVODMetadata(t *testing.T) {
	client := &uploadClientStub{}
	provider := &Provider{client: client, spaceName: "short-drama"}
	metadata := operations.VODEpisodeMetadata{
		VID: "vid-1", Title: "测试短剧 - 第001集 - 相遇", Description: "权威简介",
		Tags:                      []string{"drama-managed", "tt-drama-abcd", "tt-tag-21"},
		PrimaryClassificationName: "悬疑推理 · TT-TAG-21", PrimaryClassificationKey: "TT-TAG-21",
		DramaClassificationName: "测试短剧 · TT-DRAMA-abcd", DramaClassificationKey: "TT-DRAMA-abcd",
	}
	err := provider.SyncVODEpisodeMetadata(context.Background(), metadata, operations.MediaObservation{
		VID: "vid-1", Exists: true, Title: "控制台标题", Description: "控制台简介",
		Tags: []string{"external"}, ClassificationID: "2101",
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.mediaInfoUpdate.Get("Vid") != "vid-1" ||
		client.mediaInfoUpdate.Get("Title") != "控制台标题" || client.mediaInfoUpdate.Get("Description") != metadata.Description ||
		client.mediaInfoUpdate.Get("Tags") != strings.Join(metadata.Tags, ",") || client.mediaInfoUpdate.Get("ClassificationId") != "" {
		t.Fatalf("VOD metadata did not preserve original title or sync metadata: media=%v", client.mediaInfoUpdate)
	}
}

func TestProviderSkipsMediaUpdateWhenAuthoritativeMetadataMatches(t *testing.T) {
	client := &uploadClientStub{}
	provider := &Provider{client: client, spaceName: "short-drama"}
	metadata := operations.VODEpisodeMetadata{
		VID: "vid-1", Title: "测试短剧 - 第001集", Description: "权威简介",
		Tags:                      []string{"drama-managed", "tt-tag-21"},
		PrimaryClassificationName: "悬疑推理 · TT-TAG-21", PrimaryClassificationKey: "TT-TAG-21",
		DramaClassificationName: "测试短剧 · TT-DRAMA-abcd", DramaClassificationKey: "TT-DRAMA-abcd",
	}
	err := provider.SyncVODEpisodeMetadata(context.Background(), metadata, operations.MediaObservation{
		VID: "vid-1", Exists: true, Title: metadata.Title, Description: metadata.Description,
		Tags: []string{"tt-tag-21", "drama-managed"}, ClassificationID: "2101",
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.mediaInfoUpdate != nil {
		t.Fatalf("matching metadata must not trigger writes: media=%v", client.mediaInfoUpdate)
	}
}

func TestProviderObservesPublishedAndConfirmedMissingMedia(t *testing.T) {
	for name, testCase := range map[string]struct {
		response *response.VodGetMediaInfosResponse
		exists   bool
		status   string
	}{
		"published": {
			response: &response.VodGetMediaInfosResponse{ResponseMetadata: &servicebase.ResponseMetadata{}, Result: &business.VodGetMediaInfosData{
				MediaInfoList: []*business.VodMediaInfo{{BasicInfo: &business.VodMediaBasicInfo{
					Vid: "vid-1", PublishStatus: "Published", Title: "权威标题", Description: "权威简介",
					Tags: []string{"drama-managed", "tt-tag-21"}, Classification: &business.VodClassification{ClassificationId: 2101},
				}}},
			}},
			exists: true, status: operations.MediaProviderPublished,
		},
		"missing": {
			response: &response.VodGetMediaInfosResponse{ResponseMetadata: &servicebase.ResponseMetadata{}, Result: &business.VodGetMediaInfosData{
				NotExistVids: []string{"vid-1"},
			}},
			exists: false, status: operations.MediaProviderMissing,
		},
	} {
		t.Run(name, func(t *testing.T) {
			provider := &Provider{client: &uploadClientStub{mediaInfoResponse: testCase.response}}
			observation, err := provider.ObserveMedia(context.Background(), "vid-1")
			if err != nil {
				t.Fatal(err)
			}
			if observation.Exists != testCase.exists || observation.PublishStatus != testCase.status || observation.VID != "vid-1" {
				t.Fatalf("unexpected media observation: %+v", observation)
			}
			if testCase.exists && (observation.Title != "权威标题" || observation.Description != "权威简介" || observation.ClassificationID != "2101") {
				t.Fatalf("media observation omitted business metadata: %+v", observation)
			}
		})
	}
}

func TestProviderObservesMaterialByPersistedStoreURI(t *testing.T) {
	client := &uploadClientStub{materialInfoResponse: &response.VodListFileMetaInfosByFileNamesResponse{
		ResponseMetadata: &servicebase.ResponseMetadata{},
		Result: &business.VodListFileMetaInfosByFileNamesResult{VodFileMetaInfos: []*business.VodFileMetaInfo{{
			MaterialId: "mid-1", FileName: "covers/cover 1.png", FileType: "MaterialUri",
		}}},
	}}
	provider := &Provider{client: client}
	asset := operations.MediaResource{
		ResourceClass: operations.MediaResourceMaterial, ProviderAssetID: "mid-1",
		SpaceName: "short-drama", ProviderFileName: "covers/cover 1.png",
	}
	observation, err := provider.ObserveMediaResource(context.Background(), asset)
	if err != nil || !observation.Exists || observation.ProviderAssetID != "mid-1" {
		t.Fatalf("素材存在性观察错误：observation=%+v err=%v", observation, err)
	}
	if client.materialInfoForm.Get("SpaceName") != "short-drama" ||
		client.materialInfoForm.Get("FileNameEncodeds") != "covers%2Fcover%201.png" {
		t.Fatalf("素材查询未使用账本 StoreUri：%v", client.materialInfoForm)
	}

	client.materialInfoResponse = &response.VodListFileMetaInfosByFileNamesResponse{
		ResponseMetadata: &servicebase.ResponseMetadata{}, Result: &business.VodListFileMetaInfosByFileNamesResult{},
	}
	observation, err = provider.ObserveMediaResource(context.Background(), asset)
	if err != nil || observation.Exists {
		t.Fatalf("空权威查询应确认素材不存在：observation=%+v err=%v", observation, err)
	}
}

func (client *contextQueryUploadClient) CtxQuery(ctx context.Context, api string, _ url.Values) ([]byte, int, error) {
	client.apis = append(client.apis, api)
	if api == "UpdateMediaPublishStatus" {
		body, err := protojson.Marshal(&response.VodUpdateMediaPublishStatusResponse{
			ResponseMetadata: &servicebase.ResponseMetadata{},
		})
		return body, 200, err
	}
	<-ctx.Done()
	return nil, 0, ctx.Err()
}

func (s *uploadClientStub) GetUploadAuthWithExpiredTime(expiry time.Duration) (*byteplusbase.SecurityToken2, error) {
	s.authExpiry = expiry
	if s.authError != nil {
		return nil, s.authError
	}
	return &byteplusbase.SecurityToken2{
		AccessKeyID: "temporary-ak", SecretAccessKey: "temporary-sk", SessionToken: "temporary-session",
		CurrentTime: "2026-07-14T00:00:00Z", ExpiredTime: "2026-07-14T01:00:00Z",
	}, nil
}

func (s *uploadClientStub) DeleteMedia(input *request.VodDeleteMediaRequest) (*response.VodDeleteMediaResponse, int, error) {
	s.deleteRequest = input
	status := s.deleteStatus
	if status == 0 {
		status = 200
	}
	metadata := &servicebase.ResponseMetadata{}
	if s.deleteErrorCode != "" {
		metadata.Error = &servicebase.ResponseError{Code: s.deleteErrorCode, Message: "delete failed"}
	}
	return &response.VodDeleteMediaResponse{ResponseMetadata: metadata}, status, nil
}

func (s *uploadClientStub) UploadStreamWithCallback(input *materialStreamUploadRequest) (*response.VodCommitUploadInfoResponse, int, error) {
	s.materialRequest = input
	return &response.VodCommitUploadInfoResponse{
		ResponseMetadata: &servicebase.ResponseMetadata{RequestId: "cover-request-1"},
		Result: &business.VodCommitUploadInfoResult{Data: &business.VodCommitUploadInfoData{
			Vid: "episode-vid-1", Mid: "material-1", SourceInfo: &business.VodSourceInfo{StoreUri: "tos-vod-test/covers/cover.png"},
		}},
	}, 200, nil
}

func (s *uploadClientStub) UpdateMediaPublishStatus(input *request.VodUpdateMediaPublishStatusRequest) (*response.VodUpdateMediaPublishStatusResponse, int, error) {
	s.publishRequest = input
	status := s.publishStatus
	if status == 0 {
		status = 200
	}
	metadata := &servicebase.ResponseMetadata{}
	if s.publishErrorCode != "" {
		metadata.Error = &servicebase.ResponseError{Code: s.publishErrorCode, Message: "publish failed"}
	}
	return &response.VodUpdateMediaPublishStatusResponse{ResponseMetadata: metadata}, status, nil
}

func (s *uploadClientStub) GetPlayInfo(input *request.VodGetPlayInfoRequest) (*response.VodGetPlayInfoResponse, int, error) {
	s.readyRequest = input
	s.playInfoCalls++
	s.playInfoCodecs = append(s.playInfoCodecs, input.GetCodec())
	s.playInfoFormats = append(s.playInfoFormats, input.GetFormat())
	status := s.playInfoStatus
	if status == 0 {
		status = 200
	}
	metadata := &servicebase.ResponseMetadata{}
	if s.playInfoErrorCode != "" {
		metadata.Error = &servicebase.ResponseError{Code: s.playInfoErrorCode, Message: "playback unavailable"}
	}
	result := &business.VodPlayInfoModel{}
	if s.playInfoCalls > s.playInfoPending && !strings.EqualFold(input.GetCodec(), s.playInfoMissingCodec) &&
		!strings.EqualFold(input.GetFormat(), s.playInfoMissingFormat) {
		result.PlayInfoList = []*business.VodPlayInfo{{MainPlayUrl: s.playURL}}
	}
	return &response.VodGetPlayInfoResponse{ResponseMetadata: metadata, Result: result}, status, nil
}

func TestProviderUploadsCoverAsImageMaterial(t *testing.T) {
	t.Parallel()

	client := &uploadClientStub{}
	provider := &Provider{
		client: client, spaceName: "short-drama", maxFileBytes: 20 << 20,
		coverDomain: "https://cover.drama.test",
	}
	content := []byte("\x89PNG\r\n\x1a\ncover")
	asset, err := provider.UploadCover(context.Background(), operations.Upload{
		FileName: "cover.png", Size: int64(len(content)), Reader: bytes.NewReader(content),
	})
	if err != nil {
		t.Fatal(err)
	}
	if asset.ID != "material-1" || asset.URL != "https://cover.drama.test/tos-vod-test/covers/cover.png~tplv-vod-noop.image" {
		t.Fatalf("unexpected cover asset: %+v", asset)
	}
	if client.materialRequest == nil || client.materialRequest.SpaceName != "short-drama" ||
		client.materialRequest.FileType != "image" || client.materialRequest.FileExtension != ".png" ||
		!strings.Contains(client.materialRequest.Functions, `"Category":"image"`) ||
		!strings.Contains(client.materialRequest.Functions, `"RecordType":2`) {
		t.Fatalf("unexpected image material request: %+v", client.materialRequest)
	}
}

func TestProviderUploadsEpisodeWithGeneratedObjectPath(t *testing.T) {
	t.Parallel()

	client := &uploadClientStub{}
	provider := &Provider{client: client, spaceName: "short-drama", maxFileBytes: 20 << 20}
	content := []byte("video")
	asset, err := provider.UploadEpisodeMedia(context.Background(), operations.Upload{
		FileName: "24.mp4", Size: int64(len(content)), Reader: bytes.NewReader(content),
	})
	if err != nil {
		t.Fatal(err)
	}
	if asset.VID != "episode-vid-1" {
		t.Fatalf("视频上传未返回 VID：%+v", asset)
	}
	if client.materialRequest == nil || client.materialRequest.FileName != "" || client.materialRequest.FileExtension != ".mp4" ||
		!strings.Contains(client.materialRequest.Functions, `"Title":"24"`) {
		t.Fatalf("同名剧集须由 VOD 生成独立路径，同时保留原文件标题：%+v", client.materialRequest)
	}
}

func TestCoverAssetURLRejectsMalformedStoreURI(t *testing.T) {
	t.Parallel()

	for _, storeURI := range []string{"", "missing-bucket", "bucket/../secret", "bucket/file.png?token=bad", "bucket//file.png"} {
		if value := coverAssetURL("https://cover.drama.test", storeURI); value != "" {
			t.Fatalf("store URI %q produced unsafe URL %q", storeURI, value)
		}
	}
}

func TestProviderIssuesConfiguredBytePlusWebUploadAuthorization(t *testing.T) {
	t.Parallel()

	client := &uploadClientStub{}
	provider := &Provider{
		client: client, authClient: client, appID: "1038811", region: "ap-southeast-1",
		spaceName: "short-drama", maxFileBytes: 20 << 30, uploadTokenTTL: time.Hour,
	}
	authorization, err := provider.UploadAuthorization(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if authorization.Mode != operations.MediaUploadModeDirect || authorization.AppID != "1038811" ||
		authorization.SpaceName != "short-drama" || authorization.Region != "ap-southeast-1" ||
		authorization.Token.AccessKeyID != "temporary-ak" || authorization.Token.SecretAccessKey != "temporary-sk" ||
		authorization.Token.SessionToken != "temporary-session" || len(authorization.ProcessActions) != 1 {
		t.Fatal("官方上传授权未保留既有响应字段")
	}
	if client.authExpiry != time.Hour || authorization.MaxFileBytes != 20<<30 {
		t.Fatalf("temporary upload settings were not preserved: expiry=%s max=%d", client.authExpiry, authorization.MaxFileBytes)
	}
	if authorization.ProcessActions[0].Name != "GetMeta" {
		t.Fatal("上传只探测原片，官方转码在发布流程发起")
	}
}

// uploadedSourceFixture 是官方 GetMediaInfos 原片已存在的最小测试证据。
func uploadedSourceFixture(vid, space string) *response.VodGetMediaInfosResponse {
	return &response.VodGetMediaInfosResponse{Result: &business.VodGetMediaInfosData{MediaInfoList: []*business.VodMediaInfo{{
		BasicInfo:  &business.VodMediaBasicInfo{Vid: vid, SpaceName: space, PosterUri: "poster-bucket/episode.jpg"},
		SourceInfo: &business.VodSourceInfo{FileId: "source-1", Duration: 60.5, Format: "mp4"},
	}}}}
}

func TestProviderFinalizesSourceWithoutWaitingForCustomTranscode(t *testing.T) {
	t.Parallel()
	client := &uploadClientStub{mediaInfoResponse: uploadedSourceFixture("direct-vid-1", "drama")}
	provider := &Provider{client: client, spaceName: "drama", coverDomain: "https://cover.drama.test"}
	asset, err := provider.FinalizeUpload(context.Background(), "direct-vid-1", "upload-1")
	if err != nil || asset.Status != "ready" || asset.DurationSeconds != 61 || asset.VID != "direct-vid-1" ||
		asset.PosterURL != "https://cover.drama.test/poster-bucket/episode.jpg~tplv-vod-noop.image" {
		t.Fatalf("原片确认失败：%+v %v", asset, err)
	}
	if client.playInfoCalls != 0 {
		t.Fatal("原片上传不得等待自定义 HLS 或 MP4 转码输出")
	}
	if client.publishRequest == nil || client.publishRequest.Vid != asset.VID {
		t.Fatal("BytePlus 视频未发布供 TikTok 登记")
	}
}

func TestProviderAllowsReadyMediaWithoutOfficialPoster(t *testing.T) {
	t.Parallel()
	fixture := uploadedSourceFixture("direct-vid-1", "drama")
	fixture.Result.MediaInfoList[0].BasicInfo.PosterUri = ""
	client := &uploadClientStub{mediaInfoResponse: fixture}
	provider := &Provider{client: client, spaceName: "drama", coverDomain: "https://cover.drama.test"}
	asset, err := provider.FinalizeUpload(context.Background(), "direct-vid-1", "upload-1")
	if err != nil || asset.Status != "ready" || asset.PosterURL != "" {
		t.Fatalf("官方海报缺失不应阻塞视频 ready：asset=%+v err=%v", asset, err)
	}
	if client.publishRequest == nil || client.publishRequest.Vid != "direct-vid-1" {
		t.Fatal("视频仍应按既有流程发布")
	}
}

func TestProviderKeepsMissingSourceProcessingAndRejectsWrongSpace(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		info *response.VodGetMediaInfosResponse
		want error
	}{
		{"missing source", &response.VodGetMediaInfosResponse{Result: &business.VodGetMediaInfosData{NotExistVids: []string{"vid-1"}}}, operations.ErrMediaProcessing},
		{"missing metadata", &response.VodGetMediaInfosResponse{Result: &business.VodGetMediaInfosData{MediaInfoList: []*business.VodMediaInfo{{BasicInfo: &business.VodMediaBasicInfo{Vid: "vid-1", SpaceName: "drama"}}}}}, operations.ErrMediaProcessing},
		{"wrong space", uploadedSourceFixture("vid-1", "other-space"), operations.ErrInvalidInput},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &uploadClientStub{mediaInfoResponse: test.info}
			_, err := (&Provider{client: client, spaceName: "drama"}).FinalizeUpload(context.Background(), "vid-1", "upload-1")
			if !errors.Is(err, test.want) {
				t.Fatalf("result=%v want=%v", err, test.want)
			}
			if client.publishRequest != nil || client.playInfoCalls != 0 {
				t.Fatal("未确认原片不得发布或查询播放地址")
			}
		})
	}
}

func TestProviderFinalizationRequestsHonorContextDeadline(t *testing.T) {
	t.Parallel()

	client := &contextQueryUploadClient{uploadClientStub: &uploadClientStub{}}
	provider := &Provider{client: client}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	startedAt := time.Now()

	_, err := provider.FinalizeUpload(ctx, "direct-vid-1", "upload-request-1")

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("finalization must stop at the request deadline, got %v", err)
	}
	if time.Since(startedAt) > 250*time.Millisecond {
		t.Fatalf("finalization ignored its request deadline")
	}
	if strings.Join(client.apis, ",") != "GetMediaInfos" {
		t.Fatalf("context-aware VOD calls were not used: %+v", client.apis)
	}
}

func TestProviderRejectsFailedSourceMetadataRequest(t *testing.T) {
	t.Parallel()
	client := &uploadClientStub{mediaInfoStatus: 403, mediaInfoResponse: &response.VodGetMediaInfosResponse{}}
	_, err := (&Provider{client: client}).FinalizeUpload(context.Background(), "vid-1", "upload-1")
	if err == nil || errors.Is(err, operations.ErrMediaProcessing) {
		t.Fatalf("错误响应不能视为处理就绪：%v", err)
	}
	if client.publishRequest != nil {
		t.Fatal("元数据错误时不得发布视频")
	}
}

func TestProviderDeletesCompleteMediaByVID(t *testing.T) {
	t.Parallel()

	client := &uploadClientStub{}
	provider := &Provider{client: client}
	if err := provider.Delete(context.Background(), operations.MediaResourceVideo, "short-drama", "byteplus-vid-1", ""); err != nil {
		t.Fatal(err)
	}
	if client.deleteRequest == nil || client.deleteRequest.Vids != "byteplus-vid-1" {
		t.Fatalf("unexpected delete request: %+v", client.deleteRequest)
	}
}

func TestProviderDeletesMaterialByStoreURIAndOwningSpace(t *testing.T) {
	t.Parallel()

	client := &uploadClientStub{}
	provider := &Provider{client: client, spaceName: "current-space"}
	if err := provider.Delete(context.Background(), operations.MediaResourceMaterial, "asset-space", "mid-1", "covers/cover-1.png"); err != nil {
		t.Fatal(err)
	}
	if client.materialDeleteFile != "covers/cover-1.png" || client.materialDeleteSpace != "asset-space" {
		t.Fatalf("unexpected material delete request: file=%q space=%q", client.materialDeleteFile, client.materialDeleteSpace)
	}
}

func TestProviderRejectsDeleteResponseWithVODError(t *testing.T) {
	t.Parallel()

	provider := &Provider{client: &uploadClientStub{deleteErrorCode: "InternalError"}}
	if err := provider.Delete(context.Background(), operations.MediaResourceVideo, "short-drama", "byteplus-vid-1", ""); err == nil {
		t.Fatal("expected VOD delete failure")
	}
}

func TestProviderRejectsUploadWhenMediaCannotBePublished(t *testing.T) {
	t.Parallel()

	provider := &Provider{client: &uploadClientStub{publishStatus: 500, mediaInfoResponse: uploadedSourceFixture("byteplus-vid-1", "short-drama")}, spaceName: "short-drama", maxFileBytes: 1024}
	_, err := provider.FinalizeUpload(context.Background(), "byteplus-vid-1", "request-1")
	if err == nil {
		t.Fatal("expected publish failure")
	}
}

func TestProviderRejectsPublishResponseWithVODError(t *testing.T) {
	t.Parallel()

	provider := &Provider{client: &uploadClientStub{publishErrorCode: "InternalError"}, spaceName: "short-drama", maxFileBytes: 1024}
	_, err := provider.FinalizeUpload(context.Background(), "byteplus-vid-1", "request-1")
	if err == nil {
		t.Fatal("expected VOD publish business error")
	}
}
