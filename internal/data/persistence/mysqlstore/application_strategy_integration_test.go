package mysqlstore

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"

	"drama-server/internal/biz/adminrbac"
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/monetization"
	"drama-server/internal/biz/operations"
	operationshttp "drama-server/internal/service/operations"
)

// TestCurrentSchemaIntegrationApplicationStrategy 验证真实 HTTP、SQL 保存、应用独立归属、旧页面覆盖和消费者配置读回。
func TestCurrentSchemaIntegrationApplicationStrategy(t *testing.T) {
	f := newIAAFixture(t)
	defer f.store.data.pool.Close()
	a := application.WithContext(context.Background(), "101")
	b := application.WithContext(context.Background(), "102")
	original, err := f.store.GetApplicationStrategy(a)
	if err != nil || original.Strategy == nil {
		t.Fatal("读取初始策略失败", err)
	}
	before, err := f.store.GetApplicationStrategy(a)
	if err != nil {
		t.Fatal(err)
	}
	otherBefore, err := f.store.GetApplicationStrategy(b)
	if err != nil {
		t.Fatal(err)
	}
	handler := operationshttp.NewHandler(operations.NewService(operations.Repositories{Application: operations.ApplicationPorts{Subjects: f.store, Registry: f.store}, Monetization: operations.MonetizationPorts{Settings: f.store}, Reporting: operations.ReportingPorts{Audits: f.store}}, nil), adminrbac.NewService(f.store, adminrbac.Config{SigningKey: []byte("test-only-admin-jwt-signing-key-32-bytes")}), operationshttp.Config{})
	server := httptest.NewServer(handler)
	defer server.Close()
	login := iaaRequest(t, server.URL, "POST", "/admin/v1/session", "", "", map[string]string{"username": "iaa-admin", "password": "LocalIAA123!"}, 200)
	var session struct {
		Token string `json:"token"`
	}
	mustDecodeApplicationStrategy(t, login, &session)
	input := monetization.ApplicationStrategyInput{Document: before.Strategy.Document, Rules: before.Rules}
	input.Document.FreeEpisodeCount = 7
	savedRaw := iaaRequest(t, server.URL, "PUT", "/admin/v1/application-strategy", session.Token, "101", input, 200)
	var saved monetization.ApplicationStrategy
	mustDecodeApplicationStrategy(t, savedRaw, &saved)
	if saved.Strategy.ID != original.Strategy.ID || saved.Strategy.Document.FreeEpisodeCount != 7 {
		t.Fatal("策略归属或修订错误", saved)
	}
	// 同一旧页面再次保存时，以事务内最新修订继续写入，不能要求运营刷新后重做。
	input.Document.FreeEpisodeCount = 8
	var overwritten monetization.ApplicationStrategy
	mustDecodeApplicationStrategy(t, iaaRequest(t, server.URL, "PUT", "/admin/v1/application-strategy", session.Token, "101", input, 200), &overwritten)
	if overwritten.Strategy.Document.FreeEpisodeCount != 8 {
		t.Fatal("旧页面未按最新修订保存", overwritten)
	}
	var loaded monetization.ApplicationStrategy
	mustDecodeApplicationStrategy(t, iaaRequest(t, server.URL, "GET", "/admin/v1/application-strategy", session.Token, "101", nil, 200), &loaded)
	if loaded.Strategy.ID != saved.Strategy.ID || loaded.Strategy.Document.FreeEpisodeCount != 8 {
		t.Fatal("保存结果未持久化")
	}
	other, err := f.store.GetApplicationStrategy(b)
	if err != nil || other.Strategy.ID != otherBefore.Strategy.ID || other.Strategy.Document.FreeEpisodeCount != otherBefore.Strategy.Document.FreeEpisodeCount {
		t.Fatal("修改应用 A 影响了 B", err)
	}
	config, err := f.store.GetApplicationMonetization(a)
	if err != nil || config.FreeEpisodeCount != 8 {
		t.Fatal("消费者仍读取旧规则", err)
	}
	input.Document.FreeEpisodeCount = 9
	next, err := f.store.SaveApplicationStrategy(a, input)
	if err != nil || next.Strategy.ID != saved.Strategy.ID {
		t.Fatal("再次保存未复用独立策略", err)
	}
	// 无效资源必须整体回滚，不能提前递增修订或改写策略。
	if err := f.store.data.DB().Table("applications").Where("id = ?", "101").UpdateColumn("ad_units_json", `{"rewardedVideo":[],"interstitial":[]}`).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.SaveApplicationStrategy(a, input); !errors.Is(err, operations.ErrApplicationAdUnitsIncomplete) {
		t.Fatal("缺少广告位仍保存", err)
	}
	after, err := f.store.GetApplicationStrategy(a)
	if err != nil || after.Strategy.Document.FreeEpisodeCount != 9 {
		t.Fatal("失败请求改变了应用修订", err)
	}
	// 失效引用或空引用应复用应用所属策略，不能因唯一索引冲突阻断保存。
	if err := f.store.data.DB().Table("applications").Where("id = ?", "102").UpdateColumn("unlock_strategy_id", "999999999999999999").Error; err != nil {
		t.Fatal(err)
	}
	missing, err := f.store.GetApplicationStrategy(b)
	if err != nil || missing.Strategy != nil {
		t.Fatal("失效策略引用未按空配置读取", err)
	}
	created, err := f.store.SaveApplicationStrategy(b, monetization.ApplicationStrategyInput{Document: otherBefore.Strategy.Document, Rules: otherBefore.Rules})
	if err != nil || created.Strategy == nil {
		t.Fatal("失效策略引用无法恢复", err)
	}
	if created.Strategy.ID != otherBefore.Strategy.ID {
		t.Fatal("失效引用未复用应用所属策略")
	}
	if err := f.store.data.DB().Table("applications").Where("id = ?", "102").UpdateColumn("unlock_strategy_id", nil).Error; err != nil {
		t.Fatal(err)
	}
	restored, err := f.store.SaveApplicationStrategy(b, monetization.ApplicationStrategyInput{Document: otherBefore.Strategy.Document, Rules: otherBefore.Rules})
	if err != nil || restored.Strategy == nil || restored.Strategy.ID != otherBefore.Strategy.ID {
		t.Fatal("空引用未复用应用所属策略", err)
	}
	var count int64
	if err := f.store.data.DB().Table("unlock_strategies").Where("application_id = ?", "102").Count(&count).Error; err != nil || count != 1 {
		t.Fatal("恢复后应用策略不唯一", count, err)
	}
	readBack, err := f.store.GetApplicationStrategy(b)
	if err != nil || readBack.Strategy == nil || readBack.Strategy.ID != restored.Strategy.ID {
		t.Fatal("恢复后的绑定无法读取", err)
	}
	// 所属记录确实已删除时，才创建新策略并修复残留绑定。
	if err := f.store.data.DB().Where("application_id = ?", "102").Delete(&unlockStrategyModel{}).Error; err != nil {
		t.Fatal(err)
	}
	recreated, err := f.store.SaveApplicationStrategy(b, monetization.ApplicationStrategyInput{Document: otherBefore.Strategy.Document, Rules: otherBefore.Rules})
	if err != nil || recreated.Strategy == nil || recreated.Strategy.ID == restored.Strategy.ID {
		t.Fatal("已删除策略无法重建", err)
	}
}

func mustDecodeApplicationStrategy(t *testing.T, raw []byte, target any) {
	t.Helper()
	if err := json.Unmarshal(raw, target); err != nil {
		t.Fatal(err)
	}
}
