package tiktok

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"drama-server/internal/biz/visitor"
	"drama-server/internal/pkg/requestmeta"
)

const defaultOAuthURL = "https://open.tiktokapis.com/v2/oauth/token/"

// OAuthClient 只在服务端交换一次性登录码，不向消费者暴露平台 Token。
type OAuthClient struct {
	// 保存客户端键。
	clientKey string
	// 保存客户端密钥。
	clientSecret string
	// 保存端点。
	endpoint string
	// 保存网络客户端。
	httpClient *http.Client
}

// NewOAuthClient 创建应用范围内的身份适配器；缺配置在使用时返回服务故障而非 401。
func NewOAuthClient(clientKey, clientSecret string, httpClient *http.Client) *OAuthClient {
	return &OAuthClient{
		clientKey: strings.TrimSpace(clientKey), clientSecret: strings.TrimSpace(clientSecret),
		endpoint: defaultOAuthURL, httpClient: httpClient,
	}
}

// ExchangeCode 区分登录码拒绝、服务端配置、平台故障、超时和取消，禁止统一返回 401。
func (c *OAuthClient) ExchangeCode(ctx context.Context, code string) (visitor.Identity, error) {
	if err := ctx.Err(); err != nil {
		return visitor.Identity{}, err
	}
	if strings.TrimSpace(code) == "" {
		return visitor.Identity{}, visitor.ErrUnauthorized
	}
	if c == nil || c.httpClient == nil || c.clientKey == "" || c.clientSecret == "" {
		return visitor.Identity{}, oauthFailure(ctx, "configuration", 0, visitor.ErrIdentityUnavailable, "")
	}
	form := url.Values{}
	form.Set("client_key", c.clientKey)
	form.Set("client_secret", c.clientSecret)
	form.Set("code", strings.TrimSpace(code))
	form.Set("grant_type", "authorization_code")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return visitor.Identity{}, oauthFailure(ctx, "configuration", 0, err, "", c.clientSecret, strings.TrimSpace(code))
	}
	req.Header.Set("content-type", "application/x-www-form-urlencoded")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return visitor.Identity{}, oauthFailure(ctx, "transport", 0, err, "", c.clientSecret, strings.TrimSpace(code))
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode == http.StatusGatewayTimeout {
		return visitor.Identity{}, oauthFailure(ctx, "upstream_timeout", resp.StatusCode, context.DeadlineExceeded, "")
	}
	// 平台限流或服务故障不能因错误正文中的字段被误判为用户登录码失效。
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= http.StatusInternalServerError {
		return visitor.Identity{}, oauthFailure(ctx, "upstream_unavailable", resp.StatusCode, visitor.ErrIdentityUnavailable, "")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil {
		return visitor.Identity{}, oauthFailure(ctx, "read_response", resp.StatusCode, err, "", c.clientSecret, strings.TrimSpace(code))
	}
	if len(body) > 1<<20 {
		return visitor.Identity{}, oauthFailure(ctx, "invalid_response", resp.StatusCode, visitor.ErrIdentityUnavailable, "")
	}
	var payload struct {
		// 保存平台用户标识。
		OpenID string `json:"open_id"`
		// 保存平台访问令牌。
		AccessToken string `json:"access_token"`
		// Error 只提取允许的错误代码；description 和原始正文绝不写日志。
		Error json.RawMessage `json:"error"`
		// 保存 TikTok data 包装响应中的身份数据。
		Data *struct {
			// 保存包装结构中的平台用户标识。
			OpenID string `json:"open_id"`
			// 保存包装结构中的平台访问令牌。
			AccessToken string `json:"access_token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return visitor.Identity{}, oauthFailure(ctx, "invalid_response", resp.StatusCode, err, "", c.clientSecret, strings.TrimSpace(code))
	}
	errorCode := oauthErrorCode(payload.Error)
	if errorCode == "invalid_grant" {
		slog.WarnContext(ctx, "tiktok login code rejected", append(requestmeta.LogFields(ctx),
			"flow", "tiktok_oauth", "stage", "exchange_code", "outcome", "invalid_grant", "http_status", resp.StatusCode, "provider_error_code", "invalid_grant")...)
		return visitor.Identity{}, visitor.ErrUnauthorized
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || errorCode != "" {
		reason := "provider_rejected"
		if errorCode == "invalid_client" || errorCode == "unauthorized_client" {
			reason = "configuration"
		}
		return visitor.Identity{}, oauthFailure(ctx, reason, resp.StatusCode, visitor.ErrIdentityUnavailable, errorCode)
	}
	if payload.Data != nil {
		payload.OpenID = first(payload.OpenID, payload.Data.OpenID)
		payload.AccessToken = first(payload.AccessToken, payload.Data.AccessToken)
	}
	if strings.TrimSpace(payload.OpenID) == "" || strings.TrimSpace(payload.AccessToken) == "" {
		return visitor.Identity{}, oauthFailure(ctx, "invalid_response", resp.StatusCode, visitor.ErrIdentityUnavailable, "")
	}
	return visitor.Identity{
		OpenID: strings.TrimSpace(payload.OpenID),
	}, nil
}

// oauthErrorCode 只提取用于分类的错误代码，未知结构一律当作平台坏响应。
func oauthErrorCode(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var code string
	if json.Unmarshal(raw, &code) == nil {
		return strings.TrimSpace(code)
	}
	var wrapped struct {
		// Code 是官方结构化错误的机器可读分类。
		Code string `json:"code"`
	}
	if json.Unmarshal(raw, &wrapped) == nil && strings.TrimSpace(wrapped.Code) != "" {
		if wrapped.Code == "ok" {
			return ""
		}
		return strings.TrimSpace(wrapped.Code)
	}
	return "invalid_response"
}

// oauthFailure 记录实际错误和受控平台错误码，遮盖已知凭据；返回值仍保留原有安全分类与错误链。
func oauthFailure(ctx context.Context, reason string, status int, cause error, providerCode string, secrets ...string) error {
	level := slog.LevelError
	if errors.Is(cause, context.Canceled) {
		reason = "cancelled"
		level = slog.LevelInfo
	} else if status == http.StatusTooManyRequests {
		level = slog.LevelWarn
	}
	detail := cause.Error()
	for _, secret := range secrets {
		if secret != "" {
			detail = strings.ReplaceAll(detail, secret, "[redacted]")
		}
	}
	fields := append(requestmeta.LogFields(ctx), "reason", reason, "http_status", status, "error", detail)
	if providerCode != "" {
		fields = append(fields, "provider_error_code", oauthLogCode(providerCode))
	}
	slog.Log(ctx, level, "tiktok identity service unavailable", fields...)
	return visitor.DependencyError(visitor.ErrIdentityUnavailable, cause)
}

// oauthLogCode 只允许固定错误码进入日志，未知返回值归为 unknown，避免错误字段夹带凭据或原始响应。
func oauthLogCode(code string) string {
	switch code {
	case "invalid_client", "unauthorized_client", "invalid_grant", "invalid_request", "invalid_scope", "unsupported_grant_type", "server_error", "temporarily_unavailable", "invalid_response":
		return code
	default:
		return "unknown"
	}
}

// 返回第一个非空字符串。
func first(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
