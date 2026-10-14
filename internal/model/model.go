package model

import (
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

const MaxWordLength = 64

const (
	CategoryAdjectives = "adjectives"
	CategoryNouns      = "nouns"
	StatusProposed     = "proposed"
	StatusApproved     = "approved"
)

var (
	ErrDuplicateWord    = errors.New("word already exists")
	ErrInvalidCategory  = errors.New("invalid word category")
	ErrInvalidWord      = errors.New("invalid word")
	ErrLastWord         = errors.New("cannot remove the last word in a category")
	ErrNoWords          = errors.New("not enough words to generate a project name")
	ErrNoMatchingWords  = errors.New("no words match the requested length constraints")
	ErrInvalidLimit     = errors.New("invalid page size")
	ErrInvalidPageToken = errors.New("invalid page token")
	ErrInvalidStatus    = errors.New("invalid word status")
	ErrWordNotFound     = errors.New("word not found")
)

type Word struct {
	ID       string `json:"id"`
	Value    string `json:"value"`
	Category string `json:"category"`
	Status   string `json:"status"`
	Safe     bool   `json:"safe"`
}

type WordPage struct {
	Words         []Word `json:"words"`
	NextPageToken string `json:"next_page_token,omitempty"`
}

type NameConstraints struct {
	MaxNounLength      int
	MaxAdjectiveLength int
	Unsafe             bool
}

type pageCursor struct {
	Version  int    `json:"v"`
	Category string `json:"category"`
	Status   string `json:"status,omitempty"`
	After    string `json:"after"`
}

type WordList struct {
	mu         sync.RWMutex
	adjectives wordCategory
	nouns      wordCategory
	records    map[string]Word
	byKey      map[string]string
}

type wordCategory struct {
	index map[string]int
	words []string
}

func (w *WordList) GetWords(constraints NameConstraints) (string, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	noun, ok := w.randomWordWithin(
		w.nouns.words,
		CategoryNouns,
		constraints.MaxNounLength,
		!constraints.Unsafe,
	)
	if !ok {
		if constraints.MaxNounLength > 0 {
			return "", ErrNoMatchingWords
		}
		return "", ErrNoWords
	}
	adjective, ok := w.randomWordWithin(
		w.adjectives.words,
		CategoryAdjectives,
		constraints.MaxAdjectiveLength,
		!constraints.Unsafe,
	)
	if !ok {
		if constraints.MaxAdjectiveLength > 0 {
			return "", ErrNoMatchingWords
		}
		return "", ErrNoWords
	}
	return noun + "-" + adjective, nil
}

func (w *WordList) randomWordWithin(
	words []string,
	category string,
	maxLength int,
	safe bool,
) (string, bool) {
	selected := ""
	matches := 0
	for _, word := range words {
		if maxLength > 0 && utf8.RuneCountInString(word) > maxLength {
			continue
		}
		record := w.records[w.byKey[wordKey(category, word)]]
		if record.Safe != safe {
			continue
		}
		matches++
		if rand.Intn(matches) == 0 {
			selected = word
		}
	}
	return selected, matches > 0
}

func (w *WordList) Ready() error {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if len(w.nouns.words) == 0 || len(w.adjectives.words) == 0 {
		return ErrNoWords
	}
	return nil
}

func (wc *wordCategory) remove(word string) bool {
	idx, exists := wc.index[word]
	if !exists {
		return false
	}

	lastIdx := len(wc.words) - 1
	lastWord := wc.words[lastIdx]
	wc.words[idx] = lastWord
	wc.index[lastWord] = idx
	wc.words = wc.words[:lastIdx]
	delete(wc.index, word)
	return true
}

func (w *WordList) RemoveWord(category, word string) (bool, error) {
	normalized, err := NormalizeWord(word)
	if err != nil {
		return false, err
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if err := ValidateCategory(category); err != nil {
		return false, err
	}
	id, exists := w.byKey[wordKey(category, normalized)]
	if !exists {
		return false, nil
	}
	return w.deleteWordLocked(id)
}

func (w *WordList) AddWord(category, word string) error {
	return w.AddWordWithSafety(category, word, true)
}

func (w *WordList) AddWordWithSafety(category, word string, safe bool) error {
	normalized, err := NormalizeWord(word)
	if err != nil {
		return err
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if err := ValidateCategory(category); err != nil {
		return err
	}
	return w.addApprovedLocked(category, normalized, safe)
}

func (w *WordList) CreateProposal(category, word string) (Word, error) {
	normalized, err := NormalizeWord(word)
	if err != nil {
		return Word{}, err
	}
	if err := ValidateCategory(category); err != nil {
		return Word{}, err
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	key := wordKey(category, normalized)
	if _, exists := w.byKey[key]; exists {
		return Word{}, fmt.Errorf("%w: %q", ErrDuplicateWord, normalized)
	}
	id, err := NewWordID()
	if err != nil {
		return Word{}, fmt.Errorf("create word ID: %w", err)
	}
	record := Word{
		ID:       id,
		Value:    normalized,
		Category: category,
		Status:   StatusProposed,
		Safe:     true,
	}
	w.records[id] = record
	w.byKey[key] = id
	return record, nil
}

func (w *WordList) ApproveWord(id string) (Word, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	record, exists := w.records[id]
	if !exists {
		return Word{}, ErrWordNotFound
	}
	if record.Status == StatusApproved {
		return record, nil
	}
	if err := w.category(record.Category).put(record.Value); err != nil {
		return Word{}, err
	}
	record.Status = StatusApproved
	w.records[id] = record
	return record, nil
}

func (w *WordList) SetWordSafety(id string, safe bool) (Word, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	record, exists := w.records[id]
	if !exists {
		return Word{}, ErrWordNotFound
	}
	record.Safe = safe
	w.records[id] = record
	return record, nil
}

func (w *WordList) DeleteWord(id string) (bool, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.deleteWordLocked(id)
}

func (w *WordList) deleteWordLocked(id string) (bool, error) {
	record, exists := w.records[id]
	if !exists {
		return false, nil
	}
	if record.Status == StatusApproved {
		category := w.category(record.Category)
		if len(category.words) == 1 {
			return false, ErrLastWord
		}
		category.remove(record.Value)
	}
	delete(w.records, id)
	delete(w.byKey, wordKey(record.Category, record.Value))
	return true, nil
}

func (w *WordList) ListWords(category, status string, limit int, pageToken string) (WordPage, error) {
	if err := ValidateList(category, status, limit); err != nil {
		return WordPage{}, err
	}

	cursor, err := decodePageToken(pageToken, category, status)
	if err != nil {
		return WordPage{}, err
	}

	w.mu.RLock()
	words := make([]Word, 0)
	for _, record := range w.records {
		if record.Category == category && (status == "" || record.Status == status) {
			words = append(words, record)
		}
	}
	w.mu.RUnlock()
	sort.Slice(words, func(i, j int) bool {
		if words[i].Value == words[j].Value {
			return words[i].ID < words[j].ID
		}
		return words[i].Value < words[j].Value
	})

	start := 0
	if cursor.After != "" {
		start = sort.Search(len(words), func(index int) bool {
			return wordSortKey(words[index]) > cursor.After
		})
	}
	end := min(start+limit, len(words))

	page := WordPage{Words: append([]Word(nil), words[start:end]...)}
	if end < len(words) {
		page.NextPageToken, err = encodePageToken(pageCursor{
			Version:  1,
			Category: category,
			Status:   status,
			After:    wordSortKey(words[end-1]),
		})
		if err != nil {
			return WordPage{}, err
		}
	}
	return page, nil
}

func decodePageToken(token, category, status string) (pageCursor, error) {
	if token == "" {
		return pageCursor{Version: 1, Category: category, Status: status}, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return pageCursor{}, fmt.Errorf("%w: malformed encoding", ErrInvalidPageToken)
	}
	var cursor pageCursor
	if err := json.Unmarshal(data, &cursor); err != nil {
		return pageCursor{}, fmt.Errorf("%w: malformed data", ErrInvalidPageToken)
	}
	if cursor.Version != 1 || cursor.Category != category ||
		cursor.Status != status || cursor.After == "" {
		return pageCursor{}, fmt.Errorf("%w: cursor does not match request", ErrInvalidPageToken)
	}
	return cursor, nil
}

func encodePageToken(cursor pageCursor) (string, error) {
	data, err := json.Marshal(cursor)
	if err != nil {
		return "", fmt.Errorf("encode page token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func WordID(category, value string) string {
	sum := sha256.Sum256([]byte(category + "\x00" + value))
	return fmt.Sprintf("%x", sum[:16])
}

func NewWordID() (string, error) {
	data := make([]byte, 16)
	if _, err := cryptorand.Read(data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}

func NormalizeWord(word string) (string, error) {
	word = strings.TrimSpace(word)
	if word == "" {
		return "", fmt.Errorf("%w: must not be empty", ErrInvalidWord)
	}
	if !utf8.ValidString(word) {
		return "", fmt.Errorf("%w: must be valid UTF-8", ErrInvalidWord)
	}
	if utf8.RuneCountInString(word) > MaxWordLength {
		return "", fmt.Errorf("%w: must contain at most %d characters", ErrInvalidWord, MaxWordLength)
	}
	if strings.ContainsFunc(word, unicode.IsControl) {
		return "", fmt.Errorf("%w: must not contain control characters", ErrInvalidWord)
	}
	return strings.ToLower(word), nil
}

func NewWordList() *WordList {
	return &WordList{
		adjectives: wordCategory{
			index: make(map[string]int),
			words: make([]string, 0),
		},
		nouns: wordCategory{
			index: make(map[string]int),
			words: make([]string, 0),
		},
		records: make(map[string]Word),
		byKey:   make(map[string]string),
	}
}

func NewWordListFromSnapshot(records []Word) (*WordList, error) {
	wordList := NewWordList()
	if err := wordList.replaceSnapshot(records); err != nil {
		return nil, err
	}
	return wordList, nil
}

func (w *WordList) Snapshot() []Word {
	w.mu.RLock()
	defer w.mu.RUnlock()

	records := make([]Word, 0, len(w.records))
	for _, record := range w.records {
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].ID < records[j].ID
	})
	return records
}

func (w *WordList) ReplaceSnapshot(records []Word) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.replaceSnapshotLocked(records)
}

func (w *WordList) replaceSnapshot(records []Word) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.replaceSnapshotLocked(records)
}

func (w *WordList) replaceSnapshotLocked(records []Word) error {
	replacement := NewWordList()
	for _, record := range records {
		if record.ID == "" {
			return fmt.Errorf("%w: word ID must not be empty", ErrInvalidWord)
		}
		if err := ValidateCategory(record.Category); err != nil {
			return err
		}
		if record.Status != StatusProposed && record.Status != StatusApproved {
			return fmt.Errorf("%w: %q", ErrInvalidStatus, record.Status)
		}
		normalized, err := NormalizeWord(record.Value)
		if err != nil {
			return err
		}
		record.Value = normalized
		key := wordKey(record.Category, record.Value)
		if _, exists := replacement.records[record.ID]; exists {
			return fmt.Errorf("%w: duplicate ID %q", ErrDuplicateWord, record.ID)
		}
		if _, exists := replacement.byKey[key]; exists {
			return fmt.Errorf("%w: %q", ErrDuplicateWord, record.Value)
		}
		if record.Status == StatusApproved {
			if err := replacement.category(record.Category).put(record.Value); err != nil {
				return err
			}
		}
		replacement.records[record.ID] = record
		replacement.byKey[key] = record.ID
	}

	w.adjectives = replacement.adjectives
	w.nouns = replacement.nouns
	w.records = replacement.records
	w.byKey = replacement.byKey
	return nil
}

func (wc *wordCategory) put(word string) error {
	if _, exists := wc.index[word]; exists {
		return fmt.Errorf("%w: %q", ErrDuplicateWord, word)
	}
	wc.index[word] = len(wc.words)
	wc.words = append(wc.words, word)
	return nil
}

func (w *WordList) addApprovedLocked(category, word string, safe bool) error {
	key := wordKey(category, word)
	if _, exists := w.byKey[key]; exists {
		return fmt.Errorf("%w: %q", ErrDuplicateWord, word)
	}
	record := Word{
		ID:       WordID(category, word),
		Value:    word,
		Category: category,
		Status:   StatusApproved,
		Safe:     safe,
	}
	if err := w.category(category).put(word); err != nil {
		return err
	}
	w.records[record.ID] = record
	w.byKey[key] = record.ID
	return nil
}

func (w *WordList) category(category string) *wordCategory {
	if category == CategoryAdjectives {
		return &w.adjectives
	}
	return &w.nouns
}

func ValidateCategory(category string) error {
	if category != CategoryAdjectives && category != CategoryNouns {
		return fmt.Errorf("%w: %q", ErrInvalidCategory, category)
	}
	return nil
}

func ValidateList(category, status string, limit int) error {
	if err := ValidateCategory(category); err != nil {
		return err
	}
	if status != "" && status != StatusProposed && status != StatusApproved {
		return fmt.Errorf("%w: %q", ErrInvalidStatus, status)
	}
	if limit < 1 || limit > 200 {
		return fmt.Errorf("%w: must be between 1 and 200", ErrInvalidLimit)
	}
	return nil
}

func wordKey(category, value string) string {
	return category + "\x00" + value
}

func wordSortKey(word Word) string {
	return word.Value + "\x00" + word.ID
}

func LoadWords(dirPath string) (*WordList, error) {
	wordList := NewWordList()

	for _, category := range []string{CategoryAdjectives, CategoryNouns} {
		fileName := dirPath + "/" + category + ".json"
		content, err := os.ReadFile(fileName)
		if err != nil {
			return nil, fmt.Errorf("read %s words: %w", category, err)
		}

		var words []json.RawMessage
		if err := json.Unmarshal(content, &words); err != nil {
			return nil, fmt.Errorf("parse %s words: %w", category, err)
		}
		duplicates := 0
		for _, rawWord := range words {
			word, safe, err := decodeSeedWord(rawWord)
			if err != nil {
				return nil, fmt.Errorf("parse %s word: %w", category, err)
			}
			if err := wordList.AddWordWithSafety(category, word, safe); err != nil {
				if errors.Is(err, ErrDuplicateWord) {
					duplicates++
					continue
				}
				return nil, fmt.Errorf("load %s word %q: %w", category, word, err)
			}
		}
		if duplicates > 0 {
			slog.Warn("skipped duplicate seed words",
				"category", category,
				"count", duplicates,
			)
		}
	}

	return wordList, nil
}

func decodeSeedWord(data json.RawMessage) (string, bool, error) {
	var value string
	if err := json.Unmarshal(data, &value); err == nil {
		return value, true, nil
	}
	var annotated struct {
		Value string `json:"value"`
		Safe  *bool  `json:"safe"`
	}
	if err := json.Unmarshal(data, &annotated); err != nil {
		return "", false, err
	}
	if annotated.Safe == nil {
		return annotated.Value, true, nil
	}
	return annotated.Value, *annotated.Safe, nil
}
