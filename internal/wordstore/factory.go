package wordstore

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/tehbooom/project_name/internal/model"
)

const (
	DriverEnvironment      = "WORD_STORE_DRIVER"
	FilePathEnv            = "WORD_STORE_FILE"
	PostgresDSNEnv         = "WORD_STORE_POSTGRES_DSN"
	AutoMigrateEnv         = "WORD_STORE_AUTO_MIGRATE"
	SeedEnvironment        = "WORD_STORE_SEED"
	FirestoreProjectEnv    = "WORD_STORE_FIRESTORE_PROJECT_ID"
	FirestoreCollectionEnv = "WORD_STORE_FIRESTORE_COLLECTION"
)

func NewFromEnvironment(ctx context.Context, seedDirectory string) (Store, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	driver := strings.TrimSpace(os.Getenv(DriverEnvironment))
	switch driver {
	case "", "memory":
		words, err := model.LoadWords(seedDirectory)
		if err != nil {
			return nil, err
		}
		return instrumentStore(NewMemory(words), "memory")
	case "file":
		path := strings.TrimSpace(os.Getenv(FilePathEnv))
		if path == "" {
			return nil, fmt.Errorf("%s is required for file word storage", FilePathEnv)
		}
		store, err := NewFile(path, seedDirectory)
		if err != nil {
			return nil, err
		}
		instrumented, err := instrumentStore(store, "file")
		if err != nil {
			_ = store.Close(ctx)
			return nil, err
		}
		return instrumented, nil
	case "postgres":
		dsn := strings.TrimSpace(os.Getenv(PostgresDSNEnv))
		if dsn == "" {
			return nil, fmt.Errorf("%s is required for PostgreSQL word storage", PostgresDSNEnv)
		}
		autoMigrate, err := environmentBool(AutoMigrateEnv)
		if err != nil {
			return nil, err
		}
		store, err := NewPostgres(ctx, dsn, autoMigrate)
		if err != nil {
			return nil, err
		}
		if err := seedIfConfigured(ctx, seedDirectory, store.Seed); err != nil {
			_ = store.Close(ctx)
			return nil, err
		}
		instrumented, err := instrumentStore(store, "postgres")
		if err != nil {
			_ = store.Close(ctx)
			return nil, err
		}
		return instrumented, nil
	case "firestore":
		projectID := firstConfigured(
			os.Getenv(FirestoreProjectEnv),
			os.Getenv("FIREBASE_PROJECT_ID"),
			os.Getenv("GOOGLE_CLOUD_PROJECT"),
		)
		if projectID == "" {
			return nil, fmt.Errorf("%s is required for Firestore word storage", FirestoreProjectEnv)
		}
		collection := strings.TrimSpace(os.Getenv(FirestoreCollectionEnv))
		if collection == "" {
			collection = "words"
		}
		store, err := NewFirestore(ctx, projectID, collection)
		if err != nil {
			return nil, err
		}
		if err := seedIfConfigured(ctx, seedDirectory, store.Seed); err != nil {
			_ = store.Close(ctx)
			return nil, err
		}
		instrumented, err := instrumentStore(store, "firestore")
		if err != nil {
			_ = store.Close(ctx)
			return nil, err
		}
		return instrumented, nil
	default:
		return nil, fmt.Errorf("unsupported word store driver %q", driver)
	}
}

func seedIfConfigured(
	ctx context.Context,
	seedDirectory string,
	seed func(context.Context, []model.Word) error,
) error {
	enabled, err := environmentBool(SeedEnvironment)
	if err != nil {
		return err
	}
	if !enabled {
		return nil
	}
	words, err := model.LoadWords(seedDirectory)
	if err != nil {
		return err
	}
	return seed(ctx, words.Snapshot())
}

func environmentBool(name string) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return false, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean: %w", name, err)
	}
	return value, nil
}

func firstConfigured(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
