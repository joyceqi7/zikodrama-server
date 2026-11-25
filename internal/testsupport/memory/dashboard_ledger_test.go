package memory

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/monetization"
)

func TestDashboardReadsCatalogEntitlementLedger(t *testing.T) {
	t.Parallel()
	store := NewStore(Seed{})
	applicationID := TestApplicationID
	visitorID := "visitor-metrics"
	firstGrantedAt := time.Date(2026, time.August, 3, 8, 0, 0, 0, time.UTC)
	secondGrantedAt := firstGrantedAt.AddDate(0, 0, 1)
	for _, record := range []entitlement{
		{ApplicationID: applicationID, VisitorID: visitorID, SourceID: "reward_grant:first", GrantedAt: firstGrantedAt, ExpiresAt: firstGrantedAt.Add(24 * time.Hour)},
		{ApplicationID: applicationID, VisitorID: visitorID, SourceID: "reward_grant:second", GrantedAt: secondGrantedAt, ExpiresAt: secondGrantedAt.Add(24 * time.Hour)},
	} {
		items, err := monetization.DefaultStrategies().BuildEntitlements([]monetization.GrantTemplate{{Type: "vip", Config: json.RawMessage(`{"scope":"application","validity":"duration","durationSeconds":86400}`)}}, monetization.GrantContext{ApplicationID: applicationID, UserID: visitorID, SourceType: "reward_grant", SourceID: record.SourceID, Now: record.GrantedAt})
		if err != nil {
			t.Fatal(err)
		}
		store.writeBenefitsLocked(items)
	}

	ctx := application.WithContext(context.Background(), applicationID)
	for _, periodStart := range []time.Time{firstGrantedAt.Truncate(24 * time.Hour), secondGrantedAt.Truncate(24 * time.Hour)} {
		metrics, err := store.DashboardDays(ctx, periodStart, periodStart.Add(24*time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		if metrics[0].VIPActivations != 1 {
			t.Fatalf("日期 %s 的目录权益授予数=%d，期望=1", periodStart.Format(time.DateOnly), metrics[0].VIPActivations)
		}
	}
}
