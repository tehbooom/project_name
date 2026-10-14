package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/time/rate"
)

const (
	BrowserOTLPEndpointEnv         = "BROWSER_OTEL_EXPORTER_OTLP_ENDPOINT"
	BrowserOTELSampleRatioEnv      = "BROWSER_OTEL_SAMPLE_RATIO"
	BrowserOTELServiceNameEnv      = "BROWSER_OTEL_SERVICE_NAME"
	BrowserOTELServiceVersionEnv   = "BROWSER_OTEL_SERVICE_VERSION"
	BrowserOTELServiceNamespaceEnv = "BROWSER_OTEL_SERVICE_NAMESPACE"
	BrowserOTELDeploymentEnv       = "BROWSER_OTEL_DEPLOYMENT_ENVIRONMENT"

	maxTelemetryBodyBytes = 512 << 10
)

type Telemetry struct {
	enabled               bool
	upstream              *url.URL
	sampleRatio           float64
	serviceName           string
	serviceNamespace      string
	serviceVersion        string
	deploymentEnvironment string
	client                *http.Client
	limiter               *rate.Limiter
}

type browserConfig struct {
	Enabled               bool    `json:"enabled"`
	SampleRatio           float64 `json:"sampleRatio"`
	ServiceName           string  `json:"serviceName"`
	ServiceNamespace      string  `json:"serviceNamespace"`
	ServiceVersion        string  `json:"serviceVersion"`
	DeploymentEnvironment string  `json:"deploymentEnvironment"`
}

func NewTelemetryFromEnvironment() (Telemetry, error) {
	telemetry := Telemetry{
		sampleRatio:           0.1,
		serviceName:           environmentOrDefault(BrowserOTELServiceNameEnv, "project-name-web"),
		serviceNamespace:      environmentOrDefault(BrowserOTELServiceNamespaceEnv, "examples"),
		serviceVersion:        environmentOrDefault(BrowserOTELServiceVersionEnv, "dev"),
		deploymentEnvironment: environmentOrDefault(BrowserOTELDeploymentEnv, "local"),
		client:                &http.Client{Timeout: 5 * time.Second},
		limiter:               rate.NewLimiter(100, 200),
	}

	rawEndpoint := strings.TrimSpace(os.Getenv(BrowserOTLPEndpointEnv))
	if rawEndpoint == "" {
		return telemetry, nil
	}
	upstream, err := url.Parse(rawEndpoint)
	if err != nil ||
		(upstream.Scheme != "http" && upstream.Scheme != "https") ||
		upstream.Host == "" ||
		upstream.User != nil ||
		upstream.RawQuery != "" ||
		upstream.Fragment != "" {
		return Telemetry{}, fmt.Errorf("%s must be an HTTP(S) origin URL", BrowserOTLPEndpointEnv)
	}
	upstream.Path = strings.TrimRight(upstream.Path, "/")

	if rawRatio := strings.TrimSpace(os.Getenv(BrowserOTELSampleRatioEnv)); rawRatio != "" {
		ratio, err := strconv.ParseFloat(rawRatio, 64)
		if err != nil || ratio < 0 || ratio > 1 {
			return Telemetry{}, fmt.Errorf("%s must be between 0 and 1", BrowserOTELSampleRatioEnv)
		}
		telemetry.sampleRatio = ratio
	}
	telemetry.enabled = telemetry.sampleRatio > 0
	telemetry.upstream = upstream
	return telemetry, nil
}

func (t Telemetry) ConfigHandler() http.Handler {
	config := browserConfig{
		Enabled:               t.enabled,
		SampleRatio:           t.sampleRatio,
		ServiceName:           t.serviceName,
		ServiceNamespace:      t.serviceNamespace,
		ServiceVersion:        t.serviceVersion,
		DeploymentEnvironment: t.deploymentEnvironment,
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		panic(err)
	}
	body := append([]byte("globalThis.__PROJECT_NAME_OTEL__="), encoded...)
	body = append(body, ';', '\n')

	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(body)
	})
}

func (t Telemetry) ExportHandler(signal string) http.Handler {
	if signal != "traces" && signal != "metrics" && signal != "logs" {
		panic("unsupported browser telemetry signal")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.export(w, r, signal)
	})
}

func (t Telemetry) export(w http.ResponseWriter, r *http.Request, signal string) {
	w.Header().Set("Cache-Control", "no-store")
	if !t.enabled {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if !t.limiter.Allow() {
		respondTelemetryError(w, http.StatusTooManyRequests)
		return
	}
	if r.Header.Get("Content-Encoding") != "" {
		respondTelemetryError(w, http.StatusUnsupportedMediaType)
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil ||
		(mediaType != "application/json" && mediaType != "application/x-protobuf") {
		respondTelemetryError(w, http.StatusUnsupportedMediaType)
		return
	}

	body := http.MaxBytesReader(w, r.Body, maxTelemetryBodyBytes)
	payload, err := io.ReadAll(body)
	if err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			respondTelemetryError(w, http.StatusRequestEntityTooLarge)
			return
		}
		respondTelemetryError(w, http.StatusBadRequest)
		return
	}

	target := *t.upstream
	target.Path += "/v1/" + signal
	upstreamRequest, err := http.NewRequestWithContext(
		r.Context(),
		http.MethodPost,
		target.String(),
		bytes.NewReader(payload),
	)
	if err != nil {
		respondTelemetryError(w, http.StatusBadGateway)
		return
	}
	upstreamRequest.Header.Set("Content-Type", mediaType)
	response, err := t.client.Do(upstreamRequest)
	if err != nil {
		slog.ErrorContext(r.Context(), "browser telemetry export failed",
			"signal", signal,
			"error_type", "upstream_unavailable",
		)
		respondTelemetryError(w, http.StatusBadGateway)
		return
	}
	defer func() {
		_ = response.Body.Close()
	}()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		slog.ErrorContext(r.Context(), "browser telemetry export rejected",
			"signal", signal,
			"status", response.StatusCode,
		)
		respondTelemetryError(w, http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func respondTelemetryError(w http.ResponseWriter, status int) {
	http.Error(w, http.StatusText(status), status)
}

func environmentOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
