// Package apireply 提供消费者和管理 API 共用的响应封装。
package apireply

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"drama-server/internal/pkg/requestmeta"
)

// Envelope 与统一 BFF 合同一致，业务错误原因与 HTTP 状态分开表达。
type Envelope struct {
	// Code 成功为 0，失败使用 HTTP 错误状态码。
	Code int `json:"code"`
	// Reason 是稳定业务错误码，成功时省略。
	Reason string `json:"reason,omitempty"`
	// Message 是安全的对外文案，不包含底层异常。
	Message string `json:"message"`
	// Data 保存成功数据；失败或无结果时为 null。
	Data any `json:"data"`
	// ServerTime 是成功响应生成时的 UTC 基准；与业务事实的请求接收时间互相独立。
	ServerTime string `json:"serverTime,omitempty"`
}

// Write 输出统一 JSON，先编码再写状态；失败日志使用调用方 Context 关联请求，不记录响应载荷。
func Write(ctx context.Context, w http.ResponseWriter, status int, payload Envelope) {
	if payload.Code == 0 && status >= 200 && status < 300 {
		payload.ServerTime = time.Now().UTC().Format(time.RFC3339Nano)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		slog.ErrorContext(ctx, "marshal API response failed", append(requestmeta.LogFields(ctx), "error", err)...)
		status = http.StatusInternalServerError
		body = []byte(`{"code":500,"reason":"internal_error","message":"The action could not be completed.","data":null}`)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if _, err := w.Write(append(body, '\n')); err != nil {
		slog.ErrorContext(ctx, "write API response failed", append(requestmeta.LogFields(ctx), "error", err)...)
	}
}

// Success 保留接口约定的成功 HTTP 状态和成功码，并把请求 Context 传给统一响应错误日志。
func Success(ctx context.Context, w http.ResponseWriter, status int, data any) {
	Write(ctx, w, status, Envelope{Code: 0, Message: "OK", Data: data})
}
