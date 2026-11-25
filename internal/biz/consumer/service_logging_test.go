package consumer_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/monetization"
)

// errorLoggingStore 注入目录和奖励流水故障，不连接数据库或官方平台。
type errorLoggingStore struct {
	domain.Store
	err error
}

// ListDramas 返回指定的读取错误。
func (s errorLoggingStore) ListDramas(context.Context) ([]domain.Drama, error) { return nil, s.err }

// FindRewardGrant 模拟事务入口前的幂等查询故障。
func (s errorLoggingStore) FindRewardGrant(context.Context, string, string) (monetization.RewardGrant, bool, error) {
	return monetization.RewardGrant{}, false, s.err
}

func TestConsumerLogsRawErrorBeforeWrapping(t *testing.T) {
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })
	failure := errors.New("SELECT claim_id FROM reward_grants: connection refused")
	for _, reward := range []bool{false, true} {
		var output bytes.Buffer
		slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
		service := domain.NewService(errorLoggingStore{err: failure}, nil)
		var err error
		if reward {
			_, err = service.ClaimReward(context.Background(), "visitor-1", monetization.ClaimRewardInput{ClaimID: "claim_logging_00001", OfferID: "private-offer", CompletedAdUnitID: "ad-1", IsEnded: true})
		} else {
			_, err = service.ListDramas(context.Background())
		}
		if !errors.Is(err, failure) {
			t.Fatalf("依赖错误链丢失：%v", err)
		}
		if reward && !errors.Is(err, domain.ErrServiceUnavailable) {
			t.Fatalf("奖励错误分类发生变化：%v", err)
		}
		if !strings.Contains(output.String(), failure.Error()) || !strings.Contains(output.String(), `"level":"ERROR"`) {
			t.Fatalf("日志缺少原始错误：%s", output.String())
		}
		if strings.Contains(output.String(), "private-offer") {
			t.Fatal("日志泄漏完整请求参数")
		}
	}
	var output bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	if _, err := domain.NewService(errorLoggingStore{}, nil).ListDramas(context.Background()); err != nil || output.Len() != 0 {
		t.Fatal("目录查询成功产生错误日志")
	}
}
