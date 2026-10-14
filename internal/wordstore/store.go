package wordstore

import (
	"context"

	"github.com/tehbooom/project_name/internal/model"
)

type Store interface {
	GetWords(context.Context, model.NameConstraints) (string, error)
	CreateProposal(context.Context, string, string) (model.Word, error)
	ApproveWord(context.Context, string) (model.Word, error)
	SetWordSafety(context.Context, string, bool) (model.Word, error)
	DeleteWord(context.Context, string) (bool, error)
	ListWords(context.Context, string, string, int, string) (model.WordPage, error)
	Ready(context.Context) error
	Close(context.Context) error
}
