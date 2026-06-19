// Package postgres provides a PostgreSQL-backed account mapping for the Tuya
// library: the owner-ID → Tuya-UID link a consumer resolves before driving
// devices via tuya.IoT.
package postgres

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

//go:embed migrations
var migrationFiles embed.FS

// ErrAccountNotLinked indicates the owner has no Tuya account linked, i.e. there
// is no owner-ID → Tuya-UID mapping. Get returns it when no row exists, so
// consumers can route the human into the account-linking flow.
var ErrAccountNotLinked = errors.New("tuya: no tuya account linked to owner")

// Account is the link between an opaque owner ID (whatever the consumer uses to
// identify a human) and that human's Tuya account UID. Devices are listed and
// controlled under the UID.
type Account struct {
	OwnerID   string    `json:"owner_id"`
	TuyaUID   string    `json:"tuya_uid"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Querier is the subset of *pgxpool.Pool / *pgx.Conn / *pgx.Tx that Store
// needs, so consumers can inject any of them (including test doubles).
type Querier interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Store maps an owner ID to the human's Tuya account UID, backed by PostgreSQL.
// Linking and unlinking accounts (writing rows) is the consumer's
// responsibility; this store only reads the mapping the consumer needs to
// resolve an owner before calling tuya.IoT.
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
	}
	if err := s.validateSchema(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// Get returns the full Account linked to ownerID, or ErrAccountNotLinked if
// none is linked.
func (s *Store) Get(ctx context.Context, ownerID string) (Account, error) {
	var acc Account
	err := s.db.QueryRow(ctx,
		`SELECT owner_id, tuya_uid, created_at, updated_at FROM tuya_app_accounts WHERE owner_id = $1 AND deleted_at IS NULL`,
		ownerID,
	).Scan(&acc.OwnerID, &acc.TuyaUID, &acc.CreatedAt, &acc.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Account{}, ErrAccountNotLinked
		}
		return Account{}, fmt.Errorf("get account: %w", err)
	}
	return acc, nil
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
