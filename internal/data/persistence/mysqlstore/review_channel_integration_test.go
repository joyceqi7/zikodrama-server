package mysqlstore

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"drama-server/internal/biz/adminrbac"
	"drama-server/internal/biz/directplay"
	"drama-server/internal/biz/operations"
	"drama-server/internal/biz/subject"
)

// TestReviewChannelListsHTTPIntegration 验证两个审核列表的主体渠道边界及独立菜单权限。
func TestReviewChannelListsHTTPIntegration(t *testing.T) {
	f := newIAAFixture(t)
	defer f.store.data.pool.Close()
	repo, scope := seedDirect(t, f)
	f.direct = directplay.NewService(repo, nil)
	if _, err := repo.Accept(scope, "201", directplay.Command{Kind: "publish", RequestID: "review-channel-content"}, "fixture"); err != nil {
		t.Fatal(err)
	}
	other := subject.Subject{Name: "审核隔离主体"}
	if err := f.store.CreateSubject(t.Context(), &other); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(f.handler(t))
	defer server.Close()
	var admin adminrbac.LoginResult
	json.Unmarshal(iaaRequest(t, server.URL, "POST", "/admin/v1/session", "", "", map[string]string{"username": "iaa-admin", "password": "LocalIAA123!"}, 200), &admin)
	call := func(method, channel, subjectID, appID string, input any, want int) operations.ReviewWhitelist {
		t.Helper()
		raw, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		request, err := http.NewRequest(method, server.URL+"/admin/v1/"+channel+"/review-list", bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer "+admin.Token)
		request.Header.Set("X-Subject-ID", subjectID)
		request.Header.Set("X-Application-ID", appID)
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != want {
			t.Fatalf("%s %s review list status=%d want=%d", method, channel, response.StatusCode, want)
		}
		var envelope struct {
			Data operations.ReviewWhitelist `json:"data"`
		}
		if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
			t.Fatal(err)
		}
		return envelope.Data
	}
	call("GET", "official", "", "101", nil, 400)
	call("GET", "direct", "999999", "102", nil, 400)
	official := call("PUT", "official", "1", "102", map[string]any{"revision": 0, "dramaIds": []string{"201"}}, 200)
	direct := call("GET", "direct", "1", "101", nil, 200)
	if len(official.Dramas) != 1 || len(direct.DramaIDs) != 0 || direct.Revision != 0 {
		t.Fatal("官方保存影响了直连审核列表，或应用选择器改变了渠道")
	}
	direct = call("PUT", "direct", "1", "101", map[string]any{"revision": 0, "dramaIds": []string{"201"}}, 200)
	if len(direct.Dramas) != 1 || direct.Dramas[0].Title != "Independent source" || official.Dramas[0].Title == direct.Dramas[0].Title {
		t.Fatal("审核列表未分别读取官方与直连的发布快照")
	}
	isolated := call("GET", "official", other.ID, "101", nil, 200)
	if len(isolated.DramaIDs) != 0 || isolated.Revision != 0 {
		t.Fatal("审核列表跨主体泄漏")
	}
	call("PUT", "direct", "1", "101", map[string]any{"revision": direct.Revision, "dramaIds": []string{"999999"}}, 400)
	call("PUT", "direct", "1", "101", map[string]any{"revision": 0, "dramaIds": []string{"201"}}, 409)
}
