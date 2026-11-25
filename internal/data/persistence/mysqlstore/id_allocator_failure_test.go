package mysqlstore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"drama-server/internal/data/idgen"
)

// allocatorCacheStub 注入网络与租约结果，验证编排失败时不会退回本地发号。
type allocatorCacheStub struct {
	next    func(context.Context, string, string, time.Duration) (idgen.Allocation, error)
	publish func(context.Context, string, string, idgen.Segment) (bool, error)
	backoff func(context.Context, string, string, time.Duration) error
}

// Next 返回测试指定的消费结果，不持有真实业务状态。
func (s allocatorCacheStub) Next(ctx context.Context, key, token string, lease time.Duration) (idgen.Allocation, error) {
	return s.next(ctx, key, token, lease)
}

// Publish 模拟发布成功、失败或租约被接管。
func (s allocatorCacheStub) Publish(ctx context.Context, key, token string, segment idgen.Segment) (bool, error) {
	return s.publish(ctx, key, token, segment)
}

// Backoff 验证失败后共享退避以及调用上下文。
func (s allocatorCacheStub) Backoff(ctx context.Context, key, token string, delay time.Duration) error {
	return s.backoff(ctx, key, token, delay)
}

func allocatorWithScope(cache idgen.Cache) *segmentIDAllocator {
	a := newSegmentIDAllocator(nil, consumerIDNamespace, cache)
	key := "drama:uid:{isolated-unit-test}"
	a.scope.Store(&key)
	return a
}

// TestSharedIDCacheFailureNeverFallsBack 验证缓存失效直接失败，不能产生另一条本地发号链。
func TestSharedIDCacheFailureNeverFallsBack(t *testing.T) {
	failure := errors.New("Redis connection interrupted")
	a := allocatorWithScope(allocatorCacheStub{next: func(context.Context, string, string, time.Duration) (idgen.Allocation, error) {
		return idgen.Allocation{}, failure
	}})
	if id, err := a.NextID(context.Background()); id != 0 || !errors.Is(err, failure) {
		t.Fatalf("缓存失败必须保留真实原因且不发号：id=%d err=%v", id, err)
	}
	if _, err := newSegmentIDAllocator(nil, consumerIDNamespace, nil).NextID(context.Background()); err == nil {
		t.Fatal("未配置共享缓存仍允许发号")
	}
}

// TestSharedIDWaitRespectsCancellation 验证另一个实例持有租约时可以取消等待。
func TestSharedIDWaitRespectsCancellation(t *testing.T) {
	a := allocatorWithScope(allocatorCacheStub{next: func(context.Context, string, string, time.Duration) (idgen.Allocation, error) {
		return idgen.Allocation{RetryAfter: time.Hour}, nil
	}})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if id, err := a.NextID(ctx); id != 0 || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("等待号段必须服从请求超时：id=%d err=%v", id, err)
	}
}

// TestSharedIDIntegerPrecision 验证应用端仅用整数合成超过 Lua 精确范围的 UID。
func TestSharedIDIntegerPrecision(t *testing.T) {
	for _, tc := range []struct {
		start  string
		offset uint32
		want   uint64
	}{
		{"9007199254740993", 17, 9007199254741010},
		{"9223372036854775800", 7, math.MaxInt64},
		{"9223372036854775800", 8, 0},
		{"18446744073709551615", 1, 0},
		{"0", 1, 0},
		{"invalid", 0, 0},
	} {
		t.Run(tc.start, func(t *testing.T) {
			a := allocatorWithScope(allocatorCacheStub{next: func(context.Context, string, string, time.Duration) (idgen.Allocation, error) {
				return idgen.Allocation{Available: true, Start: tc.start, Offset: tc.offset}, nil
			}})
			id, err := a.NextID(context.Background())
			if tc.want == 0 {
				if id != 0 || err == nil {
					t.Fatalf("非法缓存范围未被拒绝：id=%d err=%v", id, err)
				}
			} else if err != nil || id != tc.want {
				t.Fatalf("大整数精度丢失：id=%d want=%d err=%v", id, tc.want, err)
			}
		})
	}
}

// TestIDReservationFailuresNeverPublish 覆盖事务各阶段失败，尤其是提交结果不确定时不得发放该段。
func TestIDReservationFailuresNeverPublish(t *testing.T) {
	for _, stage := range []string{"begin", "lock", "update", "commit"} {
		t.Run(stage, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			a := newTestStore(db).ids
			failure := errors.New(stage + " injected database failure")
			if stage == "begin" {
				mock.ExpectBegin().WillReturnError(failure)
			} else {
				mock.ExpectBegin()
				query := mock.ExpectQuery("SELECT .* FROM `id_allocators`.*FOR UPDATE").WithArgs(consumerIDNamespace, 1)
				if stage == "lock" {
					query.WillReturnError(failure)
					mock.ExpectRollback()
				} else {
					query.WillReturnRows(sqlmock.NewRows([]string{"namespace", "next_id", "segment_size"}).AddRow(consumerIDNamespace, 101, 100))
					update := mock.ExpectExec("UPDATE `id_allocators` SET").WithArgs("201", consumerIDNamespace)
					if stage == "update" {
						update.WillReturnError(failure)
						mock.ExpectRollback()
					} else {
						update.WillReturnResult(sqlmock.NewResult(0, 1))
						mock.ExpectCommit().WillReturnError(failure)
					}
				}
			}
			var published, backedOff bool
			a.cache = allocatorCacheStub{
				publish: func(context.Context, string, string, idgen.Segment) (bool, error) { published = true; return true, nil },
				backoff: func(context.Context, string, string, time.Duration) error { backedOff = true; return nil },
			}
			var logs bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			defer slog.SetDefault(previous)
			err = a.refill(context.Background(), "test-key", "lease-not-for-logs")
			if !errors.Is(err, failure) || published || !backedOff {
				t.Fatalf("失败号段不得发布，且应退避：published=%v backoff=%v err=%v", published, backedOff, err)
			}
			if !strings.Contains(logs.String(), "reserve consumer ID segment failed") || !strings.Contains(logs.String(), failure.Error()) || strings.Contains(logs.String(), "lease-not-for-logs") {
				t.Fatalf("日志必须包含操作和实际错误且无租约值：%s", logs.String())
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestIDPrefetchDoesNotDelayIssuedID 验证预取失败不影响已发 UID，并在请求结束后仍执行及记录退避故障。
func TestIDPrefetchDoesNotDelayIssuedID(t *testing.T) {
	entered, release, completed := make(chan struct{}), make(chan struct{}), make(chan struct{})
	failure := errors.New("backoff Redis unavailable")
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	a := allocatorWithScope(allocatorCacheStub{
		next: func(context.Context, string, string, time.Duration) (idgen.Allocation, error) {
			return idgen.Allocation{Available: true, Start: "1", Offset: 79, Refill: true}, nil
		},
		backoff: func(ctx context.Context, _, _ string, _ time.Duration) error {
			close(entered)
			<-release
			if ctx.Err() != nil {
				t.Error("后台预取被发号请求取消")
			}
			return failure
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	id, err := a.NextID(ctx)
	cancel()
	if err != nil || id != 80 {
		t.Fatalf("已发号不应被预取失败影响：id=%d err=%v", id, err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("80% 消耗没有触发预取")
	}
	// 在后台错误记录后才读取日志，避免测试自身的数据竞争。
	logger := slog.New(&completionLogHandler{Handler: slog.NewJSONHandler(&logs, nil), done: completed})
	slog.SetDefault(logger)
	close(release)
	select {
	case <-completed:
	case <-time.After(time.Second):
		t.Fatal("异步退避失败缺少诊断日志")
	}
	if !strings.Contains(logs.String(), "reserve consumer ID segment failed") || !strings.Contains(logs.String(), failure.Error()) {
		t.Fatalf("后台错误日志缺少实际原因：%s", logs.String())
	}
}

// completionLogHandler 在目标日志写完后通知测试安全读取缓冲区。
type completionLogHandler struct {
	// Handler 保留被测结构化日志的原始处理器。
	slog.Handler
	done chan struct{}
}

// Handle 保持原日志内容并通知退避失败已完成写入。
func (h *completionLogHandler) Handle(ctx context.Context, record slog.Record) error {
	err := h.Handler.Handle(ctx, record)
	if record.Message == "back off consumer ID segment refill failed" {
		close(h.done)
	}
	return err
}

// TestIDReservationRangeLimits 验证非法水位或容量不更新数据库，边界内的段在提交后才可发布。
func TestIDReservationRangeLimits(t *testing.T) {
	for _, tc := range []struct {
		name  string
		start uint64
		size  uint32
		valid bool
	}{
		{"zero_start", 0, 100, false},
		{"zero_size", 1, 0, false},
		{"oversize", 1, maxIDSegmentSize + 1, false},
		{"signed_overflow", math.MaxInt64, 2, false},
		{"unsigned_overflow", math.MaxUint64, 2, false},
		{"last_signed_uid", math.MaxInt64, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			mock.ExpectBegin()
			mock.ExpectQuery("SELECT .* FROM `id_allocators`.*FOR UPDATE").WithArgs(consumerIDNamespace, 1).
				WillReturnRows(sqlmock.NewRows([]string{"namespace", "next_id", "segment_size"}).AddRow(consumerIDNamespace, fmt.Sprint(tc.start), tc.size))
			if tc.valid {
				// 超过 MaxInt64 的预留水位以十进制字符串绑定，避免 database/sql 拒绝高位 uint64。
				mock.ExpectExec("UPDATE `id_allocators` SET").WithArgs("9223372036854775808", consumerIDNamespace).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()
			} else {
				mock.ExpectRollback()
			}
			segment, err := newTestStore(db).ids.reserveSegment(context.Background())
			if tc.valid {
				if err != nil || segment.Start != tc.start || segment.Size != tc.size {
					t.Fatalf("合法边界段未提交：%+v %v", segment, err)
				}
			} else if err == nil || segment.Start != 0 {
				t.Fatalf("非法范围不得发布：%+v %v", segment, err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestIDRefillPublicationFailure 保证已提交但发布失败的段不会重复发布，且退避保留实际错误。
func TestIDRefillPublicationFailure(t *testing.T) {
	for _, scenario := range []string{"success", "network_error", "lost_lease"} {
		t.Run(scenario, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			mock.ExpectBegin()
			mock.ExpectQuery("SELECT .* FROM `id_allocators`.*FOR UPDATE").WithArgs(consumerIDNamespace, 1).
				WillReturnRows(sqlmock.NewRows([]string{"namespace", "next_id", "segment_size"}).AddRow(consumerIDNamespace, 9007199254740993, 100))
			mock.ExpectExec("UPDATE `id_allocators` SET").WithArgs("9007199254741093", consumerIDNamespace).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectCommit()
			a := newTestStore(db).ids
			failure := errors.New("publish response lost")
			published, backedOff := 0, 0
			a.cache = allocatorCacheStub{
				publish: func(_ context.Context, _, _ string, segment idgen.Segment) (bool, error) {
					published++
					if segment.Start != 9007199254740993 || segment.Size != 100 {
						t.Fatalf("发布的已提交范围错误：%+v", segment)
					}
					if scenario == "network_error" {
						return false, failure
					}
					return scenario == "success", nil
				},
				backoff: func(context.Context, string, string, time.Duration) error { backedOff++; return nil },
			}
			var logs bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			defer slog.SetDefault(previous)
			err = a.refill(context.Background(), "test-key", "not-for-logs")
			if published != 1 {
				t.Fatalf("不能因不确定响应重发相同号段：%d", published)
			}
			if scenario == "network_error" {
				if !errors.Is(err, failure) || backedOff != 1 || !strings.Contains(logs.String(), failure.Error()) || !strings.Contains(logs.String(), "publish consumer ID segment failed") {
					t.Fatalf("发布失败未保留错误与退避：err=%v backoff=%d logs=%s", err, backedOff, logs.String())
				}
			} else if err != nil || backedOff != 0 || strings.Contains(logs.String(), `"level":"ERROR"`) {
				t.Fatalf("成功或预期租约接管不应被误报故障：err=%v backoff=%d logs=%s", err, backedOff, logs.String())
			}
			if strings.Contains(logs.String(), "not-for-logs") {
				t.Fatal("日志泄露租约值")
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
