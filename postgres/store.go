// Package postgres provides a PostgreSQL-backed account mapping for the Tuya
// library: the owner-ID → Tuya-UID link a consumer resolves (via tuya.Client)
// before driving devices.
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
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Store maps an owner ID to the human's Tuya account UID, backed by PostgreSQL.
// It owns the full lifecycle of that mapping: Get reads it, Link creates or
// refreshes it, and Unlink soft-deletes it. A consumer links an account once
// (after the human authorizes Tuya), then drives devices by owner ID via
// tuya.Client, which resolves owner -> UID with Get.
type Store struct {
	db          Querier
	autoMigrate bool
}

// Option configures a Store.
type Option func(*Store)

// WithAutoMigrate runs pending schema migrations when NewAccountStore is called.
func WithAutoMigrate() Option {
	return func(s *Store) {
		s.autoMigrate = true
	}
}

// NewAccountStore builds a Store over db. Pass WithAutoMigrate() to apply
// pending schema migrations on startup; otherwise the caller is responsible
// for running migrations before the store is used.
func NewAccountStore(ctx context.Context, db Querier, opts ...Option) (*Store, error) {
	if db == nil {
		panic("postgres: NewAccountStore called with nil Querier")
	}
	s := &Store{db: db}
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

// Get returns the full Account linked to ownerID, or tuya.ErrAccountNotLinked if
// none is linked.
func (s *Store) Get(ctx context.Context, ownerID string) (tuya.Account, error) {
	var acc tuya.Account
	err := s.db.QueryRow(ctx,
		`SELECT owner_id, tuya_uid, created_at, updated_at FROM tuya_app_accounts WHERE owner_id = $1 AND deleted_at IS NULL`,
		ownerID,
	).Scan(&acc.OwnerID, &acc.TuyaUID, &acc.CreatedAt, &acc.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return tuya.Account{}, tuya.ErrAccountNotLinked
		}
		return tuya.Account{}, fmt.Errorf("get account: %w", err)
	}
	return acc, nil
}

// Link records that ownerID maps to tuyaUID, returning the resulting Account. It
// is an upsert: linking an owner that is already linked refreshes the UID and
// updated_at, and re-linking a previously unlinked owner revives the row
// (clearing deleted_at) rather than failing on the primary key.
func (s *Store) Link(ctx context.Context, ownerID, tuyaUID string) (tuya.Account, error) {
	var acc tuya.Account
	err := s.db.QueryRow(ctx,
		`INSERT INTO tuya_app_accounts (owner_id, tuya_uid)
		 VALUES ($1, $2)
		 ON CONFLICT (owner_id) DO UPDATE
		   SET tuya_uid = EXCLUDED.tuya_uid, updated_at = NOW(), deleted_at = NULL
		 RETURNING owner_id, tuya_uid, created_at, updated_at`,
		ownerID, tuyaUID,
	).Scan(&acc.OwnerID, &acc.TuyaUID, &acc.CreatedAt, &acc.UpdatedAt)
	if err != nil {
		return tuya.Account{}, fmt.Errorf("link account: %w", err)
	}
	return acc, nil
}

// Unlink soft-deletes the mapping for ownerID (setting deleted_at), so Get stops
// returning it while the row is preserved for audit. Returns
// tuya.ErrAccountNotLinked if no live mapping exists.
func (s *Store) Unlink(ctx context.Context, ownerID string) error {
	tag, err := s.db.Exec(ctx,
		`UPDATE tuya_app_accounts
		   SET deleted_at = NOW(), updated_at = NOW()
		 WHERE owner_id = $1 AND deleted_at IS NULL`,
		ownerID,
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
func (s *Store) migrate(ctx context.Context) error {
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
		var applied bool
		if err := s.db.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM tuya_schema_migrations WHERE version = $1)`, name,
		).Scan(&applied); err != nil {
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

func (s *Store) validateSchema(ctx context.Context) error {
	rows, err := s.db.Query(ctx,
		`SELECT owner_id, tuya_uid, created_at, updated_at FROM tuya_app_accounts LIMIT 0`,
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
