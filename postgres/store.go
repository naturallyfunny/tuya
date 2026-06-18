// Package postgres provides a PostgreSQL-backed implementation of
// tuya.Repository: the owner-ID → Tuya-UID account mapping.
package postgres

import (
	"context"
	"embed"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"go.naturallyfunny.dev/tuya"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Store implements tuya.Repository backed by PostgreSQL, mapping an owner ID to
// the human's Tuya account UID. Linking and unlinking accounts (writing rows)
// is the consumer's responsibility; this store only reads the mapping the
// library needs.
type Store struct {
	pool *pgxpool.Pool
	dsn  string
}

// New builds a Store over pool. dsn is held for Migrate (which needs a
// connection string, not a pool).
func New(pool *pgxpool.Pool, dsn string) *Store {
	return &Store{pool: pool, dsn: dsn}
}

// GetTuyaUID returns the Tuya account UID linked to ownerID, or
// tuya.ErrAccountNotLinked if none is linked.
func (s *Store) GetTuyaUID(ctx context.Context, ownerID string) (string, error) {
	var tuyaUID string
	err := s.pool.QueryRow(ctx,
		`SELECT tuya_uid FROM tuya_app_accounts WHERE owner_id = $1 AND deleted_at IS NULL`,
		ownerID,
	).Scan(&tuyaUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", tuya.ErrAccountNotLinked
		}
		return "", fmt.Errorf("get tuya uid: %w", err)
	}
	return tuyaUID, nil
}

// Get returns the full Account linked to ownerID, or tuya.ErrAccountNotLinked if
// none is linked.
func (s *Store) Get(ctx context.Context, ownerID string) (tuya.Account, error) {
	var acc tuya.Account
	err := s.pool.QueryRow(ctx,
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

// Migrate runs all pending database migrations.
func (s *Store) Migrate() error {
	src, err := iofs.New(migrations, "migrations")
	if err != nil {
		return fmt.Errorf("migrations source: %w", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, s.dsn)
	if err != nil {
		return fmt.Errorf("migrate init: %w", err)
	}
	defer m.Close()

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}
