package middleware

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPAccessLogRecordsJSONBodiesWithoutChangingTransport(t *testing.T) {
	requestBody := `{"episode_id":"123","finished":true}`
	responseBody := `{"code":0,"data":{"episode_id":"123"}}`
	record, response := accessBodyRecord(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("读取请求正文失败：%v", err)
			return
		}
		if string(body) != requestBody {
			t.Errorf("业务收到的请求正文被修改：%q", body)
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := io.WriteString(w, responseBody); err != nil {
			t.Errorf("写入响应正文失败：%v", err)
		}
	}), httptest.NewRequest(http.MethodPost, "/episode", strings.NewReader(requestBody)))
	if response.Body.String() != responseBody {
		t.Fatalf("客户端收到的响应正文被修改：%q", response.Body.String())
	}
	requestJSON, ok := record["request_body"].(map[string]any)
	if !ok || requestJSON["episode_id"] != "123" || requestJSON["finished"] != true {
		t.Fatalf("请求正文没有作为 JSON 对象输出：%v", record)
	}
	responseJSON, ok := record["response_body"].(map[string]any)
	if !ok || responseJSON["code"] != float64(0) {
		t.Fatalf("响应正文没有作为 JSON 对象输出：%v", record)
	}
	for _, field := range []string{"request_body_bytes", "response_body_bytes", "request_body_encoding", "response_body_encoding"} {
		if _, exists := record[field]; exists {
			t.Fatalf("访问日志不应输出冗余字段 %s：%v", field, record)
		}
	}
}

func TestHTTPAccessLogKeepsFirst4KiBOfLargeBodies(t *testing.T) {
	body := strings.Repeat("界", accessBodyLimit/3+2)
	record, response := accessBodyRecord(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(w, r.Body); err != nil {
			t.Errorf("传输正文失败：%v", err)
		}
	}), httptest.NewRequest(http.MethodPost, "/large", strings.NewReader(body)))
	if response.Body.String() != body {
		t.Fatal("正文采集改变了实际传输结果")
	}
	for _, side := range []string{"request", "response"} {
		encoded, ok := record[side+"_body_base64"].(string)
		if !ok {
			t.Fatalf("%s 缺少 Base64 正文：%v", side, record)
		}
		prefix, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || !bytes.Equal(prefix, []byte(body)[:accessBodyLimit]) {
			t.Fatalf("%s 正文前缀不是精确的 4 KiB：error=%v", side, err)
		}
		if _, exists := record[side+"_body"]; exists {
			t.Fatalf("%s 截断正文不能被误记为完整正文", side)
		}
	}
}

func TestHTTPAccessLogDoesNotTreatTruncatedJSONAsComplete(t *testing.T) {
	body := "{}" + strings.Repeat(" ", accessBodyLimit-2) + "x"
	record, _ := accessBodyRecord(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(w, r.Body); err != nil {
			t.Errorf("传输正文失败：%v", err)
		}
	}), httptest.NewRequest(http.MethodPost, "/large-json", strings.NewReader(body)))
	if prefix, ok := record["request_body"].(string); !ok || prefix != body[:accessBodyLimit] {
		t.Fatalf("截断前缀不能作为完整 JSON 输出：%v", record)
	}
}

func TestHTTPAccessLogMarksUnreadAndPartialRequestBodies(t *testing.T) {
	for _, testCase := range []struct {
		name string
		read int
	}{
		{name: "未读取", read: 0},
		{name: "只读取三个字节", read: 3},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			record, _ := accessBodyRecord(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if testCase.read > 0 {
					buffer := make([]byte, testCase.read)
					if _, err := io.ReadFull(r.Body, buffer); err != nil {
						t.Errorf("读取部分请求正文失败：%v", err)
					}
				}
				w.WriteHeader(http.StatusNoContent)
			}), httptest.NewRequest(http.MethodPost, "/partial", strings.NewReader("abcdef")))
			if testCase.read == 0 {
				if _, exists := record["request_body"]; exists {
					t.Fatalf("未读取正文不应输出正文：%v", record)
				}
				return
			}
			if record["request_body"] != "abc" {
				t.Fatalf("部分读取正文应记录实际前缀：%v", record)
			}
		})
	}
}

func TestHTTPAccessLogRecordsOnlyWrittenResponseBytes(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	handler := HTTPAccessLog(auditDependencies(t, logger))(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, err := w.Write([]byte("abcd")); err != io.ErrShortWrite {
			t.Errorf("未保留底层响应写入错误：%v", err)
		}
	}))
	handler.ServeHTTP(&shortBodyWriter{header: make(http.Header)}, httptest.NewRequest(http.MethodGet, "/short-write", nil))
	record := decodeAccessBodyRecord(t, output.Bytes())
	if record["response_body"] != "ab" {
		t.Fatalf("日志应只记录实际写出的字节：%v", record)
	}
}

// shortBodyWriter 模拟底层只接受部分响应正文。
type shortBodyWriter struct{ header http.Header }

func (w *shortBodyWriter) Header() http.Header { return w.header }
func (w *shortBodyWriter) WriteHeader(int)     {}
func (w *shortBodyWriter) Write([]byte) (int, error) {
	return 2, io.ErrShortWrite
}

// accessBodyRecord 使用真实 JSON 日志器验证最终字段类型，并返回客户端收到的响应。
func accessBodyRecord(t *testing.T, business http.Handler, request *http.Request) (map[string]any, *httptest.ResponseRecorder) {
	t.Helper()
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	handler := HTTPAccessLog(auditDependencies(t, logger))(business)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return decodeAccessBodyRecord(t, output.Bytes()), response
}

func decodeAccessBodyRecord(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var record map[string]any
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatalf("访问日志不是有效 JSON：%v", err)
	}
	if record["msg"] != "http access" {
		t.Fatalf("没有输出访问日志：%v", record)
	}
	return record
}
