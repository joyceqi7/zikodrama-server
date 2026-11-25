package tiktokmedia

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"drama-server/internal/biz/operations"
)

// albumReorderRetryFixture 固定复现 33→28 集、删除第 7/13/26/30/32 集后保留身份并重排的冻结内容。
func albumReorderRetryFixture() (operations.AlbumDraft, map[string]any, map[string]any) {
	draft, base := albumRecoveryFixture()
	draft.Version, draft.Episodes = 1, nil
	old, next := []any{}, []any{}
	for sequence := 1; sequence <= 33; sequence++ {
		id := fmt.Sprintf("768560338405391%04d", sequence)
		episode := map[string]any{"episode_id": id, "title": fmt.Sprintf("原第%d集", sequence), "seq": sequence,
			"cover_list": []string{draft.CoverID}, "byteplus_vid": fmt.Sprintf("vid-%d", sequence)}
		old = append(old, episode)
		if sequence == 7 || sequence == 13 || sequence == 26 || sequence == 30 || sequence == 32 {
			continue
		}
		renumbered := len(next) + 1
		draft.Episodes = append(draft.Episodes, operations.AlbumEpisodeDraft{Sequence: renumbered,
			PlatformEpisodeID: id, Title: episode["title"].(string), CoverID: draft.CoverID, BytePlusVID: episode["byteplus_vid"].(string)})
		next = append(next, map[string]any{"episode_id": id, "title": episode["title"], "seq": renumbered,
			"cover_list": []string{draft.CoverID}, "byteplus_vid": episode["byteplus_vid"]})
	}
	base["review_status"], base["episode_info_list"] = 3, old
	base["album_info"].(map[string]any)["seq_num"] = 33
	encoded, _ := json.Marshal(base)
	var updated map[string]any
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	_ = decoder.Decode(&updated)
	updated["version"], updated["current_version"], updated["review_status"] = 2, 2, 0
	updated["episode_info_list"] = next
	updated["album_info"].(map[string]any)["seq_num"] = 28
	return draft, base, updated
}

// albumRetryServer 仅允许本地查询和一次携带原版本号的更新，禁止创建、送审、上线或改用新版本重放。
func albumRetryServer(t *testing.T, draft operations.AlbumDraft, before, after []any, updateStatus int, updateError string) (*Client, map[string]int) {
	t.Helper()
	calls := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls[r.Method+" "+r.URL.Path]++
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v2/oauth/token/":
			_, _ = io.WriteString(w, `{"access_token":"sensitive-token","expires_in":7200}`)
		case "/v2/sg/shortdrama/album/query/":
			versions := before
			if calls["POST /v2/sg/shortdrama/album/update/"] > 0 {
				versions = after
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"album_version_list": versions}})
		case "/v2/sg/shortdrama/album/update/":
			var request albumUpdateRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
				http.Error(w, "invalid local test request", http.StatusBadRequest)
				return
			}
			if request.Version != 1 || request.AlbumID.String() != draft.AlbumID || request.OperationType != 1 || len(request.EpisodeInfoList) != 28 || request.AlbumInfo.SequenceCount != 28 {
				t.Errorf("恢复未固定原版本与 28 集冻结内容：%+v", request)
			}
			mapping := map[string]string{}
			for index, episode := range request.EpisodeInfoList {
				want := draft.Episodes[index]
				if episode.EpisodeID != want.PlatformEpisodeID || episode.BytePlusVID != want.BytePlusVID || episode.Sequence != want.Sequence {
					t.Error("恢复改变了冻结内容或分集身份")
				}
				mapping[want.PlatformEpisodeID] = want.PlatformEpisodeID
			}
			w.WriteHeader(updateStatus)
			if updateError != "" {
				_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": updateError, "message": "test platform failure"}})
			} else {
				_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"version": 2, "episode_id_map": mapping}})
			}
		default:
			t.Errorf("恢复调用了未授权阶段：%s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	client := NewClient("owner-client", "sensitive-secret", server.Client())
	client.baseURL = server.URL
	return client, calls
}

func TestRetryAlbumUpdateRecoversUnchangedBaseWithVersionProtection(t *testing.T) {
	for _, test := range []struct {
		name, code string
		status     int
		writes     int
		queries    int
		already    bool
	}{
		{name: "原拒审版仍为 V1，恢复 28 集", status: 200, writes: 1, queries: 1},
		{name: "已有完整 V2，只核对", status: 200, queries: 1, already: true},
		{name: "更新响应 500，查询确认已写入", status: 500, code: "internal_error", writes: 1, queries: 2},
		{name: "原请求延迟完成，22003 冲突只核对", status: 200, code: "22003", writes: 1, queries: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			draft, base, updated := albumReorderRetryFixture()
			before := []any{base}
			if test.already {
				before = []any{updated, base}
			}
			client, calls := albumRetryServer(t, draft, before, []any{updated, base}, test.status, test.code)
			result, err := client.RetryAlbumUpdate(context.Background(), draft)
			want := map[int]string{}
			for _, episode := range draft.Episodes {
				want[episode.Sequence] = episode.PlatformEpisodeID
			}
			if err != nil || result.Version != 2 || !reflect.DeepEqual(result.EpisodeIDsBySequence, want) {
				t.Fatalf("没有恢复冻结的 28 集：%+v %v", result, err)
			}
			if calls["POST /v2/sg/shortdrama/album/update/"] != test.writes || calls["GET /v2/sg/shortdrama/album/query/"] != test.queries {
				t.Fatalf("恢复额外写入或跳过核对：%v", calls)
			}
		})
	}
}

func TestRetryAlbumUpdateNeverRewritesIncompleteOrAdvancedBase(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*operations.AlbumDraft, map[string]any, map[string]any) []any
	}{
		{"首次同步无版本", func(d *operations.AlbumDraft, _, _ map[string]any) []any { d.Version = 0; return nil }},
		{"原版本不可见", func(_ *operations.AlbumDraft, _, _ map[string]any) []any { return nil }},
		{"新版本详情尚不可见", func(_ *operations.AlbumDraft, b, _ map[string]any) []any { b["current_version"] = 2; return []any{b} }},
		{"原版本缺分集", func(_ *operations.AlbumDraft, b, _ map[string]any) []any {
			b["episode_info_list"] = b["episode_info_list"].([]any)[:32]
			return []any{b}
		}},
		{"原版本重复", func(_ *operations.AlbumDraft, b, _ map[string]any) []any { return []any{b, b} }},
		{"原版本分集身份重复", func(_ *operations.AlbumDraft, b, _ map[string]any) []any {
			episodes := b["episode_info_list"].([]any)
			episodes[1].(map[string]any)["episode_id"] = episodes[0].(map[string]any)["episode_id"]
			return []any{b}
		}},
		{"原版本集号重复", func(_ *operations.AlbumDraft, b, _ map[string]any) []any {
			b["episode_info_list"].([]any)[1].(map[string]any)["seq"] = 1
			return []any{b}
		}},
		{"缺当前版本", func(_ *operations.AlbumDraft, b, _ map[string]any) []any {
			delete(b, "current_version")
			return []any{b}
		}},
		{"原版本审核中", func(_ *operations.AlbumDraft, b, _ map[string]any) []any { b["review_status"] = 1; return []any{b} }},
		{"原版本申诉中", func(_ *operations.AlbumDraft, b, _ map[string]any) []any { b["review_status"] = 5; return []any{b} }},
		{"审核状态缺失", func(_ *operations.AlbumDraft, b, _ map[string]any) []any { delete(b, "review_status"); return []any{b} }},
		{"存在冲突新内容", func(_ *operations.AlbumDraft, b, u map[string]any) []any {
			u["album_info"].(map[string]any)["desp"] = "其他内容"
			return []any{u, b}
		}},
		{"新版本缺一集，不能以旧映射补齐", func(_ *operations.AlbumDraft, b, u map[string]any) []any {
			u["episode_info_list"] = u["episode_info_list"].([]any)[:27]
			return []any{u, b}
		}},
		{"新版本缺少全部分集，不能以旧映射确认", func(_ *operations.AlbumDraft, b, u map[string]any) []any {
			delete(u, "episode_info_list")
			return []any{u, b}
		}},
		{"分集归属不同", func(d *operations.AlbumDraft, b, _ map[string]any) []any {
			d.Episodes[0].PlatformEpisodeID = "999"
			return []any{b}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			draft, base, updated := albumReorderRetryFixture()
			client, calls := albumRetryServer(t, draft, test.change(&draft, base, updated), nil, 200, "")
			_, err := client.RetryAlbumUpdate(context.Background(), draft)
			var failure *operations.PlatformCallError
			if !errors.As(err, &failure) || failure.Code != "album_update_unconfirmed" || !failure.OutcomeUnknown {
				t.Fatalf("缺失或冲突的结果没有保留检查点：%v", err)
			}
			if calls["POST /v2/sg/shortdrama/album/update/"] != 0 {
				t.Fatalf("不安全地重写 Album：%v", calls)
			}
		})
	}
}

// TestRetryAlbumUpdateConflictNeverAdvancesBase 验证查询后的并发版本推进只核对，不以新版本号覆盖其他内容。
func TestRetryAlbumUpdateConflictNeverAdvancesBase(t *testing.T) {
	draft, base, updated := albumReorderRetryFixture()
	updated["album_info"].(map[string]any)["desp"] = "其他操作者的新内容"
	client, calls := albumRetryServer(t, draft, []any{base}, []any{updated, base}, 200, "22003")
	_, err := client.RetryAlbumUpdate(context.Background(), draft)
	var failure *operations.PlatformCallError
	if !errors.As(err, &failure) || failure.Code != "album_update_unconfirmed" || !failure.OutcomeUnknown ||
		!strings.Contains(err.Error(), "album_version_conflict") {
		t.Fatalf("并发冲突没有保留未确认检查点及原故障：%v", err)
	}
	if calls["POST /v2/sg/shortdrama/album/update/"] != 1 || calls["GET /v2/sg/shortdrama/album/query/"] != 2 {
		t.Fatalf("并发冲突后推进了原版本或连续重写：%v", calls)
	}
}

func TestRetryAlbumUpdatePreserves500CauseAndSafeLogs(t *testing.T) {
	previous := slog.Default()
	t.Cleanup(func() { slog.SetDefault(previous) })
	var logs bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	draft, base, updated := albumReorderRetryFixture()
	client, calls := albumRetryServer(t, draft, []any{base}, []any{base}, 500, "internal_error")
	_, err := client.RetryAlbumUpdate(context.Background(), draft)
	if err == nil || !strings.Contains(err.Error(), "http_500") || !strings.Contains(err.Error(), "album_update_unconfirmed") {
		t.Fatalf("原平台错误被核对错误遮蔽：%v", err)
	}
	if calls["POST /v2/sg/shortdrama/album/update/"] != 1 || !strings.Contains(logs.String(), "TikTok API request failed") || !strings.Contains(logs.String(), "http_500") {
		t.Fatal("失败缺少原错误日志或发生连续重写", logs.String(), calls)
	}
	for _, sensitive := range []string{"sensitive-secret", "sensitive-token", "vid-", "official-cover"} {
		if strings.Contains(logs.String(), sensitive) {
			t.Fatal("日志泄漏敏感内容", logs.String())
		}
	}
	logs.Reset()
	client, _ = albumRetryServer(t, draft, []any{base}, []any{updated}, 200, "")
	if _, err := client.RetryAlbumUpdate(context.Background(), draft); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logs.String(), `"level":"ERROR"`) || strings.Contains(logs.String(), `"level":"WARN"`) {
		t.Fatal("正常恢复误报错误日志", logs.String())
	}
}
