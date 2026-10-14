//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

type word struct {
	ID       string `json:"id"`
	Value    string `json:"value"`
	Category string `json:"category"`
	Status   string `json:"status"`
	Safe     bool   `json:"safe"`
}

type wordPage struct {
	Words []word `json:"words"`
}

type nameResponse struct {
	Name string `json:"name"`
}

func TestAPIWorkflow(t *testing.T) {
	baseURL := environment(t, "E2E_BASE_URL")
	authMode := environment(t, "E2E_AUTH_MODE")
	contributorToken := environment(t, "E2E_CONTRIBUTOR_TOKEN")
	moderatorToken := environment(t, "E2E_MODERATOR_TOKEN")
	client := &http.Client{Timeout: 10 * time.Second}

	assertStatus(t, client, http.MethodGet, baseURL+"/health/live", "", "", nil, http.StatusOK)
	assertStatus(t, client, http.MethodGet, baseURL+"/health/ready", "", "", nil, http.StatusOK)

	response := request(t, client, http.MethodGet, baseURL+"/v1/names/random", "", "", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("generate status = %d; body: %s", response.StatusCode, response.Body)
	}
	var generated nameResponse
	decode(t, response.Body, &generated)
	if !strings.Contains(generated.Name, "-") {
		t.Fatalf("generated name = %q, want a compound name", generated.Name)
	}

	response = request(t, client, http.MethodGet, baseURL+"/v1/names/random?safe=false", "", "", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("unsafe generate status = %d; body: %s", response.StatusCode, response.Body)
	}
	decode(t, response.Body, &generated)
	unsafeNames := map[string]bool{
		"bullshit": true, "prostitute": true, "prostitution": true, "shit": true,
	}
	unsafeDescriptions := map[string]bool{
		"damn": true, "goddamn": true, "goddamned": true, "obscene": true,
		"sexual": true, "sexy": true, "vulgar": true,
	}
	parts := strings.Split(generated.Name, "-")
	if len(parts) != 2 || !unsafeNames[parts[0]] || !unsafeDescriptions[parts[1]] {
		t.Fatalf("unsafe name = %q, want an explicitly classified pair", generated.Name)
	}

	assertStatus(
		t,
		client,
		http.MethodGet,
		baseURL+"/v1/words?category=nouns",
		"",
		"",
		nil,
		http.StatusUnauthorized,
	)

	value := "e2e-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	payload := []byte(fmt.Sprintf(`{"category":"nouns","value":%q}`, value))
	response = request(
		t,
		client,
		http.MethodPost,
		baseURL+"/v1/words",
		authMode,
		contributorToken,
		payload,
	)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d; body: %s", response.StatusCode, response.Body)
	}
	var proposal word
	decode(t, response.Body, &proposal)
	if proposal.ID == "" || proposal.Status != "proposed" || !proposal.Safe {
		t.Fatalf("proposal = %+v", proposal)
	}
	t.Cleanup(func() {
		_ = requestWithoutTestFailure(
			client,
			http.MethodDelete,
			baseURL+"/v1/words/"+proposal.ID,
			authMode,
			moderatorToken,
			nil,
		)
	})

	assertStatus(
		t,
		client,
		http.MethodPost,
		baseURL+"/v1/words",
		authMode,
		contributorToken,
		payload,
		http.StatusConflict,
	)

	if os.Getenv("E2E_EXPECT_CONTRIBUTOR_FORBIDDEN") == "true" {
		assertStatus(
			t,
			client,
			http.MethodGet,
			baseURL+"/v1/words?category=nouns",
			authMode,
			contributorToken,
			nil,
			http.StatusForbidden,
		)
	}

	response = request(
		t,
		client,
		http.MethodGet,
		baseURL+"/v1/words?category=nouns&status=proposed&limit=200",
		authMode,
		moderatorToken,
		nil,
	)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("list proposals status = %d; body: %s", response.StatusCode, response.Body)
	}
	var proposals wordPage
	decode(t, response.Body, &proposals)
	if !containsWord(proposals.Words, proposal.ID) {
		t.Fatalf("proposal %q was not listed", proposal.ID)
	}

	response = request(
		t,
		client,
		http.MethodPatch,
		baseURL+"/v1/words/"+proposal.ID+"/safety",
		authMode,
		moderatorToken,
		[]byte(`{"safe":false}`),
	)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("safety status = %d; body: %s", response.StatusCode, response.Body)
	}
	var classified word
	decode(t, response.Body, &classified)
	if classified.Safe {
		t.Fatal("classified word remained safe")
	}

	response = request(
		t,
		client,
		http.MethodPost,
		baseURL+"/v1/words/"+proposal.ID+"/approve",
		authMode,
		moderatorToken,
		nil,
	)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("approve status = %d; body: %s", response.StatusCode, response.Body)
	}
	var approved word
	decode(t, response.Body, &approved)
	if approved.Status != "approved" {
		t.Fatalf("approved status = %q", approved.Status)
	}

	assertStatus(
		t,
		client,
		http.MethodDelete,
		baseURL+"/v1/words/"+proposal.ID,
		authMode,
		moderatorToken,
		nil,
		http.StatusNoContent,
	)
	assertStatus(
		t,
		client,
		http.MethodDelete,
		baseURL+"/v1/words/"+proposal.ID,
		authMode,
		moderatorToken,
		nil,
		http.StatusNoContent,
	)
}

type httpResponse struct {
	StatusCode int
	Body       []byte
}

func request(
	t *testing.T,
	client *http.Client,
	method, url, authMode, token string,
	body []byte,
) httpResponse {
	t.Helper()
	response, err := makeRequest(client, method, url, authMode, token, body)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	return response
}

func requestWithoutTestFailure(
	client *http.Client,
	method, url, authMode, token string,
	body []byte,
) error {
	_, err := makeRequest(client, method, url, authMode, token, body)
	return err
}

func makeRequest(
	client *http.Client,
	method, url, authMode, token string,
	body []byte,
) (httpResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return httpResponse{}, err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	switch authMode {
	case "static":
		request.Header.Set("X-Admin-Token", token)
	case "bearer":
		request.Header.Set("Authorization", "Bearer "+token)
	case "":
	default:
		return httpResponse{}, fmt.Errorf("unsupported auth mode %q", authMode)
	}

	response, err := client.Do(request)
	if err != nil {
		return httpResponse{}, err
	}
	responseBody, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if readErr != nil {
		return httpResponse{}, readErr
	}
	if closeErr != nil {
		return httpResponse{}, closeErr
	}
	return httpResponse{StatusCode: response.StatusCode, Body: responseBody}, nil
}

func assertStatus(
	t *testing.T,
	client *http.Client,
	method, url, authMode, token string,
	body []byte,
	want int,
) {
	t.Helper()
	response := request(t, client, method, url, authMode, token, body)
	if response.StatusCode != want {
		t.Fatalf(
			"%s %s status = %d, want %d; body: %s",
			method,
			url,
			response.StatusCode,
			want,
			response.Body,
		)
	}
}

func decode(t *testing.T, data []byte, destination any) {
	t.Helper()
	if err := json.Unmarshal(data, destination); err != nil {
		t.Fatalf("decode response %q: %v", data, err)
	}
}

func containsWord(words []word, id string) bool {
	for _, candidate := range words {
		if candidate.ID == id {
			return true
		}
	}
	return false
}

func environment(t *testing.T, name string) string {
	t.Helper()
	value := os.Getenv(name)
	if value == "" {
		t.Fatalf("%s is required", name)
	}
	return value
}
