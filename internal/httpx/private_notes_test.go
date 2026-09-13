package httpx

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uvwt/nexusdock/internal/config"
	"github.com/uvwt/nexusdock/internal/recall"
)

func TestPrivateNoteRoutesAreAbsentWithoutConfiguredStore(t *testing.T) {
	store, err := recall.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(config.Config{}, store, slog.Default()).Handler()
	response := doJSON(t, handler, http.MethodPost, "/v1/private-notes/status", `{"action":"check"}`)
	if response.Code != http.StatusNotFound && response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("unconfigured private-note route status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestPrivateNoteAPIRequiresBearerToken(t *testing.T) {
	h := newTestHandler(t, config.Config{AuthToken: "private-token"})
	response := doJSON(t, h, http.MethodPost, "/v1/private-notes/status", `{"action":"check"}`)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status without token = %d body=%s", response.Code, response.Body.String())
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/private-notes/status", strings.NewReader(`{"action":"check"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer private-token")
	response = httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("authorized status = %d body=%s", response.Code, response.Body.String())
	}
}

func TestPrivateNoteAPILifecycleAndMetadataOnlySearch(t *testing.T) {
	h := newTestHandler(t, config.Config{})
	response := doJSON(t, h, http.MethodPost, "/v1/private-notes/maintenance", `{"action":"init-encryption"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("init status=%d body=%s", response.Code, response.Body.String())
	}

	bodyMarker := "HTTP_BODY_ONLY_4a91"
	response = doJSON(t, h, http.MethodPost, "/v1/private-notes/write", `{
		"title":"Nexus 恢复信息",
		"category":"recovery",
		"summary":"记录 Nexus 恢复资料的位置",
		"tags":["nexus","recovery"],
		"content":"# 正文\n\n`+bodyMarker+`\nTOKEN=private-value",
		"confirmed":true
	}`)
	if response.Code != http.StatusOK {
		t.Fatalf("write status=%d body=%s", response.Code, response.Body.String())
	}
	var write struct {
		Path          string `json:"path"`
		EncryptedPath string `json:"encrypted_path"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &write); err != nil {
		t.Fatal(err)
	}
	if write.Path == "" || write.EncryptedPath == "" {
		t.Fatalf("write response missing paths: %s", response.Body.String())
	}

	response = doJSON(t, h, http.MethodPost, "/v1/private-notes/search", `{"query":"Nexus 恢复","max_results":8}`)
	if response.Code != http.StatusOK {
		t.Fatalf("search status=%d body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"metadata_only":true`) || !strings.Contains(response.Body.String(), "Nexus 恢复信息") {
		t.Fatalf("metadata search response incomplete: %s", response.Body.String())
	}
	if strings.Contains(response.Body.String(), bodyMarker) || strings.Contains(response.Body.String(), "private-value") || strings.Contains(response.Body.String(), `"snippet"`) {
		t.Fatalf("search response leaked private body: %s", response.Body.String())
	}
	response = doJSON(t, h, http.MethodPost, "/v1/private-notes/search", `{"query":"`+bodyMarker+`"}`)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"count":0`) {
		t.Fatalf("body-only term must not be searchable: %d %s", response.Code, response.Body.String())
	}

	response = doJSON(t, h, http.MethodPost, "/v1/private-notes/read", `{"path":`+quotedJSON(t, write.Path)+`}`)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), bodyMarker) {
		t.Fatalf("explicit read failed: %d %s", response.Code, response.Body.String())
	}

	response = doJSON(t, h, http.MethodPost, "/v1/private-notes/delete", `{"path":`+quotedJSON(t, write.Path)+`,"confirmed":false}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unconfirmed delete status=%d body=%s", response.Code, response.Body.String())
	}
	response = doJSON(t, h, http.MethodPost, "/v1/private-notes/delete", `{"path":`+quotedJSON(t, write.Path)+`,"confirmed":true}`)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"deleted_plaintext":true`) || !strings.Contains(response.Body.String(), `"deleted_encrypted":true`) {
		t.Fatalf("delete failed: %d %s", response.Code, response.Body.String())
	}
}

func quotedJSON(t *testing.T, value string) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
