package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/felixge/httpsnoop"
	"github.com/gorilla/mux"
	"github.com/tehbooom/project_name/internal/auth"
	"github.com/tehbooom/project_name/internal/model"
	webui "github.com/tehbooom/project_name/internal/web"
	"github.com/tehbooom/project_name/internal/wordstore"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gorilla/mux/otelmux"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

const (
	instrumentationName   = "github.com/tehbooom/project_name/internal/app"
	maxRequestBodyBytes   = 4 << 10
	shutdownTimeout       = 10 * time.Second
	readinessTimeout      = 2 * time.Second
	slowRequestThreshold  = time.Second
	UnsafeModeEnvironment = "UNSAFE_MODE_ENABLED"
)

type App struct {
	Router             *mux.Router
	Words              wordstore.Store
	namesGenerated     metric.Int64Counter
	nameOperationCount metric.Int64Counter
	wordOperationCount metric.Int64Counter
	authAttemptCount   metric.Int64Counter
	healthCheckCount   metric.Int64Counter
	healthDuration     metric.Float64Histogram
	authorizer         auth.Authorizer
	readinessMu        sync.Mutex
	readinessKnown     bool
	readinessReady     bool
	browserTelemetry   webui.Telemetry
	unsafeModeEnabled  bool
}

type Option func(*App)

func WithAuthorizer(authorizer auth.Authorizer) Option {
	return func(app *App) {
		app.authorizer = authorizer
	}
}

type Name struct {
	Name string `json:"name"`
}

type CreateWordRequest struct {
	Category string `json:"category"`
	Value    string `json:"value"`
}

type SetWordSafetyRequest struct {
	Safe *bool `json:"safe"`
}

type PublicConfig struct {
	UnsafeModeEnabled bool `json:"unsafe_mode_enabled"`
}

type ErrorResponse struct {
	Error APIError `json:"error"`
}

type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type HealthResponse struct {
	Status string `json:"status"`
}

func (a *App) Initialize(words wordstore.Store, options ...Option) error {
	a.authorizer = auth.DenyAll{}
	for _, option := range options {
		option(a)
	}
	if a.authorizer == nil {
		return errors.New("authorizer must not be nil")
	}
	if words == nil {
		return errors.New("word store must not be nil")
	}
	a.Words = words
	browserTelemetry, err := webui.NewTelemetryFromEnvironment()
	if err != nil {
		return fmt.Errorf("configure browser telemetry: %w", err)
	}
	a.browserTelemetry = browserTelemetry
	if raw := strings.TrimSpace(os.Getenv(UnsafeModeEnvironment)); raw != "" {
		a.unsafeModeEnabled, err = strconv.ParseBool(raw)
		if err != nil {
			return fmt.Errorf("%s must be a boolean", UnsafeModeEnvironment)
		}
	}

	meter := otel.Meter(instrumentationName)
	a.namesGenerated, err = meter.Int64Counter(
		"project.names.generated",
		metric.WithDescription("Number of project names generated"),
	)
	if err != nil {
		return err
	}
	a.nameOperationCount, err = meter.Int64Counter(
		"project.name_generation.operations",
		metric.WithDescription("Number of attempted project name generations"),
	)
	if err != nil {
		return err
	}
	a.wordOperationCount, err = meter.Int64Counter(
		"project.word.operations",
		metric.WithDescription("Number of attempted word operations"),
	)
	if err != nil {
		return err
	}
	a.authAttemptCount, err = meter.Int64Counter(
		"project.auth.attempts",
		metric.WithDescription("Number of authorization attempts"),
	)
	if err != nil {
		return err
	}
	a.healthCheckCount, err = meter.Int64Counter(
		"project.health.checks",
		metric.WithDescription("Number of health checks"),
	)
	if err != nil {
		return err
	}
	a.healthDuration, err = meter.Float64Histogram(
		"project.health.check.duration",
		metric.WithDescription("Duration of health checks"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return err
	}
	a.Router = mux.NewRouter()
	a.Router.Use(otelmux.Middleware(
		"project-name",
		otelmux.WithFilter(func(request *http.Request) bool {
			return request.URL.Path != "/health/live" &&
				request.URL.Path != "/health/ready" &&
				!strings.HasPrefix(request.URL.Path, "/otel/")
		}),
	))
	a.Router.Use(a.observeRequests)
	a.Router.Use(a.recoverPanics)

	a.initializeRoutes()

	return nil
}

func (a *App) Run(ctx context.Context, addr string) (err error) {
	srv := &http.Server{
		Addr:              addr,
		BaseContext:       func(net.Listener) context.Context { return ctx },
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
		Handler:           a.Router,
	}
	srvErr := make(chan error, 1)
	go func() {
		slog.InfoContext(ctx, "running HTTP server", "address", addr)
		srvErr <- srv.ListenAndServe()
	}()

	select {
	case err = <-srvErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	slog.InfoContext(shutdownCtx, "shutting down HTTP server")
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.ErrorContext(shutdownCtx, "failed to shut down HTTP server", "error", err)
		return fmt.Errorf("shut down HTTP server: %w", err)
	}
	slog.InfoContext(shutdownCtx, "HTTP server stopped")
	return nil
}

func (a *App) initializeRoutes() {
	a.Router.HandleFunc("/health/live", a.live).Methods("GET")
	a.Router.HandleFunc("/health/ready", a.ready).Methods("GET")
	a.Router.HandleFunc("/project", a.getName).Methods("GET")
	a.Router.HandleFunc("/v1/names/random", a.getName).Methods("GET")
	a.Router.HandleFunc("/v1/config", a.publicConfig).Methods("GET")
	a.Router.HandleFunc("/v1/words", a.createWordProposal).Methods("POST")
	a.Router.HandleFunc("/v1/words", a.listWords).Methods("GET")
	a.Router.HandleFunc("/v1/words/{id}/approve", a.approveWord).Methods("POST")
	a.Router.HandleFunc("/v1/words/{id}/safety", a.setWordSafety).Methods("PATCH")
	a.Router.HandleFunc("/v1/words/{id}", a.deleteWord).Methods("DELETE")
	a.Router.Handle("/otel/config.js", a.browserTelemetry.ConfigHandler()).
		Methods(http.MethodGet, http.MethodHead)
	for _, signal := range []string{"traces", "metrics", "logs"} {
		a.Router.Handle(
			"/otel/v1/"+signal,
			a.browserTelemetry.ExportHandler(signal),
		).Methods(http.MethodPost)
	}
}

func (a *App) observeRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured := httpsnoop.CaptureMetricsFn(w, func(w http.ResponseWriter) {
			next.ServeHTTP(w, r)
		})
		if captured.Code < http.StatusInternalServerError &&
			captured.Duration < slowRequestThreshold {
			return
		}

		attributes := requestLogAttributes(r, captured.Code, captured.Duration)
		if captured.Code >= http.StatusInternalServerError {
			slog.ErrorContext(r.Context(), "HTTP request failed", attributes...)
			return
		}
		slog.WarnContext(r.Context(), "slow HTTP request", attributes...)
	})
}

func (a *App) recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func(ctx context.Context) {
			recovered := recover()
			if recovered == nil {
				return
			}
			recordSpanError(trace.SpanFromContext(ctx), errors.New("panic"))
			slog.ErrorContext(
				ctx,
				"panic recovered while serving HTTP request",
				"method", r.Method,
				"route", routeTemplate(r),
				"panic_type", fmt.Sprintf("%T", recovered),
				"stack", string(debug.Stack()),
			)
			respondWithError(
				ctx,
				w,
				http.StatusInternalServerError,
				"internal_error",
				"internal server error",
			)
		}(r.Context())
		next.ServeHTTP(w, r)
	})
}

func requestLogAttributes(r *http.Request, status int, duration time.Duration) []any {
	return []any{
		"method", r.Method,
		"route", routeTemplate(r),
		"status", status,
		"duration_ms", duration.Milliseconds(),
	}
}

func routeTemplate(r *http.Request) string {
	if route := mux.CurrentRoute(r); route != nil {
		if template, err := route.GetPathTemplate(); err == nil {
			return template
		}
	}
	return "unmatched"
}

func (a *App) live(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	w.Header().Set("Cache-Control", "no-store")
	respondWithJSON(r.Context(), w, http.StatusOK, HealthResponse{Status: "live"})
	a.recordHealthCheck(r.Context(), "live", "success", started)
}

func (a *App) ready(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), readinessTimeout)
	defer cancel()

	if err := a.Words.Ready(ctx); err != nil {
		a.recordReadinessState(r.Context(), false, err)
		a.recordHealthCheck(r.Context(), "ready", "failure", started)
		respondWithError(
			r.Context(),
			w,
			http.StatusServiceUnavailable,
			"not_ready",
			"service dependencies are not ready",
		)
		return
	}
	a.recordReadinessState(r.Context(), true, nil)
	respondWithJSON(r.Context(), w, http.StatusOK, HealthResponse{Status: "ready"})
	a.recordHealthCheck(r.Context(), "ready", "success", started)
}

func (a *App) listWords(w http.ResponseWriter, r *http.Request) {
	ctx, span := otel.Tracer(instrumentationName).Start(r.Context(), "words.list")
	defer span.End()

	if !a.authorize(ctx, span, w, r, auth.PermissionListWords) {
		return
	}

	category := r.URL.Query().Get("category")
	status := r.URL.Query().Get("status")
	limit := 50
	if rawLimit := r.URL.Query().Get("limit"); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil {
			a.recordWordOperation(ctx, "list", "invalid_limit")
			respondWithError(ctx, w, http.StatusBadRequest, "invalid_limit", "limit must be an integer")
			return
		}
		limit = parsed
	}

	page, err := a.Words.ListWords(ctx, category, status, limit, r.URL.Query().Get("page_token"))
	if err != nil {
		_, code := modelErrorResponse(err)
		a.recordWordOperation(ctx, "list", code)
		recordSpanError(span, err)
		status, code := modelErrorResponse(err)
		respondWithError(ctx, w, status, code, err.Error())
		return
	}

	a.recordWordOperation(ctx, "list", "success")
	span.SetAttributes(
		attribute.String("word.category", category),
		attribute.String("word.status", status),
		attribute.Int("page.size", len(page.Words)),
	)
	respondWithJSON(ctx, w, http.StatusOK, page)
}

func (a *App) createWordProposal(w http.ResponseWriter, r *http.Request) {
	ctx, span := otel.Tracer(instrumentationName).Start(r.Context(), "words.propose")
	defer span.End()

	if !a.authorize(ctx, span, w, r, auth.PermissionCreateWord) {
		return
	}

	var requestBody CreateWordRequest
	if status, err := decodeJSONRequest(w, r, &requestBody); err != nil {
		a.recordWordOperation(ctx, "propose", requestErrorCode(status))
		recordSpanError(span, err)
		respondWithError(ctx, w, status, requestErrorCode(status), err.Error())
		return
	}
	span.SetAttributes(attribute.String("word.category", requestBody.Category))

	word, err := a.Words.CreateProposal(ctx, requestBody.Category, requestBody.Value)
	if err != nil {
		a.recordWordOperation(ctx, "propose", "error")
		recordSpanError(span, err)
		status, code := modelErrorResponse(err)
		respondWithError(ctx, w, status, code, err.Error())
		return
	}

	a.recordWordOperation(ctx, "propose", "created")
	slog.InfoContext(ctx, "created word proposal",
		"word_id", word.ID,
		"category", word.Category,
	)
	respondWithJSON(ctx, w, http.StatusCreated, word)
}

func (a *App) getName(w http.ResponseWriter, r *http.Request) {
	ctx, span := otel.Tracer(instrumentationName).Start(r.Context(), "project_name.generate")
	defer span.End()

	constraints, err := nameConstraints(r)
	if err != nil {
		a.recordNameOperation(ctx, "invalid_length")
		recordSpanError(span, err)
		respondWithError(ctx, w, http.StatusBadRequest, "invalid_length", err.Error())
		return
	}
	if rawSafe := r.URL.Query().Get("safe"); rawSafe != "" {
		safe, parseErr := strconv.ParseBool(rawSafe)
		if parseErr != nil {
			a.recordNameOperation(ctx, "invalid_safety")
			respondWithError(ctx, w, http.StatusBadRequest, "invalid_safety", "safe must be a boolean")
			return
		}
		constraints.Unsafe = !safe
	}
	if constraints.Unsafe && !a.unsafeModeEnabled {
		a.recordNameOperation(ctx, "unsafe_mode_disabled")
		respondWithError(
			ctx,
			w,
			http.StatusForbidden,
			"unsafe_mode_disabled",
			"unsafe name generation is disabled",
		)
		return
	}
	span.SetAttributes(
		attribute.Int("name.max_noun_length", constraints.MaxNounLength),
		attribute.Int("name.max_adjective_length", constraints.MaxAdjectiveLength),
		attribute.Bool("name.safe", !constraints.Unsafe),
	)

	var projectName Name
	projectName.Name, err = a.Words.GetWords(ctx, constraints)
	if err != nil {
		recordSpanError(span, err)
		a.recordNameOperation(ctx, errorType(err))
		status, code := http.StatusServiceUnavailable, "names_unavailable"
		if errors.Is(err, model.ErrNoMatchingWords) {
			status, code = http.StatusNotFound, "no_matching_words"
		}
		respondWithError(ctx, w, status, code, err.Error())
		return
	}
	a.recordNameOperation(ctx, "success")
	a.namesGenerated.Add(ctx, 1)
	respondWithJSON(ctx, w, http.StatusOK, projectName)
}

func (a *App) publicConfig(w http.ResponseWriter, r *http.Request) {
	respondWithJSON(r.Context(), w, http.StatusOK, PublicConfig{
		UnsafeModeEnabled: a.unsafeModeEnabled,
	})
}

func nameConstraints(r *http.Request) (model.NameConstraints, error) {
	query := r.URL.Query()
	both, err := optionalNameLength(query.Get("max_length"), "max_length")
	if err != nil {
		return model.NameConstraints{}, err
	}
	constraints := model.NameConstraints{
		MaxNounLength:      both,
		MaxAdjectiveLength: both,
	}
	if raw := query.Get("max_noun_length"); raw != "" {
		constraints.MaxNounLength, err = optionalNameLength(raw, "max_noun_length")
		if err != nil {
			return model.NameConstraints{}, err
		}
	}
	if raw := query.Get("max_adjective_length"); raw != "" {
		constraints.MaxAdjectiveLength, err = optionalNameLength(raw, "max_adjective_length")
		if err != nil {
			return model.NameConstraints{}, err
		}
	}
	return constraints, nil
}

func optionalNameLength(raw, parameter string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	length, err := strconv.Atoi(raw)
	if err != nil || length < 1 || length > model.MaxWordLength {
		return 0, fmt.Errorf(
			"%s must be an integer between 1 and %d",
			parameter,
			model.MaxWordLength,
		)
	}
	return length, nil
}

func (a *App) approveWord(w http.ResponseWriter, r *http.Request) {
	ctx, span := otel.Tracer(instrumentationName).Start(r.Context(), "words.approve")
	defer span.End()

	if !a.authorize(ctx, span, w, r, auth.PermissionApproveWord) {
		return
	}

	id := mux.Vars(r)["id"]
	word, err := a.Words.ApproveWord(ctx, id)
	if err != nil {
		a.recordWordOperation(ctx, "approve", "error")
		recordSpanError(span, err)
		status, code := modelErrorResponse(err)
		respondWithError(ctx, w, status, code, err.Error())
		return
	}

	a.recordWordOperation(ctx, "approve", "approved")
	slog.InfoContext(ctx, "approved word proposal",
		"word_id", word.ID,
		"category", word.Category,
	)
	respondWithJSON(ctx, w, http.StatusOK, word)
}

func (a *App) setWordSafety(w http.ResponseWriter, r *http.Request) {
	ctx, span := otel.Tracer(instrumentationName).Start(r.Context(), "words.set_safety")
	defer span.End()

	if !a.authorize(ctx, span, w, r, auth.PermissionSetSafety) {
		return
	}

	var requestBody SetWordSafetyRequest
	if status, err := decodeJSONRequest(w, r, &requestBody); err != nil {
		a.recordWordOperation(ctx, "set_safety", requestErrorCode(status))
		recordSpanError(span, err)
		respondWithError(ctx, w, status, requestErrorCode(status), err.Error())
		return
	}
	if requestBody.Safe == nil {
		err := errors.New("safe is required")
		a.recordWordOperation(ctx, "set_safety", "invalid_request")
		recordSpanError(span, err)
		respondWithError(ctx, w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	word, err := a.Words.SetWordSafety(ctx, mux.Vars(r)["id"], *requestBody.Safe)
	if err != nil {
		a.recordWordOperation(ctx, "set_safety", "error")
		recordSpanError(span, err)
		status, code := modelErrorResponse(err)
		respondWithError(ctx, w, status, code, err.Error())
		return
	}
	span.SetAttributes(attribute.Bool("word.safe", word.Safe))
	a.recordWordOperation(ctx, "set_safety", "updated")
	slog.InfoContext(ctx, "updated word safety",
		"word_id", word.ID,
		"safe", word.Safe,
	)
	respondWithJSON(ctx, w, http.StatusOK, word)
}

func (a *App) deleteWord(w http.ResponseWriter, r *http.Request) {
	ctx, span := otel.Tracer(instrumentationName).Start(r.Context(), "words.delete")
	defer span.End()

	if !a.authorize(ctx, span, w, r, auth.PermissionDeleteWord) {
		return
	}

	id := mux.Vars(r)["id"]
	removed, err := a.Words.DeleteWord(ctx, id)
	if err != nil {
		a.recordWordOperation(ctx, "delete", "error")
		recordSpanError(span, err)
		status, code := modelErrorResponse(err)
		respondWithError(ctx, w, status, code, err.Error())
		return
	}

	outcome := "not_found"
	if removed {
		outcome = "deleted"
	}
	a.recordWordOperation(ctx, "delete", outcome)
	slog.InfoContext(ctx, "deleted word", "word_id", id, "removed", removed)
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) authorize(
	ctx context.Context,
	span trace.Span,
	w http.ResponseWriter,
	r *http.Request,
	permission string,
) bool {
	authCtx, authSpan := otel.Tracer(instrumentationName).Start(
		ctx,
		"auth.authorize",
		trace.WithAttributes(attribute.String("auth.permission", permission)),
	)
	defer authSpan.End()

	err := a.authorizer.Authorize(r.WithContext(authCtx), permission)
	result := authorizationResult(err)
	authSpan.SetAttributes(attribute.String("auth.result", result))
	a.authAttemptCount.Add(authCtx, 1, metric.WithAttributes(
		attribute.String("auth.permission", permission),
		attribute.String("auth.result", result),
	))
	if err == nil {
		return true
	}

	recordSpanError(authSpan, err)
	recordSpanError(span, err)
	switch {
	case errors.Is(err, auth.ErrUnauthenticated):
		slog.WarnContext(ctx, "authorization denied",
			"permission", permission,
			"result", "unauthenticated",
		)
		w.Header().Set("WWW-Authenticate", "Bearer")
		respondWithError(ctx, w, http.StatusUnauthorized, "unauthenticated", err.Error())
	case errors.Is(err, auth.ErrForbidden):
		slog.WarnContext(ctx, "authorization denied",
			"permission", permission,
			"result", "forbidden",
		)
		respondWithError(ctx, w, http.StatusForbidden, "forbidden", err.Error())
	default:
		slog.ErrorContext(ctx, "authorization failed",
			"permission", permission,
			"error_type", "authorization_error",
		)
		respondWithError(ctx, w, http.StatusInternalServerError, "internal_error", "authorization failed")
	}
	return false
}

func (a *App) recordWordOperation(ctx context.Context, operation, outcome string) {
	a.wordOperationCount.Add(ctx, 1, metric.WithAttributes(
		attribute.String("operation", operation),
		attribute.String("outcome", outcome),
	))
}

func (a *App) recordNameOperation(ctx context.Context, result string) {
	a.nameOperationCount.Add(ctx, 1, metric.WithAttributes(
		attribute.String("result", result),
	))
}

func (a *App) recordHealthCheck(
	ctx context.Context,
	check, result string,
	started time.Time,
) {
	attributes := []attribute.KeyValue{
		attribute.String("health.check", check),
		attribute.String("result", result),
	}
	a.healthCheckCount.Add(ctx, 1, metric.WithAttributes(attributes...))
	a.healthDuration.Record(
		ctx,
		time.Since(started).Seconds(),
		metric.WithAttributes(attributes...),
	)
}

func (a *App) recordReadinessState(ctx context.Context, ready bool, err error) {
	a.readinessMu.Lock()
	changed := !a.readinessKnown || a.readinessReady != ready
	a.readinessKnown = true
	a.readinessReady = ready
	a.readinessMu.Unlock()
	if !changed {
		return
	}

	if ready {
		slog.InfoContext(ctx, "readiness state changed", "ready", true)
		return
	}
	slog.WarnContext(ctx, "readiness state changed",
		"ready", false,
		"error_type", errorType(err),
	)
}

func recordSpanError(span trace.Span, err error) {
	errorType := errorType(err)
	span.RecordError(errors.New(errorType))
	span.SetAttributes(attribute.String("error.type", errorType))
	span.SetStatus(codes.Error, errorType)
}

func authorizationResult(err error) string {
	switch {
	case err == nil:
		return "success"
	case errors.Is(err, auth.ErrUnauthenticated):
		return "unauthenticated"
	case errors.Is(err, auth.ErrForbidden):
		return "forbidden"
	default:
		return "error"
	}
}

func errorType(err error) string {
	if errors.Is(err, auth.ErrUnauthenticated) {
		return "unauthenticated"
	}
	if errors.Is(err, auth.ErrForbidden) {
		return "forbidden"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline_exceeded"
	}
	_, code := modelErrorResponse(err)
	return code
}

func decodeJSONRequest(w http.ResponseWriter, r *http.Request, dst any) (int, error) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return http.StatusUnsupportedMediaType, errors.New("Content-Type must be application/json")
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(dst); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			return http.StatusRequestEntityTooLarge, fmt.Errorf(
				"request body must not exceed %d bytes",
				maxRequestBodyBytes,
			)
		}
		return http.StatusBadRequest, fmt.Errorf("request body must contain one valid JSON object: %w", err)
	}

	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return http.StatusBadRequest, errors.New("request body must contain only one JSON object")
	}
	return 0, nil
}

func requestErrorCode(status int) string {
	if status == http.StatusUnsupportedMediaType {
		return "unsupported_media_type"
	}
	if status == http.StatusRequestEntityTooLarge {
		return "request_too_large"
	}
	return "invalid_request"
}

func modelErrorResponse(err error) (int, string) {
	switch {
	case errors.Is(err, model.ErrDuplicateWord):
		return http.StatusConflict, "duplicate_word"
	case errors.Is(err, model.ErrLastWord):
		return http.StatusConflict, "last_word"
	case errors.Is(err, model.ErrInvalidCategory):
		return http.StatusBadRequest, "invalid_category"
	case errors.Is(err, model.ErrInvalidWord):
		return http.StatusBadRequest, "invalid_word"
	case errors.Is(err, model.ErrInvalidLimit):
		return http.StatusBadRequest, "invalid_limit"
	case errors.Is(err, model.ErrInvalidPageToken):
		return http.StatusBadRequest, "invalid_page_token"
	case errors.Is(err, model.ErrInvalidStatus):
		return http.StatusBadRequest, "invalid_status"
	case errors.Is(err, model.ErrWordNotFound):
		return http.StatusNotFound, "word_not_found"
	case errors.Is(err, model.ErrNoMatchingWords):
		return http.StatusNotFound, "no_matching_words"
	default:
		return http.StatusInternalServerError, "internal_error"
	}
}

func respondWithJSON(ctx context.Context, w http.ResponseWriter, code int, payload interface{}) {
	response, err := json.MarshalIndent(payload, "", "    ")
	if err != nil {
		recordSpanError(trace.SpanFromContext(ctx), err)
		slog.ErrorContext(ctx, "failed to encode response", "error", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, err = w.Write(response)
	if err != nil {
		recordSpanError(trace.SpanFromContext(ctx), err)
		slog.ErrorContext(ctx, "failed to write response", "error", err)
	}
}

func respondWithError(ctx context.Context, w http.ResponseWriter, status int, code, message string) {
	respondWithJSON(ctx, w, status, ErrorResponse{
		Error: APIError{
			Code:    code,
			Message: message,
		},
	})
}
