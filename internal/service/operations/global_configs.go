package operations

import (
	"drama-server/internal/biz/adminrbac"
	"drama-server/internal/biz/globalconfig"
	"drama-server/internal/pkg/apierr"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
)

func (h *Handler) registerGlobalConfigRoutes() {
	h.mux.HandleFunc("GET /admin/v1/global-configs", h.requirePermission(adminrbac.PermissionConfigRead, h.listGlobalConfigs))
	h.mux.HandleFunc("PUT /admin/v1/global-configs/{key}", h.requirePermission(adminrbac.PermissionConfigUpdate, h.saveGlobalConfig))
	h.mux.HandleFunc("DELETE /admin/v1/global-configs/{key}", h.requirePermission(adminrbac.PermissionConfigUpdate, h.deleteGlobalConfig))
}
func (h *Handler) listGlobalConfigs(w http.ResponseWriter, r *http.Request) {
	if h.config.GlobalConfigs == nil {
		writeError(w, r, apierr.OperationsProviderNotConfigured)
		return
	}
	rows, err := h.config.GlobalConfigs.List(r.Context())
	writeResult(w, r, http.StatusOK, rows, err)
}
func (h *Handler) saveGlobalConfig(w http.ResponseWriter, r *http.Request) {
	var input struct {
		// Value 是配置内容，沿用各配置类型的业务合同。
		Value json.RawMessage `json:"value"`
		// Remark 是运营填写的用途说明，最多 500 字。
		Remark string `json:"remark"`
	}
	if !decode(w, r, &input) {
		return
	}
	key := r.PathValue("key")
	if len([]rune(input.Remark)) > 500 {
		slog.WarnContext(r.Context(), "global configuration remark input rejected", "error", globalconfig.ErrInvalid)
		writeError(w, r, apierr.OperationsInvalidRequest)
		return
	}
	if key == "media" {
		value, err := normalizeMediaAccountInput(input.Value)
		if err != nil {
			slog.WarnContext(r.Context(), "media account input rejected", "error", err)
			writeError(w, r, apierr.OperationsInvalidRequest)
			return
		}
		input.Value = value
	}
	if err := globalconfig.ValidateInput(key, input.Value); err != nil {
		slog.WarnContext(r.Context(), "global configuration input rejected", "error", err)
		writeError(w, r, apierr.OperationsInvalidRequest)
		return
	}

	if h.config.GlobalConfigs == nil {
		writeError(w, r, apierr.OperationsProviderNotConfigured)
		return
	}
	session, _ := r.Context().Value(adminSessionContextKey{}).(adminrbac.Session)
	row, err := h.config.GlobalConfigs.Save(r.Context(), globalconfig.Entry{Key: key, Value: input.Value, Remark: input.Remark, UpdatedBy: session.User.Username})
	writeResult(w, r, http.StatusOK, row, err)
}

// normalizeMediaAccountInput 在 HTTP 边界规范可选 VOD 账号，保留数字字符串前导零；空值不增加直连门禁。
func normalizeMediaAccountInput(raw json.RawMessage) (json.RawMessage, error) {
	var values map[string]json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, err
	}
	if values == nil {
		return nil, globalconfig.ErrInvalid
	}
	value, present := values["byteplus_account_id"]
	if !present {
		return raw, nil
	}
	var id *string
	if err := json.Unmarshal(value, &id); err != nil {
		return nil, err
	}
	if id == nil {
		return nil, globalconfig.ErrInvalid
	}
	normalized := strings.TrimSpace(*id)
	if normalized != "" && !publisherAppIDPattern.MatchString(normalized) {
		return nil, globalconfig.ErrInvalid
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return nil, err
	}
	values["byteplus_account_id"] = encoded
	return json.Marshal(values)
}
func (h *Handler) deleteGlobalConfig(w http.ResponseWriter, r *http.Request) {
	if err := globalconfig.ValidateInput(r.PathValue("key"), json.RawMessage(`{}`)); err != nil {
		slog.WarnContext(r.Context(), "global configuration key rejected", "error", err)
		writeError(w, r, apierr.OperationsInvalidRequest)
		return
	}
	if h.config.GlobalConfigs == nil {
		writeError(w, r, apierr.OperationsProviderNotConfigured)
		return
	}
	err := h.config.GlobalConfigs.Delete(r.Context(), r.PathValue("key"))
	writeResult(w, r, http.StatusOK, map[string]any{}, err)
}
