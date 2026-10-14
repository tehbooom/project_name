package wordstore

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/tehbooom/project_name/internal/model"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

const instrumentationName = "github.com/tehbooom/project_name/internal/wordstore"

type instrumentedStore struct {
	Store
	driver         string
	operationCount metric.Int64Counter
	duration       metric.Float64Histogram
}

func instrumentStore(store Store, driver string) (Store, error) {
	meter := otel.Meter(instrumentationName)
	operationCount, err := meter.Int64Counter(
		"project.wordstore.operations",
		metric.WithDescription("Number of word store operations"),
	)
	if err != nil {
		return nil, err
	}
	duration, err := meter.Float64Histogram(
		"project.wordstore.operation.duration",
		metric.WithDescription("Duration of word store operations"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, err
	}
	return &instrumentedStore{
		Store:          store,
		driver:         driver,
		operationCount: operationCount,
		duration:       duration,
	}, nil
}

func (s *instrumentedStore) GetWords(
	ctx context.Context,
	constraints model.NameConstraints,
) (string, error) {
	ctx, span, started := s.start(ctx, "get_words")
	span.SetAttributes(
		attribute.Int("name.max_noun_length", constraints.MaxNounLength),
		attribute.Int("name.max_adjective_length", constraints.MaxAdjectiveLength),
		attribute.Bool("name.safe", !constraints.Unsafe),
	)
	value, err := s.Store.GetWords(ctx, constraints)
	s.finish(ctx, span, started, "get_words", resultForError(err), err)
	return value, err
}

func (s *instrumentedStore) CreateProposal(
	ctx context.Context,
	category, value string,
) (model.Word, error) {
	ctx, span, started := s.start(ctx, "create_proposal")
	span.SetAttributes(attribute.String("word.category", category))
	word, err := s.Store.CreateProposal(ctx, category, value)
	s.finish(ctx, span, started, "create_proposal", resultForError(err), err)
	return word, err
}

func (s *instrumentedStore) ApproveWord(ctx context.Context, id string) (model.Word, error) {
	ctx, span, started := s.start(ctx, "approve_word")
	word, err := s.Store.ApproveWord(ctx, id)
	s.finish(ctx, span, started, "approve_word", resultForError(err), err)
	return word, err
}

func (s *instrumentedStore) SetWordSafety(
	ctx context.Context,
	id string,
	safe bool,
) (model.Word, error) {
	ctx, span, started := s.start(ctx, "set_word_safety")
	span.SetAttributes(attribute.Bool("word.safe", safe))
	word, err := s.Store.SetWordSafety(ctx, id, safe)
	s.finish(ctx, span, started, "set_word_safety", resultForError(err), err)
	return word, err
}

func (s *instrumentedStore) DeleteWord(ctx context.Context, id string) (bool, error) {
	ctx, span, started := s.start(ctx, "delete_word")
	removed, err := s.Store.DeleteWord(ctx, id)
	result := resultForError(err)
	if err == nil && !removed {
		result = "not_found"
	}
	s.finish(ctx, span, started, "delete_word", result, err)
	return removed, err
}

func (s *instrumentedStore) ListWords(
	ctx context.Context,
	category, status string,
	limit int,
	pageToken string,
) (model.WordPage, error) {
	ctx, span, started := s.start(ctx, "list_words")
	span.SetAttributes(
		attribute.String("word.category", category),
		attribute.String("word.status", status),
		attribute.Int("page.limit", limit),
		attribute.Bool("page.has_token", pageToken != ""),
	)
	page, err := s.Store.ListWords(ctx, category, status, limit, pageToken)
	if err == nil {
		span.SetAttributes(attribute.Int("page.size", len(page.Words)))
	}
	s.finish(ctx, span, started, "list_words", resultForError(err), err)
	return page, err
}

func (s *instrumentedStore) Ready(ctx context.Context) error {
	started := time.Now()
	err := s.Store.Ready(ctx)
	result := resultForError(err)
	attributes := []attribute.KeyValue{
		attribute.String("wordstore.driver", s.driver),
		attribute.String("wordstore.operation", "ready"),
		attribute.String("result", result),
	}
	s.operationCount.Add(ctx, 1, metric.WithAttributes(attributes...))
	s.duration.Record(ctx, time.Since(started).Seconds(), metric.WithAttributes(attributes...))
	return err
}

func (s *instrumentedStore) Close(ctx context.Context) error {
	ctx, span, started := s.start(ctx, "close")
	err := s.Store.Close(ctx)
	s.finish(ctx, span, started, "close", resultForError(err), err)
	return err
}

func (s *instrumentedStore) start(
	ctx context.Context,
	operation string,
) (context.Context, trace.Span, time.Time) {
	ctx, span := otel.Tracer(instrumentationName).Start(
		ctx,
		"wordstore."+operation,
		trace.WithAttributes(
			attribute.String("wordstore.driver", s.driver),
			attribute.String("wordstore.operation", operation),
		),
	)
	return ctx, span, time.Now()
}

func (s *instrumentedStore) finish(
	ctx context.Context,
	span trace.Span,
	started time.Time,
	operation, result string,
	err error,
) {
	attributes := []attribute.KeyValue{
		attribute.String("wordstore.driver", s.driver),
		attribute.String("wordstore.operation", operation),
		attribute.String("result", result),
	}
	s.operationCount.Add(ctx, 1, metric.WithAttributes(attributes...))
	s.duration.Record(ctx, time.Since(started).Seconds(), metric.WithAttributes(attributes...))
	span.SetAttributes(attribute.String("result", result))
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, result)
		span.SetAttributes(attribute.String("error.type", result))
		if result == "error" {
			slog.ErrorContext(ctx, "word store operation failed",
				"driver", s.driver,
				"operation", operation,
				"error", err,
			)
		}
	}
	span.End()
}

func resultForError(err error) string {
	switch {
	case err == nil:
		return "success"
	case errors.Is(err, model.ErrDuplicateWord):
		return "duplicate_word"
	case errors.Is(err, model.ErrLastWord):
		return "last_word"
	case errors.Is(err, model.ErrInvalidCategory):
		return "invalid_category"
	case errors.Is(err, model.ErrInvalidWord):
		return "invalid_word"
	case errors.Is(err, model.ErrInvalidLimit):
		return "invalid_limit"
	case errors.Is(err, model.ErrInvalidPageToken):
		return "invalid_page_token"
	case errors.Is(err, model.ErrInvalidStatus):
		return "invalid_status"
	case errors.Is(err, model.ErrWordNotFound):
		return "word_not_found"
	case errors.Is(err, model.ErrNoWords):
		return "no_words"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	default:
		return "error"
	}
}

var _ Store = (*instrumentedStore)(nil)
