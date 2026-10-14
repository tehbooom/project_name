package auth

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRandomStaticTokenAuthorizer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "admin-token")
	authorizer, err := NewRandomStaticTokenAuthorizer(path)
	if err != nil {
		t.Fatalf("NewRandomStaticTokenAuthorizer(): %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if permissions := info.Mode().Perm(); permissions != 0o600 {
		t.Fatalf("token file permissions = %o, want 600", permissions)
	}
	token, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	token = []byte(strings.TrimSpace(string(token)))
	if len(token) < 40 {
		t.Fatalf("generated token has only %d encoded bytes", len(token))
	}

	request := httptest.NewRequest(http.MethodGet, "/v1/words", nil)
	request.Header.Set(StaticTokenHeader, string(token))
	if err := authorizer.Authorize(request, PermissionListWords); err != nil {
		t.Fatalf("Authorize(): %v", err)
	}

	request.Header.Set(StaticTokenHeader, "wrong")
	if err := authorizer.Authorize(request, PermissionListWords); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("Authorize() error = %v, want ErrUnauthenticated", err)
	}
}

func TestStaticTokenRotatesAtStartup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "admin-token")
	if _, err := NewRandomStaticTokenAuthorizer(path); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewRandomStaticTokenAuthorizer(path); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) == string(second) {
		t.Fatal("token did not rotate")
	}
}
