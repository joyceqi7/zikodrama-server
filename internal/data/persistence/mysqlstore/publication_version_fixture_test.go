package mysqlstore

import (
	"context"
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
	"testing"
)

// seedReadyPublicationContent 为任务事务用例准备就绪内容，不提前创建版本或绕过发布事务发号。
func seedReadyPublicationContent(t *testing.T, store *Store, p *operations.TikTokPublication) {
	t.Helper()
	ctx := application.WithSubject(context.Background(), "1")
	d, err := store.FindDrama(ctx, p.DramaID)
	if err != nil {
		t.Fatal(err)
	}
	if p.ContentSnapshot != nil {
		copy := *p.ContentSnapshot
		d = &copy
	}
	if d.Description == "" {
		d.Description = "存储事务测试简介"
	}
	if d.CoverURL == "" {
		d.CoverURL = "https://fixture.example/cover.png"
	}
	if err := store.SaveDramaAggregate(ctx, d, nil); err != nil {
		t.Fatal(err)
	}
	ready, err := store.PrepareDrama(ctx, p.DramaID, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	frozen := consumer.FreezeContent(*ready)
	p.ContentSnapshot = &frozen
}
