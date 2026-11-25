package operations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"drama-server/internal/biz/operations"
)

func TestOtherHandlersValidateBeforeService(t *testing.T) {
	// 空业务依赖加单条入口日志断言，防止参数先落到 Service 才被拒绝。
	h := &Handler{}
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })
	for _, test := range []struct {
		name        string
		handle      http.HandlerFunc
		body, query string
		paths       map[string]string
	}{
		{name: "记录类别无效", handle: h.listPublicationRecords, paths: map[string]string{"kind": "anything"}},
		{name: "记录分页超限", handle: h.listPublicationRecords, query: "?pageSize=101", paths: map[string]string{"kind": "reviews"}},
		{name: "记录状态无效", handle: h.listPublicationRecords, query: "?status=bad", paths: map[string]string{"kind": "reviews"}},
		{name: "记录ID无效", handle: h.publicationRecordAction, paths: map[string]string{"kind": "reviews", "action": "publish", "recordID": "1abc"}},
		{name: "记录动作跨流程", handle: h.publicationRecordAction, paths: map[string]string{"kind": "releases", "action": "withdraw", "recordID": "1"}},
		{name: "创建应用缺字段", handle: h.createApplication, body: `{}`},
		{name: "更新应用缺字段", handle: h.updateApplication, body: `{}`, paths: map[string]string{"applicationID": "5001"}},
		{name: "消费设置缺广告位", handle: h.updateApplicationMonetization, body: `{"freeEpisodeCount":1}`},
		{name: "解锁集数不能为零", handle: h.updateApplicationMonetization, body: `{"freeEpisodeCount":1,"episodeUnlockAdUnitCode":"rewarded","unlockEpisodeCount":0}`},
		{name: "每日上限不能为负数", handle: h.updateApplicationMonetization, body: `{"freeEpisodeCount":1,"episodeUnlockAdUnitCode":"rewarded","dailyRewardLimit":-1}`},
		{name: "解锁集数不能超出范围", handle: h.updateApplicationMonetization, body: `{"freeEpisodeCount":1,"episodeUnlockAdUnitCode":"rewarded","unlockEpisodeCount":10001}`},
		{name: "每日上限不能超出范围", handle: h.updateApplicationMonetization, body: `{"freeEpisodeCount":1,"episodeUnlockAdUnitCode":"rewarded","dailyRewardLimit":10001}`},
		{name: "解锁集数不能为小数", handle: h.updateApplicationMonetization, body: `{"freeEpisodeCount":1,"episodeUnlockAdUnitCode":"rewarded","unlockEpisodeCount":1.5}`},
		{name: "消费设置负数", handle: h.updateApplicationMonetization, body: `{"freeEpisodeCount":-1,"episodeUnlockAdUnitCode":"rewarded"}`},
		{name: "创建活动缺目标", handle: h.createCampaign, body: `{"name":"Launch","status":"active"}`},
		{name: "更新活动缺目标", handle: h.updateCampaign, body: `{"name":"Launch","status":"active"}`, paths: map[string]string{"campaignID": "campaign-a"}},
		{name: "创建空分集草稿仍要求剧名", handle: h.createDrama, body: `{"title":" ","description":"简介","episodes":[]}`},
		{name: "创建短剧缺少简介", handle: h.createDrama, body: `{"title":"Drama","episodes":[]}`},
		{name: "创建短剧简介只有空格", handle: h.createDrama, body: `{"title":"Drama","description":"   ","episodes":[]}`},
		{name: "更新短剧缺少简介", handle: h.updateDrama, body: `{"title":"Drama","episodes":[]}`, paths: map[string]string{"dramaID": "5010"}},
		{name: "更新短剧缺分集", handle: h.updateDrama, body: `{"title":"Drama","description":"简介"}`, paths: map[string]string{"dramaID": "5010"}},
		{name: "创建短剧不能指定分集ID", handle: h.createDrama, body: `{"title":"Drama","description":"简介","episodes":[{"id":"5016","title":"One","number":1}]}`},
		{name: "送审优先级无效", handle: h.submitTikTokReview, body: `{"priority":"invalid"}`, paths: map[string]string{"dramaID": "5010"}},
		{name: "送审禁止客户端指定拥有方和授权范围", handle: h.submitTikTokReview, body: `{"ownerApplicationId":"5001","targetApplicationIds":[" "]}`, paths: map[string]string{"dramaID": "5010"}},
		{name: "上传声明类型错误", handle: h.reserveEpisodeMediaUpload, body: `{"fileName":"video.exe","size":12}`, paths: map[string]string{"episodeID": "5016"}},
		{name: "直传回执缺幂等键", handle: h.completeDirectEpisodeMedia, body: `{"uploadId":"5005","vid":"vid-a","fileName":"video.mp4","size":12}`, paths: map[string]string{"episodeID": "5016"}},
		{name: "奖励报表范围必须明确", handle: h.rewardAnalysis, query: "?from=2026-01-01&to=2026-01-01"},
		{name: "奖励报表单应用必须有上下文", handle: h.rewardAnalysis, query: "?scope=application&from=2026-01-01&to=2026-01-01"},
		{name: "奖励报表拒绝非法范围", handle: h.rewardAnalysis, query: "?scope=all&range=invalid&from=2026-01-01&to=2026-01-01"},
		{name: "奖励报表日期超限", handle: h.rewardAnalysis, query: "?scope=all&from=2026-01-01&to=2026-05-01"},
		{name: "奖励趋势拒绝历史范围", handle: h.rewardTrend, query: "?scope=all&range=all&dimension=drama&targetId=201&from=2026-01-01&to=2026-01-01"},
		{name: "奖励趋势维度非法", handle: h.rewardTrend, query: "?scope=all&dimension=invalid&targetId=201&from=2026-01-01&to=2026-01-01"},
		{name: "分集剧标识非法", handle: h.rewardEpisodes, paths: map[string]string{"dramaID": "201abc"}},
		{name: "无效投放排行范围", handle: h.dashboard, query: "?from=2026-01-01&to=2026-01-01&rankingRange=unknown"},
		{name: "无效仪表盘日期", handle: h.dashboard, query: "?from=2026-02-30&to=2026-03-01"},
		{name: "回传日志事件无效", handle: h.tiktokPostbackLogs, query: "?event=unknown"},
		{name: "回传日志状态无效", handle: h.tiktokPostbackLogs, query: "?outcome=pending"},
		{name: "回传日志分页超限", handle: h.tiktokPostbackLogs, query: "?pageSize=101"},
		{name: "回传日志场景无效", handle: h.tiktokPostbackLogs, query: "?scene=arbitrary"},
		{name: "回传日志构建版本无效", handle: h.tiktokPostbackLogs, query: "?clientVersion=x%7Cy"},
		{name: "回传日志错误码无效", handle: h.tiktokPostbackLogs, query: "?errorCode=secret"},
		{name: "回传日志日期不完整", handle: h.tiktokPostbackLogs, query: "?from=2026-10-01"},
		{name: "审计数量非数字", handle: h.auditLogs, query: "?limit=many"},
		{name: "审计数量为零", handle: h.auditLogs, query: "?limit=0"},
		{name: "审计数量超上限", handle: h.auditLogs, query: "?limit=201"},
		{name: "应用路径为空", handle: h.updateApplication, body: `{}`},
		{name: "查询短剧路径为空", handle: h.findDrama},
		{name: "拒绝数字后缀隐式转换", handle: h.findDrama, paths: map[string]string{"dramaID": "1abc"}},
		{name: "拒绝自定义实体主键", handle: h.mediaJob, paths: map[string]string{"jobID": "job_123"}},
		{name: "拒绝超出数据库主键上限", handle: h.findDrama, paths: map[string]string{"dramaID": "18446744073709551616"}},
		{name: "删除短剧路径为空", handle: h.deleteDrama},
		{name: "查询发布路径为空", handle: h.findTikTokPublication},
		{name: "手动查询审核路径为空", handle: h.refreshTikTokReview},
		{name: "手动上线短剧路径为空", handle: h.publishTikTokPublication},
		{name: "手动上线任务标识为空", handle: h.publishTikTokPublication, paths: map[string]string{"dramaID": "5010"}},
		{name: "重试发布路径为空", handle: h.retryTikTokPublication},
		{name: "下架路径为空", handle: h.unpublishDrama},
		{name: "查询媒资任务路径为空", handle: h.listDramaMediaJobs},
		{name: "任务详情路径为空", handle: h.mediaJob},
		{name: "重试任务路径为空", handle: h.retryMediaJob},
		{name: "多余JSON值", handle: h.createApplication, body: `{} {}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			var logs bytes.Buffer
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			r := httptest.NewRequest(http.MethodPost, "/"+test.query, strings.NewReader(test.body))
			for key, value := range test.paths {
				r.SetPathValue(key, value)
			}
			w := httptest.NewRecorder()
			test.handle(w, r)
			if w.Code != 400 {
				t.Fatalf("入口应返回400，实际%d：%s", w.Code, w.Body.String())
			}
			var record map[string]any
			if err := json.Unmarshal(bytes.TrimSpace(logs.Bytes()), &record); err != nil {
				t.Fatalf("应只有入口拒绝日志：%v %s", err, logs.String())
			}
			if record["error"] == nil || record["msg"] == "operations request failed" {
				t.Fatalf("参数未经入口校验：%s", logs.String())
			}
			for _, key := range []string{"flow", "stage", "outcome", "error_type"} {
				if _, ok := record[key]; ok {
					t.Fatalf("错误日志含冗余字段 %s", key)
				}
			}
		})
	}
}

type failingUploadReader struct{ err error }

func (r failingUploadReader) Read([]byte) (int, error) { return 0, r.err }

func TestUploadValidatorsPreserveReadErrors(t *testing.T) {
	failure := errors.New("upload stream interrupted")
	for _, test := range []struct {
		name     string
		validate func(operations.Upload) (operations.Upload, error)
		filename string
	}{
		{"封面", validatedCoverUpload, "cover.png"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.validate(operations.Upload{FileName: test.filename, Size: 12, Reader: failingUploadReader{failure}})
			if !errors.Is(err, failure) {
				t.Fatalf("文件读取错误被丢失：%v", err)
			}
		})
	}
}

func TestVODInputParsingAndFallbackLogs(t *testing.T) {
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })
	var logs bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	body := []byte(`{"RequestId":"retry-a","Version":"1.0","EventType":"FileUploadComplete","EventTime":"2026-09-08T09:00:00+08:00","Data":{"Vid":"9007199254740993","CallbackArgs":"{"}}`)
	event, err := parseVODCallback(context.Background(), body, "current")
	if err != nil {
		t.Fatalf("回调降级或原始载荷被改变：%+v %v", event, err)
	}
	if !strings.Contains(logs.String(), "unexpected end of JSON input") {
		t.Fatalf("忽略 CallbackArgs 时丢失实际错误：%s", logs.String())
	}
	for _, body := range []string{`{`, `{"RequestId":"x","Version":"1.0","EventTime":"bad"}`, `{"RequestId":"x","Version":"1.0","EventTime":"2026-09-08T00:00:00Z","EventType":"FileUploadComplete"}`} {
		if _, err := parseVODCallback(context.Background(), []byte(body), "current"); err == nil {
			t.Fatal("无效回调字段应被拒绝")
		}
	}
	logs.Reset()
	ignored, err := parseVODCallback(context.Background(), []byte(`{"RequestId":"x","Version":"1.0","EventTime":"2026-09-08T00:00:00Z","EventType":"OtherEvent"}`), "current")
	if err != nil || ignored.EventType != "OtherEvent" || logs.Len() != 0 {
		t.Fatalf("未知已验签事件应继续被安全忽略：%+v %v %s", ignored, err, logs.String())
	}
}
