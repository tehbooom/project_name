package wordstore

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"time"
	"unicode/utf8"

	"cloud.google.com/go/firestore"
	"github.com/tehbooom/project_name/internal/model"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type firestoreWord struct {
	Value     string    `firestore:"value"`
	Category  string    `firestore:"category"`
	Status    string    `firestore:"status"`
	Safe      bool      `firestore:"safe"`
	Length    int       `firestore:"length"`
	Random    float64   `firestore:"random"`
	CreatedAt time.Time `firestore:"created_at"`
	UpdatedAt time.Time `firestore:"updated_at"`
}

type Firestore struct {
	client     *firestore.Client
	collection *firestore.CollectionRef
}

func NewFirestore(ctx context.Context, projectID, collection string) (*Firestore, error) {
	client, err := firestore.NewClient(
		ctx,
		projectID,
		option.WithGRPCDialOption(grpc.WithStatsHandler(otelgrpc.NewClientHandler())),
	)
	if err != nil {
		return nil, fmt.Errorf("connect Firestore word store: %w", err)
	}
	return &Firestore{
		client:     client,
		collection: client.Collection(collection),
	}, nil
}

func (f *Firestore) Seed(ctx context.Context, records []model.Word) error {
	writer := f.client.BulkWriter(ctx)
	jobs := make([]*firestore.BulkWriterJob, 0, len(records))
	for _, word := range records {
		random, err := randomFraction()
		if err != nil {
			writer.End()
			return err
		}
		now := time.Now().UTC()
		job, err := writer.Create(f.collection.Doc(word.ID), firestoreWord{
			Value:     word.Value,
			Category:  word.Category,
			Status:    word.Status,
			Safe:      word.Safe,
			Length:    utf8.RuneCountInString(word.Value),
			Random:    random,
			CreatedAt: now,
			UpdatedAt: now,
		})
		if err != nil {
			writer.End()
			return fmt.Errorf("queue Firestore seed: %w", err)
		}
		jobs = append(jobs, job)
	}
	writer.End()

	for _, job := range jobs {
		if _, err := job.Results(); err != nil && status.Code(err) != codes.AlreadyExists {
			return fmt.Errorf("seed Firestore words: %w", err)
		}
	}
	return nil
}

func (f *Firestore) GetWords(ctx context.Context, constraints model.NameConstraints) (string, error) {
	noun, err := f.randomApprovedWord(
		ctx,
		model.CategoryNouns,
		constraints.MaxNounLength,
		!constraints.Unsafe,
	)
	if err != nil {
		return "", err
	}
	adjective, err := f.randomApprovedWord(
		ctx,
		model.CategoryAdjectives,
		constraints.MaxAdjectiveLength,
		!constraints.Unsafe,
	)
	if err != nil {
		return "", err
	}
	return noun + "-" + adjective, nil
}

func (f *Firestore) randomApprovedWord(
	ctx context.Context,
	category string,
	maxLength int,
	safe bool,
) (string, error) {
	base := f.collection.
		Where("category", "==", category).
		Where("status", "==", model.StatusApproved).
		Where("safe", "==", safe)
	if maxLength > 0 {
		base = base.Where("length", "<=", maxLength)
	}

	random, err := randomFraction()
	if err != nil {
		return "", err
	}

	query := base.Where("random", ">=", random).OrderBy("random", firestore.Asc).Limit(1)
	document, err := query.Documents(ctx).Next()
	if errors.Is(err, iterator.Done) {
		document, err = base.OrderBy("random", firestore.Asc).Limit(1).Documents(ctx).Next()
	}
	if errors.Is(err, iterator.Done) {
		return "", model.ErrNoMatchingWords
	}
	if err != nil {
		return "", fmt.Errorf("select random Firestore %s word: %w", category, err)
	}
	var stored firestoreWord
	if err := document.DataTo(&stored); err != nil {
		return "", fmt.Errorf("decode Firestore word: %w", err)
	}
	return stored.Value, nil
}

func (f *Firestore) CreateProposal(
	ctx context.Context,
	category, value string,
) (model.Word, error) {
	if err := model.ValidateCategory(category); err != nil {
		return model.Word{}, err
	}
	value, err := model.NormalizeWord(value)
	if err != nil {
		return model.Word{}, err
	}
	random, err := randomFraction()
	if err != nil {
		return model.Word{}, err
	}
	word := model.Word{
		ID:       model.WordID(category, value),
		Value:    value,
		Category: category,
		Status:   model.StatusProposed,
		Safe:     true,
	}
	now := time.Now().UTC()
	_, err = f.collection.Doc(word.ID).Create(ctx, firestoreWord{
		Value:     word.Value,
		Category:  word.Category,
		Status:    word.Status,
		Safe:      word.Safe,
		Length:    utf8.RuneCountInString(word.Value),
		Random:    random,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if status.Code(err) == codes.AlreadyExists {
		return model.Word{}, fmt.Errorf("%w: %q", model.ErrDuplicateWord, value)
	}
	if err != nil {
		return model.Word{}, fmt.Errorf("create Firestore word proposal: %w", err)
	}
	return word, nil
}

func (f *Firestore) ApproveWord(ctx context.Context, id string) (model.Word, error) {
	var word model.Word
	err := f.client.RunTransaction(ctx, func(_ context.Context, tx *firestore.Transaction) error {
		ref := f.collection.Doc(id)
		snapshot, err := tx.Get(ref)
		if status.Code(err) == codes.NotFound {
			return model.ErrWordNotFound
		}
		if err != nil {
			return err
		}
		var stored firestoreWord
		if err := snapshot.DataTo(&stored); err != nil {
			return err
		}
		if stored.Status != model.StatusApproved {
			if err := tx.Update(ref, []firestore.Update{
				{Path: "status", Value: model.StatusApproved},
				{Path: "updated_at", Value: time.Now().UTC()},
			}); err != nil {
				return err
			}
		}
		word = model.Word{
			ID:       id,
			Value:    stored.Value,
			Category: stored.Category,
			Status:   model.StatusApproved,
			Safe:     stored.Safe,
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, model.ErrWordNotFound) {
			return model.Word{}, err
		}
		return model.Word{}, fmt.Errorf("approve Firestore word: %w", err)
	}
	return word, nil
}

func (f *Firestore) SetWordSafety(
	ctx context.Context,
	id string,
	safe bool,
) (model.Word, error) {
	ref := f.collection.Doc(id)
	snapshot, err := ref.Get(ctx)
	if status.Code(err) == codes.NotFound {
		return model.Word{}, model.ErrWordNotFound
	}
	if err != nil {
		return model.Word{}, fmt.Errorf("find Firestore word: %w", err)
	}
	var stored firestoreWord
	if err := snapshot.DataTo(&stored); err != nil {
		return model.Word{}, fmt.Errorf("decode Firestore word: %w", err)
	}
	if _, err := ref.Update(ctx, []firestore.Update{
		{Path: "safe", Value: safe},
		{Path: "updated_at", Value: time.Now().UTC()},
	}); err != nil {
		return model.Word{}, fmt.Errorf("set Firestore word safety: %w", err)
	}
	return model.Word{
		ID:       id,
		Value:    stored.Value,
		Category: stored.Category,
		Status:   stored.Status,
		Safe:     safe,
	}, nil
}

func (f *Firestore) DeleteWord(ctx context.Context, id string) (bool, error) {
	removed := false
	err := f.client.RunTransaction(ctx, func(_ context.Context, tx *firestore.Transaction) error {
		ref := f.collection.Doc(id)
		snapshot, err := tx.Get(ref)
		if status.Code(err) == codes.NotFound {
			return nil
		}
		if err != nil {
			return err
		}
		var stored firestoreWord
		if err := snapshot.DataTo(&stored); err != nil {
			return err
		}
		if stored.Status == model.StatusApproved {
			query := f.collection.
				Where("category", "==", stored.Category).
				Where("status", "==", model.StatusApproved).
				Limit(2)
			documents, err := tx.Documents(query).GetAll()
			if err != nil {
				return err
			}
			if len(documents) <= 1 {
				return model.ErrLastWord
			}
		}
		if err := tx.Delete(ref); err != nil {
			return err
		}
		removed = true
		return nil
	})
	if err != nil {
		if errors.Is(err, model.ErrLastWord) {
			return false, err
		}
		return false, fmt.Errorf("delete Firestore word: %w", err)
	}
	return removed, nil
}

func (f *Firestore) ListWords(
	ctx context.Context,
	category, wordStatus string,
	limit int,
	pageToken string,
) (model.WordPage, error) {
	if err := model.ValidateList(category, wordStatus, limit); err != nil {
		return model.WordPage{}, err
	}
	cursor, err := decodeCursor(pageToken, "firestore", category, wordStatus)
	if err != nil {
		return model.WordPage{}, err
	}

	query := f.collection.Where("category", "==", category)
	if wordStatus != "" {
		query = query.Where("status", "==", wordStatus)
	}
	query = query.
		OrderBy("value", firestore.Asc).
		OrderBy(firestore.DocumentID, firestore.Asc)
	if cursor.Value != "" {
		query = query.StartAfter(cursor.Value, f.collection.Doc(cursor.ID))
	}
	documents, err := query.Limit(limit + 1).Documents(ctx).GetAll()
	if err != nil {
		return model.WordPage{}, fmt.Errorf("list Firestore words: %w", err)
	}

	words := make([]model.Word, 0, len(documents))
	for _, document := range documents {
		var stored firestoreWord
		if err := document.DataTo(&stored); err != nil {
			return model.WordPage{}, fmt.Errorf("decode Firestore word: %w", err)
		}
		words = append(words, model.Word{
			ID:       document.Ref.ID,
			Value:    stored.Value,
			Category: stored.Category,
			Status:   stored.Status,
			Safe:     stored.Safe,
		})
	}

	page := model.WordPage{Words: words}
	if len(words) > limit {
		last := words[limit-1]
		page.Words = words[:limit]
		page.NextPageToken, err = encodeCursor(backendCursor{
			Driver:   "firestore",
			Category: category,
			Status:   wordStatus,
			Value:    last.Value,
			ID:       last.ID,
		})
		if err != nil {
			return model.WordPage{}, err
		}
	}
	return page, nil
}

func (f *Firestore) Ready(ctx context.Context) error {
	for _, category := range []string{model.CategoryNouns, model.CategoryAdjectives} {
		_, err := f.collection.
			Where("category", "==", category).
			Where("status", "==", model.StatusApproved).
			Select().
			Limit(1).
			Documents(ctx).
			Next()
		if errors.Is(err, iterator.Done) {
			return model.ErrNoWords
		}
		if err != nil {
			return fmt.Errorf("check Firestore word catalog: %w", err)
		}
	}
	return nil
}

func (f *Firestore) Close(context.Context) error {
	return f.client.Close()
}

func randomFraction() (float64, error) {
	var data [8]byte
	if _, err := rand.Read(data[:]); err != nil {
		return 0, fmt.Errorf("generate random word position: %w", err)
	}
	return float64(binary.BigEndian.Uint64(data[:])) / float64(math.MaxUint64), nil
}

var _ Store = (*Firestore)(nil)
