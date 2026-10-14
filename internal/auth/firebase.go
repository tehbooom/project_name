package auth

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	firebase "firebase.google.com/go/v4"
	firebaseauth "firebase.google.com/go/v4/auth"
	"google.golang.org/api/option"
)

const (
	FirebaseDriver       = "google-identity-platform"
	FirebaseProjectIDEnv = "FIREBASE_PROJECT_ID"
)

type idTokenVerifier interface {
	VerifyIDToken(context.Context, string) (*firebaseauth.Token, error)
}

type FirebaseAuthorizer struct {
	verifier idTokenVerifier
}

func NewFirebaseAuthorizer(ctx context.Context, projectID string) (*FirebaseAuthorizer, error) {
	app, err := firebase.NewApp(
		ctx,
		&firebase.Config{ProjectID: projectID},
		option.WithHTTPClient(telemetryHTTPClient),
	)
	if err != nil {
		return nil, fmt.Errorf("initialize Google Identity Platform: %w", err)
	}
	client, err := app.Auth(ctx)
	if err != nil {
		return nil, fmt.Errorf("initialize Google Identity Platform auth client: %w", err)
	}
	return &FirebaseAuthorizer{verifier: client}, nil
}

func (a *FirebaseAuthorizer) Authorize(r *http.Request, permission string) error {
	rawToken, err := bearerToken(r)
	if err != nil {
		return err
	}

	token, err := a.verifier.VerifyIDToken(r.Context(), rawToken)
	if err != nil {
		return fmt.Errorf("%w: invalid bearer token", ErrUnauthenticated)
	}
	if !firebaseClaimsAllow(token.Claims, permission) {
		return ErrForbidden
	}
	return nil
}

func (*FirebaseAuthorizer) telemetryName() string {
	return FirebaseDriver
}

func bearerToken(r *http.Request) (string, error) {
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return "", fmt.Errorf("%w: Bearer token required", ErrUnauthenticated)
	}
	return parts[1], nil
}

func firebaseClaimsAllow(claims map[string]any, permission string) bool {
	if moderator, ok := claims["moderator"].(bool); ok &&
		moderator && roleCan("moderator", permission) {
		return true
	}
	if contributor, ok := claims["contributor"].(bool); ok &&
		contributor && roleCan("contributor", permission) {
		return true
	}

	switch roles := claims["roles"].(type) {
	case []any:
		for _, role := range roles {
			if roleName, ok := role.(string); ok && roleCan(roleName, permission) {
				return true
			}
		}
	case []string:
		for _, role := range roles {
			if roleCan(role, permission) {
				return true
			}
		}
	case string:
		for _, role := range strings.Fields(roles) {
			if roleCan(role, permission) {
				return true
			}
		}
	}
	return false
}

var _ Authorizer = (*FirebaseAuthorizer)(nil)
