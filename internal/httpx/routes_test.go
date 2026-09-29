package httpx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/uvwt/nexusdock/internal/agentdock"
	"github.com/uvwt/nexusdock/internal/core"
	"github.com/uvwt/nexusdock/internal/recall"
)

func newRouteSecurityTestServer(t *testing.T) (*Server, string) {
	t.Helper()

	server := newNodeTestServer(t)
	store, err := recall.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server.store = store

	pairing, err := server.agentDock.CreatePairingCode(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	node, err := server.agentDock.Pair(t.Context(), agentdock.PairInput{
		Code: pairing.Code, DeviceID: "device_route_security", Name: "Route Security",
	})
	if err != nil {
		t.Fatal(err)
	}
	issued, err := server.access.auth.IssueToken(
		t.Context(),
		core.Actor{Type: core.ActorDevice, ID: node.ID},
		"device_token",
		nil,
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	return server, issued.Token
}

func TestRouterSecurityDomainsKeepDeviceOutOfAdminRoutes(t *testing.T) {
	server, deviceToken := newRouteSecurityTestServer(t)
	handler := server.Handler()

	adminRequest := httptest.NewRequest(http.MethodGet, "/v1/system/status", nil)
	adminRequest.Header.Set("Authorization", "Bearer "+deviceToken)
	adminResponse := httptest.NewRecorder()
	handler.ServeHTTP(adminResponse, adminRequest)
	if adminResponse.Code != http.StatusUnauthorized {
		t.Fatalf("device token reached admin route: status=%d body=%s", adminResponse.Code, adminResponse.Body.String())
	}

	recallRequest := httptest.NewRequest(http.MethodGet, "/v1/recall", nil)
	recallRequest.Header.Set("Authorization", "Bearer "+deviceToken)
	recallResponse := httptest.NewRecorder()
	handler.ServeHTTP(recallResponse, recallRequest)
	if recallResponse.Code != http.StatusOK {
		t.Fatalf("device token could not reach shared Recall route: status=%d body=%s", recallResponse.Code, recallResponse.Body.String())
	}
}

func TestRouterInternalLifecycleRequiresEnabledDevice(t *testing.T) {
	server, deviceToken := newRouteSecurityTestServer(t)
	handler := server.Handler()

	unauthorized := httptest.NewRequest(
		http.MethodPost,
		"/internal/recall/lifecycle/query",
		strings.NewReader("{}"),
	)
	unauthorized.Header.Set("Content-Type", "application/json")
	unauthorizedResponse := httptest.NewRecorder()
	handler.ServeHTTP(unauthorizedResponse, unauthorized)
	if unauthorizedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("internal lifecycle route allowed anonymous request: status=%d body=%s", unauthorizedResponse.Code, unauthorizedResponse.Body.String())
	}

	authorized := httptest.NewRequest(
		http.MethodPost,
		"/internal/recall/lifecycle/query",
		strings.NewReader("{}"),
	)
	authorized.Header.Set("Content-Type", "application/json")
	authorized.Header.Set("Authorization", "Bearer "+deviceToken)
	authorizedResponse := httptest.NewRecorder()
	handler.ServeHTTP(authorizedResponse, authorized)
	if authorizedResponse.Code != http.StatusOK {
		t.Fatalf("enabled device could not reach internal lifecycle route: status=%d body=%s", authorizedResponse.Code, authorizedResponse.Body.String())
	}
}

func TestRouterLeavesLoginAssetsPublicWhileProtectingUI(t *testing.T) {
	server := newNodeTestServer(t)
	handler := server.Handler()

	asset := httptest.NewRequest(http.MethodGet, "/ui/assets/index-BjxNsJ2Y.css", nil)
	assetResponse := httptest.NewRecorder()
	handler.ServeHTTP(assetResponse, asset)
	if assetResponse.Code != http.StatusOK {
		t.Fatalf("login asset unexpectedly required a session: status=%d body=%s", assetResponse.Code, assetResponse.Body.String())
	}

	root := httptest.NewRequest(http.MethodGet, "/", nil)
	rootResponse := httptest.NewRecorder()
	handler.ServeHTTP(rootResponse, root)
	if rootResponse.Code != http.StatusFound || !strings.HasPrefix(rootResponse.Header().Get("Location"), "/login?return_to=") {
		t.Fatalf("protected UI did not redirect to login: status=%d location=%q", rootResponse.Code, rootResponse.Header().Get("Location"))
	}
}

func TestRouterAPIFallbackPreservesNotFoundAndMethodNotAllowed(t *testing.T) {
	server := newNodeTestServer(t)
	handler := server.Handler()

	for _, target := range []string{"/v1/does-not-exist", "/api/does-not-exist"} {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("GET %s status=%d, want 404", target, response.Code)
		}
	}

	unknownPost := httptest.NewRequest(http.MethodPost, "/v1/does-not-exist", nil)
	unknownPostResponse := httptest.NewRecorder()
	handler.ServeHTTP(unknownPostResponse, unknownPost)
	if unknownPostResponse.Code != http.StatusMethodNotAllowed {
		t.Fatalf("unknown POST status=%d, want 405", unknownPostResponse.Code)
	}

	wrongMethod := httptest.NewRequest(http.MethodPost, "/v1/system/status", nil)
	wrongMethodResponse := httptest.NewRecorder()
	handler.ServeHTTP(wrongMethodResponse, wrongMethod)
	if wrongMethodResponse.Code != http.StatusMethodNotAllowed {
		t.Fatalf("known route with wrong method status=%d, want 405", wrongMethodResponse.Code)
	}
}

func TestRouterPreservesServeMuxGetHeadSemantics(t *testing.T) {
	server := newNodeTestServer(t)
	request := httptest.NewRequest(http.MethodHead, "/health", nil)
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("HEAD /health status=%d, want 200", response.Code)
	}
}

func TestWildcardPathValuePreservesMultiSegmentPath(t *testing.T) {
	router := chi.NewRouter()
	router.Get("/files/*", withWildcardPathValue("filePath", func(w http.ResponseWriter, r *http.Request) {
		if got := r.PathValue("filePath"); got != "nested/path/file.txt" {
			t.Fatalf("filePath=%q, want nested/path/file.txt", got)
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	request := httptest.NewRequest(http.MethodGet, "/files/nested/path/file.txt", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("wildcard route status=%d, want 204", response.Code)
	}
}
