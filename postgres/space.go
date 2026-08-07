package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"go.naturallyfunny.dev/tuya"
	"go.naturallyfunny.dev/tuya/cloud"
)

type SpaceStore struct {
	db Querier
}

var _ tuya.SpaceStore = (*SpaceStore)(nil)

func NewSpaceStore(ctx context.Context, db Querier, opts ...Option) (*SpaceStore, error) {
	if db == nil {
		panic("postgres: NewSpaceStore called with nil Querier")
	}
	s := &SpaceStore{db: db}
	if err := prepareSchema(ctx, db, opts, s.validateSchema); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *SpaceStore) Get(ctx context.Context, owner string) (tuya.SpaceTenant, error) {
	rows, err := s.db.Query(ctx,
		`SELECT owner, root_space_id, created_at, updated_at FROM tuya_space_tenants WHERE owner = $1 AND deleted_at IS NULL`,
		owner,
	)
	if err != nil {
		return tuya.SpaceTenant{}, fmt.Errorf("get space tenant: %w", err)
	}
	tenant, err := pgx.CollectOneRow(rows, scanTenant)
	if errors.Is(err, pgx.ErrNoRows) {
		return tuya.SpaceTenant{}, tuya.ErrSpaceNotLinked
	}
	if err != nil {
		return tuya.SpaceTenant{}, fmt.Errorf("get space tenant: %w", err)
	}
	return tenant, nil
}

func (s *SpaceStore) Link(ctx context.Context, owner string, rootSpaceID cloud.SpaceID) (tuya.SpaceTenant, error) {
	rows, err := s.db.Query(ctx,
		`INSERT INTO tuya_space_tenants (owner, root_space_id)
		 VALUES ($1, $2)
		 ON CONFLICT (owner) DO UPDATE
		   SET root_space_id = EXCLUDED.root_space_id, updated_at = NOW(), deleted_at = NULL
		 RETURNING owner, root_space_id, created_at, updated_at`,
		owner, int64(rootSpaceID),
	)
	if err != nil {
		return tuya.SpaceTenant{}, fmt.Errorf("link space tenant: %w", err)
	}
	tenant, err := pgx.CollectOneRow(rows, scanTenant)
	if err != nil {
		return tuya.SpaceTenant{}, fmt.Errorf("link space tenant: %w", err)
	}
	return tenant, nil
}

func (s *SpaceStore) Unlink(ctx context.Context, owner string) error {
	tag, err := s.db.Exec(ctx,
		`UPDATE tuya_space_tenants
		   SET deleted_at = NOW(), updated_at = NOW()
		 WHERE owner = $1 AND deleted_at IS NULL`,
		owner,
	)
	if err != nil {
		return fmt.Errorf("unlink space tenant: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return tuya.ErrSpaceNotLinked
	}
	return nil
}

// The space ID is read as an int64 and converted, rather than scanned straight
// into cloud.SpaceID: the column is a plain bigint and the named Go type is the
// domain's, not the driver's.
func scanTenant(row pgx.CollectableRow) (tuya.SpaceTenant, error) {
	var (
		tenant      tuya.SpaceTenant
		rootSpaceID int64
	)
	if err := row.Scan(&tenant.Owner, &rootSpaceID, &tenant.CreatedAt, &tenant.UpdatedAt); err != nil {
		return tuya.SpaceTenant{}, err
	}
	tenant.RootSpaceID = cloud.SpaceID(rootSpaceID)
	return tenant, nil
}

func (s *SpaceStore) validateSchema(ctx context.Context) error {
	rows, err := s.db.Query(ctx,
		`SELECT owner, root_space_id, created_at, updated_at FROM tuya_space_tenants LIMIT 0`,
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
