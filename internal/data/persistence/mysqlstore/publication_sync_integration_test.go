package mysqlstore

import (
	"context"
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

// TestInterruptedPublicationReadIntegration 验证详情、列表及待办筛选一致，读取不改变中断记录。
func TestInterruptedPublicationReadIntegration(t *testing.T) {
	f := newIAAFixture(t)
	service, _ := seedRecommendationPublication(t, f)
	ctx := application.WithSubject(context.Background(), "1")
	p, err := f.store.FindTikTokPublicationByDramaID(ctx, "201")
	if err != nil {
		t.Fatal(err)
	}
	p.ID = ""
	p.OnlineAt = nil
	p.PlatformVersion = 2
	p.PublishRequested = false
	p.Status, p.Stage = operations.PublicationStatusProcessing, operations.PublicationStageUpdateAlbum
	p.LeaseOwner = "interrupted-request"
	p.LeaseExpiresAt = time.Now().UTC().Add(-time.Minute)
	p.ExternalWriteStarted = true
	p.CreatedAt = p.CreatedAt.Add(time.Second)
	row, err := newTikTokPublicationModel(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.data.DB().Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	var original tiktokPublicationModel
	if err := f.store.data.DB().Where("id=?", row.ID).Take(&original).Error; err != nil {
		t.Fatal(err)
	}
	read, err := service.FindTikTokPublication(ctx, "201")
	if err != nil || read.Status != operations.PublicationStatusFailed || read.ErrorCode != "external_write_uncertain" {
		t.Fatalf("中断详情待办错误: %+v / %v", read, err)
	}
	page, err := f.store.ListDramaPage(ctx, operations.DramaListQuery{Page: 1, PageSize: 20, Publication: "needs_action"})
	if err != nil || len(page.Items) != 1 || page.Items[0].Publication.Status != read.Status || page.Items[0].Publication.ErrorCode != read.ErrorCode {
		t.Fatalf("中断列表与详情不一致: %+v / %v", page, err)
	}
	var saved tiktokPublicationModel
	if err := f.store.data.DB().Where("id=?", row.ID).Take(&saved).Error; err != nil {
		t.Fatal(err)
	}
	if saved.Status != p.Status || saved.Revision != original.Revision || string(saved.WorkflowJSON) != string(original.WorkflowJSON) {
		t.Fatal("读取改写了中断调用记录")
	}
}

// TestHistoricalSelectedReleaseIntegration 验证运行时不猜测已删除的上架事实，已有下线失败记录仍保留 V1 入口。
func TestHistoricalSelectedReleaseIntegration(t *testing.T) {
	for _, legacyFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "缺少独立记录", true: "旧下线失败"}[legacyFailure], func(t *testing.T) {
			f := newIAAFixture(t)
			service, _ := seedRecommendationPublication(t, f)
			ctx := application.WithSubject(context.Background(), "1")
			v1, err := f.store.FindTikTokPublicationByDramaID(ctx, "201")
			if err != nil {
				t.Fatal(err)
			}
			if legacyFailure {
				fact := v1
				fact.Status, fact.Stage = operations.PublicationStatusFailed, operations.PublicationStageDelistAlbum
				raw, err := json.Marshal(fact)
				if err != nil {
					t.Fatal(err)
				}
				if err := f.store.data.DB().Table("tiktok_release_records").Where("id=?", v1.ID).Updates(map[string]any{"status": "failed", "record_json": raw}).Error; err != nil {
					t.Fatal(err)
				}
			} else if err := f.store.data.DB().Table("tiktok_release_records").Where("id=?", v1.ID).Delete(&publicationRecordModel{}).Error; err != nil {
				t.Fatal(err)
			}
			v2 := v1
			v2.ID, v2.OnlineAt, v2.PlatformVersion = "", nil, 2
			v2.Status, v2.Stage = operations.PublicationStatusUnderReview, operations.PublicationStagePollReview
			v2.CreatedAt, v2.UpdatedAt = v1.CreatedAt.Add(time.Second), v1.UpdatedAt.Add(time.Second)
			row, err := newTikTokPublicationModel(v2)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.store.data.DB().Create(&row).Error; err != nil {
				t.Fatal(err)
			}
			state, err := service.FindTikTokReleaseState(ctx, "201")
			if !legacyFailure {
				if err != nil || state != nil {
					t.Fatal("缺少权威记录时不应从其他任务猜测线上版本", err)
				}
				return
			}
			if err != nil || state == nil || state.SelectedPublicationID != v1.ID || state.Version != 1 || state.Status != operations.PublicationStatusOnline {
				t.Fatalf("旧库选择丢失: %+v / %v", state, err)
			}
			page, err := f.store.ListDramaPage(ctx, operations.DramaListQuery{Page: 1, PageSize: 20})
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, item := range page.Items {
				if item.ID == "201" {
					found = item.OfficialRelease != nil && item.OfficialRelease.SelectedPublicationID == v1.ID && item.Publication.PlatformVersion == 2
				}
			}
			if !found {
				t.Fatal("旧库列表未保留 V1 入口")
			}
			if _, err := service.ChangeTikTokRelease(ctx, "201", operations.TikTokReleaseInput{Action: "delist", SelectedPublicationID: v1.ID}); err != nil {
				t.Fatal(err)
			}
			if _, err := service.ChangeTikTokRelease(ctx, "201", operations.TikTokReleaseInput{Action: "list", SelectedPublicationID: v1.ID}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestSynchronousOfficialReleaseIntegration 验证真实 MySQL 的 V1/V2 隔离、串行幂等和原快照，不启动 Worker。
func TestSynchronousOfficialReleaseIntegration(t *testing.T) {
	f := newIAAFixture(t)
	service, provider := seedRecommendationPublication(t, f)
	ctx := application.WithSubject(context.Background(), "1")
	v1, err := f.store.FindTikTokPublicationByDramaID(ctx, "201")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := consumer.FreezeContent(*v1.ContentSnapshot)
	snapshot.Title = "已审核 V2 的固定名称"
	v2 := v1
	v2.ID = ""
	v2.PlatformVersion = 2
	v2.ContentSnapshot = &snapshot
	v2.OnlineAt = nil
	v2.Status, v2.Stage = operations.PublicationStatusUnderReview, operations.PublicationStagePollReview
	v2.ReviewID = "review-v2"
	v2.ReviewStatus = 1
	v2.CreatedAt = v1.CreatedAt.Add(time.Second)
	v2.UpdatedAt = v2.CreatedAt
	row, err := newTikTokPublicationModel(v2)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.data.DB().Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	v2.ID = row.ID
	var original tiktokPublicationModel
	if err := f.store.data.DB().Where("id=?", v2.ID).Take(&original).Error; err != nil {
		t.Fatal(err)
	}
	// 八个重复请求必须只调用一次平台下线，记录数量保持两个内容版本。
	start := make(chan struct{})
	results := make(chan error, 8)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := service.ChangeTikTokRelease(ctx, "201", operations.TikTokReleaseInput{Action: "delist", SelectedPublicationID: v1.ID})
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	if provider.delists.Load() != 1 {
		t.Fatal("重复请求重复调用平台下线")
	}
	state, err := service.FindTikTokReleaseState(ctx, "201")
	if err != nil || state.Version != 1 || state.Status != operations.PublicationStatusOffline {
		t.Fatal(state, err)
	}
	if _, err := service.ChangeTikTokRelease(ctx, "201", operations.TikTokReleaseInput{Action: "list", SelectedPublicationID: v1.ID}); err != nil {
		t.Fatal(err)
	}
	var after tiktokPublicationModel
	if err := f.store.data.DB().Where("id=?", v2.ID).Take(&after).Error; err != nil {
		t.Fatal(err)
	}
	if string(after.WorkflowJSON) != string(original.WorkflowJSON) || after.Revision != original.Revision || after.Status != original.Status {
		t.Fatal("V1 上下架改写 V2 审核")
	}
	drama, err := f.store.FindDrama(ctx, "201")
	if err != nil || drama.Published == nil || drama.Published.PlatformVersion != 1 {
		t.Fatal("未恢复原版本", err)
	}
	page, err := f.store.ListDramaPage(ctx, operations.DramaListQuery{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, item := range page.Items {
		if item.ID == "201" {
			found = item.OfficialRelease != nil && item.OfficialRelease.Version == 1 && item.Publication.PlatformVersion == 2
		}
	}
	if !found {
		t.Fatal("列表混淆已选版本与审核版本")
	}
	// 当前草稿是另一批内容，明确选择 V2 只能用审核冻结的内容。
	drama.Title = "未审核的 V3 草稿"
	if err := f.store.SaveDrama(ctx, *drama); err != nil {
		t.Fatal(err)
	}
	v2, err = f.store.FindTikTokPublicationByDramaID(ctx, "201")
	if err != nil {
		t.Fatal(err)
	}
	v2.Status, v2.Stage, v2.ReviewStatus = operations.PublicationStatusApproved, operations.PublicationStageAwaitPublish, 2
	if err := f.store.UpdateTikTokPublication(ctx, v2, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := service.PublishTikTokPublication(ctx, "201", v2.ID); err != nil {
		t.Fatal(err)
	}
	drama, err = f.store.FindDrama(ctx, "201")
	if err != nil || drama.Title != "未审核的 V3 草稿" || drama.Published.Title != snapshot.Title || drama.Published.PlatformVersion != 2 {
		t.Fatal("上线偷换草稿内容", err)
	}
	if _, err := service.ChangeTikTokRelease(ctx, "201", operations.TikTokReleaseInput{Action: "delist", SelectedPublicationID: v1.ID}); !errors.Is(err, operations.ErrConflict) {
		t.Fatal("旧页面可下架另一版本", err)
	}
	var count int64
	if err := f.store.data.DB().Model(&tiktokPublicationModel{}).Where("drama_id=201").Count(&count).Error; err != nil || count != 2 {
		t.Fatal("同步上下架额外创建任务", count, err)
	}
	var columnCount int64
	if err := f.store.data.DB().Raw("SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='tiktok_publications' AND column_name='task_kind'").Scan(&columnCount).Error; err != nil || columnCount != 0 {
		t.Fatal("依赖未发布任务字段", err)
	}
	records, err := f.store.ListPublicationRecords(ctx, operations.PublicationRecordQuery{Kind: "releases", Page: 1, PageSize: 20})
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records.Items {
		if record.Current && record.ID != v2.ID {
			raw, _ := json.Marshal(record.Publication)
			t.Fatal("当前上架记录错误", record.ID, len(raw))
		}
	}
}

// TestSynchronousAuthorizationIntegration 验证请求内授权/撤权、失败停留及上下架串行门禁。
func TestSynchronousAuthorizationIntegration(t *testing.T) {
	f := newIAAFixture(t)
	service, _ := seedRecommendationPublication(t, f)
	ctx := application.WithSubject(context.Background(), "1")
	// 直接调用端口的本地平台替身，确保提交后可见性与实际结果一致。
	calls := 0
	call := func(context.Context, operations.DramaDistribution) error { calls++; return nil }
	if err := f.store.ApplyDistribution(ctx, "201", "102", true, false, call); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("请求未直接调用平台")
	}
	rows, err := f.store.ListDistributions(ctx, "201")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.ApplicationID == "102" && row.Status != "authorized" {
			t.Fatal("成功仍保存为待执行")
		}
	}
	failure := errors.New("fixture failure")
	if err := f.store.ApplyDistribution(ctx, "201", "102", false, false, func(context.Context, operations.DramaDistribution) error { calls++; return failure }); !errors.Is(err, failure) {
		t.Fatal("同步失败未直接返回", err)
	}
	drama, err := f.store.FindDrama(ctx, "201")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := drama.PublishedForApplication("102"); ok {
		t.Fatal("撤权失败仍保留本地访问")
	}
	rows, err = f.store.ListDistributions(ctx, "201")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.ApplicationID == "102" && (row.Status != "failed" || row.LeaseOwner != "" || row.NextAttemptAt.After(time.Unix(0, 0))) {
			t.Fatal("同步失败产生重试任务", row.Status)
		}
	}
	state, err := service.FindTikTokReleaseState(ctx, "201")
	if err != nil || state.Version != 1 {
		t.Fatal("授权改变所选版本", err)
	}
}
