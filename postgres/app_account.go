// Package postgres provides a PostgreSQL-backed app-account mapping for the Tuya
// library: the owner → Tuya-UID link a consumer resolves (via
// tuya.AppAccountClient) before driving devices.
package postgres

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"go.naturallyfunny.dev/tuya"
)

//go:embed migrations
var migrationFiles embed.FS

// Querier is the subset of *pgxpool.Pool / *pgx.Conn / *pgx.Tx that Store
// needs, so consumers can inject any of them (including test doubles).
type Querier interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// AppAccountStore maps an owner to the human's Tuya app-account UID, backed by
// PostgreSQL. It owns the full lifecycle of that mapping: Get reads it, Link
// creates or refreshes it, and Unlink soft-deletes it. A consumer links an
// account once (after the human authorizes Tuya), then drives devices by owner
// via tuya.AppAccountClient, which resolves owner -> UID with Get. That is the
// app-account tenancy model; Tuya's spatial model needs no such mapping, and
// will bring its own SpaceStore rather than widening this one.
type AppAccountStore struct {
	db          Querier
	autoMigrate bool
}

// Compile-time proof this satisfies the interface the root package declares. It
// is the whole point of the type, and a signature drift should fail the build
// here rather than at a consumer's wiring.
var _ tuya.AppAccountStore = (*AppAccountStore)(nil)

// Option configures an AppAccountStore.
type Option func(*AppAccountStore)

// WithAutoMigrate runs pending schema migrations when NewAppAccountStore is called.
func WithAutoMigrate() Option {
	return func(s *AppAccountStore) {
		s.autoMigrate = true
	}
}

// NewAppAccountStore builds an AppAccountStore over db. Pass WithAutoMigrate() to apply
// pending schema migrations on startup; otherwise the caller is responsible
// for running migrations before the store is used.
func NewAppAccountStore(ctx context.Context, db Querier, opts ...Option) (*AppAccountStore, error) {
	if db == nil {
		panic("postgres: NewAppAccountStore called with nil Querier")
	}
	s := &AppAccountStore{db: db}
	for _, opt := range opts {
		opt(s)
	}
	if s.autoMigrate {
		if err := s.migrate(ctx); err != nil {
			return nil, fmt.Errorf("postgres: auto-migrate: %w", err)
		}
	} else if err := s.validateSchema(ctx); err != nil {
		// Only meaningful when we did not migrate: catches a consumer that
		// forgot to run migrations. After auto-migrate the schema is guaranteed.
		return nil, err
	}
	return s, nil
}

// Get returns the full Account linked to owner, or tuya.ErrAccountNotLinked if
// none is linked.
func (s *AppAccountStore) Get(ctx context.Context, owner string) (tuya.AppAccount, error) {
	rows, err := s.db.Query(ctx,
		`SELECT owner, tuya_uid, created_at, updated_at FROM tuya_app_accounts WHERE owner = $1 AND deleted_at IS NULL`,
		owner,
	)
	if err != nil {
		return tuya.AppAccount{}, fmt.Errorf("get account: %w", err)
	}
	acc, err := pgx.CollectOneRow(rows, func(row pgx.CollectableRow) (tuya.AppAccount, error) {
		var a tuya.AppAccount
		return a, row.Scan(&a.Owner, &a.TuyaUID, &a.CreatedAt, &a.UpdatedAt)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return tuya.AppAccount{}, tuya.ErrAccountNotLinked
	}
	if err != nil {
		return tuya.AppAccount{}, fmt.Errorf("get account: %w", err)
	}
	return acc, nil
}

// Link records that owner maps to tuyaUID, returning the resulting Account. It
// is an upsert: linking an owner that is already linked refreshes the UID and
// updated_at, and re-linking a previously unlinked owner revives the row
// (clearing deleted_at) rather than failing on the primary key.
func (s *AppAccountStore) Link(ctx context.Context, owner, tuyaUID string) (tuya.AppAccount, error) {
	rows, err := s.db.Query(ctx,
		`INSERT INTO tuya_app_accounts (owner, tuya_uid)
		 VALUES ($1, $2)
		 ON CONFLICT (owner) DO UPDATE
		   SET tuya_uid = EXCLUDED.tuya_uid, updated_at = NOW(), deleted_at = NULL
		 RETURNING owner, tuya_uid, created_at, updated_at`,
		owner, tuyaUID,
	)
	if err != nil {
		return tuya.AppAccount{}, fmt.Errorf("link account: %w", err)
	}
	acc, err := pgx.CollectOneRow(rows, func(row pgx.CollectableRow) (tuya.AppAccount, error) {
		var a tuya.AppAccount
		return a, row.Scan(&a.Owner, &a.TuyaUID, &a.CreatedAt, &a.UpdatedAt)
	})
	if err != nil {
		return tuya.AppAccount{}, fmt.Errorf("link account: %w", err)
	}
	return acc, nil
}

// Unlink soft-deletes the mapping for owner (setting deleted_at), so Get stops
// returning it while the row is preserved for audit. Returns
// tuya.ErrAccountNotLinked if no live mapping exists.
func (s *AppAccountStore) Unlink(ctx context.Context, owner string) error {
	tag, err := s.db.Exec(ctx,
		`UPDATE tuya_app_accounts
		   SET deleted_at = NOW(), updated_at = NOW()
		 WHERE owner = $1 AND deleted_at IS NULL`,
		owner,
	)
	if err != nil {
		return fmt.Errorf("unlink account: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return tuya.ErrAccountNotLinked
	}
	return nil
}

// migrate applies all pending .up.sql migrations in order, skipping any that
// have already been recorded in tuya_schema_migrations.
func (s *AppAccountStore) migrate(ctx context.Context) error {
	if _, err := s.db.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS tuya_schema_migrations (
			version    TEXT        PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`); err != nil {
		return fmt.Errorf("postgres: create migrations table: %w", err)
	}
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("postgres: read migrations: %w", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		rows, err := s.db.Query(ctx,
			`SELECT EXISTS(SELECT 1 FROM tuya_schema_migrations WHERE version = $1)`, name,
		)
		if err != nil {
			return fmt.Errorf("postgres: check migration %s: %w", name, err)
		}
		applied, err := pgx.CollectOneRow(rows, pgx.RowTo[bool])
		if err != nil {
			return fmt.Errorf("postgres: check migration %s: %w", name, err)
		}
		if applied {
			continue
		}
		content, err := migrationFiles.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("postgres: read %s: %w", name, err)
		}
		if _, err := s.db.Exec(ctx, string(content)); err != nil {
			return fmt.Errorf("postgres: execute %s: %w", name, err)
		}
		if _, err := s.db.Exec(ctx,
			`INSERT INTO tuya_schema_migrations (version) VALUES ($1)`, name,
		); err != nil {
			return fmt.Errorf("postgres: record migration %s: %w", name, err)
		}
	}
	return nil
}

func (s *AppAccountStore) validateSchema(ctx context.Context) error {
	rows, err := s.db.Query(ctx,
		`SELECT owner, tuya_uid, created_at, updated_at FROM tuya_app_accounts LIMIT 0`,
	)
	if err != nil {
		return fmt.Errorf("postgres: schema validation: %w", err)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("postgres: schema validation: %w", err)
	}
	return nil
}
