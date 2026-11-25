package operations_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
	"drama-server/internal/testsupport/memory"
)

// publicationLoggingRepository 在真实内存仓储边界注入查询或创建失败，保留未失败操作的原有行为。
type publicationLoggingRepository struct {
	operations.TikTokPublicationRepository
	findErr, startErr, updateErr error
	started                      bool
}

// FindTikTokPublicationByDramaID 返回指定错误，未注入时由内存仓储读取历史任务。
func (r *publicationLoggingRepository) FindTikTokPublicationByDramaID(ctx context.Context, id string) (operations.TikTokPublication, error) {
	if r.findErr != nil {
		return operations.TikTokPublication{SubjectID: "1"}, r.findErr
	}
	return r.TikTokPublicationRepository.FindTikTokPublicationByDramaID(ctx, id)
}

// StartTikTokPublication 记录创建是否被调用，指定错误时不改变任务或短剧状态。
func (r *publicationLoggingRepository) StartTikTokPublication(ctx context.Context, p *operations.TikTokPublication) error {
	r.started = true
	if r.startErr != nil {
		return r.startErr
	}
	return r.TikTokPublicationRepository.StartTikTokPublication(ctx, p)
}

// RestartTikTokPublication 注入恢复写入故障，验证冻结内容恢复在业务边界记录实际原因。
func (r *publicationLoggingRepository) RestartTikTokPublication(ctx context.Context, previous operations.TikTokPublication, next *operations.TikTokPublication) error {
	if r.startErr != nil {
		return r.startErr
	}
	return r.TikTokPublicationRepository.RestartTikTokPublication(ctx, previous, next)
}

// TestPublicationSubmissionLogsExpectedAndUnexpectedFailures 验证正常首次发布、缺少拥有方及实际数据库故障的日志级别和错误传播。
func TestPublicationSubmissionLogsExpectedAndUnexpectedFailures(t *testing.T) {
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })
	readFailure := errors.New("SELECT tiktok_publications: connection refused")
	writeFailure := errors.New("INSERT tiktok_publications: transaction aborted")
	for _, test := range []struct {
		name         string
		findErr      error
		startErr     error
		missingOwner bool
		wantErr      error
		wantStart    bool
		wantMessage  string
		wantLevel    string
	}{
		{name: "首次发布成功不误报", wantStart: true, wantMessage: "no previous TikTok publication", wantLevel: "DEBUG"},
		{name: "包装的未找到仍允许首次发布", findErr: fmt.Errorf("publication lookup: %w", domain.ErrNotFound), wantStart: true, wantMessage: "no previous TikTok publication", wantLevel: "DEBUG"},
		{name: "查询失败保留诊断且不创建", findErr: readFailure, wantErr: readFailure, wantMessage: "find TikTok publication by drama id failed", wantLevel: "ERROR"},
		{name: "创建失败保留诊断和原错误", startErr: writeFailure, wantErr: writeFailure, wantStart: true, wantMessage: "start TikTok publication failed", wantLevel: "ERROR"},
		{name: "新版任务竞争属于业务拒绝", startErr: operations.ErrConflict, wantErr: operations.ErrConflict, wantStart: true, wantMessage: "start TikTok publication failed", wantLevel: "WARN"},
		{name: "缺少拥有方属于业务拒绝", missingOwner: true, wantErr: operations.ErrPublicationNotReady, wantMessage: "submit TikTok publication rejected", wantLevel: "WARN"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			slog.SetDefault(slog.New(slog.NewJSONHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug})))
			drama := domain.Drama{ID: "logging-drama", Title: "发布日志", Description: "测试简介", CoverURL: "https://private.example.test/cover", PublishStatus: "draft", TagIDs: []string{"1"}, Episodes: []domain.Episode{{ID: "logging-episode", DramaID: "logging-drama", Number: 1, VID: "private-video", MediaStatus: "ready"}}}
			owner := application.Application{SubjectID: "1", TikTokAppID: "owner", ClientKey: "private-client-key", ClientSecret: "private-client-secret", BytePlusAccountID: "owner-account", BytePlusSpaceName: "owner-space", Status: application.StatusActive}
			store := memory.NewStore(memory.Seed{
				Applications: []application.Application{owner},
				Dramas:       []domain.Drama{drama},
			})
			repositories := memory.OperationsRepositories(store)
			// 模拟独立全局配置提供凭据，普通应用查询按合同会清空 ClientSecret。
			repositories.Content.OwnerApplication = func(context.Context, string) (application.Application, error) { return owner, nil }
			publicationRepo := &publicationLoggingRepository{TikTokPublicationRepository: store, findErr: test.findErr, startErr: test.startErr}
			repositories.Content.Publications = publicationRepo
			if test.missingOwner {
				repositories.Content.OwnerApplicationID = nil
			}
			service := operations.NewService(repositories, nil)
			if err := service.ConfigureTikTokPublications(func(application.Application) operations.TikTokMediaProvider { return &publicationProviderStub{} }, operations.PublicationJobConfig{WorkerID: "logging-worker"}); err != nil {
				t.Fatal(err)
			}
			publication, err := submitPreparedPublication(t, service, context.Background(), drama.ID, operations.TikTokPublicationInput{Priority: "normal"})
			if !errors.Is(err, test.wantErr) || publicationRepo.started != test.wantStart {
				t.Fatalf("错误传播或创建行为改变：error=%v started=%v", err, publicationRepo.started)
			}
			if test.wantErr == nil && (publication == nil || publication.Status != operations.PublicationStatusQueued) {
				t.Fatal("首次发布没有创建排队任务")
			}
			logs := output.String()
			if !strings.Contains(logs, test.wantMessage) || !strings.Contains(logs, `"level":"`+test.wantLevel+`"`) {
				t.Fatalf("缺少预期操作或日志级别：%s", logs)
			}
			if test.wantErr != nil && !strings.Contains(logs, test.wantErr.Error()) {
				t.Fatalf("实际错误详情丢失：%s", logs)
			}
			if test.wantLevel != "ERROR" && strings.Contains(logs, `"level":"ERROR"`) {
				t.Fatalf("正常创建或业务拒绝误报故障：%s", logs)
			}
			for _, sensitive := range []string{drama.CoverURL, "private-client-secret", "private-video"} {
				if strings.Contains(logs, sensitive) {
					t.Fatal("发布日志泄漏凭据或媒资信息")
				}
			}
		})
	}
}

// UpdateTikTokPublication 注入并发冲突或实际写入失败，未注入时保持原事务行为。
func (r *publicationLoggingRepository) UpdateTikTokPublication(ctx context.Context, p operations.TikTokPublication, d *domain.Drama) error {
	if r.updateErr != nil {
		return r.updateErr
	}
	return r.TikTokPublicationRepository.UpdateTikTokPublication(ctx, p, d)
}

// TestPublicationQueueConflictLogs 验证送审与上线的新版竞争拒绝有实际错误日志，成功和业务拒绝不误报故障或泄漏凭据。
func TestPublicationQueueConflictLogs(t *testing.T) {
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })
	for _, tc := range []struct {
		name    string
		err     error
		level   string
		release bool
	}{
		{name: "送审入队成功"},
		{name: "迟到旧版本被拒绝", err: operations.ErrConflict, level: "WARN"},
		{name: "事务写入失败", err: errors.New("UPDATE tiktok_publications: transaction aborted"), level: "ERROR"},
		{name: "上线入队成功", release: true},
		{name: "旧版上线被拒绝", release: true, err: operations.ErrConflict, level: "WARN"},
		{name: "上线事务写入失败", release: true, err: errors.New("UPDATE tiktok_publications: transaction aborted"), level: "ERROR"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
			store := memory.NewStore(memory.Seed{Dramas: []domain.Drama{{ID: "review-log"}}})
			repos := memory.OperationsRepositories(store)
			owner := application.Application{SubjectID: "1", ID: "owner", TikTokAppID: "owner", ClientKey: "private-key", ClientSecret: "private-secret", BytePlusAccountID: "account", BytePlusSpaceName: "space", Status: application.StatusActive}
			repos.Content.OwnerApplication = func(context.Context, string) (application.Application, error) { return owner, nil }
			p := operations.TikTokPublication{SubjectID: "1", DramaID: "review-log", OwnerApplicationID: owner.ID, OwnerScope: operations.PublicationOwnerScope{ClientKey: owner.ClientKey, AccountID: owner.BytePlusAccountID, SpaceName: owner.BytePlusSpaceName}, Status: operations.PublicationStatusSynced, Stage: operations.PublicationStageAwaitSubmitReview, PlatformAlbumID: "album", PlatformVersion: 1, ContentSnapshot: &domain.Drama{CoverURL: "https://private.example.test/cover"}}
			if tc.release {
				p.Status, p.Stage, p.ReviewStatus = operations.PublicationStatusApproved, operations.PublicationStageAwaitPublish, 2
				p.ContentSnapshot.Episodes = []domain.Episode{{Number: 1}}
				p.EpisodeIDsBySequence = map[int]string{1: "episode"}
			}
			store.SeedTikTokPublication(&p)
			repos.Content.Publications = &publicationLoggingRepository{TikTokPublicationRepository: store, updateErr: tc.err}
			service := operations.NewService(repos, nil)
			if err := service.ConfigureTikTokPublications(func(application.Application) operations.TikTokMediaProvider { return &publicationProviderStub{} }, operations.PublicationJobConfig{WorkerID: "review-log"}); err != nil {
				t.Fatal(err)
			}

			var result *operations.TikTokPublication
			var err error
			message := "begin synchronous official operation failed"
			if tc.release {
				result, err = service.PublishTikTokPublication(context.Background(), p.DramaID, p.ID)
				message = "change official release failed"
			} else {
				result, err = service.SubmitTikTokReview(context.Background(), p.DramaID, operations.TikTokPublicationInput{Priority: "normal"})
			}
			if !errors.Is(err, tc.err) {
				t.Fatalf("错误传播改变：%v", err)
			}
			logs := output.String()
			if tc.err == nil {
				if result == nil || (result.Status != operations.PublicationStatusUnderReview && result.Status != operations.PublicationStatusOnline) || strings.Contains(logs, `"level":"ERROR"`) || strings.Contains(logs, `"level":"WARN"`) {
					t.Fatal("成功路径误报或未入队")
				}
			} else if !strings.Contains(logs, message) || !strings.Contains(logs, tc.err.Error()) || !strings.Contains(logs, `"level":"`+tc.level+`"`) {
				t.Fatalf("失败诊断不完整：%s", logs)
			}
			for _, sensitive := range []string{owner.ClientSecret, owner.ClientKey, p.ContentSnapshot.CoverURL} {
				if strings.Contains(logs, sensitive) {
					t.Fatal("日志泄漏凭据或媒体地址")
				}
			}
		})
	}
}

// TestRetainedPublicationMediaRetryLogs 验证无版本历史任务恢复的故障日志、敏感字段保护及成功路径。
func TestRetainedPublicationMediaRetryLogs(t *testing.T) {
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })
	failure := errors.New("INSERT tiktok_publications: transaction aborted")
	for _, tc := range []struct {
		name          string
		err           error
		level         string
		ownerDisabled bool
	}{
		{name: "成功"},
		{name: "竞争", err: operations.ErrConflict, level: "WARN"},
		{name: "写入故障", err: failure, level: "ERROR"},
		{name: "拥有方未就绪", err: operations.ErrPublicationNotReady, level: "WARN", ownerDisabled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			owner := application.Application{SubjectID: "1", ID: "owner", ClientKey: "private-key", ClientSecret: "private-secret", BytePlusAccountID: "account", BytePlusSpaceName: "space", Status: application.StatusActive}
			frozen := domain.Drama{ID: "201", Title: "固定内容", CoverURL: "https://private.example.test/cover", Episodes: []domain.Episode{{ID: "301", VID: "vid"}}}
			store := memory.NewStore(memory.Seed{Dramas: []domain.Drama{frozen}})
			p := operations.TikTokPublication{SubjectID: "1", DramaID: "201", OwnerApplicationID: owner.ID, OwnerScope: operations.PublicationOwnerScope{ClientKey: owner.ClientKey, AccountID: owner.BytePlusAccountID, SpaceName: owner.BytePlusSpaceName}, Status: operations.PublicationStatusFailed, Stage: operations.PublicationStageTranscodeVideos, ErrorCode: "transcode_failed", ContentSnapshot: &frozen}
			store.SeedTikTokPublication(&p)
			repos := memory.OperationsRepositories(store)
			if tc.ownerDisabled {
				owner.ClientSecret = ""
			}
			repos.Content.OwnerApplication = func(context.Context, string) (application.Application, error) { return owner, nil }
			repos.Content.Publications = &publicationLoggingRepository{TikTokPublicationRepository: store, startErr: tc.err}
			svc := operations.NewService(repos, nil)
			if err := svc.ConfigureTikTokPublications(func(application.Application) operations.TikTokMediaProvider { return &publicationProviderStub{} }, operations.PublicationJobConfig{WorkerID: "retry-log"}); err != nil {
				t.Fatal(err)
			}
			next, err := svc.RetryTikTokPublication(context.Background(), "201")
			if !errors.Is(err, tc.err) {
				t.Fatal("恢复改变错误传播", err)
			}
			output := logs.String()
			if tc.err != nil {
				message := "restart TikTok media task failed"
				if tc.ownerDisabled {
					message = "resolve media restart owner failed"
				}
				if !strings.Contains(output, message) || !strings.Contains(output, tc.err.Error()) || !strings.Contains(output, tc.level) {
					t.Fatal("恢复日志缺少操作、详情或级别")
				}
			} else if next == nil || strings.Contains(output, "ERROR") {
				t.Fatal("成功恢复误报故障或回填版本")
			}
			for _, secret := range []string{owner.ClientKey, owner.ClientSecret, frozen.CoverURL} {
				if secret != "" && strings.Contains(output, secret) {
					t.Fatal("恢复日志泄露敏感字段")
				}
			}
		})
	}
}

// publicationContentLoggingRepository 在当前草稿读取处注入故障，其他存储合同保持不变。
type publicationContentLoggingRepository struct {
	operations.CatalogRepository
	err error
}

// FindDrama 返回注入的真实错误，或读取当前编辑内容。
func (r publicationContentLoggingRepository) FindDrama(ctx context.Context, id string) (*domain.Drama, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.CatalogRepository.FindDrama(ctx, id)
}

// TestPublicationCurrentContentLogs 验证待办对比的读取故障保留诊断，成功不输出错误或敏感内容。
func TestPublicationCurrentContentLogs(t *testing.T) {
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })
	failure := errors.New("SELECT dramas: connection refused")
	for _, errValue := range []error{nil, failure} {
		var logs bytes.Buffer
		slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
		drama := domain.Drama{ID: "publication-content-log", Title: "当前内容", PublishStatus: "ready", CoverURL: "https://private.example/cover"}
		store := memory.NewStore(memory.Seed{Dramas: []domain.Drama{drama}})
		store.SeedTikTokPublication(&operations.TikTokPublication{SubjectID: "1", ID: "task", DramaID: drama.ID, ContentSnapshot: &drama, Status: operations.PublicationStatusSynced})
		repositories := memory.OperationsRepositories(store)
		repositories.Content.Catalog = publicationContentLoggingRepository{CatalogRepository: store, err: errValue}
		service := operations.NewService(repositories, nil)
		result, err := service.FindTikTokPublication(context.Background(), drama.ID)
		if !errors.Is(err, errValue) {
			t.Fatal("读取故障传播改变", err)
		}
		output := logs.String()
		if errValue != nil {
			if result != nil || !strings.Contains(output, "read current content for publication failed") || !strings.Contains(output, failure.Error()) || !strings.Contains(output, `"level":"ERROR"`) {
				t.Fatal("读取故障缺少实际错误或错误结果", output)
			}
		} else if result == nil || result.CurrentContentChanged || output != "" {
			t.Fatal("同内容比较或成功日志异常", output)
		}
		if strings.Contains(output, drama.CoverURL) {
			t.Fatal("内容比较日志泄露媒体地址")
		}
	}
}

// BeginTikTokPublicationCall 在同步请求占用版本时注入竞争或写入故障。
func (r *publicationLoggingRepository) BeginTikTokPublicationCall(ctx context.Context, p *operations.TikTokPublication) error {
	if r.updateErr != nil {
		return r.updateErr
	}
	return r.TikTokPublicationRepository.(operations.TikTokExecutionRepository).BeginTikTokPublicationCall(ctx, p)
}

// FindTikTokReleaseState 保留组合仓储的已选版本读取能力。
func (r *publicationLoggingRepository) FindTikTokReleaseState(ctx context.Context, id string) (*operations.TikTokReleaseState, error) {
	return r.TikTokPublicationRepository.(operations.TikTokReleaseRepository).FindTikTokReleaseState(ctx, id)
}

// ExecuteTikTokRelease 保留同步版本选择和上下架门禁。
func (r *publicationLoggingRepository) ExecuteTikTokRelease(ctx context.Context, id string, input operations.TikTokReleaseInput, call operations.TikTokReleaseCall) (*operations.TikTokPublication, error) {
	if r.updateErr != nil {
		return nil, r.updateErr
	}
	return r.TikTokPublicationRepository.(operations.TikTokReleaseRepository).ExecuteTikTokRelease(ctx, id, input, call)
}
