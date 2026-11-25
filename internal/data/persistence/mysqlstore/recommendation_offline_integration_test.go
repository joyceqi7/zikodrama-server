package mysqlstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"drama-server/internal/biz/adminrbac"
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/consumer"
	"drama-server/internal/biz/directplay"
	"drama-server/internal/biz/homepage"
	"drama-server/internal/biz/operations"
)

// recommendationOfflineProvider 仅记录本进程中的官方下架，禁止访问真实平台。
type recommendationOfflineProvider struct {
	operations.TikTokMediaProvider
	delists atomic.Int32
}

// SetAlbumListed 记录下架是否真的发出，便于断言推荐校验失败没有平台副作用。
func (p *recommendationOfflineProvider) SetAlbumListed(_ context.Context, _ string, listed bool) error {
	if !listed {
		p.delists.Add(1)
	}
	return nil
}

// seedRecommendationPublication 准备平台已确认上线的任务，测试服务继续使用真实 MySQL 事务。
func seedRecommendationPublication(t *testing.T, f *iaaFixture) (*operations.Service, *recommendationOfflineProvider) {
	t.Helper()
	ctx := application.WithSubject(context.Background(), "1")
	drama, err := f.store.FindDrama(ctx, "201")
	if err != nil || drama.Published == nil {
		t.Fatal("缺少官方线上夹具", err)
	}
	owner, err := f.store.FindApplication(ctx, "101")
	if err != nil {
		t.Fatal(err)
	}
	owner.ClientSecret, owner.BytePlusAccountID, owner.BytePlusSpaceName = "fixture-secret", "fixture-account", "fixture-space"
	snapshot := consumer.FreezeContent(*drama.Published)
	now := time.Now().UTC()
	p := operations.TikTokPublication{SubjectID: "1", DramaID: "201", OwnerApplicationID: "101", OwnerScope: operations.PublicationOwnerScope{ClientKey: owner.ClientKey, AccountID: owner.BytePlusAccountID, SpaceName: owner.BytePlusSpaceName}, ContentSnapshot: &snapshot,
		ReviewStatus: 2, Status: operations.PublicationStatusOnline, Stage: operations.PublicationStageDone, PlatformAlbumID: "fixture-album", PlatformVersion: 1, OnlineAt: &now, CreatedAt: now, UpdatedAt: now, NextAttemptAt: now}
	p.EpisodeIDsBySequence = map[int]string{}
	for _, ep := range drama.Published.Episodes {
		p.EpisodeIDsBySequence[ep.Number] = ep.PlatformEpisodeID
	}
	model, err := newTikTokPublicationModel(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.data.DB().Create(&model).Error; err != nil {
		t.Fatal(err)
	}
	p.ID = model.ID
	if err := syncPublicationRecords(f.store.data.DB(), p); err != nil {
		t.Fatal(err)
	}
	service := operations.NewService(operations.Repositories{
		Application: operations.ApplicationPorts{Subjects: f.store, Registry: f.store}, Monetization: operations.MonetizationPorts{Settings: f.store}, Campaign: operations.CampaignPorts{Repository: f.store}, Media: operations.MediaPorts{Jobs: f.store},
		Content:   operations.ContentPorts{Catalog: f.store, Publications: f.store, Records: f.store, Tags: f.store, Distributions: f.store, OwnerApplication: func(context.Context, string) (application.Application, error) { return owner, nil }},
		Reporting: operations.ReportingPorts{Audits: f.store, Metrics: f.store},
	}, nil)
	provider := &recommendationOfflineProvider{}
	if err := service.ConfigureTikTokPublications(func(application.Application) operations.TikTokMediaProvider { return provider }, operations.PublicationJobConfig{WorkerID: "recommendation-offline", PollInterval: time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	return service, provider
}

// removeRecommendation 在当前修订下移除完整栏目，不使用隐藏替代移除。
func removeRecommendation(t *testing.T, store *Store, ctx context.Context, hot bool) {
	t.Helper()
	read, replace := store.GetCarousel, store.ReplaceCarousel
	if hot {
		read, replace = store.GetHot, store.ReplaceHot
	}
	current, err := read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := replace(ctx, homepage.Carousel{Revision: current.Revision, DramaIDs: []string{}}); err != nil {
		t.Fatal(err)
	}
}

// TestRecommendationOfflineGuardIntegration 验证两渠道的轮播、隐藏热播、旧字段和渠道隔离，拒绝不产生版本或任务写入。
func TestRecommendationOfflineGuardIntegration(t *testing.T) {
	for _, direct := range []bool{false, true} {
		t.Run(fmt.Sprintf("direct=%v", direct), func(t *testing.T) {
			f := newIAAFixture(t)
			repository, directCtx := seedDirect(t, f)
			submitDirect(t, repository, "publish", "")
			official, provider := seedRecommendationPublication(t, f)
			f.operations = official
			f.direct = directplay.NewService(repository, nil)
			server := httptest.NewServer(f.handler(t))
			defer server.Close()
			login := iaaRequest(t, server.URL, "POST", "/admin/v1/session", "", "", map[string]string{"username": "iaa-admin", "password": "LocalIAA123!"}, 200)
			var admin adminrbac.LoginResult
			if err := json.Unmarshal(login, &admin); err != nil {
				t.Fatal(err)
			}
			ctx, other := application.WithSubject(context.Background(), "1"), directCtx
			if direct {
				ctx, other = directCtx, application.WithSubject(context.Background(), "1")
			}
			// 另一渠道同样推荐该剧，只影响它自己的下线门禁。
			if _, err := f.store.ReplaceCarousel(other, homepage.Carousel{DramaIDs: []string{"201"}}); err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.ReplaceCarousel(ctx, homepage.Carousel{DramaIDs: []string{"201"}}); err != nil {
				t.Fatal(err)
			}
			w, err := repository.Workbench(ctx, "201")
			if err != nil {
				t.Fatal(err)
			}
			before, err := f.store.FindTikTokPublicationByDramaID(ctx, "201")
			if err != nil {
				t.Fatal(err)
			}
			command := directplay.Command{RequestID: "recommendation-offline", ExpectedRevision: w.Publication.Revision, Kind: "offline"}
			attempt := func() error {
				if direct {
					_, err := repository.Accept(ctx, "201", command, "fixture")
					return err
				}
				_, err := official.QueueTikTokUnpublish(ctx, "201")
				return err
			}
			assertRejected := func() {
				t.Helper()
				if err := attempt(); !errors.Is(err, homepage.ErrDramaRecommended) {
					t.Fatal("推荐中仍可下线", err)
				}
				// 真实 HTTP 入口也必须拒绝，不能由页面自行判断后绕过权威门禁。
				if direct {
					iaaRequest(t, server.URL, "POST", "/admin/v1/direct/dramas/201/publication/operations", admin.Token, "", command, 409)
				} else {
					iaaRequest(t, server.URL, "POST", "/admin/v1/dramas/201/unpublish", admin.Token, "", nil, 409)
				}
				current, err := repository.Workbench(ctx, "201")
				if err != nil || current.Publication != w.Publication {
					t.Fatal("拒绝改变直连指针或修订", err)
				}
				task, err := f.store.FindTikTokPublicationByDramaID(ctx, "201")
				if err != nil || task.Revision != before.Revision || task.Status != before.Status || task.Stage != before.Stage {
					t.Fatal("拒绝改变官方任务", err)
				}
				var count int64
				if err := f.store.data.DB().Model(&directPublicationOperationModel{}).Where("request_key = ?", command.RequestID).Count(&count).Error; err != nil || count != 0 {
					t.Fatal("拒绝仍保存幂等操作", err)
				}
				if provider.delists.Load() != 0 {
					t.Fatal("拒绝触发真实下架步骤")
				}
			}
			assertRejected() // 历史纯 ID 轮播。
			if _, err := f.store.ReplaceHot(ctx, homepage.Carousel{Items: []homepage.CarouselItem{{DramaID: "201", Visible: false}}}); err != nil {
				t.Fatal(err)
			}
			assertRejected() // 同时存在轮播与隐藏热播。
			removeRecommendation(t, f.store, ctx, false)
			assertRejected() // 只移除轮播后，隐藏热播仍须移除。
			removeRecommendation(t, f.store, ctx, true)
			if err := attempt(); err != nil {
				t.Fatal("完整移除后无法下线，或另一渠道引用串扰", err)
			}
			if direct {
				repeat, err := repository.Accept(ctx, "201", command, "fixture")
				if err != nil || repeat.Status != "succeeded" {
					t.Fatal("移除后下线重试幂等失效", err)
				}
				if _, err := f.store.ReplaceHot(ctx, homepage.Carousel{Revision: 2, DramaIDs: []string{"201"}}); !errors.Is(err, consumer.ErrNotFound) {
					t.Fatal("已下线剧目重新加入推荐", err)
				}
			} else {
				current, err := f.store.GetCarousel(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := f.store.ReplaceCarousel(ctx, homepage.Carousel{Revision: current.Revision, DramaIDs: []string{"201"}}); !errors.Is(err, homepage.ErrConflict) && !errors.Is(err, consumer.ErrNotFound) {
					t.Fatal("官方下架排队后仍可新增推荐", err)
				}
			}
		})
	}
}

// TestRecommendationOfflineConcurrentIntegration 验证推荐新增与直连下线同用剧目锁，任何完成顺序都不会留下离线推荐。
func TestRecommendationOfflineConcurrentIntegration(t *testing.T) {
	f := newIAAFixture(t)
	repository, ctx := seedDirect(t, f)
	for i := range 8 {
		removeRecommendation(t, f.store, ctx, false)
		submitDirect(t, repository, "publish", "")
		w, err := repository.Workbench(ctx, "201")
		if err != nil {
			t.Fatal(err)
		}
		config, err := f.store.GetCarousel(ctx)
		if err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		results := make(chan error, 2)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, err := repository.Accept(ctx, "201", directplay.Command{RequestID: fmt.Sprintf("concurrent-offline-%d", i), ExpectedRevision: w.Publication.Revision, Kind: "offline"}, "fixture")
			results <- err
		}()
		go func() {
			defer wg.Done()
			<-start
			_, err := f.store.ReplaceCarousel(ctx, homepage.Carousel{Revision: config.Revision, DramaIDs: []string{"201"}})
			results <- err
		}()
		close(start)
		wg.Wait()
		close(results)
		successes := 0
		for err := range results {
			if err == nil {
				successes++
			} else if !errors.Is(err, homepage.ErrDramaRecommended) && !errors.Is(err, consumer.ErrNotFound) {
				t.Fatal("并发返回非预期错误", err)
			}
		}
		if successes != 1 {
			t.Fatal("推荐与下线必须只成功一个", successes)
		}
		current, err := repository.Workbench(ctx, "201")
		if err != nil {
			t.Fatal(err)
		}
		latest, err := f.store.GetCarousel(ctx)
		if err != nil || current.Publication.Status == "offline" && latest.ReferencesDrama("201") {
			t.Fatal("并发留下已下线推荐", err)
		}
	}
}

// TestRecommendationOfflineOfficialConcurrentIntegration 验证官方下架排队与新增推荐互斥，不留下等待下架的推荐。
func TestRecommendationOfflineOfficialConcurrentIntegration(t *testing.T) {
	f := newIAAFixture(t)
	service, provider := seedRecommendationPublication(t, f)
	ctx := application.WithSubject(context.Background(), "1")
	for range 8 {
		removeRecommendation(t, f.store, ctx, false)
		state, err := service.FindTikTokReleaseState(ctx, "201")
		if err != nil {
			t.Fatal(err)
		}
		if state.Status == operations.PublicationStatusOffline {
			if _, err := service.ChangeTikTokRelease(ctx, "201", operations.TikTokReleaseInput{Action: "list", SelectedPublicationID: state.SelectedPublicationID}); err != nil {
				t.Fatal(err)
			}
		}
		before := provider.delists.Load()
		config, err := f.store.GetCarousel(ctx)
		if err != nil {
			t.Fatal(err)
		}
		start, results := make(chan struct{}), make(chan error, 2)
		go func() {
			<-start
			_, err := service.QueueTikTokUnpublish(ctx, "201")
			results <- err
		}()
		go func() {
			<-start
			_, err := f.store.ReplaceCarousel(ctx, homepage.Carousel{Revision: config.Revision, DramaIDs: []string{"201"}})
			results <- err
		}()
		close(start)
		successes := 0
		for range 2 {
			if err := <-results; err == nil {
				successes++
			} else if !errors.Is(err, homepage.ErrDramaRecommended) && !errors.Is(err, homepage.ErrConflict) && !errors.Is(err, consumer.ErrNotFound) {
				t.Fatal("官方并发返回非预期错误", err)
			}
		}
		if successes != 1 || provider.delists.Load()-before > 1 {
			t.Fatal("推荐和官方下架必须只成功一个且不调用平台", successes)
		}
		current, err := f.store.FindTikTokReleaseState(ctx, "201")
		if err != nil {
			t.Fatal(err)
		}
		latest, err := f.store.GetCarousel(ctx)
		if err != nil || current.Status == operations.PublicationStatusOffline && latest.ReferencesDrama("201") {
			t.Fatal("官方并发留下等待下架的推荐", err)
		}
	}
}

// TestRecommendationOfflineLegacyWorkerIntegration 验证旧同步操作不再由 Worker 自动执行，人工下线仍受推荐门禁保护。
func TestRecommendationOfflineLegacyWorkerIntegration(t *testing.T) {
	f := newIAAFixture(t)
	service, provider := seedRecommendationPublication(t, f)
	ctx := application.WithSubject(context.Background(), "1")
	original, err := f.store.FindTikTokPublicationByDramaID(ctx, "201")
	if err != nil {
		t.Fatal(err)
	}
	// 原版本上架事实先独立留档，随后模拟旧版排队下架记录。
	if err := syncPublicationRecords(f.store.data.DB(), original); err != nil {
		t.Fatal(err)
	}
	original.Status, original.Stage = operations.PublicationStatusQueued, operations.PublicationStageDelistAlbum
	if err := f.store.UpdateTikTokPublication(ctx, original, nil); err != nil {
		t.Fatal(err)
	}
	task, err := f.store.ClaimTikTokPublication(ctx, "worker", time.Now().UTC().Add(time.Hour), time.Now().UTC().Add(2*time.Hour))
	if err != nil || task != nil {
		t.Fatal("旧同步步骤仍被自动执行", task, err)
	}
	raw, err := json.Marshal(homepage.Carousel{DramaIDs: []string{"201"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.data.DB().Create(&homepageRecommendationModel{SubjectID: "1", Section: "hot", ConfigJSON: string(raw), Revision: 1}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.RetryTikTokPublication(ctx, "201"); !errors.Is(err, homepage.ErrDramaRecommended) {
		t.Fatal("未拦截推荐引用", err)
	}
	if provider.delists.Load() != 0 {
		t.Fatal("被推荐剧目发出下线请求")
	}
	removeRecommendation(t, f.store, ctx, true)
	if _, err := service.RetryTikTokPublication(ctx, "201"); err != nil {
		t.Fatal(err)
	}
	state, err := service.FindTikTokReleaseState(ctx, "201")
	if err != nil || state.Status != operations.PublicationStatusOffline || provider.delists.Load() != 1 {
		t.Fatal("移除后未同步下线", state, err)
	}
}

// SetOnlineVersion 为上下架回归提供本地替身，不调用 TikTok。
func (p *recommendationOfflineProvider) SetOnlineVersion(context.Context, string, int) error {
	return nil
}
