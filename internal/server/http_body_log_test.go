package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	styx "gitee.com/linpbin/styx"
	"gitee.com/linpbin/styx/requestmeta"
)

func TestDramaHTTPServerKeepsProxyUploadBodyOutOfAccessLog(t *testing.T) {
	t.Parallel()

	media := []byte{0, 1, 2, 255, 128, 64}
	output := newSynchronizedBuffer()
	_, address := newTestEngine(t, Dependencies{
		Consumer: http.NotFoundHandler(),
		Admin: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			body, err := io.ReadAll(request.Body)
			if err != nil || !bytes.Equal(body, media) {
				t.Errorf("业务 Handler 未收到完整视频正文：error=%v received_bytes=%d", err, len(body))
			}
			writer.WriteHeader(http.StatusAccepted)
		}),
		Styx: newHTTPTestRuntimeWithSlog(t, styx.NewLogger(nil, styx.WithConsoleOutput(output))),
	})
	path := "/admin/v1/episodes/317/media-uploads/278/proxy"
	request, err := http.NewRequest(http.MethodPost, address+path, bytes.NewReader(media))
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if closeErr := response.Body.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("服务端代传响应状态=%d", response.StatusCode)
	}
	var entry map[string]any
	if err := json.Unmarshal([]byte(output.Wait(t)), &entry); err != nil {
		t.Fatalf("访问日志不是有效 JSON：%v", err)
	}
	if entry["path"] != path || entry["status"] != float64(http.StatusAccepted) || entry["request_id"] != response.Header.Get(requestmeta.HeaderName) {
		t.Fatal("代传访问日志缺少路径、状态或请求标识")
	}
	if _, exists := entry["request_body"]; exists {
		t.Fatal("代传访问日志泄露视频正文")
	}
	if _, exists := entry["request_body_base64"]; exists {
		t.Fatal("代传访问日志泄露二进制视频正文")
	}
}

func TestDramaHTTPServerLogsJSONRequestAndResponseBodies(t *testing.T) {
	t.Parallel()

	const requestBody = `{"episode_id":"123","finished":true}`
	const responseBody = `{"code":0,"data":{"episode_id":"123"}}`
	output := newSynchronizedBuffer()
	_, address := newTestEngine(t, Dependencies{
		Consumer: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Errorf("读取请求正文失败：%v", err)
				return
			}
			if string(body) != requestBody {
				t.Errorf("Drama Handler 收到的正文不一致：%q", body)
			}
			writer.Header().Set("Content-Type", "application/json")
			if _, err := io.WriteString(writer, responseBody); err != nil {
				t.Errorf("写入响应正文失败：%v", err)
			}
		}),
		Admin: http.NotFoundHandler(),
		Styx:  newHTTPTestRuntimeWithSlog(t, styx.NewLogger(nil, styx.WithConsoleOutput(output))),
	})
	request, err := http.NewRequest(http.MethodPost, address+"/api/v1/body-log", strings.NewReader(requestBody))
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	actualResponseBody, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("读取或关闭响应正文失败：read=%v close=%v", readErr, closeErr)
	}
	if string(actualResponseBody) != responseBody {
		t.Fatalf("客户端收到的正文不一致：%q", actualResponseBody)
	}
	var entry map[string]any
	if err := json.Unmarshal([]byte(output.Wait(t)), &entry); err != nil {
		t.Fatalf("Drama access 日志不是有效 JSON：%v", err)
	}
	loggedTime, ok := entry["time"].(string)
	if !ok {
		t.Fatalf("Drama access 日志缺少时间字段：%v", entry)
	}
	if _, err := time.Parse("2006-01-02 15:04:05.000-07:00", loggedTime); err != nil {
		t.Fatalf("Drama access 日志时间格式不正确：%v", err)
	}
	requestJSON, requestOK := entry["request_body"].(map[string]any)
	responseJSON, responseOK := entry["response_body"].(map[string]any)
	if !requestOK || requestJSON["episode_id"] != "123" || !responseOK || responseJSON["code"] != float64(0) {
		t.Fatalf("Drama 未接入 Styx 的 JSON 正文日志：%v", entry)
	}
	if entry["request_id"] != response.Header.Get(requestmeta.HeaderName) {
		t.Fatalf("正文日志与响应请求标识不一致：%v", entry)
	}
}

// TestPlaybackAccessLogOmitsCredentials 验证播放授权正文仍送达客户端，访问日志只保留关联字段。
func TestPlaybackAccessLogOmitsCredentials(t *testing.T) {
	t.Parallel()
	const requestBody = `{"launchQuery":{"ttclid":"fixture-attribution"}}`
	const responseBody = `{"data":{"entry":{"play":{"playback":{"playAuthToken":"fixture-token"}}}}}`
	output := newSynchronizedBuffer()
	_, address := newTestEngine(t, Dependencies{
		Consumer: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(r.Body)
			if err != nil || string(body) != requestBody {
				t.Errorf("播放请求正文读取失败：%v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			if _, err := io.WriteString(w, responseBody); err != nil {
				t.Errorf("播放响应写入失败：%v", err)
			}
		}),
		Admin: http.NotFoundHandler(),
		Styx:  newHTTPTestRuntimeWithSlog(t, styx.NewLogger(nil, styx.WithConsoleOutput(output))),
	})
	response, err := http.Post(address+"/api/v1/play/%65ntry", "application/json", strings.NewReader(requestBody))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK || string(payload) != responseBody {
		t.Fatal("过滤日志改变了播放响应", err)
	}
	logged := output.Wait(t)
	var entry map[string]any
	if err := json.Unmarshal([]byte(logged), &entry); err != nil {
		t.Fatal(err)
	}
	if entry["path"] != "/api/v1/play/%65ntry" || entry["request_id"] != response.Header.Get(requestmeta.HeaderName) || entry["duration_ms"] == nil ||
		strings.Contains(logged, "request_body") || strings.Contains(logged, "response_body") ||
		strings.Contains(logged, "fixture-attribution") || strings.Contains(logged, "fixture-token") {
		t.Fatal("播放访问日志未正确限制正文或缺少关联字段")
	}
}

func TestDramaHTTPServerKeepsLargeBodiesAndLogsBoundedPrefixes(t *testing.T) {
	t.Parallel()

	const bodyLimit = 4 * 1024
	requestBody := `{"request":"` + strings.Repeat("r", 1024*1024) + `"}`
	responseBody := `{"response":"` + strings.Repeat("s", 1024*1024) + `"}`
	output := newSynchronizedBuffer()
	_, address := newTestEngine(t, Dependencies{
		Consumer: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Errorf("读取大请求正文失败：%v", err)
				return
			}
			if string(body) != requestBody {
				t.Errorf("大请求正文传到业务时发生改变：received=%d want=%d", len(body), len(requestBody))
			}
			writer.Header().Set("Content-Type", "application/json")
			if _, err := io.WriteString(writer, responseBody); err != nil {
				t.Errorf("写入大响应正文失败：%v", err)
			}
		}),
		Admin: http.NotFoundHandler(),
		Styx:  newHTTPTestRuntimeWithSlog(t, styx.NewLogger(nil, styx.WithConsoleOutput(output))),
	})
	request, err := http.NewRequest(http.MethodPost, address+"/api/v1/body-log-large", strings.NewReader(requestBody))
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	actualResponseBody, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("读取或关闭大响应正文失败：read=%v close=%v", readErr, closeErr)
	}
	duration := time.Since(start)
	if response.StatusCode != http.StatusOK || string(actualResponseBody) != responseBody {
		t.Fatalf("大正文影响了接口结果：status=%d response_bytes=%d want=%d", response.StatusCode, len(actualResponseBody), len(responseBody))
	}
	var entry map[string]any
	if err := json.Unmarshal([]byte(output.Wait(t)), &entry); err != nil {
		t.Fatalf("大正文访问日志不是有效 JSON：%v", err)
	}
	for _, side := range []struct {
		name string
		body string
	}{
		{name: "request", body: requestBody},
		{name: "response", body: responseBody},
	} {
		prefix, ok := entry[side.name+"_body"].(string)
		if !ok || prefix != side.body[:bodyLimit] {
			t.Fatalf("%s 日志未保留精确的 4 KiB 前缀：prefix_bytes=%d", side.name, len(prefix))
		}
		for _, field := range []string{"_body_prefix", "_body_bytes", "_body_encoding", "_body_truncated", "_body_incomplete"} {
			if _, exists := entry[side.name+field]; exists {
				t.Fatalf("%s 日志不应输出冗余字段 %s", side.name, field)
			}
		}
	}
	t.Logf("大正文接口状态=%d，请求=%d 字节，响应=%d 字节，耗时=%s；日志请求/响应各保留 %d 字节前缀", response.StatusCode, len(requestBody), len(responseBody), duration, bodyLimit)
}
