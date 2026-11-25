package tiktokmedia

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"drama-server/internal/biz/operations"
)

// TestAuthorizationUnknownStructure 保留未知结果列表的字段结构，仍拒绝未经确认的授权，不泄漏字段值。
func TestAuthorizationUnknownStructure(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	client := contractClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Tt-Logid", "shape-request")
		_, _ = io.WriteString(w, `{"data":{"unexpected_results":[{"client_key":"private-target","auth_status":1,"error_message":"private message","private_nested":{"value":"private-value"},"access_token":"private-token"}]}}`)
	})
	err := client.AuthorizeAlbum(context.Background(), "123", []string{"target"})
	var platform *operations.PlatformCallError
	if !errors.As(err, &platform) || platform.Code != "authorization_result_incomplete" {
		t.Fatalf("未知结果不应放行：%v", err)
	}
	var record map[string]json.RawMessage
	if err := json.Unmarshal(logs.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	var shape []responseShapeField
	if err := json.Unmarshal(record["authorization_response_shape"], &shape); err != nil {
		t.Fatal(err)
	}
	foundList, foundStatus := false, false
	for _, field := range shape {
		if field.Path == "$.data.unexpected_results" && field.Kind == "array" && field.Length != nil && *field.Length == 1 {
			foundList = true
		}
		if field.Path == "$.data.unexpected_results[].auth_status" && field.Kind == "number" {
			foundStatus = true
		}
	}
	if !foundList || !foundStatus {
		t.Fatal("缺少未知列表及元素结构")
	}
	for _, value := range []string{"private-target", "private message", "private-value", "private-token"} {
		if strings.Contains(logs.String(), value) {
			t.Fatal("诊断泄漏值")
		}
	}
}

// TestAuthorizationShapeBounds 验证敏感键、异常字段名和大响应不会泄漏或无限扩展日志。
func TestAuthorizationShapeBounds(t *testing.T) {
	d := platformCallDiagnostic{secrets: []string{"hiddenSecret"}}
	shape := d.responseShape([]byte(`{"data":{"hiddenSecret":"x","https://private.example":"x","account":{"privateName":"x"},"target_map":{"privateClientKey":"x"},"results":[]}}`))
	raw, err := json.Marshal(shape)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"hiddenSecret", "private.example", "privateName", "privateClientKey"} {
		if strings.Contains(string(raw), value) {
			t.Fatal("结构泄漏敏感键")
		}
	}
	huge := map[string]any{}
	for i := 0; i < 100; i++ {
		huge["field"+strings.Repeat("a", i+1)] = 1
	}
	body, _ := json.Marshal(huge)
	if len(d.responseShape(body)) > 32 {
		t.Fatal("结构日志未限制大小")
	}
}

// TestAuthorizationResultContainerIsVisible 验证真实结果容器不再被误遮蔽，目标标识仍不写入日志。
func TestAuthorizationResultContainerIsVisible(t *testing.T) {
	d := platformCallDiagnostic{}
	for _, key := range []string{"client_key_result", "client_key_result_list"} {
		shape := d.responseShape([]byte(`{"data":{"` + key + `":[{"client_key":"private-target","auth_status":1}]}}`))
		raw, err := json.Marshal(shape)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, item := range shape {
			if item.Path == "$.data."+key && item.Kind == "array" {
				found = true
			}
		}
		if !found || strings.Contains(string(raw), "private-target") {
			t.Fatal("容器不可见或泄漏目标")
		}
	}
}
