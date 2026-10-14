package wordstore

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/tehbooom/project_name/internal/model"
)

func TestBackendCursor(t *testing.T) {
	token, err := encodeCursor(backendCursor{
		Driver:   "postgres",
		Category: model.CategoryNouns,
		Status:   model.StatusProposed,
		Value:    "gopher",
		ID:       "word-id",
	})
	if err != nil {
		t.Fatal(err)
	}

	cursor, err := decodeCursor(
		token,
		"postgres",
		model.CategoryNouns,
		model.StatusProposed,
	)
	if err != nil {
		t.Fatal(err)
	}
	if cursor.Value != "gopher" || cursor.ID != "word-id" {
		t.Fatalf("cursor = %+v", cursor)
	}

	if _, err := decodeCursor(
		token,
		"firestore",
		model.CategoryNouns,
		model.StatusProposed,
	); !errors.Is(err, model.ErrInvalidPageToken) {
		t.Fatalf("cross-driver cursor error = %v, want ErrInvalidPageToken", err)
	}
}

func TestRandomFractionRange(t *testing.T) {
	for iteration := 0; iteration < 100; iteration++ {
		value, err := randomFraction()
		if err != nil {
			t.Fatal(err)
		}
		if value < 0 || value > 1 {
			t.Fatalf("randomFraction() = %f, want [0, 1]", value)
		}
	}
}

func TestUniqueViolation(t *testing.T) {
	if !isUniqueViolation(&pgconn.PgError{Code: "23505"}) {
		t.Fatal("isUniqueViolation() = false for PostgreSQL unique violation")
	}
	if isUniqueViolation(errors.New("other")) {
		t.Fatal("isUniqueViolation() = true for unrelated error")
	}
}

func TestDistributedDriverConfigurationValidation(t *testing.T) {
	t.Run("postgres DSN required", func(t *testing.T) {
		t.Setenv(DriverEnvironment, "postgres")
		t.Setenv(PostgresDSNEnv, "")
		if _, err := NewFromEnvironment(context.Background(), t.TempDir()); err == nil {
			t.Fatal("NewFromEnvironment() accepted empty PostgreSQL DSN")
		}
	})

	t.Run("firestore project required", func(t *testing.T) {
		t.Setenv(DriverEnvironment, "firestore")
		t.Setenv(FirestoreProjectEnv, "")
		t.Setenv("FIREBASE_PROJECT_ID", "")
		t.Setenv("GOOGLE_CLOUD_PROJECT", "")
		if _, err := NewFromEnvironment(context.Background(), t.TempDir()); err == nil {
			t.Fatal("NewFromEnvironment() accepted empty Firestore project")
		}
	})

	t.Run("migration flag is boolean", func(t *testing.T) {
		t.Setenv(DriverEnvironment, "postgres")
		t.Setenv(PostgresDSNEnv, "postgres://unused")
		t.Setenv(AutoMigrateEnv, "sometimes")
		if _, err := NewFromEnvironment(context.Background(), t.TempDir()); err == nil {
			t.Fatal("NewFromEnvironment() accepted invalid migration flag")
		}
	})
}
