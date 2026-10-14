package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

const (
	instrumentationName      = "github.com/tehbooom/project_name/internal/auth"
	DriversEnvironment       = "AUTH_DRIVERS"
	legacyDriverEnvironment  = "AUTH_DRIVER"
	OIDCProvidersEnvironment = "OIDC_PROVIDERS_JSON"
	StaticTokenDriver        = "static-token"
	StaticTokenFileEnv       = "STATIC_ADMIN_TOKEN_FILE"
)

type Chain []Authorizer

func (chain Chain) Authorize(request *http.Request, permission string) error {
	for _, authorizer := range chain {
		driver := authorizerTelemetryName(authorizer)
		ctx, span := otel.Tracer(instrumentationName).Start(request.Context(), "auth.verify")
		span.SetAttributes(
			attribute.String("auth.driver", driver),
			attribute.String("auth.permission", permission),
		)
		err := authorizer.Authorize(request.WithContext(ctx), permission)
		result := authorizationResult(err)
		span.SetAttributes(attribute.String("auth.result", result))
		if err != nil && result == "error" {
			span.RecordError(err)
			span.SetStatus(codes.Error, result)
		}
		span.End()
		if err == nil {
			return nil
		}
		if errors.Is(err, ErrForbidden) {
			return ErrForbidden
		}
		if !errors.Is(err, ErrUnauthenticated) {
			return err
		}
	}
	return ErrUnauthenticated
}

type telemetryNamer interface {
	telemetryName() string
}

func authorizerTelemetryName(authorizer Authorizer) string {
	if named, ok := authorizer.(telemetryNamer); ok {
		return named.telemetryName()
	}
	return "custom"
}

func authorizationResult(err error) string {
	switch {
	case err == nil:
		return "success"
	case errors.Is(err, ErrForbidden):
		return "forbidden"
	case errors.Is(err, ErrUnauthenticated):
		return "unauthenticated"
	default:
		return "error"
	}
}

// NewFromEnvironment constructs all explicitly configured authentication
// drivers. With no configured drivers it returns DenyAll.
func NewFromEnvironment(ctx context.Context) (Authorizer, error) {
	rawDrivers := strings.TrimSpace(os.Getenv(DriversEnvironment))
	if rawDrivers == "" {
		rawDrivers = strings.TrimSpace(os.Getenv(legacyDriverEnvironment))
	}
	if rawDrivers == "" || rawDrivers == "none" {
		return DenyAll{}, nil
	}

	var chain Chain
	seen := make(map[string]bool)
	for _, rawDriver := range strings.Split(rawDrivers, ",") {
		driver := strings.TrimSpace(rawDriver)
		if driver == "" || seen[driver] {
			continue
		}
		seen[driver] = true

		switch driver {
		case FirebaseDriver, "firebase":
			projectID := firstNonEmpty(
				os.Getenv(FirebaseProjectIDEnv),
				os.Getenv("GOOGLE_CLOUD_PROJECT"),
			)
			if projectID == "" {
				return nil, fmt.Errorf("%s is required for %s", FirebaseProjectIDEnv, driver)
			}
			authorizer, err := NewFirebaseAuthorizer(ctx, projectID)
			if err != nil {
				return nil, err
			}
			chain = append(chain, authorizer)
		case "oidc", "jwks":
			authorizers, err := NewOIDCAuthorizers(
				ctx,
				os.Getenv(OIDCProvidersEnvironment),
			)
			if err != nil {
				return nil, err
			}
			chain = append(chain, authorizers...)
		case StaticTokenDriver:
			path := strings.TrimSpace(os.Getenv(StaticTokenFileEnv))
			if path == "" {
				return nil, fmt.Errorf("%s is required for %s", StaticTokenFileEnv, driver)
			}
			authorizer, err := NewRandomStaticTokenAuthorizer(path)
			if err != nil {
				return nil, err
			}
			chain = append(chain, authorizer)
		default:
			return nil, fmt.Errorf("unsupported authentication driver %q", driver)
		}
	}
	if len(chain) == 0 {
		return nil, errors.New("no authentication drivers were configured")
	}
	return chain, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

var _ Authorizer = Chain{}
