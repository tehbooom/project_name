package wordstore

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tehbooom/project_name/internal/model"
)

func TestFileStorePersistsModerationLifecycle(t *testing.T) {
	seedDirectory := writeSeedWords(t)
	statePath := filepath.Join(t.TempDir(), "state", "words.json")
	ctx := context.Background()

	store, err := NewFile(statePath, seedDirectory)
	if err != nil {
		t.Fatalf("NewFile(): %v", err)
	}
	proposal, err := store.CreateProposal(ctx, model.CategoryNouns, "capybara")
	if err != nil {
		t.Fatalf("CreateProposal(): %v", err)
	}

	store, err = NewFile(statePath, seedDirectory)
	if err != nil {
		t.Fatalf("reopen after proposal: %v", err)
	}
	page, err := store.ListWords(ctx, model.CategoryNouns, model.StatusProposed, 50, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Words) != 1 || page.Words[0].ID != proposal.ID {
		t.Fatalf("persisted proposals = %+v, want %s", page.Words, proposal.ID)
	}

	if _, err := store.ApproveWord(ctx, proposal.ID); err != nil {
		t.Fatalf("ApproveWord(): %v", err)
	}
	store, err = NewFile(statePath, seedDirectory)
	if err != nil {
		t.Fatalf("reopen after approval: %v", err)
	}
	page, err = store.ListWords(ctx, model.CategoryNouns, model.StatusApproved, 50, "")
	if err != nil {
		t.Fatal(err)
	}
	if !pageContainsID(page, proposal.ID) {
		t.Fatalf("approved words do not contain %s", proposal.ID)
	}

	if removed, err := store.DeleteWord(ctx, proposal.ID); err != nil || !removed {
		t.Fatalf("DeleteWord() = (%v, %v), want (true, nil)", removed, err)
	}
	store, err = NewFile(statePath, seedDirectory)
	if err != nil {
		t.Fatalf("reopen after delete: %v", err)
	}
	page, err = store.ListWords(ctx, model.CategoryNouns, "", 50, "")
	if err != nil {
		t.Fatal(err)
	}
	if pageContainsID(page, proposal.ID) {
		t.Fatalf("deleted word %s was restored", proposal.ID)
	}

	info, err := os.Stat(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if permissions := info.Mode().Perm(); permissions != 0o600 {
		t.Fatalf("state permissions = %o, want 600", permissions)
	}
}

func TestFileStoreRollsBackFailedWrite(t *testing.T) {
	seedDirectory := writeSeedWords(t)
	store, err := NewFile(filepath.Join(t.TempDir(), "words.json"), seedDirectory)
	if err != nil {
		t.Fatal(err)
	}

	store.path = t.TempDir() // Renaming a file over a directory must fail.
	if _, err := store.CreateProposal(context.Background(), model.CategoryNouns, "capybara"); err == nil {
		t.Fatal("CreateProposal() succeeded despite an invalid state path")
	}
	page, err := store.ListWords(
		context.Background(),
		model.CategoryNouns,
		model.StatusProposed,
		50,
		"",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Words) != 0 {
		t.Fatalf("failed proposal remained in memory: %+v", page.Words)
	}
}

func TestFactorySelectsConfiguredStore(t *testing.T) {
	seedDirectory := writeSeedWords(t)

	t.Run("memory default", func(t *testing.T) {
		t.Setenv(DriverEnvironment, "")
		store, err := NewFromEnvironment(context.Background(), seedDirectory)
		if err != nil {
			t.Fatal(err)
		}
		assertInstrumentedStore(t, store, "memory", func(store Store) bool {
			_, ok := store.(*Memory)
			return ok
		})
	})

	t.Run("file", func(t *testing.T) {
		t.Setenv(DriverEnvironment, "file")
		t.Setenv(FilePathEnv, filepath.Join(t.TempDir(), "words.json"))
		store, err := NewFromEnvironment(context.Background(), seedDirectory)
		if err != nil {
			t.Fatal(err)
		}
		assertInstrumentedStore(t, store, "file", func(store Store) bool {
			_, ok := store.(*File)
			return ok
		})
	})
}

func assertInstrumentedStore(
	t *testing.T,
	store Store,
	driver string,
	isExpectedStore func(Store) bool,
) {
	t.Helper()
	instrumented, ok := store.(*instrumentedStore)
	if !ok {
		t.Fatalf("store = %T, want *instrumentedStore", store)
	}
	if instrumented.driver != driver {
		t.Fatalf("driver = %q, want %q", instrumented.driver, driver)
	}
	if !isExpectedStore(instrumented.Store) {
		t.Fatalf("wrapped store = %T, want %s driver", instrumented.Store, driver)
	}
}

func writeSeedWords(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	files := map[string]string{
		"adjectives.json": `["brave","calm"]`,
		"nouns.json":      `["gopher","badger"]`,
	}
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return directory
}

func pageContainsID(page model.WordPage, id string) bool {
	for _, word := range page.Words {
		if word.ID == id {
			return true
		}
	}
	return false
}
