package wordstore

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"strings"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tehbooom/project_name/internal/model"
)

//go:embed migrations/001_words.sql
var postgresSchema string

type Postgres struct {
	pool *pgxpool.Pool
}

func NewPostgres(ctx context.Context, dsn string, autoMigrate bool) (*Postgres, error) {
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse PostgreSQL word store configuration: %w", err)
	}
	config.ConnConfig.Tracer = otelpgx.NewTracer()

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("configure PostgreSQL word store: %w", err)
	}
	if err := otelpgx.RecordStats(pool); err != nil {
		pool.Close()
		return nil, fmt.Errorf("record PostgreSQL pool metrics: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect PostgreSQL word store: %w", err)
	}
	store := &Postgres{pool: pool}
	if autoMigrate {
		if _, err := pool.Exec(ctx, postgresSchema); err != nil {
			pool.Close()
			return nil, fmt.Errorf("migrate PostgreSQL word store: %w", err)
		}
	}
	return store, nil
}

func (p *Postgres) Seed(ctx context.Context, records []model.Word) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin PostgreSQL seed: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	for _, word := range records {
		if _, err := tx.Exec(
			ctx,
			`INSERT INTO words (id, value, category, status, safe)
			 VALUES ($1, $2, $3, $4, $5)
			 ON CONFLICT (category, value) DO UPDATE
			 SET safe = words.safe AND EXCLUDED.safe`,
			word.ID,
			word.Value,
			word.Category,
			word.Status,
			word.Safe,
		); err != nil {
			return fmt.Errorf("seed PostgreSQL word %q: %w", word.Value, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit PostgreSQL seed: %w", err)
	}
	return nil
}

func (p *Postgres) GetWords(ctx context.Context, constraints model.NameConstraints) (string, error) {
	noun, err := p.randomApprovedWord(
		ctx,
		model.CategoryNouns,
		constraints.MaxNounLength,
		!constraints.Unsafe,
	)
	if err != nil {
		return "", err
	}
	adjective, err := p.randomApprovedWord(
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

func (p *Postgres) randomApprovedWord(
	ctx context.Context,
	category string,
	maxLength int,
	safe bool,
) (string, error) {
	var value string
	err := p.pool.QueryRow(
		ctx,
		`SELECT value FROM words
		 WHERE category = $1
		   AND status = $2
		   AND ($3 = 0 OR char_length(value) <= $3)
		   AND safe = $4
		 ORDER BY random()
		 LIMIT 1`,
		category,
		model.StatusApproved,
		maxLength,
		safe,
	).Scan(&value)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", model.ErrNoMatchingWords
	}
	if err != nil {
		return "", fmt.Errorf("select random %s word: %w", category, err)
	}
	return value, nil
}

func (p *Postgres) CreateProposal(
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
	id, err := model.NewWordID()
	if err != nil {
		return model.Word{}, err
	}

	word := model.Word{
		ID:       id,
		Value:    value,
		Category: category,
		Status:   model.StatusProposed,
		Safe:     true,
	}
	_, err = p.pool.Exec(
		ctx,
		`INSERT INTO words (id, value, category, status, safe) VALUES ($1, $2, $3, $4, $5)`,
		word.ID,
		word.Value,
		word.Category,
		word.Status,
		word.Safe,
	)
	if isUniqueViolation(err) {
		return model.Word{}, fmt.Errorf("%w: %q", model.ErrDuplicateWord, value)
	}
	if err != nil {
		return model.Word{}, fmt.Errorf("create PostgreSQL word proposal: %w", err)
	}
	return word, nil
}

func (p *Postgres) ApproveWord(ctx context.Context, id string) (model.Word, error) {
	var word model.Word
	err := p.pool.QueryRow(
		ctx,
		`UPDATE words SET status = $2, updated_at = now()
		 WHERE id = $1
		 RETURNING id, value, category, status, safe`,
		id,
		model.StatusApproved,
	).Scan(&word.ID, &word.Value, &word.Category, &word.Status, &word.Safe)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Word{}, model.ErrWordNotFound
	}
	if err != nil {
		return model.Word{}, fmt.Errorf("approve PostgreSQL word: %w", err)
	}
	return word, nil
}

func (p *Postgres) SetWordSafety(
	ctx context.Context,
	id string,
	safe bool,
) (model.Word, error) {
	var word model.Word
	err := p.pool.QueryRow(
		ctx,
		`UPDATE words SET safe = $2, updated_at = now()
		 WHERE id = $1
		 RETURNING id, value, category, status, safe`,
		id,
		safe,
	).Scan(&word.ID, &word.Value, &word.Category, &word.Status, &word.Safe)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Word{}, model.ErrWordNotFound
	}
	if err != nil {
		return model.Word{}, fmt.Errorf("set PostgreSQL word safety: %w", err)
	}
	return word, nil
}

func (p *Postgres) DeleteWord(ctx context.Context, id string) (bool, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin PostgreSQL delete: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	var category, status string
	err = tx.QueryRow(
		ctx,
		`SELECT category, status FROM words WHERE id = $1 FOR UPDATE`,
		id,
	).Scan(&category, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("find PostgreSQL word: %w", err)
	}

	if status == model.StatusApproved {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, category); err != nil {
			return false, fmt.Errorf("lock PostgreSQL word category: %w", err)
		}
		var count int
		if err := tx.QueryRow(
			ctx,
			`SELECT count(*) FROM words WHERE category = $1 AND status = $2`,
			category,
			model.StatusApproved,
		).Scan(&count); err != nil {
			return false, fmt.Errorf("count PostgreSQL words: %w", err)
		}
		if count <= 1 {
			return false, model.ErrLastWord
		}
	}

	if _, err := tx.Exec(ctx, `DELETE FROM words WHERE id = $1`, id); err != nil {
		return false, fmt.Errorf("delete PostgreSQL word: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit PostgreSQL delete: %w", err)
	}
	return true, nil
}

func (p *Postgres) ListWords(
	ctx context.Context,
	category, status string,
	limit int,
	pageToken string,
) (model.WordPage, error) {
	if err := model.ValidateList(category, status, limit); err != nil {
		return model.WordPage{}, err
	}
	cursor, err := decodeCursor(pageToken, "postgres", category, status)
	if err != nil {
		return model.WordPage{}, err
	}

	conditions := []string{"category = $1"}
	args := []any{category}
	if status != "" {
		args = append(args, status)
		conditions = append(conditions, fmt.Sprintf("status = $%d", len(args)))
	}
	if cursor.Value != "" {
		args = append(args, cursor.Value, cursor.ID)
		conditions = append(
			conditions,
			fmt.Sprintf("(value, id) > ($%d, $%d)", len(args)-1, len(args)),
		)
	}
	args = append(args, limit+1)
	query := fmt.Sprintf(
		`SELECT id, value, category, status, safe FROM words
		 WHERE %s
		 ORDER BY value, id
		 LIMIT $%d`,
		strings.Join(conditions, " AND "),
		len(args),
	)

	rows, err := p.pool.Query(ctx, query, args...)
	if err != nil {
		return model.WordPage{}, fmt.Errorf("list PostgreSQL words: %w", err)
	}
	defer rows.Close()

	words := make([]model.Word, 0, limit+1)
	for rows.Next() {
		var word model.Word
		if err := rows.Scan(
			&word.ID,
			&word.Value,
			&word.Category,
			&word.Status,
			&word.Safe,
		); err != nil {
			return model.WordPage{}, fmt.Errorf("scan PostgreSQL word: %w", err)
		}
		words = append(words, word)
	}
	if err := rows.Err(); err != nil {
		return model.WordPage{}, fmt.Errorf("iterate PostgreSQL words: %w", err)
	}

	page := model.WordPage{Words: words}
	if len(words) > limit {
		last := words[limit-1]
		page.Words = words[:limit]
		page.NextPageToken, err = encodeCursor(backendCursor{
			Driver:   "postgres",
			Category: category,
			Status:   status,
			Value:    last.Value,
			ID:       last.ID,
		})
		if err != nil {
			return model.WordPage{}, err
		}
	}
	return page, nil
}

func (p *Postgres) Ready(ctx context.Context) error {
	if err := p.pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping PostgreSQL word store: %w", err)
	}
	var ready bool
	if err := p.pool.QueryRow(
		ctx,
		`SELECT count(DISTINCT category) = 2
		 FROM words
		 WHERE status = $1`,
		model.StatusApproved,
	).Scan(&ready); err != nil {
		return fmt.Errorf("check PostgreSQL word catalog: %w", err)
	}
	if !ready {
		return model.ErrNoWords
	}
	return nil
}

func (p *Postgres) Close(context.Context) error {
	p.pool.Close()
	return nil
}

func isUniqueViolation(err error) bool {
	var pgError *pgconn.PgError
	return errors.As(err, &pgError) && pgError.Code == "23505"
}

var _ Store = (*Postgres)(nil)
