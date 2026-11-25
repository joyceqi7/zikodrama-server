package operations

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"

	"drama-server/internal/biz/adminrbac"
)

// reportAuditError 把审计持久化错误交给服务装配层统一记录。
func (h *Handler) reportAuditError(err error) {
	if h.config.AuditError != nil {
		h.config.AuditError(err)
	} else if err != nil {
		slog.Error("admin audit persistence failed", "error", err)
	}
}

// adminActorID 从已认证的请求上下文读取管理员标识。
func adminActorID(ctx context.Context) string {
	session, _ := ctx.Value(adminSessionContextKey{}).(adminrbac.Session)
	return session.User.ID
}

// auditResponseWriter 暂存写接口响应，供审计中间件读取最终状态码。
type auditResponseWriter struct {
	header      http.Header
	body        bytes.Buffer
	status      int
	wroteHeader bool
}

// newAuditResponseWriter 创建审计响应暂存器。
func newAuditResponseWriter() *auditResponseWriter {
	return &auditResponseWriter{header: make(http.Header), status: http.StatusOK}
}

// Header 返回待写出的响应头集合。
func (writer *auditResponseWriter) Header() http.Header { return writer.header }

// WriteHeader 记录首次写出的 HTTP 状态码。
func (writer *auditResponseWriter) WriteHeader(status int) {
	if writer.wroteHeader {
		return
	}
	writer.wroteHeader = true
	writer.status = status
}

// Write 暂存响应正文。
func (writer *auditResponseWriter) Write(contents []byte) (int, error) {
	if !writer.wroteHeader {
		writer.WriteHeader(http.StatusOK)
	}
	return writer.body.Write(contents)
}

// flushTo 将暂存的审计响应完整写入目标写入器。
func (writer *auditResponseWriter) flushTo(target http.ResponseWriter) {
	for key, values := range writer.header {
		target.Header()[key] = append([]string(nil), values...)
	}
	target.WriteHeader(writer.status)
	if _, err := target.Write(writer.body.Bytes()); err != nil {
		slog.Error("flush audited response failed", "error", err)
	}
}

// auditAction 使用匹配到的路由合同作为稳定审计动作。
func auditAction(r *http.Request) string {
	pattern := strings.TrimSpace(r.Pattern)
	if pattern == "" {
		pattern = r.Method + " " + r.URL.Path
	}
	return pattern
}

// auditResourceType 从运营接口首段提取资源类型。
func auditResourceType(r *http.Request) string {
	path := strings.TrimPrefix(r.URL.Path, "/admin/v1/")
	if index := strings.Index(path, "/"); index >= 0 {
		path = path[:index]
	}
	return path
}

// auditResourceID 从路由参数提取资源标识；敏感身份仅记录摘要。
func auditResourceID(r *http.Request) string {
	for _, key := range []string{"subjectID", "strategyID", "applicationID", "dramaID", "campaignID", "tiktokTagID", "episodeID", "jobID", "userID", "roleID", "permissionID"} {
		if value := strings.TrimSpace(r.PathValue(key)); value != "" {
			return value
		}
	}
	if identifier := strings.TrimSpace(r.PathValue("identifier")); identifier != "" {
		return fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(identifier)))
	}
	return ""
}

// clientIP 读取入口网关传递的客户端地址；无合法代理头时回退到直连地址。
func clientIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	// Nginx 会覆盖 X-Real-IP，因此优先使用它，避免把 Docker 网桥地址记录为客户端地址。
	if address := validIP(r.Header.Get("X-Real-IP")); address != "" {
		return address
	}
	// 兼容多级代理；X-Forwarded-For 的最左侧地址是最初客户端地址。
	for _, value := range strings.Split(r.Header.Get("X-Forwarded-For"), ",") {
		if address := validIP(value); address != "" {
			return address
		}
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		slog.WarnContext(r.Context(), "parse client address failed", "error", err)
	}
	if err == nil && host != "" {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}

// validIP 校验并规范化代理头中的 IP 地址，拒绝把任意文本写入审计记录。
func validIP(value string) string {
	value = strings.TrimSpace(value)
	if parsed := net.ParseIP(value); parsed != nil {
		return parsed.String()
	}
	return ""
}
