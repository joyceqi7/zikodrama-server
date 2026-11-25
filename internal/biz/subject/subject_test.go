package subject_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/subject"
	"drama-server/internal/testsupport/memory"
)

// TestSubjectPublisherIsolation 验证主体可先建空配置，发布凭据独立且不会进入管理 JSON。
func TestSubjectPublisherIsolation(t *testing.T) {
	store := memory.NewStore(memory.Seed{})
	service := subject.NewService(store)
	a, err := service.Save(context.Background(), "", subject.Input{Name: "主体 A"})
	if err != nil || a.ClientSecretConfigured {
		t.Fatal("空发布方主体创建失败", err)
	}
	a, err = service.Save(context.Background(), a.ID, subject.Input{Name: "主体 A", TikTokAppID: "000123", ClientKey: "publisher-A", ClientSecret: "secret-A"})
	if err != nil || !a.ClientSecretConfigured {
		t.Fatal("主体发布方保存失败", err)
	}
	b, err := service.Save(context.Background(), "", subject.Input{Name: "主体 B", TikTokAppID: "000124", ClientKey: "publisher-B", ClientSecret: "secret-B"})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := service.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(rows)
	if strings.Contains(string(raw), "secret-A") || strings.Contains(string(raw), "secret-B") {
		t.Fatal("管理响应泄漏发布密钥")
	}
	ctx := application.WithSubject(context.Background(), a.ID)
	id, err := service.OwnerID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := service.Owner(ctx, id)
	if err != nil || owner.ClientSecret != "secret-A" {
		t.Fatal("主体身份解析错误", err)
	}
	if _, err = service.Owner(application.WithSubject(ctx, b.ID), id); err == nil {
		t.Fatal("其他主体借用了发布凭据")
	}
	if _, err = service.OwnerID(context.Background()); err == nil {
		t.Fatal("缺少主体时不能自动借用默认主体")
	}
	_, err = service.Save(ctx, a.ID, subject.Input{Name: "主体 A", TikTokAppID: "000123", ClientKey: "publisher-A"})
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := store.FindSubject(ctx, a.ID)
	if stored.ClientSecret != "secret-A" {
		t.Fatal("空密钥覆盖已有凭据")
	}
}
