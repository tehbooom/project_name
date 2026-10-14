package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tehbooom/project_name/internal/auth"
	"github.com/tehbooom/project_name/internal/model"
	"github.com/tehbooom/project_name/internal/wordstore"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestGetName(t *testing.T) {
	a := newTestApp(t)
	request := httptest.NewRequest(http.MethodGet, "/v1/names/random", nil)
	response := httptest.NewRecorder()

	a.Router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusOK, response.Body)
	}
	var body Name
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Name == "" || !strings.Contains(body.Name, "-") {
		t.Fatalf("name = %q, want a compound project name", body.Name)
	}
}

func TestGetNameLengthConstraints(t *testing.T) {
	a := newTestApp(t)

	request := httptest.NewRequest(
		http.MethodGet,
		"/v1/names/random?max_length=4&max_noun_length=6",
		nil,
	)
	response := httptest.NewRecorder()
	a.Router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusOK, response.Body)
	}
	var body Name
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(body.Name, "-")
	if len(parts) != 2 || len(parts[0]) > 6 || len(parts[1]) > 4 {
		t.Fatalf("name = %q, want noun <= 6 and adjective <= 4 characters", body.Name)
	}

	for _, query := range []string{"max_length=0", "max_noun_length=65", "max_adjective_length=nope"} {
		request := httptest.NewRequest(http.MethodGet, "/v1/names/random?"+query, nil)
		response := httptest.NewRecorder()
		a.Router.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want %d", query, response.Code, http.StatusBadRequest)
		}
		assertErrorCode(t, response, "invalid_length")
	}

	request = httptest.NewRequest(http.MethodGet, "/v1/names/random?max_length=1", nil)
	response = httptest.NewRecorder()
	a.Router.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("no-match status = %d, want %d", response.Code, http.StatusNotFound)
	}
	assertErrorCode(t, response, "no_matching_words")
}

func TestUnsafeGenerationRequiresFeatureFlag(t *testing.T) {
	t.Setenv(UnsafeModeEnvironment, "false")
	words := model.NewWordList()
	for _, word := range []struct {
		category string
		value    string
		safe     bool
	}{
		{model.CategoryNouns, "otter", true},
		{model.CategoryAdjectives, "calm", true},
		{model.CategoryNouns, "shit", false},
		{model.CategoryAdjectives, "damn", false},
	} {
		if err := words.AddWordWithSafety(word.category, word.value, word.safe); err != nil {
			t.Fatal(err)
		}
	}

	a := &App{}
	if err := a.Initialize(wordstore.NewMemory(words)); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/names/random?safe=false", nil)
	response := httptest.NewRecorder()
	a.Router.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("disabled status = %d, want %d", response.Code, http.StatusForbidden)
	}
	assertErrorCode(t, response, "unsafe_mode_disabled")

	t.Setenv(UnsafeModeEnvironment, "true")
	a = &App{}
	if err := a.Initialize(wordstore.NewMemory(words)); err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(http.MethodGet, "/v1/names/random?safe=false", nil)
	response = httptest.NewRecorder()
	a.Router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("enabled status = %d, want %d; body: %s", response.Code, http.StatusOK, response.Body)
	}
	var name Name
	if err := json.Unmarshal(response.Body.Bytes(), &name); err != nil {
		t.Fatal(err)
	}
	if name.Name != "shit-damn" {
		t.Fatalf("unsafe name = %q, want shit-damn", name.Name)
	}

	request = httptest.NewRequest(http.MethodGet, "/v1/config", nil)
	response = httptest.NewRecorder()
	a.Router.ServeHTTP(response, request)
	var config PublicConfig
	if err := json.Unmarshal(response.Body.Bytes(), &config); err != nil {
		t.Fatal(err)
	}
	if !config.UnsafeModeEnabled {
		t.Fatal("unsafe mode is not advertised")
	}
}

func TestHealthChecks(t *testing.T) {
	t.Run("healthy", func(t *testing.T) {
		a := newTestApp(t)
		for path, wantStatus := range map[string]int{
			"/health/live":  http.StatusOK,
			"/health/ready": http.StatusOK,
		} {
			request := httptest.NewRequest(http.MethodGet, path, nil)
			response := httptest.NewRecorder()
			a.Router.ServeHTTP(response, request)
			if response.Code != wantStatus {
				t.Fatalf("%s status = %d, want %d; body: %s", path, response.Code, wantStatus, response.Body)
			}
			if cacheControl := response.Header().Get("Cache-Control"); cacheControl != "no-store" {
				t.Fatalf("%s Cache-Control = %q, want no-store", path, cacheControl)
			}
		}
	})

	t.Run("not ready", func(t *testing.T) {
		a := &App{}
		if err := a.Initialize(wordstore.NewMemory(model.NewWordList())); err != nil {
			t.Fatal(err)
		}

		liveRequest := httptest.NewRequest(http.MethodGet, "/health/live", nil)
		liveResponse := httptest.NewRecorder()
		a.Router.ServeHTTP(liveResponse, liveRequest)
		if liveResponse.Code != http.StatusOK {
			t.Fatalf("liveness status = %d, want %d", liveResponse.Code, http.StatusOK)
		}

		readyRequest := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
		readyResponse := httptest.NewRecorder()
		a.Router.ServeHTTP(readyResponse, readyRequest)
		if readyResponse.Code != http.StatusServiceUnavailable {
			t.Fatalf("readiness status = %d, want %d", readyResponse.Code, http.StatusServiceUnavailable)
		}
		assertErrorCode(t, readyResponse, "not_ready")
	})
}

func TestApplicationMetrics(t *testing.T) {
	previousProvider := otel.GetMeterProvider()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	otel.SetMeterProvider(provider)
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
		otel.SetMeterProvider(previousProvider)
	})

	a := newTestApp(t)
	for _, path := range []string{"/v1/names/random", "/health/ready"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		a.Router.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want %d", path, response.Code, http.StatusOK)
		}
	}

	var metrics metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &metrics); err != nil {
		t.Fatal(err)
	}
	got := make(map[string]bool)
	for _, scope := range metrics.ScopeMetrics {
		for _, metric := range scope.Metrics {
			got[metric.Name] = true
		}
	}
	for _, name := range []string{
		"http.server.request.duration",
		"http.server.response.body.size",
		"project.health.checks",
		"project.name_generation.operations",
		"project.names.generated",
	} {
		if !got[name] {
			t.Errorf("metric %q was not recorded", name)
		}
	}
}

func TestPanicRecovery(t *testing.T) {
	a := newTestApp(t)
	a.Router.HandleFunc("/panic", func(http.ResponseWriter, *http.Request) {
		panic("test panic")
	}).Methods(http.MethodGet)

	request := httptest.NewRequest(http.MethodGet, "/panic", nil)
	response := httptest.NewRecorder()
	a.Router.ServeHTTP(response, request)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf(
			"status = %d, want %d; body: %s",
			response.Code,
			http.StatusInternalServerError,
			response.Body,
		)
	}
	assertErrorCode(t, response, "internal_error")
}

func TestLegacyMutationsAreRemoved(t *testing.T) {
	a := newTestApp(t, WithAuthorizer(stubAuthorizer{}))
	for _, method := range []string{http.MethodPost, http.MethodDelete} {
		request := httptest.NewRequest(method, "/project", nil)
		response := httptest.NewRecorder()
		a.Router.ServeHTTP(response, request)
		if response.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s /project status = %d, want %d", method, response.Code, http.StatusMethodNotAllowed)
		}
	}
}

func TestContributorPermissions(t *testing.T) {
	a := newTestApp(t, WithAuthorizer(stubAuthorizer{
		permissions: map[string]bool{auth.PermissionCreateWord: true},
	}))

	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/words",
		strings.NewReader(`{"category":"nouns","value":"otter"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	a.Router.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d; body: %s", response.Code, http.StatusCreated, response.Body)
	}

	request = httptest.NewRequest(http.MethodGet, "/v1/words?category=nouns", nil)
	response = httptest.NewRecorder()
	a.Router.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("list status = %d, want %d; body: %s", response.Code, http.StatusForbidden, response.Body)
	}
}

func TestCreateWordProposalValidation(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
		wantStatus  int
		wantCode    string
	}{
		{
			name:        "valid",
			contentType: "application/json",
			body:        `{"category":"nouns","value":"otter"}`,
			wantStatus:  http.StatusCreated,
		},
		{
			name:        "duplicate",
			contentType: "application/json",
			body:        `{"category":"nouns","value":"gopher"}`,
			wantStatus:  http.StatusConflict,
			wantCode:    "duplicate_word",
		},
		{
			name:        "unknown field",
			contentType: "application/json",
			body:        `{"category":"nouns","value":"otter","admin":true}`,
			wantStatus:  http.StatusBadRequest,
			wantCode:    "invalid_request",
		},
		{
			name:        "multiple objects",
			contentType: "application/json",
			body:        `{"category":"nouns","value":"otter"} {}`,
			wantStatus:  http.StatusBadRequest,
			wantCode:    "invalid_request",
		},
		{
			name:       "missing content type",
			body:       `{"category":"nouns","value":"otter"}`,
			wantStatus: http.StatusUnsupportedMediaType,
			wantCode:   "unsupported_media_type",
		},
		{
			name:        "body too large",
			contentType: "application/json",
			body:        `{"category":"nouns","value":"` + strings.Repeat("a", maxRequestBodyBytes) + `"}`,
			wantStatus:  http.StatusRequestEntityTooLarge,
			wantCode:    "request_too_large",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			a := newTestApp(t, WithAuthorizer(stubAuthorizer{}))
			request := httptest.NewRequest(http.MethodPost, "/v1/words", strings.NewReader(test.body))
			if test.contentType != "" {
				request.Header.Set("Content-Type", test.contentType)
			}
			response := httptest.NewRecorder()

			a.Router.ServeHTTP(response, request)

			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body: %s", response.Code, test.wantStatus, response.Body)
			}
			if test.wantCode != "" {
				assertErrorCode(t, response, test.wantCode)
			}
		})
	}
}

func TestProposalModerationWorkflow(t *testing.T) {
	a := newTestApp(t, WithAuthorizer(stubAuthorizer{}))

	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/words",
		strings.NewReader(`{"category":"nouns","value":"otter"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	a.Router.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d; body: %s", response.Code, http.StatusCreated, response.Body)
	}
	var proposal model.Word
	if err := json.Unmarshal(response.Body.Bytes(), &proposal); err != nil {
		t.Fatal(err)
	}
	if proposal.Status != model.StatusProposed {
		t.Fatalf("proposal status = %q", proposal.Status)
	}
	if !proposal.Safe {
		t.Fatal("new proposal is unsafe by default")
	}

	request = httptest.NewRequest(
		http.MethodPatch,
		"/v1/words/"+proposal.ID+"/safety",
		strings.NewReader(`{"safe":false}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	a.Router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("safety status = %d, want %d; body: %s", response.Code, http.StatusOK, response.Body)
	}
	var safetyUpdated model.Word
	if err := json.Unmarshal(response.Body.Bytes(), &safetyUpdated); err != nil {
		t.Fatal(err)
	}
	if safetyUpdated.Safe {
		t.Fatal("word remained safe after moderation")
	}

	request = httptest.NewRequest(http.MethodPost, "/v1/words/"+proposal.ID+"/approve", nil)
	response = httptest.NewRecorder()
	a.Router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("approve status = %d, want %d; body: %s", response.Code, http.StatusOK, response.Body)
	}
	var approved model.Word
	if err := json.Unmarshal(response.Body.Bytes(), &approved); err != nil {
		t.Fatal(err)
	}
	if approved.Status != model.StatusApproved {
		t.Fatalf("approved status = %q", approved.Status)
	}

	for attempt := 0; attempt < 2; attempt++ {
		request = httptest.NewRequest(http.MethodDelete, "/v1/words/"+proposal.ID, nil)
		response = httptest.NewRecorder()
		a.Router.ServeHTTP(response, request)
		if response.Code != http.StatusNoContent {
			t.Fatalf("delete attempt %d status = %d, want %d; body: %s", attempt+1, response.Code, http.StatusNoContent, response.Body)
		}
	}
}

func TestListWordsRequiresAuthentication(t *testing.T) {
	a := newTestApp(t)
	request := httptest.NewRequest(http.MethodGet, "/v1/words?category=nouns", nil)
	response := httptest.NewRecorder()

	a.Router.ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusUnauthorized, response.Body)
	}
	assertErrorCode(t, response, "unauthenticated")
}

func TestListWordsPagination(t *testing.T) {
	a := newTestApp(t, WithAuthorizer(stubAuthorizer{}))

	request := httptest.NewRequest(http.MethodGet, "/v1/words?category=nouns&limit=1", nil)
	response := httptest.NewRecorder()
	a.Router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusOK, response.Body)
	}
	var firstPage struct {
		Words []struct {
			Value string `json:"value"`
		} `json:"words"`
		NextPageToken string `json:"next_page_token"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &firstPage); err != nil {
		t.Fatal(err)
	}
	if len(firstPage.Words) != 1 || firstPage.Words[0].Value != "badger" {
		t.Fatalf("first page = %+v, want badger", firstPage.Words)
	}
	if firstPage.NextPageToken == "" {
		t.Fatal("first page has no next page token")
	}

	request = httptest.NewRequest(
		http.MethodGet,
		"/v1/words?category=nouns&limit=1&page_token="+firstPage.NextPageToken,
		nil,
	)
	response = httptest.NewRecorder()
	a.Router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("second page status = %d, want %d; body: %s", response.Code, http.StatusOK, response.Body)
	}
}

type stubAuthorizer struct {
	err         error
	permissions map[string]bool
}

func (a stubAuthorizer) Authorize(_ *http.Request, permission string) error {
	if a.err != nil {
		return a.err
	}
	if a.permissions != nil && !a.permissions[permission] {
		return auth.ErrForbidden
	}
	return nil
}

func newTestApp(t *testing.T, options ...Option) *App {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"adjectives.json": `["brave","calm"]`,
		"nouns.json":      `["gopher","badger"]`,
	}
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	words, err := model.LoadWords(dir)
	if err != nil {
		t.Fatalf("LoadWords(): %v", err)
	}
	a := &App{}
	if err := a.Initialize(wordstore.NewMemory(words), options...); err != nil {
		t.Fatalf("Initialize(): %v", err)
	}
	return a
}

func assertErrorCode(t *testing.T, response *httptest.ResponseRecorder, want string) {
	t.Helper()
	var body ErrorResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error response: %v; body: %s", err, response.Body)
	}
	if body.Error.Code != want {
		t.Fatalf("error code = %q, want %q", body.Error.Code, want)
	}
}
