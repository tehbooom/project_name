package wordstore

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/tehbooom/project_name/internal/model"
)

type backendCursor struct {
	Version  int    `json:"v"`
	Driver   string `json:"driver"`
	Category string `json:"category"`
	Status   string `json:"status,omitempty"`
	Value    string `json:"value"`
	ID       string `json:"id"`
}

func encodeCursor(cursor backendCursor) (string, error) {
	cursor.Version = 1
	data, err := json.Marshal(cursor)
	if err != nil {
		return "", fmt.Errorf("encode page token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeCursor(token, driver, category, status string) (backendCursor, error) {
	if token == "" {
		return backendCursor{
			Version:  1,
			Driver:   driver,
			Category: category,
			Status:   status,
		}, nil
	}

	data, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return backendCursor{}, fmt.Errorf("%w: malformed encoding", model.ErrInvalidPageToken)
	}
	var cursor backendCursor
	if err := json.Unmarshal(data, &cursor); err != nil {
		return backendCursor{}, fmt.Errorf("%w: malformed data", model.ErrInvalidPageToken)
	}
	if cursor.Version != 1 || cursor.Driver != driver ||
		cursor.Category != category || cursor.Status != status ||
		cursor.Value == "" || cursor.ID == "" {
		return backendCursor{}, fmt.Errorf("%w: cursor does not match request", model.ErrInvalidPageToken)
	}
	return cursor, nil
}
