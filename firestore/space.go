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
)

const DefaultSpaceCollection = "tuya_spaces"

type spaceDoc struct {
	SpaceID   int64      `firestore:"space_id"`
	CreatedAt time.Time  `firestore:"created_at"`
	UpdatedAt time.Time  `firestore:"updated_at"`
	DeletedAt *time.Time `firestore:"deleted_at"`
}

func (d spaceDoc) space(owner string) tuya.Space {
	return tuya.Space{
		Owner:     owner,
		SpaceID:   d.SpaceID,
		CreatedAt: d.CreatedAt,
		UpdatedAt: d.UpdatedAt,
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

func (s *SpaceStore) Get(ctx context.Context, owner string) (tuya.Space, error) {
	ref, err := s.doc(owner)
	if err != nil {
		return tuya.Space{}, err
	}
	snap, err := ref.Get(ctx)
	if status.Code(err) == codes.NotFound {
		return tuya.Space{}, tuya.ErrSpaceNotLinked
	}
	if err != nil {
		return tuya.Space{}, fmt.Errorf("get space: %w", err)
	}
	var doc spaceDoc
	if err := snap.DataTo(&doc); err != nil {
		return tuya.Space{}, fmt.Errorf("get space: decode %q: %w", owner, err)
	}
	if doc.DeletedAt != nil {
		return tuya.Space{}, tuya.ErrSpaceNotLinked
	}
	return doc.space(owner), nil
}

func (s *SpaceStore) Link(ctx context.Context, owner string, spaceID int64) (tuya.Space, error) {
	if spaceID == 0 {
		return tuya.Space{}, errors.New("firestore: space id is zero")
	}
	ref, err := s.doc(owner)
	if err != nil {
		return tuya.Space{}, err
	}
	var space tuya.Space
	err = s.client.RunTransaction(ctx, func(_ context.Context, tx *firestore.Transaction) error {
		now := time.Now().UTC()
		doc := spaceDoc{SpaceID: spaceID, CreatedAt: now, UpdatedAt: now}
		snap, err := tx.Get(ref)
		switch {
		case status.Code(err) == codes.NotFound:
		case err != nil:
			return err
		default:
			var prev spaceDoc
			if err := snap.DataTo(&prev); err != nil {
				return fmt.Errorf("decode %q: %w", owner, err)
			}
			doc.CreatedAt = prev.CreatedAt
		}
		space = doc.space(owner)
		return tx.Set(ref, doc)
	})
	if err != nil {
		return tuya.Space{}, fmt.Errorf("link space: %w", err)
	}
	return space, nil
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
		var doc spaceDoc
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
		return fmt.Errorf("unlink space: %w", err)
	}
	return nil
}

func (s *SpaceStore) doc(owner string) (*firestore.DocumentRef, error) {
	if err := validateOwner(owner); err != nil {
		return nil, err
	}
	return s.client.Collection(s.collection).Doc(owner), nil
}
