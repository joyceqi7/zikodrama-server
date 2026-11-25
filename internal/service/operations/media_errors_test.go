package operations

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"drama-server/internal/biz/operations"
)

func TestMediaUploadDiscardedUsesDistinctErrorCode(t *testing.T) {
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/admin/v1/media", nil)

	writeOperationsError(response, request, operations.ErrMediaUploadDiscarded)

	if response.Code != http.StatusConflict {
		t.Fatalf("unexpected status: %d", response.Code)
	}
	var payload envelope
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Reason != "media_upload_discarded" {
		t.Fatalf("unexpected error payload: %+v", payload.Reason)
	}
}
