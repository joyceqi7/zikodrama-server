package apireply

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"
)

// TestUnifiedResponse 验证成功、失败与空结果均保持统一响应合同。
func TestUnifiedResponse(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		payload Envelope
	}{
		{"success", 201, Envelope{Code: 0, Message: "OK", Data: map[string]string{"id": "1"}}},
		{"failure", 403, Envelope{Code: 403, Reason: "permission_denied", Message: "Permission denied"}},
		{"empty", 200, Envelope{Code: 0, Message: "OK"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			Write(context.Background(), w, tc.status, tc.payload)
			var body map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if w.Code != tc.status || body["code"] != float64(tc.payload.Code) || body["message"] != tc.payload.Message {
				t.Fatalf("unexpected response: %s", w.Body.String())
			}
			if _, ok := body["data"]; !ok {
				t.Fatal("data must always be present")
			}
			if _, ok := body["error"]; ok {
				t.Fatal("legacy error envelope leaked")
			}
		})
	}
}

// TestSuccessServerTimeUsesResponseClock 验证新增可选时间字段来自响应生成时刻，旧消费者仍可只解析原业务字段。
func TestSuccessServerTimeUsesResponseClock(t *testing.T) {
	w := httptest.NewRecorder()
	before := time.Now().UTC()
	Success(context.Background(), w, 200, map[string]bool{"accepted": true})
	var body struct {
		Code       int    `json:"code"`
		ServerTime string `json:"serverTime"`
		Data       struct {
			Accepted bool `json:"accepted"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	stamp, err := time.Parse(time.RFC3339Nano, body.ServerTime)
	if err != nil || stamp.Before(before) || stamp.After(time.Now().UTC()) || body.Code != 0 || !body.Data.Accepted {
		t.Fatalf("响应时间基准或旧业务合同无效: %s %v", w.Body, err)
	}
}

// TestEncodingFailureDoesNotReturnSuccess 验证编码失败在写出响应头前切换为内部错误。
func TestEncodingFailureDoesNotReturnSuccess(t *testing.T) {
	w := httptest.NewRecorder()
	Success(context.Background(), w, 200, make(chan int))
	if w.Code != 500 {
		t.Fatalf("status=%d", w.Code)
	}
	var body Envelope
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Code != 500 || body.Reason != "internal_error" || body.Data != nil {
		t.Fatalf("unexpected error: %+v", body)
	}
}
