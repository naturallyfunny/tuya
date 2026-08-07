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

func (s *SpaceStore) Get(ctx context.Context, owner tuya.Owner) (tuya.Space, error) {
	rows, err := s.db.Query(ctx,
		`SELECT owner, space_id, created_at, updated_at FROM tuya_spaces WHERE owner = $1 AND deleted_at IS NULL`,
		string(owner),
	)
	if err != nil {
		return tuya.Space{}, fmt.Errorf("get space: %w", err)
	}
	space, err := pgx.CollectOneRow(rows, scanSpace)
	if errors.Is(err, pgx.ErrNoRows) {
		return tuya.Space{}, tuya.ErrSpaceNotLinked
	}
	if err != nil {
		return tuya.Space{}, fmt.Errorf("get space: %w", err)
	}
	return space, nil
}

func (s *SpaceStore) Link(ctx context.Context, owner tuya.Owner, spaceID cloud.SpaceID) (tuya.Space, error) {
	rows, err := s.db.Query(ctx,
		`INSERT INTO tuya_spaces (owner, space_id)
		 VALUES ($1, $2)
		 ON CONFLICT (owner) DO UPDATE
		   SET space_id = EXCLUDED.space_id, updated_at = NOW(), deleted_at = NULL
		 RETURNING owner, space_id, created_at, updated_at`,
		string(owner), int64(spaceID),
	)
	if err != nil {
		return tuya.Space{}, fmt.Errorf("link space: %w", err)
	}
	space, err := pgx.CollectOneRow(rows, scanSpace)
	if err != nil {
		return tuya.Space{}, fmt.Errorf("link space: %w", err)
	}
	return space, nil
}

func (s *SpaceStore) Unlink(ctx context.Context, owner tuya.Owner) error {
	tag, err := s.db.Exec(ctx,
		`UPDATE tuya_spaces
		   SET deleted_at = NOW(), updated_at = NOW()
		 WHERE owner = $1 AND deleted_at IS NULL`,
		string(owner),
	)
	if err != nil {
		return fmt.Errorf("unlink space: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return tuya.ErrSpaceNotLinked
	}
	return nil
}

func scanSpace(row pgx.CollectableRow) (tuya.Space, error) {
	var (
		space   tuya.Space
		owner   string
		spaceID int64
	)
	if err := row.Scan(&owner, &spaceID, &space.CreatedAt, &space.UpdatedAt); err != nil {
		return tuya.Space{}, err
	}
	space.Owner = tuya.Owner(owner)
	space.SpaceID = cloud.SpaceID(spaceID)
	return space, nil
}

func (s *SpaceStore) validateSchema(ctx context.Context) error {
	rows, err := s.db.Query(ctx,
		`SELECT owner, space_id, created_at, updated_at FROM tuya_spaces LIMIT 0`,
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
