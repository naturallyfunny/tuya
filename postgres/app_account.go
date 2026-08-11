package postgres

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"go.naturallyfunny.dev/tuya/appaccount"
)

//go:embed migrations
var migrationFiles embed.FS

type Querier interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

type AppAccountStore struct {
	db Querier
}

var _ appaccount.Store = (*AppAccountStore)(nil)

type options struct {
	autoMigrate bool
}

type Option func(*options)

func WithAutoMigrate() Option {
	return func(o *options) {
		o.autoMigrate = true
	}
}

func NewAppAccountStore(ctx context.Context, db Querier, opts ...Option) (*AppAccountStore, error) {
	if db == nil {
		panic("postgres: NewAppAccountStore called with nil Querier")
	}
	s := &AppAccountStore{db: db}
	if err := prepareSchema(ctx, db, opts, "app_account", s.validateSchema); err != nil {
		return nil, err
	}
	return s, nil
}

func prepareSchema(ctx context.Context, db Querier, opts []Option, door string, validate func(context.Context) error) error {
	var cfg options
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.autoMigrate {
		if err := migrate(ctx, db, door); err != nil {
			return fmt.Errorf("postgres: auto-migrate: %w", err)
		}
		return nil
	}
	return validate(ctx)
}

func (s *AppAccountStore) Get(ctx context.Context, owner string) (appaccount.Account, error) {
	rows, err := s.db.Query(ctx,
		`SELECT owner, tuya_uid, created_at, updated_at FROM tuya_app_accounts WHERE owner = $1 AND deleted_at IS NULL`,
		owner,
	)
	if err != nil {
		return appaccount.Account{}, fmt.Errorf("get account: %w", err)
	}
	acc, err := pgx.CollectOneRow(rows, scanAppAccount)
	if errors.Is(err, pgx.ErrNoRows) {
		return appaccount.Account{}, appaccount.ErrNotLinked
	}
	if err != nil {
		return appaccount.Account{}, fmt.Errorf("get account: %w", err)
	}
	return acc, nil
}

func (s *AppAccountStore) Link(ctx context.Context, owner string, tuyaUID string) (appaccount.Account, error) {
	rows, err := s.db.Query(ctx,
		`INSERT INTO tuya_app_accounts (owner, tuya_uid)
		 VALUES ($1, $2)
		 ON CONFLICT (owner) DO UPDATE
		   SET tuya_uid = EXCLUDED.tuya_uid, updated_at = NOW(), deleted_at = NULL
		 RETURNING owner, tuya_uid, created_at, updated_at`,
		owner, tuyaUID,
	)
	if err != nil {
		return appaccount.Account{}, fmt.Errorf("link account: %w", err)
	}
	acc, err := pgx.CollectOneRow(rows, scanAppAccount)
	if err != nil {
		return appaccount.Account{}, fmt.Errorf("link account: %w", err)
	}
	return acc, nil
}

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
		return appaccount.ErrNotLinked
	}
	return nil
}

func scanAppAccount(row pgx.CollectableRow) (appaccount.Account, error) {
	var acc appaccount.Account
	if err := row.Scan(&acc.Owner, &acc.TuyaUID, &acc.CreatedAt, &acc.UpdatedAt); err != nil {
		return appaccount.Account{}, err
	}
	return acc, nil
}

func migrate(ctx context.Context, db Querier, door string) error {
	if _, err := db.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS tuya_schema_migrations (
			version    TEXT        PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`); err != nil {
		return fmt.Errorf("postgres: create migrations table: %w", err)
	}
	dir := "migrations/" + door
	entries, err := migrationFiles.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("postgres: read %s: %w", dir, err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".up.sql") {
			continue
		}
		version := door + "/" + entry.Name()
		rows, err := db.Query(ctx,
			`SELECT EXISTS(SELECT 1 FROM tuya_schema_migrations WHERE version = $1)`, version,
		)
		if err != nil {
			return fmt.Errorf("postgres: check migration %s: %w", version, err)
		}
		applied, err := pgx.CollectOneRow(rows, pgx.RowTo[bool])
		if err != nil {
			return fmt.Errorf("postgres: check migration %s: %w", version, err)
		}
		if applied {
			continue
		}
		content, err := migrationFiles.ReadFile(dir + "/" + entry.Name())
		if err != nil {
			return fmt.Errorf("postgres: read %s: %w", version, err)
		}
		if _, err := db.Exec(ctx, string(content)); err != nil {
			return fmt.Errorf("postgres: execute %s: %w", version, err)
		}
		if _, err := db.Exec(ctx,
			`INSERT INTO tuya_schema_migrations (version) VALUES ($1)`, version,
		); err != nil {
			return fmt.Errorf("postgres: record migration %s: %w", version, err)
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
