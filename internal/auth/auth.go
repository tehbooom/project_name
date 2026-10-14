package auth

import (
	"errors"
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

var (
	ErrUnauthenticated = errors.New("authentication required")
	ErrForbidden       = errors.New("permission denied")
)

const (
	PermissionCreateWord  = "words:create"
	PermissionListWords   = "words:list"
	PermissionApproveWord = "words:approve"
	PermissionSetSafety   = "words:set-safety"
	PermissionDeleteWord  = "words:delete"
)

func roleCan(role, permission string) bool {
	switch role {
	case "moderator":
		return permission == PermissionCreateWord ||
			permission == PermissionListWords ||
			permission == PermissionApproveWord ||
			permission == PermissionSetSafety ||
			permission == PermissionDeleteWord
	case "contributor":
		return permission == PermissionCreateWord
	default:
		return false
	}
}

// Authorizer is the boundary between the application and an identity provider.
// Implementations may validate a broker session, an OIDC bearer token, or any
// other authentication mechanism before checking the requested permission.
type Authorizer interface {
	Authorize(r *http.Request, permission string) error
}

var telemetryHTTPClient = &http.Client{
	Transport: otelhttp.NewTransport(http.DefaultTransport),
}

// DenyAll keeps protected routes closed when no authentication driver is
// configured.
type DenyAll struct{}

func (DenyAll) Authorize(*http.Request, string) error {
	return ErrUnauthenticated
}

func (DenyAll) telemetryName() string {
	return "none"
}
