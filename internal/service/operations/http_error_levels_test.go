package operations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"drama-server/internal/biz/adminrbac"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/directplay"
	"drama-server/internal/biz/globalconfig"
	"drama-server/internal/biz/homepage"
	"drama-server/internal/biz/monetization"
	"drama-server/internal/biz/operations"
	"drama-server/internal/pkg/apierr"
)

// TestOperationsErrorLogLevelsPreserveHTTPContract 覆盖业务拒绝、取消和真实故障，验证错误状态与错误码不随日志级别调整改变。
func TestOperationsErrorLogLevelsPreserveHTTPContract(t *testing.T) {
	previous := slog.Default()
	t.Cleanup(func() { slog.SetDefault(previous) })
	for _, tc := range []struct {
		cause error
		want  apierr.Definition
	}{
		{&operations.PlatformCallError{Code: "album_update_unconfirmed"}, apierr.OperationsAlbumUpdateUnconfirmed},
		{homepage.ErrDramaRecommended, apierr.OperationsPublicationRecommended},
		{directplay.ErrConflict, apierr.OperationsStateConflict},
		{homepage.ErrConflict, apierr.OperationsStateConflict},
		{directplay.ErrInvalid, apierr.OperationsInvalidRequest},
		{globalconfig.ErrInvalid, apierr.OperationsInvalidRequest},
		{globalconfig.ErrNotFound, apierr.OperationsNotFound},
		{adminrbac.ErrNotFound, apierr.OperationsNotFound},
		{adminrbac.ErrInvalidInput, apierr.OperationsInvalidRequest},
		{adminrbac.ErrConflict, apierr.OperationsStateConflict},
		{adminrbac.ErrLastSuperAdmin, apierr.OperationsLastSuperAdmin},
		{adminrbac.ErrUnauthorized, apierr.OperationsUnauthorized},
		{adminrbac.ErrForbidden, apierr.OperationsPermissionDenied},
		{domain.ErrNotFound, apierr.OperationsNotFound},
		{operations.ErrInvalidInput, apierr.OperationsInvalidRequest},
		{operations.ErrMediaNotReady, apierr.OperationsMediaNotReady},
		{operations.ErrPublicationNotReady, apierr.OperationsPublicationNotReady},
		{operations.ErrMediaUploadDiscarded, apierr.OperationsMediaUploadDiscarded},
		{operations.ErrApplicationAdUnitsIncomplete, apierr.OperationsApplicationAdUnitsIncomplete},
		{monetization.ErrApplicationModeConflict, apierr.OperationsApplicationModeConflict},
		{operations.ErrConflict, apierr.OperationsStateConflict},
		{operations.ErrMediaNotConfigured, apierr.OperationsProviderNotConfigured},
		{operations.ErrPublicationUnavailable, apierr.OperationsProviderNotConfigured},
		{operations.ErrCampaignLinkUnavailable, apierr.OperationsCampaignLinkUnavailable},
		{errors.New("SELECT id FROM media_jobs: connection refused"), apierr.OperationsInternal},
		{context.Canceled, apierr.OperationsInternal},
	} {
		t.Run(fmt.Sprintf("%s/%v", tc.want.Code, tc.cause), func(t *testing.T) {
			var logs bytes.Buffer
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			writer := httptest.NewRecorder()
			cause := fmt.Errorf("operation failed: %w", tc.cause)
			writeOperationsError(writer, httptest.NewRequest("GET", "/admin/v1/dramas", nil), cause)
			var response struct {
				Reason string `json:"reason"`
				Code   int    `json:"code"`
			}
			if json.Unmarshal(writer.Body.Bytes(), &response) != nil || writer.Code != tc.want.Status || response.Reason != tc.want.Code || response.Code != tc.want.Status {
				t.Fatalf("HTTP错误合同改变: %s", writer.Body.String())
			}
			var entry map[string]any
			if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
				t.Fatal(err)
			}
			level := "ERROR"
			if tc.want.Status < 500 {
				level = "WARN"
			}
			if errors.Is(tc.cause, context.Canceled) {
				level = "INFO"
			}
			if entry["level"] != level || entry["error"] != cause.Error() || entry["msg"] != "operations request failed" {
				t.Fatalf("错误原因或级别不正确: %v", entry)
			}
			if strings.Contains(writer.Body.String(), cause.Error()) {
				t.Fatal("对外响应泄漏底层错误")
			}
		})
	}
}
