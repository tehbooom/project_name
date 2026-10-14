package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
)

var defaultSigningAlgorithms = []string{"RS256", "ES256", "PS256", "EdDSA"}

type OIDCProviderConfig struct {
	Name              string   `json:"name"`
	Issuer            string   `json:"issuer"`
	Audience          string   `json:"audience"`
	JWKSURL           string   `json:"jwks_url,omitempty"`
	RoleClaim         string   `json:"role_claim,omitempty"`
	ModeratorRole     string   `json:"moderator_role,omitempty"`
	ContributorRole   string   `json:"contributor_role,omitempty"`
	SigningAlgorithms []string `json:"signing_algorithms,omitempty"`
}

type oidcTokenVerifier interface {
	Verify(context.Context, string) (*oidc.IDToken, error)
}

type OIDCAuthorizer struct {
	name            string
	verifier        oidcTokenVerifier
	roleClaim       string
	moderatorRole   string
	contributorRole string
}

func NewOIDCAuthorizers(ctx context.Context, rawConfig string) ([]Authorizer, error) {
	if strings.TrimSpace(rawConfig) == "" {
		return nil, fmt.Errorf("%s is required for OIDC authentication", OIDCProvidersEnvironment)
	}

	var configs []OIDCProviderConfig
	decoder := json.NewDecoder(strings.NewReader(rawConfig))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&configs); err != nil {
		return nil, fmt.Errorf("parse %s: %w", OIDCProvidersEnvironment, err)
	}
	if len(configs) == 0 {
		return nil, fmt.Errorf("%s must contain at least one provider", OIDCProvidersEnvironment)
	}

	authorizers := make([]Authorizer, 0, len(configs))
	for _, config := range configs {
		authorizer, err := newOIDCAuthorizer(ctx, config)
		if err != nil {
			return nil, err
		}
		authorizers = append(authorizers, authorizer)
	}
	return authorizers, nil
}

func newOIDCAuthorizer(ctx context.Context, config OIDCProviderConfig) (*OIDCAuthorizer, error) {
	ctx = oidc.ClientContext(ctx, telemetryHTTPClient)
	config.Name = strings.TrimSpace(config.Name)
	config.Issuer = strings.TrimSpace(config.Issuer)
	config.Audience = strings.TrimSpace(config.Audience)
	config.JWKSURL = strings.TrimSpace(config.JWKSURL)
	if config.Name == "" || config.Issuer == "" || config.Audience == "" {
		return nil, errors.New("OIDC provider name, issuer, and audience are required")
	}
	if config.RoleClaim == "" {
		config.RoleClaim = "roles"
	}
	if config.ModeratorRole == "" {
		config.ModeratorRole = "moderator"
	}
	if config.ContributorRole == "" {
		config.ContributorRole = "contributor"
	}
	if len(config.SigningAlgorithms) == 0 {
		config.SigningAlgorithms = append([]string(nil), defaultSigningAlgorithms...)
	}
	if err := validateSigningAlgorithms(config.SigningAlgorithms); err != nil {
		return nil, fmt.Errorf("OIDC provider %q: %w", config.Name, err)
	}

	verifierConfig := &oidc.Config{
		ClientID:             config.Audience,
		SupportedSigningAlgs: config.SigningAlgorithms,
	}
	var verifier oidcTokenVerifier
	if config.JWKSURL != "" {
		keySet := oidc.NewRemoteKeySet(ctx, config.JWKSURL)
		verifier = oidc.NewVerifier(config.Issuer, keySet, verifierConfig)
	} else {
		provider, err := oidc.NewProvider(ctx, config.Issuer)
		if err != nil {
			return nil, fmt.Errorf("discover OIDC provider %q: %w", config.Name, err)
		}
		verifier = provider.Verifier(verifierConfig)
	}

	return &OIDCAuthorizer{
		name:            config.Name,
		verifier:        verifier,
		roleClaim:       config.RoleClaim,
		moderatorRole:   config.ModeratorRole,
		contributorRole: config.ContributorRole,
	}, nil
}

func (a *OIDCAuthorizer) Authorize(r *http.Request, permission string) error {
	rawToken, err := bearerToken(r)
	if err != nil {
		return err
	}
	token, err := a.verifier.Verify(r.Context(), rawToken)
	if err != nil {
		return fmt.Errorf("%w: token was not accepted by %s", ErrUnauthenticated, a.name)
	}

	var claims map[string]any
	if err := token.Claims(&claims); err != nil {
		return fmt.Errorf("%w: invalid token claims", ErrUnauthenticated)
	}
	moderatorAllowed := claimHasRole(claims, a.roleClaim, a.moderatorRole) &&
		roleCan("moderator", permission)
	contributorAllowed := claimHasRole(claims, a.roleClaim, a.contributorRole) &&
		roleCan("contributor", permission)
	if !moderatorAllowed && !contributorAllowed {
		return ErrForbidden
	}
	return nil
}

func (a *OIDCAuthorizer) telemetryName() string {
	return "oidc:" + a.name
}

func claimHasRole(claims map[string]any, claimPath, requiredRole string) bool {
	var value any = claims
	for _, part := range strings.Split(claimPath, ".") {
		object, ok := value.(map[string]any)
		if !ok {
			return false
		}
		value, ok = object[part]
		if !ok {
			return false
		}
	}

	switch roles := value.(type) {
	case bool:
		return roles && requiredRole == "moderator"
	case string:
		for _, role := range strings.Fields(roles) {
			if role == requiredRole {
				return true
			}
		}
	case []any:
		for _, role := range roles {
			if role == requiredRole {
				return true
			}
		}
	case []string:
		for _, role := range roles {
			if role == requiredRole {
				return true
			}
		}
	}
	return false
}

func validateSigningAlgorithms(algorithms []string) error {
	allowed := map[string]bool{
		"RS256": true, "RS384": true, "RS512": true,
		"PS256": true, "PS384": true, "PS512": true,
		"ES256": true, "ES384": true, "ES512": true,
		"EdDSA": true,
	}
	for _, algorithm := range algorithms {
		if !allowed[algorithm] {
			return fmt.Errorf("signing algorithm %q is not allowed", algorithm)
		}
	}
	return nil
}

var _ Authorizer = (*OIDCAuthorizer)(nil)
