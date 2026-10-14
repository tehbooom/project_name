package wordstore

import (
	"context"

	"github.com/tehbooom/project_name/internal/model"
)

type Memory struct {
	words *model.WordList
}

func NewMemory(words *model.WordList) *Memory {
	return &Memory{words: words}
}

func (m *Memory) GetWords(ctx context.Context, constraints model.NameConstraints) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return m.words.GetWords(constraints)
}

func (m *Memory) CreateProposal(ctx context.Context, category, value string) (model.Word, error) {
	if err := ctx.Err(); err != nil {
		return model.Word{}, err
	}
	return m.words.CreateProposal(category, value)
}

func (m *Memory) ApproveWord(ctx context.Context, id string) (model.Word, error) {
	if err := ctx.Err(); err != nil {
		return model.Word{}, err
	}
	return m.words.ApproveWord(id)
}

func (m *Memory) SetWordSafety(ctx context.Context, id string, safe bool) (model.Word, error) {
	if err := ctx.Err(); err != nil {
		return model.Word{}, err
	}
	return m.words.SetWordSafety(id, safe)
}

func (m *Memory) DeleteWord(ctx context.Context, id string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return m.words.DeleteWord(id)
}

func (m *Memory) ListWords(
	ctx context.Context,
	category, status string,
	limit int,
	pageToken string,
) (model.WordPage, error) {
	if err := ctx.Err(); err != nil {
		return model.WordPage{}, err
	}
	return m.words.ListWords(category, status, limit, pageToken)
}

func (m *Memory) Ready(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return m.words.Ready()
}

func (m *Memory) Close(context.Context) error {
	return nil
}

var _ Store = (*Memory)(nil)
