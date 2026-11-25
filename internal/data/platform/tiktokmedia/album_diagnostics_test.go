package tiktokmedia

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// TestAlbumUpdateUnknownMappingDiagnostics 验证映射无法解析时能定位键类别，且不泄漏动态键、标识或凭据。
func TestAlbumUpdateUnknownMappingDiagnostics(t *testing.T) {
	logs := capturePlatformDiagnostics(t)
	draft, _ := albumRecoveryFixture()
	draft.ClientKey = "owner"
	draft.Episodes = draft.Episodes[:5]
	writes, queries := 0, 0
	client := contractClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Tt-Logid", "album-test-token-request")
		switch r.URL.Path {
		case "/v2/sg/shortdrama/album/update/":
			writes++
			_, _ = io.WriteString(w, `{"data":{"version":1,"episode_id_map":{"1":"private-id-1","2":"private-id-2","seq:invalid":"private-id-3","test-token":"private-id-4","https://private.test/episode":"private-id-5"},"access_token":"response-token","private-field":"private-value"}}`)
		case "/v2/sg/shortdrama/album/query/":
			queries++
			_, _ = io.WriteString(w, `{"data":{"album_version_list":[]}}`)
		default:
			t.Errorf("非预期请求：%s", r.URL.Path)
		}
	})
	_, err := client.UpdateAlbum(context.Background(), draft)
	assertPlatformFailureUnchanged(t, err, "album_update_unconfirmed", true, true)
	if writes != 1 || queries != 1 {
		t.Fatalf("未保留一次更新、一次只读确认：更新=%d，查询=%d", writes, queries)
	}
	records := albumDiagnosticRecords(t, logs.String())
	if len(records) != 2 {
		t.Fatalf("应各记录一次更新异常和确认失败：%s", logs.String())
	}
	update := records[0]
	for key, want := range map[string]any{
		"msg": "TikTok album update response incomplete", "level": "WARN",
		"error": "TikTok platform call failed: invalid_episode_mapping", "response_payload": "data",
		"response_mapping_count": float64(5), "resolved_mapping_count": float64(0),
		"platform_request_id": "album-[REDACTED]-request",
	} {
		if update[key] != want {
			t.Errorf("更新诊断字段 %s：得到 %v，期望 %v", key, update[key], want)
		}
	}
	if !reflect.DeepEqual(update["episode_mapping_key_counts"], map[string]any{"numeric": float64(2), "invalid_sequence": float64(1), "unrecognized": float64(2)}) {
		t.Errorf("缺少映射键类别计数：%v", update["episode_mapping_key_counts"])
	}
	if !reflect.DeepEqual(update["root_field_types"], map[string]any{"data": "object"}) ||
		!reflect.DeepEqual(update["data_field_types"], map[string]any{"version": "number", "episode_id_map": "object", "unknown_field_count": "2"}) {
		t.Errorf("缺少响应字段类型：%v", update)
	}
	assertDiagnosticSecretsAbsent(t, logs.String(), "test-token", "test-secret", "response-token", "private-id", "private.test", "seq:invalid", "private-field", "private-value")
}

// TestAlbumUpdateExistingIDsCannotHideInvalidResponse 验证完整旧映射不能掩盖未知键、非法标识或同集冲突。
func TestAlbumUpdateExistingIDsCannotHideInvalidResponse(t *testing.T) {
	for _, test := range []struct{ name, mapping string }{
		{"未知键", `{"private-key":"7685603384053909999"}`},
		{"同集冲突", `{"7685603384053909501":"7685603384053909998","seq:1":"7685603384053909999"}`},
		{"非法标识", `{"seq:1":"private-id"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			logs := capturePlatformDiagnostics(t)
			draft, _ := albumRecoveryFixture()
			draft.ClientKey = "owner"
			draft.Version = 1
			draft.Episodes = draft.Episodes[:1]
			draft.Episodes[0].PlatformEpisodeID = "7685603384053909501"
			writes, queries := 0, 0
			client := contractClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v2/sg/shortdrama/album/update/" {
					writes++
					_, _ = io.WriteString(w, `{"data":{"version":2,"episode_id_map":`+test.mapping+`}}`)
				} else if r.URL.Path == "/v2/sg/shortdrama/album/query/" && r.Method == http.MethodGet {
					queries++
					_, _ = io.WriteString(w, `{"data":{"album_version_list":[]}}`)
				} else {
					t.Errorf("非预期请求：%s %s", r.Method, r.URL.Path)
				}
			})
			_, err := client.UpdateAlbum(context.Background(), draft)
			assertPlatformFailureUnchanged(t, err, "album_update_unconfirmed", true, true)
			if writes != 1 || queries != 1 {
				t.Fatal("未知映射被旧身份掩盖或重复执行了更新")
			}
			assertDiagnosticSecretsAbsent(t, logs.String(), "private-key", "private-id", "7685603384053909501", "7685603384053909998", "7685603384053909999")
		})
	}
}

// TestAlbumRecoveryVersionListDiagnostics 区分缺失、空值、空列表和实际解码层，不猜测未知字段或改变确认结果。
func TestAlbumRecoveryVersionListDiagnostics(t *testing.T) {
	for _, test := range []struct {
		name, response, payload string
		root, data              map[string]any
	}{
		{"字段缺失", `{"data":{"private-list":[]}}`, "data", map[string]any{"data": "object"}, map[string]any{"unknown_field_count": "1"}},
		{"字段空值", `{"data":{"album_version_list":null}}`, "data", map[string]any{"data": "object"}, map[string]any{"album_version_list": "null"}},
		{"字段空列表", `{"data":{"album_version_list":[]}}`, "data", map[string]any{"data": "object"}, map[string]any{"album_version_list": "array"}},
		{"顶层列表", `{"album_version_list":[]}`, "root", map[string]any{"album_version_list": "array"}, map[string]any{}},
		{"空data不读取顶层列表", `{"data":{},"album_version_list":[]}`, "data", map[string]any{"data": "object", "album_version_list": "array"}, map[string]any{}},
		{"大小写与实际解码一致", `{"DATA":{"ALBUM_VERSION_LIST":[]}}`, "data", map[string]any{"data": "object"}, map[string]any{"album_version_list": "array"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			logs := capturePlatformDiagnostics(t)
			draft, _ := albumRecoveryFixture()
			draft.ClientKey = "owner"
			queries := 0
			client := contractClient(t, func(w http.ResponseWriter, r *http.Request) {
				queries++
				if r.Method != http.MethodGet || r.URL.Path != "/v2/sg/shortdrama/album/query/" {
					t.Errorf("恢复不应写入：%s %s", r.Method, r.URL.Path)
				}
				_, _ = io.WriteString(w, test.response)
			})
			_, err := client.ResolveAlbumUpdate(context.Background(), draft)
			assertPlatformFailureUnchanged(t, err, "album_update_unconfirmed", true, true)
			record := onePlatformDiagnostic(t, logs)
			if queries != 1 || record["msg"] != "confirm TikTok album update failed" || record["error"] != err.Error() || record["level"] != "WARN" || record["version_count"] != float64(0) || record["http_status"] != float64(200) || record["response_payload"] != test.payload {
				t.Fatalf("恢复诊断或错误语义变化：%s", logs.String())
			}
			if !reflect.DeepEqual(record["root_field_types"], test.root) || !reflect.DeepEqual(record["data_field_types"], test.data) {
				t.Errorf("没有区分响应结构：%s", logs.String())
			}
			assertDiagnosticSecretsAbsent(t, logs.String(), "private-list", "test-secret", "test-token")
		})
	}
}

// TestAlbumRecoveryMismatchDiagnostics 验证最新版本已可见但内容不符时指出固定核对项，仍保留未知写入状态。
func TestAlbumRecoveryMismatchDiagnostics(t *testing.T) {
	for _, test := range []struct {
		name, reason string
		change       func(map[string]any)
	}{
		{"语言不符", "language", func(v map[string]any) { v["album_info"].(map[string]any)["language"] = "private-language" }},
		{"简介不符", "description", func(v map[string]any) { v["album_info"].(map[string]any)["desp"] = "private-description" }},
		{"封面不符", "cover_list", func(v map[string]any) { v["album_info"].(map[string]any)["cover_list"] = []string{"private-cover"} }},
		{"视频不符", "episode_vid", func(v map[string]any) {
			v["episode_info_list"].([]any)[0].(map[string]any)["byteplus_vid"] = "private-vid"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			logs := capturePlatformDiagnostics(t)
			draft, version := albumRecoveryFixture()
			test.change(version)
			client, calls := albumRecoveryServer(t, []any{version}, "", 200)
			_, err := client.ResolveAlbumUpdate(context.Background(), draft)
			assertPlatformFailureUnchanged(t, err, "album_update_unconfirmed", false, true)
			record := onePlatformDiagnostic(t, logs)
			if record["msg"] != "confirm TikTok album update failed" || record["error"] != err.Error() || record["latest_version"] != float64(1) || record["version_count"] != float64(1) ||
				!reflect.DeepEqual(record["version_mismatch_reasons"], map[string]any{test.reason: float64(1)}) {
				t.Fatalf("未定位核对失败字段：%s", logs.String())
			}
			if calls["POST /v2/sg/shortdrama/album/update/"] != 0 || calls["GET /v2/sg/shortdrama/album/query/"] != 1 {
				t.Fatal("核对内容不符时发生重复写入")
			}
			assertDiagnosticSecretsAbsent(t, logs.String(), "private-language", "private-description", "private-cover", "private-vid", "sensitive-token", "sensitive-secret")
		})
	}
}

// albumDiagnosticRecords 读取本地测试捕获的日志，不接收真实平台响应。
func albumDiagnosticRecords(t *testing.T, logs string) []map[string]any {
	t.Helper()
	var records []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(logs), "\n") {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		records = append(records, record)
	}
	return records
}
