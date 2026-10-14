package auth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/golang-jwt/jwt/v4"
)

func TestOIDCAuthorizer(t *testing.T) {
	const (
		issuer   = "https://issuer.example"
		audience = "project-name"
	)
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	verifier := oidc.NewVerifier(
		issuer,
		&oidc.StaticKeySet{PublicKeys: []crypto.PublicKey{&privateKey.PublicKey}},
		&oidc.Config{
			ClientID:             audience,
			SupportedSigningAlgs: []string{"RS256"},
		},
	)
	authorizer := &OIDCAuthorizer{
		name:          "test",
		verifier:      verifier,
		roleClaim:     "realm_access.roles",
		moderatorRole: "moderator",
	}

	token := signToken(t, privateKey, jwt.MapClaims{
		"iss": issuer,
		"aud": audience,
		"sub": "moderator-1",
		"iat": time.Now().Unix(),
		"exp": time.Now().Add(time.Hour).Unix(),
		"realm_access": map[string]any{
			"roles": []string{"user", "moderator"},
		},
	})
	request := httptest.NewRequest(http.MethodGet, "/v1/words", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	if err := authorizer.Authorize(request, PermissionListWords); err != nil {
		t.Fatalf("Authorize(): %v", err)
	}

	token = signToken(t, privateKey, jwt.MapClaims{
		"iss": issuer,
		"aud": audience,
		"sub": "user-1",
		"iat": time.Now().Unix(),
		"exp": time.Now().Add(time.Hour).Unix(),
		"realm_access": map[string]any{
			"roles": []string{"user"},
		},
	})
	request.Header.Set("Authorization", "Bearer "+token)
	if err := authorizer.Authorize(request, PermissionListWords); !errors.Is(err, ErrForbidden) {
		t.Fatalf("Authorize() error = %v, want ErrForbidden", err)
	}
}

func TestOIDCRejectsUnsafeAlgorithm(t *testing.T) {
	_, err := newOIDCAuthorizer(context.Background(), OIDCProviderConfig{
		Name:              "unsafe",
		Issuer:            "https://issuer.example",
		Audience:          "project-name",
		JWKSURL:           "https://issuer.example/keys",
		SigningAlgorithms: []string{"HS256"},
	})
	if err == nil {
		t.Fatal("newOIDCAuthorizer() accepted HS256")
	}
}

func TestClaimHasRole(t *testing.T) {
	claims := map[string]any{
		"realm_access": map[string]any{
			"roles": []any{"user", "moderator"},
		},
	}
	if !claimHasRole(claims, "realm_access.roles", "moderator") {
		t.Fatal("claimHasRole() = false, want true")
	}
	if claimHasRole(claims, "realm_access.roles", "admin") {
		t.Fatal("claimHasRole() = true for absent role")
	}
}

func signToken(t *testing.T, privateKey *rsa.PrivateKey, claims jwt.MapClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := token.SignedString(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}
