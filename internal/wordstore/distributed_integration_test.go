//go:build integration

package wordstore

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/tehbooom/project_name/internal/model"
)

func TestPostgresIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN is not set")
	}

	ctx := context.Background()
	store, err := NewPostgres(ctx, dsn, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := store.pool.Exec(context.Background(), "TRUNCATE TABLE words"); err != nil {
			t.Errorf("truncate PostgreSQL words: %v", err)
		}
		if err := store.Close(context.Background()); err != nil {
			t.Errorf("close PostgreSQL store: %v", err)
		}
	})
	if err := store.Seed(ctx, integrationSeed(t)); err != nil {
		t.Fatal(err)
	}
	testStoreConformance(t, store)
}

func TestFirestoreIntegration(t *testing.T) {
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("FIRESTORE_EMULATOR_HOST is not set")
	}
	projectID := os.Getenv("TEST_FIRESTORE_PROJECT_ID")
	if projectID == "" {
		t.Skip("TEST_FIRESTORE_PROJECT_ID is not set")
	}

	ctx := context.Background()
	collection := "words_test_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	store, err := NewFirestore(ctx, projectID, collection)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(context.Background()); err != nil {
			t.Errorf("close Firestore store: %v", err)
		}
	})
	if err := store.Seed(ctx, integrationSeed(t)); err != nil {
		t.Fatal(err)
	}
	testStoreConformance(t, store)
}

func integrationSeed(t *testing.T) []model.Word {
	t.Helper()
	words := model.NewWordList()
	for _, category := range []string{model.CategoryNouns, model.CategoryAdjectives} {
		for _, value := range []string{"base-one", "base-two"} {
			if err := words.AddWord(category, value); err != nil {
				t.Fatal(err)
			}
		}
	}
	return words.Snapshot()
}

func testStoreConformance(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()

	proposal, err := store.CreateProposal(ctx, model.CategoryNouns, "capybara")
	if err != nil {
		t.Fatal(err)
	}
	page, err := store.ListWords(ctx, model.CategoryNouns, model.StatusProposed, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Words) != 1 || page.Words[0].ID != proposal.ID {
		t.Fatalf("proposed page = %+v", page.Words)
	}
	approved, err := store.ApproveWord(ctx, proposal.ID)
	if err != nil {
		t.Fatal(err)
	}
	if approved.Status != model.StatusApproved {
		t.Fatalf("approved status = %q", approved.Status)
	}
	if _, err := store.GetWords(ctx, model.NameConstraints{}); err != nil {
		t.Fatal(err)
	}
	if removed, err := store.DeleteWord(ctx, proposal.ID); err != nil || !removed {
		t.Fatalf("DeleteWord() = (%v, %v)", removed, err)
	}
}
