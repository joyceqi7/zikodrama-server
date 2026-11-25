package tiktokmedia

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"testing"
)

// TestUpdateAlbumAcceptsInitialSequenceMapping 使用已观测的 seq:序号 合同，验证首次同步直接确认五集映射，不依赖尚未可见的版本查询。
func TestUpdateAlbumAcceptsInitialSequenceMapping(t *testing.T) {
	logs := capturePlatformDiagnostics(t)
	draft, _ := albumRecoveryFixture()
	draft.ClientKey = "owner"
	draft.Episodes = draft.Episodes[:5]
	updates, queries := 0, 0
	client := contractClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /v2/sg/shortdrama/album/update/":
			updates++
			var request albumUpdateRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("更新请求无法解码：%v", err)
			}
			if request.Version != 0 || len(request.EpisodeInfoList) != 5 {
				t.Errorf("首次同步未提交五集新内容：%+v", request)
			}
			for _, episode := range request.EpisodeInfoList {
				if episode.EpisodeID != "" {
					t.Error("首次同步不应发送既有官方分集标识")
				}
			}
			_, _ = io.WriteString(w, `{"data":{"version":1,"episode_id_map":{"seq:1":"7695601716066535445","seq:2":"7695601716066551829","seq:3":"7695601716066568213","seq:4":"7695601716066584597","seq:5":"7695601716066600981"}}}`)
		case "GET /v2/sg/shortdrama/album/query/":
			queries++
			_, _ = io.WriteString(w, `{"data":{"album_version_list":[]}}`)
		default:
			t.Errorf("出现无关官方调用：%s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})
	result, err := client.UpdateAlbum(context.Background(), draft)
	want := map[int]string{
		1: "7695601716066535445", 2: "7695601716066551829", 3: "7695601716066568213",
		4: "7695601716066584597", 5: "7695601716066600981",
	}
	if err != nil || result.Version != 1 || !reflect.DeepEqual(result.EpisodeIDsBySequence, want) {
		t.Fatalf("首次同步未直接确认实际返回的五集映射：%+v %v", result, err)
	}
	if updates != 1 || queries != 0 || logs.Len() != 0 {
		t.Fatalf("完整成功响应不应回查或记录故障：updates=%d queries=%d logs=%s", updates, queries, logs.String())
	}
}

// TestAlbumRecoveryWaitsForReportedVersion 验证 current_version 已前进但新版本详情尚未返回时，保留可重查的未知结果并记录可见性原因。
func TestAlbumRecoveryWaitsForReportedVersion(t *testing.T) {
	logs := capturePlatformDiagnostics(t)
	draft, oldVersion := albumRecoveryFixture()
	draft.Version = 2
	oldVersion["version"], oldVersion["current_version"] = 2, 3
	olderVersion := make(map[string]any, len(oldVersion))
	for key, value := range oldVersion {
		olderVersion[key] = value
	}
	olderVersion["version"] = 1
	client, calls := albumRecoveryServer(t, []any{oldVersion, olderVersion}, "", 200)
	_, err := client.ResolveAlbumUpdate(context.Background(), draft)
	assertPlatformFailureUnchanged(t, err, "album_update_unconfirmed", true, true)
	record := onePlatformDiagnostic(t, logs)
	if record["latest_version"] != float64(3) || record["version_count"] != float64(2) ||
		!reflect.DeepEqual(record["version_mismatch_reasons"], map[string]any{"latest_version_not_visible": float64(1)}) {
		t.Fatalf("未区分已宣告新版本与已返回版本详情：%s", logs.String())
	}
	assertDiagnosticSecretsAbsent(t, logs.String(), "sensitive-token", "sensitive-secret", "sensitive-payload")
	logs.Reset()
	_, newVersion := albumRecoveryFixture()
	newVersion["version"], newVersion["current_version"] = 3, 3
	client, nextCalls := albumRecoveryServer(t, []any{newVersion}, "", 200)
	result, err := client.ResolveAlbumUpdate(context.Background(), draft)
	if err != nil || result.Version != 3 || len(result.EpisodeIDsBySequence) != 11 || logs.Len() != 0 {
		t.Fatalf("新版本可见后未确认完整映射或成功路径误报：%+v %v logs=%s", result, err, logs.String())
	}
	for _, count := range []map[string]int{calls, nextCalls} {
		if count["POST /v2/sg/shortdrama/album/update/"] != 0 || count["GET /v2/sg/shortdrama/album/query/"] != 1 {
			t.Fatalf("可见性核对应只读查询，不重复更新：%v", count)
		}
	}
}
