package operations_test

import (
	"context"
	"drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
	"encoding/json"
	"sync"
	"testing"
)

// TestTikTokPublicationOwnsSnapshot 验证编辑与旧就绪接口不创建发布版本，TK 同步后快照与草稿独立。
func TestTikTokPublicationOwnsSnapshot(t *testing.T) {
	ctx := context.Background()
	svc, store, id := publicationFixture(t, &publicationProviderStub{})
	draft, err := svc.FindDrama(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := store.SaveDrama(ctx, *draft); err != nil {
			t.Fatal(err)
		}
	}
	ready, err := svc.PrepareDrama(ctx, id)
	if err != nil || ready.PublishStatus != "ready" {
		t.Fatal("就绪失败", err)
	}
	if _, err := store.FindTikTokPublicationByDramaID(ctx, id); err != consumer.ErrNotFound {
		t.Fatal("就绪创建了发布任务", err)
	}
	raw, err := json.Marshal(ready)
	if err != nil {
		t.Fatal(err)
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"contentVersionId", "contentVersion"} {
		if _, ok := data[key]; ok {
			t.Fatal("短剧管理携带发布版本", key)
		}
	}
	first, err := svc.SubmitTikTokPublication(ctx, id, operations.TikTokPublicationInput{})
	if err != nil || first.ContentSnapshot == nil {
		t.Fatal("同步未冻结快照", err)
	}
	waitForPublicationStatus(t, svc, id, operations.PublicationStatusSynced)
	draft.Title = "新内容"
	if err := store.SaveDrama(ctx, *draft); err != nil {
		t.Fatal(err)
	}
	previous, err := store.FindTikTokPublicationByDramaID(ctx, id)
	if err != nil || previous.ID != first.ID || previous.ContentSnapshot.Title == draft.Title {
		t.Fatal("编辑改写发布任务", err)
	}
	next, err := svc.SubmitTikTokPublication(ctx, id, operations.TikTokPublicationInput{})
	if err != nil || next.ID == first.ID || next.ContentSnapshot.Title != draft.Title {
		t.Fatal("新发布未冻结修改内容", err)
	}
}

// TestConcurrentContentPreparation 验证并发就绪只更新状态，版本由后续发布动作创建。
func TestConcurrentContentPreparation(t *testing.T) {
	s, _, id := publicationFixture(t, &publicationProviderStub{})
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := s.PrepareDrama(context.Background(), id); results <- err }()
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.FindTikTokPublication(context.Background(), id); err != consumer.ErrNotFound {
		t.Fatal("并发就绪创建任务", err)
	}
}

// TestTikTokSyncValidatesDraftContent 验证草稿直接同步，内容缺项仍阻止创建官方版本。
func TestTikTokSyncValidatesDraftContent(t *testing.T) {
	s, store, id := publicationFixture(t, &publicationProviderStub{})
	ctx := context.Background()
	d, err := store.FindDrama(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	d.Description = ""
	if err := store.SaveDrama(ctx, *d); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SubmitTikTokPublication(ctx, id, operations.TikTokPublicationInput{}); err != operations.ErrPublicationNotReady {
		t.Fatal("不完整草稿可同步", err)
	}
	if _, err := store.FindTikTokPublicationByDramaID(ctx, id); err != consumer.ErrNotFound {
		t.Fatal("失败校验仍创建版本", err)
	}
}

// TestRejectedVersionRequiresNewContent 验证驳回后重新就绪不绕过内容修改门禁，也不会提前产生版本。
func TestRejectedVersionRequiresNewContent(t *testing.T) {
	s, store, id := publicationFixture(t, &publicationProviderStub{})
	ctx := context.Background()
	if _, err := submitPreparedPublication(t, s, ctx, id, operations.TikTokPublicationInput{}); err != nil {
		t.Fatal(err)
	}
	waitForPublicationStatus(t, s, id, operations.PublicationStatusSynced)
	current, err := store.FindTikTokPublicationByDramaID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	current.Status, current.ReviewStatus = operations.PublicationStatusRejected, 3
	if err := store.UpdateTikTokPublication(ctx, current, nil); err != nil {
		t.Fatal(err)
	}
	d, err := s.FindDrama(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveDrama(ctx, *d); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PrepareDrama(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SubmitTikTokPublication(ctx, id, operations.TikTokPublicationInput{}); err != operations.ErrConflict {
		t.Fatal("相同内容可重送", err)
	}
	retained, err := store.FindTikTokPublicationByDramaID(ctx, id)
	if err != nil || retained.ID != current.ID {
		t.Fatal("拒绝重送仍替换任务", err)
	}
	d.Title = "修正内容"
	if err := store.SaveDrama(ctx, *d); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PrepareDrama(ctx, id); err != nil {
		t.Fatal(err)
	}
	next, err := s.SubmitTikTokPublication(ctx, id, operations.TikTokPublicationInput{})
	if err != nil || next.ID == current.ID || next.ContentSnapshot.Title != d.Title {
		t.Fatal("新内容无法重送", err)
	}
}
