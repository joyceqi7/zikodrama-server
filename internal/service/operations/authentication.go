package operations

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"drama-server/internal/biz/adminrbac"
	"drama-server/internal/pkg/apierr"
)

// registerAuthenticationRoutes 注册管理端身份认证接口；登录使用已配置的限流器。
func (h *Handler) registerAuthenticationRoutes() {
	h.mux.HandleFunc("POST /admin/v1/session", h.login)
	h.mux.HandleFunc("DELETE /admin/v1/session", h.logout)
	h.mux.HandleFunc("POST /admin/v1/session/refresh", h.refresh)
	h.mux.HandleFunc("GET /admin/v1/me", h.requireSession(h.me))
}

// login 校验管理端凭据并签发管理员 JWT。
func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var input struct {
		// Username 是管理端登录用户名。
		Username string `json:"username"`
		// Password 是本次登录提交的明文密码，仅用于当前请求校验。
		Password string `json:"password"`
	}
	if !decode(w, r, &input) {
		return
	}
	input.Username = strings.ToLower(strings.TrimSpace(input.Username))
	if input.Username == "" || input.Password == "" {
		slog.WarnContext(r.Context(), "login input rejected", "error", adminrbac.ErrUnauthorized)
		writeError(w, r, apierr.OperationsInvalidCredentials)
		return
	}
	if !h.allowRequest(w, r, "admin_login_ip", clientIP(r), adminLoginIPLimit, adminRateWindow) {
		return
	}
	// 限流主体与认证查询使用同一规范化规则，避免通过大小写或首尾空白绕过账号配额。
	accountSubject := strings.ToLower(strings.TrimSpace(input.Username))
	if !h.allowRequest(w, r, "admin_login_account", accountSubject, adminLoginAccountLimit, adminRateWindow) {
		return
	}
	result, err := h.rbac.Login(r.Context(), input.Username, input.Password)
	if err != nil {
		if errors.Is(err, adminrbac.ErrUnauthorized) {
			slog.WarnContext(r.Context(), "operations authentication rejected", "error", err)
			writeError(w, r, apierr.OperationsInvalidCredentials)
		} else {
			writeOperationsError(w, r, err)
		}
		return
	}
	writeData(w, r, http.StatusOK, result)
}

// logout 撤销当前 Bearer JWT 的账号认证版本，账号同版本凭据全部失效；重复调用保持幂等。
func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	if err := h.rbac.Logout(r.Context(), bearerToken(r)); err != nil {
		writeOperationsError(w, r, err)
		return
	}
	writeData(w, r, http.StatusOK, nil)
}

// me 返回当前管理端登录主体及其有效权限。
func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	session, err := h.rbac.Authenticate(r.Context(), bearerToken(r))
	writeResult(w, r, http.StatusOK, session, err)
}

// refresh 校验现有凭据并返回新的两小时 JWT，过期或撤销均返回登录失效。
func (h *Handler) refresh(w http.ResponseWriter, r *http.Request) {
	result, err := h.rbac.Refresh(r.Context(), bearerToken(r))
	writeResult(w, r, http.StatusOK, result, err)
}
