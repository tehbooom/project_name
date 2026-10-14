package web

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTelemetryConfigDisabledWithoutUpstream(t *testing.T) {
	t.Setenv(BrowserOTLPEndpointEnv, "")
	telemetry, err := NewTelemetryFromEnvironment()
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/otel/config.js", nil)
	response := httptest.NewRecorder()
	telemetry.ConfigHandler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if !strings.Contains(response.Body.String(), `"enabled":false`) {
		t.Fatalf("config = %s, want disabled", response.Body)
	}

	request = httptest.NewRequest(
		http.MethodPost,
		"/otel/v1/traces",
		strings.NewReader(`{}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	telemetry.ExportHandler("traces").ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("disabled export status = %d, want %d", response.Code, http.StatusNoContent)
	}
}

func TestTelemetryProxyForwardsBoundedOTLP(t *testing.T) {
	var gotPath, gotContentType, gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotContentType = r.Header.Get("Content-Type")
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		gotBody = string(body)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	t.Setenv(BrowserOTLPEndpointEnv, upstream.URL)
	t.Setenv(BrowserOTELSampleRatioEnv, "0.25")
	telemetry, err := NewTelemetryFromEnvironment()
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(
		http.MethodPost,
		"/otel/v1/traces",
		strings.NewReader(`{"resourceSpans":[]}`),
	)
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	response := httptest.NewRecorder()
	telemetry.ExportHandler("traces").ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusNoContent, response.Body)
	}
	if gotPath != "/v1/traces" {
		t.Errorf("upstream path = %q, want /v1/traces", gotPath)
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", gotContentType)
	}
	if gotBody != `{"resourceSpans":[]}` {
		t.Errorf("body = %q", gotBody)
	}

	configRequest := httptest.NewRequest(http.MethodGet, "/otel/config.js", nil)
	configResponse := httptest.NewRecorder()
	telemetry.ConfigHandler().ServeHTTP(configResponse, configRequest)
	if !strings.Contains(configResponse.Body.String(), `"sampleRatio":0.25`) {
		t.Errorf("config = %s, want configured sample ratio", configResponse.Body)
	}
}

func TestTelemetryProxyRejectsUnsafeRequests(t *testing.T) {
	t.Setenv(BrowserOTLPEndpointEnv, "http://collector.example")
	telemetry, err := NewTelemetryFromEnvironment()
	if err != nil {
		t.Fatal(err)
	}

	t.Run("unsupported content type", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodPost, "/otel/v1/logs", strings.NewReader("text"))
		request.Header.Set("Content-Type", "text/plain")
		response := httptest.NewRecorder()
		telemetry.ExportHandler("logs").ServeHTTP(response, request)
		if response.Code != http.StatusUnsupportedMediaType {
			t.Fatalf("status = %d, want %d", response.Code, http.StatusUnsupportedMediaType)
		}
	})

	t.Run("oversized body", func(t *testing.T) {
		request := httptest.NewRequest(
			http.MethodPost,
			"/otel/v1/metrics",
			bytes.NewReader(make([]byte, maxTelemetryBodyBytes+1)),
		)
		request.Header.Set("Content-Type", "application/x-protobuf")
		response := httptest.NewRecorder()
		telemetry.ExportHandler("metrics").ServeHTTP(response, request)
		if response.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status = %d, want %d", response.Code, http.StatusRequestEntityTooLarge)
		}
	})
}

func TestTelemetryConfigurationValidation(t *testing.T) {
	for _, test := range []struct {
		name     string
		endpoint string
		ratio    string
	}{
		{name: "invalid scheme", endpoint: "file:///tmp/collector"},
		{name: "credentials", endpoint: "http://user:password@collector"},
		{name: "ratio", endpoint: "http://collector", ratio: "2"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(BrowserOTLPEndpointEnv, test.endpoint)
			t.Setenv(BrowserOTELSampleRatioEnv, test.ratio)
			if _, err := NewTelemetryFromEnvironment(); err == nil {
				t.Fatal("NewTelemetryFromEnvironment() accepted invalid configuration")
			}
		})
	}
}
