package wordstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/tehbooom/project_name/internal/model"
)

const fileFormatVersion = 2

type fileState struct {
	Version int          `json:"version"`
	Words   []model.Word `json:"words"`
}

type File struct {
	mu    sync.RWMutex
	path  string
	words *model.WordList
}

func NewFile(path, seedDirectory string) (*File, error) {
	words, err := readState(path)
	if errors.Is(err, os.ErrNotExist) {
		words, err = model.LoadWords(seedDirectory)
		if err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}

	store := &File{path: path, words: words}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := store.persist(); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, fmt.Errorf("stat word store: %w", err)
	}
	return store, nil
}

func readState(path string) (*model.WordList, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var state fileState
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return nil, fmt.Errorf("decode word store %q: %w", path, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("decode word store %q: trailing data", path)
	}
	if state.Version != fileFormatVersion {
		if state.Version == 1 {
			for index := range state.Words {
				state.Words[index].Safe = true
			}
			state.Version = fileFormatVersion
		} else {
			return nil, fmt.Errorf(
				"word store %q has version %d, want %d",
				path,
				state.Version,
				fileFormatVersion,
			)
		}
	}
	words, err := model.NewWordListFromSnapshot(state.Words)
	if err != nil {
		return nil, fmt.Errorf("validate word store %q: %w", path, err)
	}
	return words, nil
}

func (f *File) GetWords(ctx context.Context, constraints model.NameConstraints) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.words.GetWords(constraints)
}

func (f *File) CreateProposal(ctx context.Context, category, value string) (model.Word, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return model.Word{}, err
	}

	before := f.words.Snapshot()
	word, err := f.words.CreateProposal(category, value)
	if err != nil {
		return model.Word{}, err
	}
	if err := f.persist(); err != nil {
		f.rollback(before)
		return model.Word{}, err
	}
	return word, nil
}

func (f *File) ApproveWord(ctx context.Context, id string) (model.Word, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return model.Word{}, err
	}

	before := f.words.Snapshot()
	word, err := f.words.ApproveWord(id)
	if err != nil {
		return model.Word{}, err
	}
	if err := f.persist(); err != nil {
		f.rollback(before)
		return model.Word{}, err
	}
	return word, nil
}

func (f *File) SetWordSafety(ctx context.Context, id string, safe bool) (model.Word, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return model.Word{}, err
	}

	before := f.words.Snapshot()
	word, err := f.words.SetWordSafety(id, safe)
	if err != nil {
		return model.Word{}, err
	}
	if err := f.persist(); err != nil {
		f.rollback(before)
		return model.Word{}, err
	}
	return word, nil
}

func (f *File) DeleteWord(ctx context.Context, id string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return false, err
	}

	before := f.words.Snapshot()
	removed, err := f.words.DeleteWord(id)
	if err != nil || !removed {
		return removed, err
	}
	if err := f.persist(); err != nil {
		f.rollback(before)
		return false, err
	}
	return true, nil
}

func (f *File) ListWords(
	ctx context.Context,
	category, status string,
	limit int,
	pageToken string,
) (model.WordPage, error) {
	if err := ctx.Err(); err != nil {
		return model.WordPage{}, err
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.words.ListWords(category, status, limit, pageToken)
}

func (f *File) Ready(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.words.Ready()
}

func (f *File) Close(context.Context) error {
	return nil
}

func (f *File) persist() error {
	state := fileState{
		Version: fileFormatVersion,
		Words:   f.words.Snapshot(),
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode word store: %w", err)
	}
	data = append(data, '\n')

	directory := filepath.Dir(f.path)
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return fmt.Errorf("create word store directory: %w", err)
	}
	temp, err := os.CreateTemp(directory, ".words-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary word store: %w", err)
	}
	tempPath := temp.Name()
	defer func() {
		_ = os.Remove(tempPath)
	}()

	if err := temp.Chmod(0o600); err != nil {
		return errors.Join(
			fmt.Errorf("secure temporary word store: %w", err),
			temp.Close(),
		)
	}
	if _, err := temp.Write(data); err != nil {
		return errors.Join(
			fmt.Errorf("write temporary word store: %w", err),
			temp.Close(),
		)
	}
	if err := temp.Sync(); err != nil {
		return errors.Join(
			fmt.Errorf("sync temporary word store: %w", err),
			temp.Close(),
		)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temporary word store: %w", err)
	}
	if err := os.Rename(tempPath, f.path); err != nil {
		return fmt.Errorf("replace word store: %w", err)
	}

	dir, err := os.Open(directory)
	if err != nil {
		return fmt.Errorf("open word store directory: %w", err)
	}
	if err := dir.Sync(); err != nil {
		return errors.Join(
			fmt.Errorf("sync word store directory: %w", err),
			dir.Close(),
		)
	}
	if err := dir.Close(); err != nil {
		return fmt.Errorf("close word store directory: %w", err)
	}
	return nil
}

func (f *File) rollback(snapshot []model.Word) {
	_ = f.words.ReplaceSnapshot(snapshot)
}

var _ Store = (*File)(nil)
