package tiktokmedia

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"drama-server/internal/biz/operations"
)

// recordedRequest 只保存验证官方合同所需的信息，不保存 client_secret 或 access_token。
type recordedRequest struct {
	Method string
	Path   string
	Query  string
	Body   []byte
}

func TestClientUsesOfficialPublicationEndpointsAndPreservesNumericIdentifiers(t *testing.T) {
	t.Parallel()
	var mutex sync.Mutex
	requests := make([]recordedRequest, 0, 9)
	tokenCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/v2/oauth/token/" {
			tokenCalls++
			if request.Header.Get("content-type") != "application/x-www-form-urlencoded" {
				t.Fatalf("unexpected token content type: %s", request.Header.Get("content-type"))
			}
			writer.Header().Set("content-type", "application/json")
			_, _ = io.WriteString(writer, `{"access_token":"sensitive-access-token","expires_in":7200}`)
			return
		}
		if request.Header.Get("authorization") != "Bearer sensitive-access-token" {
			t.Fatalf("missing access token for %s", request.URL.Path)
		}
		body, _ := io.ReadAll(request.Body)
		mutex.Lock()
		requests = append(requests, recordedRequest{Method: request.Method, Path: request.URL.Path, Query: request.URL.RawQuery, Body: body})
		mutex.Unlock()
		writer.Header().Set("content-type", "application/json")
		switch request.URL.Path {
		case "/v2/sg/shortdrama/image":
			_, _ = io.WriteString(writer, `{"data":{"open_pic_id":900719925474099312345}}`)
		case "/v2/sg/shortdrama/album/create/":
			_, _ = io.WriteString(writer, `{"data":{"album_id":900719925474099312346}}`)
		case "/v2/sg/shortdrama/album/update/":
			_, _ = io.WriteString(writer, `{"data":{"version":2,"episode_id_map":{"900719925474099312340":900719925474099312347}}}`)
		case "/v2/sg/shortdrama/album/review/submit/":
			_, _ = io.WriteString(writer, `{"data":{"review_id":900719925474099312348}}`)
		case "/v2/sg/shortdrama/album/query/":
			_, _ = io.WriteString(writer, `{"data":{"album_version_list":[{"version":2,"online_version":0,"publish_status":0,"review_status":2}]}}`)
		case "/v2/sg/shortdrama/album/authorize/":
			_, _ = io.WriteString(writer, `{"data":{"client_key_result_list":[{"client_key":"target-client","auth_status":1,"error_code":0}]}}`)
		case "/v2/sg/shortdrama/album/online_version/", "/v2/sg/shortdrama/album/status/":
			_, _ = io.WriteString(writer, `{"data":{}}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)

	client := NewClient("owner-client", "sensitive-client-secret", server.Client())
	client.baseURL = server.URL
	ctx := context.Background()

	coverID, err := client.CreateImage(ctx, "https://cdn.example.test/cover.png")
	if err != nil || coverID != "900719925474099312345" {
		t.Fatalf("unexpected cover result: %q %v", coverID, err)
	}
	album, err := client.CreateAlbum(ctx)
	if err != nil || album.AlbumID != "900719925474099312346" || album.Version != 0 {
		t.Fatalf("unexpected album result: %+v %v", album, err)
	}
	updated, err := client.UpdateAlbum(ctx, operations.AlbumDraft{
		ClientKey: "owner-client", AlbumID: album.AlbumID, Version: album.Version,
		Language: "zh", Title: "测试短剧", Description: "简介", CoverID: coverID,
		Year: 2026, DramaType: 4, TagIDs: []string{"900719925474099312349"},
		Episodes: []operations.AlbumEpisodeDraft{{Sequence: 1, Title: "第一集", CoverID: coverID, BytePlusVID: "byteplus-vid-1", PlatformEpisodeID: "900719925474099312340"}},
	})
	if err != nil || updated.Version != 2 || updated.EpisodeIDsBySequence[1] != "900719925474099312347" {
		t.Fatalf("unexpected update result: %+v %v", updated, err)
	}
	reviewID, err := client.SubmitReview(ctx, album.AlbumID, updated.Version, 2)
	if err != nil || reviewID != "900719925474099312348" {
		t.Fatalf("unexpected review result: %q %v", reviewID, err)
	}
	observation, err := client.QueryAlbum(ctx, album.AlbumID, updated.Version)
	if err != nil || observation.ReviewStatus != 2 {
		t.Fatalf("unexpected album observation: %+v %v", observation, err)
	}
	if err := client.AuthorizeAlbum(ctx, album.AlbumID, []string{"target-client"}); err != nil {
		t.Fatal(err)
	}
	if err := client.SetOnlineVersion(ctx, album.AlbumID, updated.Version); err != nil {
		t.Fatal(err)
	}
	if err := client.SetAlbumListed(ctx, album.AlbumID, true); err != nil {
		t.Fatal(err)
	}

	if tokenCalls != 1 {
		t.Fatalf("access token should be cached, calls=%d", tokenCalls)
	}
	wantPaths := []string{
		"/v2/sg/shortdrama/image", "/v2/sg/shortdrama/album/create/", "/v2/sg/shortdrama/album/update/",
		"/v2/sg/shortdrama/album/review/submit/", "/v2/sg/shortdrama/album/query/",
		"/v2/sg/shortdrama/album/authorize/", "/v2/sg/shortdrama/album/online_version/", "/v2/sg/shortdrama/album/status/",
	}
	gotPaths := make([]string, 0, len(requests))
	for _, request := range requests {
		gotPaths = append(gotPaths, request.Path)
	}
	if !reflect.DeepEqual(gotPaths, wantPaths) {
		t.Fatalf("unexpected official endpoint order:\n got: %v\nwant: %v", gotPaths, wantPaths)
	}
	assertJSONNumber(t, requests[2].Body, "album_id", album.AlbumID)
	assertNestedJSONNumber(t, requests[2].Body, "album_info", "tag_list", "900719925474099312349")
	assertEpisodeIDString(t, requests[2].Body, "900719925474099312340")
	assertJSONNumber(t, requests[3].Body, "album_id", album.AlbumID)
	if !strings.Contains(requests[4].Query, "album_id="+album.AlbumID) {
		t.Fatalf("query did not preserve album id: %s", requests[4].Query)
	}
	var updatedBody albumUpdateRequest
	if err := json.Unmarshal(requests[2].Body, &updatedBody); err != nil {
		t.Fatal(err)
	}
	if updatedBody.AlbumInfo.Language != "zh" {
		t.Fatalf("language must be the official string code: %+v", updatedBody.AlbumInfo)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(requests[2].Body, &fields); err != nil {
		t.Fatal(err)
	}
	if _, exists := fields["version"]; exists {
		t.Fatal("first update must omit unknown version")
	}
}

// TestUpdateAlbumMergesExistingAndAppendedEpisodeMappings 验证从 11 集续传到 40 集时，
// 官方只返回新增分集映射也能与原 11 集标识合并为完整版本。
func TestUpdateAlbumMergesExistingAndAppendedEpisodeMappings(t *testing.T) {
	t.Parallel()
	const albumID = "7685608308176193557"
	responseMapping := make(map[string]json.Number, 29)
	for sequence := 12; sequence <= 40; sequence++ {
		responseMapping["seq:"+strconv.Itoa(sequence)] = json.Number(strconv.FormatInt(7685603384053910000+int64(sequence), 10))
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("content-type", "application/json")
		switch request.URL.Path {
		case "/v2/oauth/token/":
			_, _ = io.WriteString(writer, `{"access_token":"token","expires_in":7200}`)
		case "/v2/sg/shortdrama/album/update/":
			_ = json.NewEncoder(writer).Encode(map[string]any{"data": map[string]any{
				"version": 2, "episode_id_map": responseMapping,
			}})
		default:
			t.Fatalf("续传响应完整时不应进入恢复查询：%s", request.URL.Path)
		}
	}))
	t.Cleanup(server.Close)

	client := NewClient("owner-client", "secret", server.Client())
	client.baseURL = server.URL
	draft := operations.AlbumDraft{
		ClientKey: "owner-client", AlbumID: albumID, Version: 1, Language: "zh",
		Title: "续传测试", Description: "从 11 集续传到 40 集", CoverID: "cover-1",
		Year: 2026, AlbumStatus: 2, DramaType: 4, TagIDs: []string{"7"},
	}
	for sequence := 1; sequence <= 40; sequence++ {
		episode := operations.AlbumEpisodeDraft{
			Sequence: sequence, Title: "第" + strconv.Itoa(sequence) + "集", CoverID: draft.CoverID,
			BytePlusVID: "vid-" + strconv.Itoa(sequence),
		}
		if sequence <= 11 {
			episode.PlatformEpisodeID = strconv.FormatInt(7685603384053909500+int64(sequence), 10)
		}
		draft.Episodes = append(draft.Episodes, episode)
	}

	result, err := client.UpdateAlbum(context.Background(), draft)
	if err != nil {
		t.Fatal(err)
	}
	if result.Version != 2 || len(result.EpisodeIDsBySequence) != 40 {
		t.Fatalf("续传映射不完整：%+v", result)
	}
	if result.EpisodeIDsBySequence[1] != draft.Episodes[0].PlatformEpisodeID ||
		result.EpisodeIDsBySequence[11] != draft.Episodes[10].PlatformEpisodeID ||
		result.EpisodeIDsBySequence[12] != responseMapping["seq:12"].String() ||
		result.EpisodeIDsBySequence[40] != responseMapping["seq:40"].String() {
		t.Fatalf("既有与新增分集映射合并错误：%+v", result.EpisodeIDsBySequence)
	}
}

func TestClientRejectsEmptyLanguageBeforePlatformWrite(t *testing.T) {
	t.Parallel()
	client := NewClient("owner-client", "secret", http.DefaultClient)
	_, err := client.UpdateAlbum(context.Background(), operations.AlbumDraft{Language: ""})
	var platformErr *operations.PlatformCallError
	if !errors.As(err, &platformErr) || platformErr.Code != "invalid_album_language" || platformErr.Retryable {
		t.Fatalf("unexpected error: %#v", err)
	}
}

func TestClientDoesNotExposeCredentialsOrPlatformResponseBody(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("content-type", "application/json")
		if request.URL.Path == "/v2/oauth/token/" {
			_, _ = io.WriteString(writer, `{"access_token":"sensitive-access-token","expires_in":7200}`)
			return
		}
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(writer, `{"error":{"code":"bad_request","message":"contains-sensitive-platform-detail"}}`)
	}))
	t.Cleanup(server.Close)
	client := NewClient("owner-client", "sensitive-client-secret", server.Client())
	client.baseURL = server.URL

	_, err := client.CreateImage(context.Background(), "https://cdn.example.test/cover.png")
	if err == nil {
		t.Fatal("expected platform error")
	}
	message := err.Error()
	for _, secret := range []string{"sensitive-access-token", "sensitive-client-secret", "contains-sensitive-platform-detail"} {
		if strings.Contains(message, secret) {
			t.Fatalf("error leaked protected detail %q: %s", secret, message)
		}
	}
}

func assertJSONNumber(t *testing.T, raw []byte, key, want string) {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var body map[string]any
	if err := decoder.Decode(&body); err != nil {
		t.Fatal(err)
	}
	number, ok := body[key].(json.Number)
	if !ok || number.String() != want {
		t.Fatalf("%s must be an exact JSON number, got %#v", key, body[key])
	}
}

func assertNestedJSONNumber(t *testing.T, raw []byte, objectKey, listKey, want string) {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var body map[string]any
	if err := decoder.Decode(&body); err != nil {
		t.Fatal(err)
	}
	object, _ := body[objectKey].(map[string]any)
	values, _ := object[listKey].([]any)
	if len(values) != 1 {
		t.Fatalf("unexpected %s.%s: %#v", objectKey, listKey, object[listKey])
	}
	number, ok := values[0].(json.Number)
	if !ok || number.String() != want {
		t.Fatalf("%s.%s must preserve exact JSON number, got %#v", objectKey, listKey, values[0])
	}
}

func assertEpisodeIDString(t *testing.T, raw []byte, want string) {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var body map[string]any
	if err := decoder.Decode(&body); err != nil {
		t.Fatal(err)
	}
	episodes, _ := body["episode_info_list"].([]any)
	if len(episodes) != 1 {
		t.Fatalf("unexpected episode_info_list: %#v", body["episode_info_list"])
	}
	episode, _ := episodes[0].(map[string]any)
	value, ok := episode["episode_id"].(string)
	if !ok || value != want {
		t.Fatalf("episode_id must remain a JSON string, got %#v", episode["episode_id"])
	}
}

// TestRevokeAlbumRequiresRemovedStatus 核对撤权使用 operate_type=2，不能把已授权结果当作撤权成功。
func TestRevokeAlbumRequiresRemovedStatus(t *testing.T) {
	for _, status := range []int{1, 2} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v2/oauth/token/" {
					io.WriteString(w, `{"access_token":"test-token","expires_in":7200}`)
					return
				}
				if r.URL.Path != "/v2/sg/shortdrama/album/authorize/" {
					t.Errorf("wrong path %s", r.URL.Path)
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body["operate_type"] != float64(2) {
					t.Error("撤权未使用操作2")
				}
				fmt.Fprintf(w, `{"data":{"client_key_result_list":[{"client_key":"target","auth_status":%d}]},"error":{"code":"ok"}}`, status)
			}))
			defer server.Close()
			client := NewClient("owner", "test-secret", server.Client())
			client.baseURL = server.URL
			err := client.RevokeAlbum(context.Background(), "123", []string{"target"})
			if (err == nil) != (status == 2) {
				t.Fatalf("status=%d err=%v", status, err)
			}
		})
	}
}
