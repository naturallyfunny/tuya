package firestore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"go.naturallyfunny.dev/tuya"
	"go.naturallyfunny.dev/tuya/cloud"
)

const DefaultSpaceCollection = "tuya_space_tenants"

type tenantDoc struct {
	RootSpaceID cloud.SpaceID `firestore:"root_space_id"`
	CreatedAt   time.Time     `firestore:"created_at"`
	UpdatedAt   time.Time     `firestore:"updated_at"`
	DeletedAt   *time.Time    `firestore:"deleted_at"`
}

func (d tenantDoc) tenant(owner string) tuya.SpaceTenant {
	return tuya.SpaceTenant{
		Owner:       owner,
		RootSpaceID: d.RootSpaceID,
		CreatedAt:   d.CreatedAt,
		UpdatedAt:   d.UpdatedAt,
	}
}

type SpaceStore struct {
	client     *firestore.Client
	collection string
}

var _ tuya.SpaceStore = (*SpaceStore)(nil)

func NewSpaceStore(client *firestore.Client, opts ...Option) *SpaceStore {
	if client == nil {
		panic("firestore: NewSpaceStore called with nil client")
	}
	return &SpaceStore{
		client:     client,
		collection: collectionOr(DefaultSpaceCollection, opts),
	}
}

func (s *SpaceStore) Get(ctx context.Context, owner string) (tuya.SpaceTenant, error) {
	ref, err := s.doc(owner)
	if err != nil {
		return tuya.SpaceTenant{}, err
	}
	snap, err := ref.Get(ctx)
	if status.Code(err) == codes.NotFound {
		return tuya.SpaceTenant{}, tuya.ErrSpaceNotLinked
	}
	if err != nil {
		return tuya.SpaceTenant{}, fmt.Errorf("get space tenant: %w", err)
	}
	var doc tenantDoc
	if err := snap.DataTo(&doc); err != nil {
		return tuya.SpaceTenant{}, fmt.Errorf("get space tenant: decode %q: %w", owner, err)
	}
	if doc.DeletedAt != nil {
		return tuya.SpaceTenant{}, tuya.ErrSpaceNotLinked
	}
	return doc.tenant(owner), nil
}

func (s *SpaceStore) Link(ctx context.Context, owner string, rootSpaceID cloud.SpaceID) (tuya.SpaceTenant, error) {
	if rootSpaceID == 0 {
		return tuya.SpaceTenant{}, errors.New("firestore: root space id is zero")
	}
	ref, err := s.doc(owner)
	if err != nil {
		return tuya.SpaceTenant{}, err
	}
	var tenant tuya.SpaceTenant
	err = s.client.RunTransaction(ctx, func(_ context.Context, tx *firestore.Transaction) error {
		now := time.Now().UTC()
		doc := tenantDoc{RootSpaceID: rootSpaceID, CreatedAt: now, UpdatedAt: now}
		snap, err := tx.Get(ref)
		switch {
		case status.Code(err) == codes.NotFound:
		case err != nil:
			return err
		default:
			var prev tenantDoc
			if err := snap.DataTo(&prev); err != nil {
				return fmt.Errorf("decode %q: %w", owner, err)
			}
			doc.CreatedAt = prev.CreatedAt
		}
		tenant = doc.tenant(owner)
		return tx.Set(ref, doc)
	})
	if err != nil {
		return tuya.SpaceTenant{}, fmt.Errorf("link space tenant: %w", err)
	}
	return tenant, nil
}

func (s *SpaceStore) Unlink(ctx context.Context, owner string) error {
	ref, err := s.doc(owner)
	if err != nil {
		return err
	}
	err = s.client.RunTransaction(ctx, func(_ context.Context, tx *firestore.Transaction) error {
		snap, err := tx.Get(ref)
		if status.Code(err) == codes.NotFound {
			return tuya.ErrSpaceNotLinked
		}
		if err != nil {
			return err
		}
		var doc tenantDoc
		if err := snap.DataTo(&doc); err != nil {
			return fmt.Errorf("decode %q: %w", owner, err)
		}
		if doc.DeletedAt != nil {
			return tuya.ErrSpaceNotLinked
		}
		now := time.Now().UTC()
		return tx.Update(ref, []firestore.Update{
			{Path: "deleted_at", Value: now},
			{Path: "updated_at", Value: now},
		})
	})
	if errors.Is(err, tuya.ErrSpaceNotLinked) {
		return tuya.ErrSpaceNotLinked
	}
	if err != nil {
		return fmt.Errorf("unlink space tenant: %w", err)
	}
	return nil
}

func (s *SpaceStore) doc(owner string) (*firestore.DocumentRef, error) {
	if err := validateOwner(owner); err != nil {
		return nil, err
	}
	return s.client.Collection(s.collection).Doc(owner), nil
}
