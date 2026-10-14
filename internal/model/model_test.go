package model

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestWordListAddGenerateAndRemove(t *testing.T) {
	words := NewWordList()
	for _, tc := range []struct {
		category string
		word     string
	}{
		{CategoryNouns, " Gopher "},
		{CategoryNouns, "Badger"},
		{CategoryAdjectives, "Brave"},
		{CategoryAdjectives, "Curious"},
	} {
		if err := words.AddWord(tc.category, tc.word); err != nil {
			t.Fatalf("AddWord(%q, %q): %v", tc.category, tc.word, err)
		}
	}

	name, err := words.GetWords(NameConstraints{})
	if err != nil {
		t.Fatalf("GetWords(): %v", err)
	}
	if !strings.Contains(name, "-") || name != strings.ToLower(name) {
		t.Fatalf("GetWords() = %q, want a lowercase compound name", name)
	}

	removed, err := words.RemoveWord(CategoryNouns, "GOPHER")
	if err != nil {
		t.Fatalf("RemoveWord(): %v", err)
	}
	if !removed {
		t.Fatal("RemoveWord() = false, want true")
	}
	if len(words.nouns.words) != len(words.nouns.index) {
		t.Fatalf("noun index has %d entries for %d words", len(words.nouns.index), len(words.nouns.words))
	}
	for word, index := range words.nouns.index {
		if words.nouns.words[index] != word {
			t.Fatalf("noun index[%q] = %d points to %q", word, index, words.nouns.words[index])
		}
	}
}

func TestWordListGetWordsHonorsCategoryLengths(t *testing.T) {
	words := NewWordList()
	for _, word := range []struct {
		category string
		value    string
	}{
		{CategoryNouns, "ox"},
		{CategoryNouns, "elephant"},
		{CategoryAdjectives, "red"},
		{CategoryAdjectives, "magnificent"},
	} {
		if err := words.AddWord(word.category, word.value); err != nil {
			t.Fatal(err)
		}
	}

	name, err := words.GetWords(NameConstraints{
		MaxNounLength:      2,
		MaxAdjectiveLength: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if name != "ox-red" {
		t.Fatalf("GetWords() = %q, want ox-red", name)
	}
	if _, err := words.GetWords(NameConstraints{MaxNounLength: 1}); !errors.Is(err, ErrNoMatchingWords) {
		t.Fatalf("GetWords() error = %v, want ErrNoMatchingWords", err)
	}
}

func TestWordListGetWordsDefaultsToSafe(t *testing.T) {
	words := NewWordList()
	for _, word := range []struct {
		category string
		value    string
		safe     bool
	}{
		{CategoryNouns, "otter", true},
		{CategoryAdjectives, "calm", true},
		{CategoryNouns, "shit", false},
		{CategoryAdjectives, "damn", false},
	} {
		if err := words.AddWordWithSafety(word.category, word.value, word.safe); err != nil {
			t.Fatal(err)
		}
	}

	if name, err := words.GetWords(NameConstraints{}); err != nil || name != "otter-calm" {
		t.Fatalf("safe GetWords() = (%q, %v), want (otter-calm, nil)", name, err)
	}
	if name, err := words.GetWords(NameConstraints{Unsafe: true}); err != nil || name != "shit-damn" {
		t.Fatalf("unsafe GetWords() = (%q, %v), want (shit-damn, nil)", name, err)
	}
}

func TestWordListErrors(t *testing.T) {
	words := NewWordList()

	if _, err := words.GetWords(NameConstraints{}); !errors.Is(err, ErrNoWords) {
		t.Fatalf("GetWords() error = %v, want ErrNoWords", err)
	}
	if err := words.AddWord("verbs", "run"); !errors.Is(err, ErrInvalidCategory) {
		t.Fatalf("AddWord() error = %v, want ErrInvalidCategory", err)
	}
	if err := words.AddWord(CategoryNouns, ""); !errors.Is(err, ErrInvalidWord) {
		t.Fatalf("AddWord() error = %v, want ErrInvalidWord", err)
	}
	if err := words.AddWord(CategoryNouns, strings.Repeat("a", MaxWordLength+1)); !errors.Is(err, ErrInvalidWord) {
		t.Fatalf("AddWord() error = %v, want ErrInvalidWord", err)
	}
	if err := words.AddWord(CategoryNouns, "valid"); err != nil {
		t.Fatal(err)
	}
	if err := words.AddWord(CategoryNouns, "VALID"); !errors.Is(err, ErrDuplicateWord) {
		t.Fatalf("AddWord() error = %v, want ErrDuplicateWord", err)
	}
	if removed, err := words.RemoveWord(CategoryNouns, "valid"); removed || !errors.Is(err, ErrLastWord) {
		t.Fatalf("RemoveWord() = (%v, %v), want (false, ErrLastWord)", removed, err)
	}
}

func TestWordListConcurrentAccess(t *testing.T) {
	words := NewWordList()
	for _, category := range []string{CategoryNouns, CategoryAdjectives} {
		for _, word := range []string{"base-one", "base-two"} {
			if err := words.AddWord(category, word); err != nil {
				t.Fatal(err)
			}
		}
	}

	const workers = 8
	const iterations = 200
	errs := make(chan error, workers*iterations)
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		worker := worker
		wg.Add(1)
		go func() {
			defer wg.Done()
			for iteration := 0; iteration < iterations; iteration++ {
				if _, err := words.GetWords(NameConstraints{}); err != nil {
					errs <- err
				}

				word := fmt.Sprintf("worker-%d-%d", worker, iteration)
				if err := words.AddWord(CategoryNouns, word); err != nil {
					errs <- err
					continue
				}
				if removed, err := words.RemoveWord(CategoryNouns, word); err != nil {
					errs <- err
				} else if !removed {
					errs <- fmt.Errorf("word %q was not removed", word)
				}
			}
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Error(err)
	}
}

func TestListWordsPagination(t *testing.T) {
	words := NewWordList()
	for _, word := range []string{"zebra", "badger", "gopher"} {
		if err := words.AddWord(CategoryNouns, word); err != nil {
			t.Fatal(err)
		}
	}

	first, err := words.ListWords(CategoryNouns, StatusApproved, 2, "")
	if err != nil {
		t.Fatalf("first ListWords(): %v", err)
	}
	if got := []string{first.Words[0].Value, first.Words[1].Value}; fmt.Sprint(got) != "[badger gopher]" {
		t.Fatalf("first page = %v, want [badger gopher]", got)
	}
	if first.NextPageToken == "" {
		t.Fatal("first page has no next page token")
	}
	if first.Words[0].ID == "" {
		t.Fatal("listed word has no ID")
	}

	second, err := words.ListWords(CategoryNouns, StatusApproved, 2, first.NextPageToken)
	if err != nil {
		t.Fatalf("second ListWords(): %v", err)
	}
	if len(second.Words) != 1 || second.Words[0].Value != "zebra" {
		t.Fatalf("second page = %+v, want zebra", second.Words)
	}
	if second.NextPageToken != "" {
		t.Fatalf("final page token = %q, want empty", second.NextPageToken)
	}

	if _, err := words.ListWords(CategoryAdjectives, StatusApproved, 2, first.NextPageToken); !errors.Is(err, ErrInvalidPageToken) {
		t.Fatalf("cross-category token error = %v, want ErrInvalidPageToken", err)
	}
	if _, err := words.ListWords(CategoryNouns, StatusApproved, 201, ""); !errors.Is(err, ErrInvalidLimit) {
		t.Fatalf("oversized page error = %v, want ErrInvalidLimit", err)
	}
}

func TestProposalLifecycle(t *testing.T) {
	words := NewWordList()
	for _, category := range []string{CategoryNouns, CategoryAdjectives} {
		for _, word := range []string{"base-one", "base-two"} {
			if err := words.AddWord(category, word); err != nil {
				t.Fatal(err)
			}
		}
	}

	proposal, err := words.CreateProposal(CategoryNouns, "Capybara")
	if err != nil {
		t.Fatalf("CreateProposal(): %v", err)
	}
	if proposal.Status != StatusProposed || proposal.ID == "" {
		t.Fatalf("proposal = %+v", proposal)
	}

	proposals, err := words.ListWords(CategoryNouns, StatusProposed, 50, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(proposals.Words) != 1 || proposals.Words[0].ID != proposal.ID {
		t.Fatalf("proposals = %+v, want %s", proposals.Words, proposal.ID)
	}

	approved, err := words.ApproveWord(proposal.ID)
	if err != nil {
		t.Fatalf("ApproveWord(): %v", err)
	}
	if approved.Status != StatusApproved {
		t.Fatalf("approved status = %q", approved.Status)
	}
	updated, err := words.SetWordSafety(proposal.ID, false)
	if err != nil || updated.Safe {
		t.Fatalf("SetWordSafety() = (%+v, %v), want safe false", updated, err)
	}

	if removed, err := words.DeleteWord(proposal.ID); err != nil || !removed {
		t.Fatalf("DeleteWord() = (%v, %v), want (true, nil)", removed, err)
	}
	if removed, err := words.DeleteWord(proposal.ID); err != nil || removed {
		t.Fatalf("second DeleteWord() = (%v, %v), want (false, nil)", removed, err)
	}
}

func TestLoadWords(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		CategoryAdjectives: `["Brave", "BRAVE", "Calm", {"value":"Damn","safe":false}]`,
		CategoryNouns:      `["Gopher", "Badger", {"value":"Shit","safe":false}]`,
	}
	for category, contents := range files {
		path := filepath.Join(dir, category+".json")
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	words, err := LoadWords(dir)
	if err != nil {
		t.Fatalf("LoadWords(): %v", err)
	}
	if got := len(words.adjectives.words); got != 3 {
		t.Fatalf("loaded %d adjectives, want 3 unique words", got)
	}
	if _, err := words.GetWords(NameConstraints{}); err != nil {
		t.Fatalf("GetWords(): %v", err)
	}
	if name, err := words.GetWords(NameConstraints{Unsafe: true}); err != nil || name != "shit-damn" {
		t.Fatalf("unsafe GetWords() = (%q, %v), want (shit-damn, nil)", name, err)
	}
}
