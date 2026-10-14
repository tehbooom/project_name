package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	firebaseauth "firebase.google.com/go/v4/auth"
)

type stubTokenVerifier struct {
	token *firebaseauth.Token
	err   error
}

func (v stubTokenVerifier) VerifyIDToken(context.Context, string) (*firebaseauth.Token, error) {
	return v.token, v.err
}

func TestFirebaseAuthorizer(t *testing.T) {
	tests := []struct {
		name       string
		header     string
		claims     map[string]any
		verifyErr  error
		permission string
		wantErr    error
	}{
		{
			name:    "missing token",
			wantErr: ErrUnauthenticated,
		},
		{
			name:      "invalid token",
			header:    "Bearer invalid",
			verifyErr: errors.New("bad signature"),
			wantErr:   ErrUnauthenticated,
		},
		{
			name:    "missing role",
			header:  "Bearer valid",
			claims:  map[string]any{},
			wantErr: ErrForbidden,
		},
		{
			name:   "moderator boolean",
			header: "Bearer valid",
			claims: map[string]any{"moderator": true},
		},
		{
			name:   "moderator role list",
			header: "Bearer valid",
			claims: map[string]any{"roles": []any{"user", "moderator"}},
		},
		{
			name:       "contributor can create",
			header:     "Bearer valid",
			claims:     map[string]any{"roles": []any{"contributor"}},
			permission: PermissionCreateWord,
		},
		{
			name:       "contributor cannot list",
			header:     "Bearer valid",
			claims:     map[string]any{"roles": []any{"contributor"}},
			permission: PermissionListWords,
			wantErr:    ErrForbidden,
		},
		{
			name:       "unknown permission",
			header:     "Bearer valid",
			claims:     map[string]any{"moderator": true},
			permission: "words:destroy-everything",
			wantErr:    ErrForbidden,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			token := &firebaseauth.Token{Claims: test.claims}
			authorizer := &FirebaseAuthorizer{
				verifier: stubTokenVerifier{token: token, err: test.verifyErr},
			}
			request := httptest.NewRequest(http.MethodGet, "/v1/words", nil)
			if test.header != "" {
				request.Header.Set("Authorization", test.header)
			}
			permission := test.permission
			if permission == "" {
				permission = PermissionListWords
			}

			err := authorizer.Authorize(request, permission)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("Authorize() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestNewFromEnvironmentDefaultsToDenyAll(t *testing.T) {
	t.Setenv(DriversEnvironment, "")
	t.Setenv(legacyDriverEnvironment, "")

	authorizer, err := NewFromEnvironment(context.Background())
	if err != nil {
		t.Fatalf("NewFromEnvironment(): %v", err)
	}
	if _, ok := authorizer.(DenyAll); !ok {
		t.Fatalf("authorizer = %T, want DenyAll", authorizer)
	}
}
