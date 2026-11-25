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
	"strings"
	"testing"

	"drama-server/internal/biz/operations"
)

// albumRecoveryFixture 构造包含大整数分集标识的完整官方查询，不从被测解析函数生成期望值。
func albumRecoveryFixture() (operations.AlbumDraft, map[string]any) {
	draft := operations.AlbumDraft{ClientKey: "owner-client", AlbumID: "7685608308176193557", Language: "zh",
		Title: "查询恢复测试", Description: "冻结简介", CoverID: "official-cover", Year: 2024, AlbumStatus: 2,
		DramaType: 4, TagIDs: []string{"7"}}
	var episodes []any
	for seq := 1; seq <= 11; seq++ {
		title, vid := fmt.Sprintf("第%d集", seq), fmt.Sprintf("vid-%d", seq)
		draft.Episodes = append(draft.Episodes, operations.AlbumEpisodeDraft{Sequence: seq, Title: title, CoverID: draft.CoverID, BytePlusVID: vid})
		episodes = append(episodes, map[string]any{"episode_id": json.Number(fmt.Sprintf("76856033840539095%02d", seq)),
			"title": title, "seq": seq, "cover_list": []string{draft.CoverID}, "byteplus_vid": vid})
	}
	version := map[string]any{"album_id": json.Number(draft.AlbumID), "version": 1, "current_version": 1,
		"review_status": 0, "publish_status": 0, "episode_info_list": episodes,
		"album_info": map[string]any{"language": draft.Language, "title": draft.Title, "desp": draft.Description,
			"seq_num": 11, "cover_list": []string{draft.CoverID}, "year": draft.Year,
			"album_status": draft.AlbumStatus, "drama_type": draft.DramaType, "tag_list": []int{7}}}
	return draft, version
}

// albumRecoveryServer 仅提供本地 HTTP 合同响应，计数明确区分一次更新和只读查询。
func albumRecoveryServer(t *testing.T, versionList []any, updateBody string, updateStatus int) (*Client, map[string]int) {
	t.Helper()
	calls := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls[r.Method+" "+r.URL.Path]++
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Tt-Logid", "album-query-request-sensitive-token")
		switch r.URL.Path {
		case "/v2/oauth/token/":
			_, _ = io.WriteString(w, `{"access_token":"sensitive-token","expires_in":7200}`)
		case "/v2/sg/shortdrama/album/update/":
			w.WriteHeader(updateStatus)
			_, _ = io.WriteString(w, updateBody)
		case "/v2/sg/shortdrama/album/query/":
			if r.Method != http.MethodGet || r.URL.Query().Get("album_id") != "7685608308176193557" || r.URL.Query().Get("client_key") != "owner-client" || r.URL.Query().Has("version") {
				t.Errorf("恢复查询未按剧目读取所有版本：%s %s", r.Method, r.URL.Path)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"album_version_list": versionList}, "private": "sensitive-payload"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	client := NewClient("owner-client", "sensitive-secret", server.Client())
	client.baseURL = server.URL
	return client, calls
}

func TestAlbumUpdateRecoversIncompleteResponseWithoutAnotherWrite(t *testing.T) {
	for _, test := range []struct {
		name, body string
		status     int
	}{
		{"映射缺失", `{"data":{"version":1}}`, 200},
		{"映射键无法识别", `{"data":{"version":1,"episode_id_map":{"1":"123"}}}`, 200},
		{"版本缺失", `{"data":{}}`, 200},
		{"响应格式错误", `{`, 200},
		{"平台响应未知", `{"error":{"code":"service_error"}}`, 502},
	} {
		t.Run(test.name, func(t *testing.T) {
			draft, version := albumRecoveryFixture()
			client, calls := albumRecoveryServer(t, []any{version}, test.body, test.status)
			result, err := client.UpdateAlbum(context.Background(), draft)
			if err != nil || result.Version != 1 || len(result.EpisodeIDsBySequence) != 11 || result.EpisodeIDsBySequence[11] != "7685603384053909511" {
				t.Fatalf("未从已写入的 11 集恢复：%+v %v", result, err)
			}
			if calls["POST /v2/sg/shortdrama/album/update/"] != 1 || calls["GET /v2/sg/shortdrama/album/query/"] != 1 {
				t.Fatalf("没有以一次更新和一次查询恢复：%v", calls)
			}
		})
	}
}

// TestResolveAlbumUpdateAcceptsListedAlbumWithOlderOnlineVersion 验证旧版本仍在线时，
// Album 整体保持上架不影响恢复本次尚未送审的新版本。
func TestResolveAlbumUpdateAcceptsListedAlbumWithOlderOnlineVersion(t *testing.T) {
	draft, version := albumRecoveryFixture()
	draft.Version = 1
	version["version"], version["current_version"] = 2, 2
	version["online_version"], version["publish_status"], version["review_status"] = 1, 1, 0
	client, calls := albumRecoveryServer(t, []any{version}, "", 200)

	result, err := client.ResolveAlbumUpdate(context.Background(), draft)
	if err != nil || result.Version != 2 || len(result.EpisodeIDsBySequence) != len(draft.Episodes) {
		t.Fatalf("已上架 Album 的新版本没有恢复：%+v %v", result, err)
	}
	if calls["POST /v2/sg/shortdrama/album/update/"] != 0 || calls["GET /v2/sg/shortdrama/album/query/"] != 1 {
		t.Fatalf("恢复新版本发生了额外写入：%v", calls)
	}
}

// TestResolveAlbumUpdateRequiresCompleteExistingAndAppendedEpisodes 验证查询只返回新增集时不能以旧映射证明原集内容，完整可见后才恢复。
func TestResolveAlbumUpdateRequiresCompleteExistingAndAppendedEpisodes(t *testing.T) {
	logs := capturePlatformDiagnostics(t)
	draft, version := albumRecoveryFixture()
	draft.Version = 1
	original := version["episode_info_list"].([]any)
	for index := range draft.Episodes {
		draft.Episodes[index].PlatformEpisodeID = version["episode_info_list"].([]any)[index].(map[string]any)["episode_id"].(json.Number).String()
	}
	appended := make([]any, 0, 29)
	for sequence := 12; sequence <= 40; sequence++ {
		title, vid := fmt.Sprintf("第%d集", sequence), fmt.Sprintf("vid-%d", sequence)
		draft.Episodes = append(draft.Episodes, operations.AlbumEpisodeDraft{
			Sequence: sequence, Title: title, CoverID: draft.CoverID, BytePlusVID: vid,
		})
		appended = append(appended, map[string]any{
			"episode_id": json.Number(fmt.Sprintf("768560338405391%04d", sequence)),
			"title":      title, "seq": sequence, "cover_list": []string{draft.CoverID}, "byteplus_vid": vid,
		})
	}
	version["version"], version["current_version"], version["online_version"], version["publish_status"] = 2, 2, 1, 1
	version["episode_info_list"] = appended
	version["album_info"].(map[string]any)["seq_num"] = 40
	client, calls := albumRecoveryServer(t, []any{version}, "", 200)
	_, err := client.ResolveAlbumUpdate(context.Background(), draft)
	var failure *operations.PlatformCallError
	if !errors.As(err, &failure) || failure.Code != "album_update_unconfirmed" || !failure.OutcomeUnknown {
		t.Fatalf("查询缺少原 11 集却确认了完整内容：%v", err)
	}
	if !strings.Contains(logs.String(), `"episode_count":1`) {
		t.Fatal("缺项查询没有记录分集数量差异", logs.String())
	}
	logs.Reset()
	version["episode_info_list"] = append(append([]any{}, original...), appended...)
	client, nextCalls := albumRecoveryServer(t, []any{version}, "", 200)

	result, err := client.ResolveAlbumUpdate(context.Background(), draft)
	if err != nil || result.Version != 2 || len(result.EpisodeIDsBySequence) != 40 || logs.Len() != 0 {
		t.Fatalf("完整新版本未恢复或误报日志：%+v %v logs=%s", result, err, logs.String())
	}
	if result.EpisodeIDsBySequence[1] != draft.Episodes[0].PlatformEpisodeID ||
		result.EpisodeIDsBySequence[11] != draft.Episodes[10].PlatformEpisodeID ||
		result.EpisodeIDsBySequence[12] == "" || result.EpisodeIDsBySequence[40] == "" {
		t.Fatalf("续传恢复没有合并既有与新增分集：%+v", result.EpisodeIDsBySequence)
	}
	for _, count := range []map[string]int{calls, nextCalls} {
		if count["POST /v2/sg/shortdrama/album/update/"] != 0 || count["GET /v2/sg/shortdrama/album/query/"] != 1 {
			t.Fatalf("续传恢复发生了额外写入：%v", count)
		}
	}
}

func TestResolveAlbumUpdateRejectsUnconfirmedContent(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(map[string]any) []any
	}{
		{"版本不存在", func(v map[string]any) []any { return nil }},
		{"剧目错误", func(v map[string]any) []any { v["album_id"] = "999"; return []any{v} }},
		{"旧版本", func(v map[string]any) []any { v["version"] = 0; return []any{v} }},
		{"存在更新版本", func(v map[string]any) []any { v["current_version"] = 2; return []any{v} }},
		{"重复版本歧义", func(v map[string]any) []any { return []any{v, v} }},
		{"已送审", func(v map[string]any) []any { v["review_status"] = 1; return []any{v} }},
		{"缺少审核状态", func(v map[string]any) []any { delete(v, "review_status"); return []any{v} }},
		{"当前版本已上线", func(v map[string]any) []any {
			v["online_version"] = v["version"]
			return []any{v}
		}},
		{"剧目资料不同", func(v map[string]any) []any {
			v["album_info"].(map[string]any)["desp"] = "其他简介"
			return []any{v}
		}},
		{"标签不同", func(v map[string]any) []any { v["album_info"].(map[string]any)["tag_list"] = []int{9}; return []any{v} }},
		{"缺少分集", func(v map[string]any) []any {
			v["episode_info_list"] = v["episode_info_list"].([]any)[:10]
			return []any{v}
		}},
		{"分集VID不同", func(v map[string]any) []any {
			v["episode_info_list"].([]any)[0].(map[string]any)["byteplus_vid"] = "other-vid"
			return []any{v}
		}},
		{"分集封面不同", func(v map[string]any) []any {
			v["episode_info_list"].([]any)[0].(map[string]any)["cover_list"] = []string{"other-cover"}
			return []any{v}
		}},
		{"序号重复", func(v map[string]any) []any {
			v["episode_info_list"].([]any)[1].(map[string]any)["seq"] = 1
			return []any{v}
		}},
		{"标识重复", func(v map[string]any) []any {
			list := v["episode_info_list"].([]any)
			list[1].(map[string]any)["episode_id"] = list[0].(map[string]any)["episode_id"]
			return []any{v}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			draft, version := albumRecoveryFixture()
			client, calls := albumRecoveryServer(t, test.change(version), "", 200)
			_, err := client.ResolveAlbumUpdate(context.Background(), draft)
			var platformErr *operations.PlatformCallError
			if !errors.As(err, &platformErr) || platformErr.Code != "album_update_unconfirmed" || !platformErr.OutcomeUnknown {
				t.Fatalf("不完整结果未保留未知状态：%v", err)
			}
			if calls["POST /v2/sg/shortdrama/album/update/"] != 0 {
				t.Fatal("查询恢复重新写入了剧目")
			}
		})
	}
}

func TestAlbumUpdateRecoveryLogsSafeDiagnostics(t *testing.T) {
	previous := slog.Default()
	t.Cleanup(func() { slog.SetDefault(previous) })
	var output bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	draft, version := albumRecoveryFixture()
	client, _ := albumRecoveryServer(t, []any{version}, `{"data":{"version":1},"client_secret":"sensitive-secret","media_url":"https://private.example/media"}`, 200)
	if _, err := client.UpdateAlbum(context.Background(), draft); err != nil {
		t.Fatal(err)
	}
	logs := output.String()
	for _, want := range []string{"TikTok album update response incomplete", "invalid_episode_mapping", `"response_version":1`, `"response_mapping_count":0`, `"expected_episode_count":11`, "platform_request_id"} {
		if !strings.Contains(logs, want) {
			t.Fatalf("缺少可定位的诊断 %s：%s", want, logs)
		}
	}
	for _, secret := range []string{"sensitive-token", "sensitive-secret", "sensitive-payload", "https://private.example/media", `"level":"ERROR"`} {
		if strings.Contains(logs, secret) {
			t.Fatalf("恢复成功误报错误或泄漏敏感数据：%s", logs)
		}
	}
	output.Reset()
	client, _ = albumRecoveryServer(t, []any{version}, `{"data":{"version":1,"episode_id_map":{"seq:1":"7685603384053909501","seq:2":"7685603384053909502","seq:3":"7685603384053909503","seq:4":"7685603384053909504","seq:5":"7685603384053909505","seq:6":"7685603384053909506","seq:7":"7685603384053909507","seq:8":"7685603384053909508","seq:9":"7685603384053909509","seq:10":"7685603384053909510","seq:11":"7685603384053909511"}}}`, 200)
	if _, err := client.UpdateAlbum(context.Background(), draft); err != nil {
		t.Fatal(err)
	}
	if output.Len() != 0 {
		t.Fatalf("正常成功误报日志：%s", output.String())
	}
}

// TestAlbumRecoveryRetryClassification 只让暂时不可用和尚未可见的版本进入自动查询重试。
func TestAlbumRecoveryRetryClassification(t *testing.T) {
	for _, status := range []int{http.StatusServiceUnavailable, http.StatusUnauthorized} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v2/oauth/token/" {
					_, _ = io.WriteString(w, `{"access_token":"test-token","expires_in":7200}`)
					return
				}
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"error":{"code":"service_error"}}`)
			}))
			defer server.Close()
			client := NewClient("owner-client", "test-secret", server.Client())
			client.baseURL = server.URL
			draft, _ := albumRecoveryFixture()
			_, err := client.ResolveAlbumUpdate(context.Background(), draft)
			var failure *operations.PlatformCallError
			if !errors.As(err, &failure) || !failure.OutcomeUnknown || failure.Retryable != (status == http.StatusServiceUnavailable) {
				t.Fatalf("错误的查询重试分类：%v", err)
			}
		})
	}
	draft, _ := albumRecoveryFixture()
	client, _ := albumRecoveryServer(t, nil, "", 200)
	_, err := client.ResolveAlbumUpdate(context.Background(), draft)
	var failure *operations.PlatformCallError
	if !errors.As(err, &failure) || !failure.Retryable {
		t.Fatal("未出现的版本应有限核对")
	}
}
