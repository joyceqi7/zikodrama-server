package mysqlstore

import (
	"context"
	"sync"
	"testing"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/visitor"
)

// assertMergedConsumerUsers 验证直接身份登录、并发唯一性和候选 ID 碰撞隔离，不依赖匿名过渡用户。
func assertMergedConsumerUsers(t *testing.T, ctx context.Context, store *Store) {
	t.Helper()
	for _, appID := range []string{"1038", "1039"} {
		if err := store.SaveApplication(ctx, application.Application{SubjectID: "1", ID: appID, TikTokAppID: appID, Name: appID, Status: application.StatusActive, TimeZone: "UTC"}); err != nil {
			t.Fatal(err)
		}
	}
	record := visitor.TikTokIdentityRecord{ApplicationID: "1038", OpenIDHash: visitor.TikTokOpenIDHash("1038", "account-a")}
	first, err := store.ResolveTikTokIdentity(ctx, record)
	if err != nil || first == "" {
		t.Fatalf("直接登录失败: %v", err)
	}
	if next, err := store.ResolveTikTokIdentity(ctx, record); err != nil || next != first {
		t.Fatalf("重复登录改变主体: %v", err)
	}
	record.OpenIDHash = visitor.TikTokOpenIDHash("1038", "another-account")
	other, err := store.ResolveTikTokIdentity(ctx, record)
	if err != nil || other == first {
		t.Fatalf("不同账号未分离: %v", err)
	}
	record.ApplicationID = "1039"
	otherApp, err := store.ResolveTikTokIdentity(ctx, record)
	if err != nil || otherApp == other {
		t.Fatalf("跨应用主体未分离: %v", err)
	}
	if exists, err := store.VisitorExists(ctx, "1038", otherApp); err != nil || exists {
		t.Fatal("跨应用用户可见", err)
	}

	t.Run("concurrent direct login creates one row", func(t *testing.T) {
		const callers = 8
		start := make(chan struct{})
		results := make([]string, callers)
		errs := make([]error, callers)
		var wg sync.WaitGroup
		for i := range results {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				results[i], errs[i] = store.ResolveTikTokIdentity(ctx, visitor.TikTokIdentityRecord{ApplicationID: "1038", OpenIDHash: visitor.TikTokOpenIDHash("1038", "concurrent-account")})
			}(i)
		}
		close(start)
		wg.Wait()
		for i := range results {
			if errs[i] != nil || results[i] == "" || results[i] != results[0] {
				t.Fatalf("concurrent result mismatch: %v", errs[i])
			}
		}
		var count int
		hash := visitor.TikTokOpenIDHash("1038", "concurrent-account")
		table := userTable("users", logicalBucket("1038", hash))
		if err := store.data.DB().WithContext(ctx).Table(table).Select("COUNT(*)").Where("application_id=? AND open_id_hash=?", "1038", hash).Scan(&count).Error; err != nil || count != 1 {
			t.Fatalf("concurrent login left extra users: count=%d err=%v", count, err)
		}
	})
	// users 只保存身份摘要，不再创建原始平台标识或令牌密文列。
	var count int
	if err := store.data.pool.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name LIKE 'users\\_%' AND column_name IN ('open_id_ciphertext','access_token_ciphertext','refresh_token_ciphertext')").Scan(&count); err != nil || count != 0 {
		t.Fatal("unused identity secret columns remain", err)
	}
}
