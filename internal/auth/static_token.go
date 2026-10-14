package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
)

const StaticTokenHeader = "X-Admin-Token"

type StaticTokenAuthorizer struct {
	token []byte
}

func NewRandomStaticTokenAuthorizer(path string) (*StaticTokenAuthorizer, error) {
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return nil, fmt.Errorf("generate static admin token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(random)

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open static admin token file: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		return nil, closeTokenFile(file, "secure static admin token file", err)
	}
	if _, err := file.WriteString(token + "\n"); err != nil {
		return nil, closeTokenFile(file, "write static admin token file", err)
	}
	if err := file.Sync(); err != nil {
		return nil, closeTokenFile(file, "sync static admin token file", err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("close static admin token file: %w", err)
	}

	return &StaticTokenAuthorizer{token: []byte(token)}, nil
}

func (a *StaticTokenAuthorizer) Authorize(r *http.Request, permission string) error {
	provided := []byte(strings.TrimSpace(r.Header.Get(StaticTokenHeader)))
	if len(provided) == 0 ||
		len(provided) != len(a.token) ||
		subtle.ConstantTimeCompare(provided, a.token) != 1 {
		return ErrUnauthenticated
	}
	if !roleCan("moderator", permission) {
		return ErrForbidden
	}
	return nil
}

func (*StaticTokenAuthorizer) telemetryName() string {
	return StaticTokenDriver
}

func closeTokenFile(file *os.File, operation string, operationErr error) error {
	return errors.Join(
		fmt.Errorf("%s: %w", operation, operationErr),
		file.Close(),
	)
}

var _ Authorizer = (*StaticTokenAuthorizer)(nil)
