package firestore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"go.naturallyfunny.dev/tuya"
)

const DefaultAppAccountCollection = "tuya_app_accounts"

type accountDoc struct {
	TuyaUID   string     `firestore:"tuya_uid"`
	CreatedAt time.Time  `firestore:"created_at"`
	UpdatedAt time.Time  `firestore:"updated_at"`
	DeletedAt *time.Time `firestore:"deleted_at"`
}

func (d accountDoc) account(owner string) tuya.AppAccount {
	return tuya.AppAccount{
		Owner:     owner,
		TuyaUID:   d.TuyaUID,
		CreatedAt: d.CreatedAt,
		UpdatedAt: d.UpdatedAt,
	}
}

type AppAccountStore struct {
	client     *firestore.Client
	collection string
}

type options struct {
	collection string
}

type Option func(*options)

// WithCollection overrides the collection a store reads and writes. Each store
// keeps its own default, so this applies to whichever one it is passed to.
func WithCollection(name string) Option {
	return func(o *options) { o.collection = name }
}

func collectionOr(fallback string, opts []Option) string {
	cfg := options{collection: fallback}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.collection == "" {
		return fallback
	}
	return cfg.collection
}

func NewAppAccountStore(client *firestore.Client, opts ...Option) *AppAccountStore {
	if client == nil {
		panic("firestore: NewAppAccountStore called with nil client")
	}
	return &AppAccountStore{
		client:     client,
		collection: collectionOr(DefaultAppAccountCollection, opts),
	}
}

func (s *AppAccountStore) Get(ctx context.Context, owner string) (tuya.AppAccount, error) {
	ref, err := s.doc(owner)
	if err != nil {
		return tuya.AppAccount{}, err
	}
	snap, err := ref.Get(ctx)
	if status.Code(err) == codes.NotFound {
		return tuya.AppAccount{}, tuya.ErrAccountNotLinked
	}
	if err != nil {
		return tuya.AppAccount{}, fmt.Errorf("get account: %w", err)
	}
	var doc accountDoc
	if err := snap.DataTo(&doc); err != nil {
		return tuya.AppAccount{}, fmt.Errorf("get account: decode %q: %w", owner, err)
	}
	if doc.DeletedAt != nil {
		return tuya.AppAccount{}, tuya.ErrAccountNotLinked
	}
	return doc.account(owner), nil
}

func (s *AppAccountStore) Link(ctx context.Context, owner, tuyaUID string) (tuya.AppAccount, error) {
	ref, err := s.doc(owner)
	if err != nil {
		return tuya.AppAccount{}, err
	}
	var acc tuya.AppAccount
	err = s.client.RunTransaction(ctx, func(_ context.Context, tx *firestore.Transaction) error {
		now := time.Now().UTC()
		doc := accountDoc{TuyaUID: tuyaUID, CreatedAt: now, UpdatedAt: now}
		snap, err := tx.Get(ref)
		switch {
		case status.Code(err) == codes.NotFound:
		case err != nil:
			return err
		default:
			var prev accountDoc
			if err := snap.DataTo(&prev); err != nil {
				return fmt.Errorf("decode %q: %w", owner, err)
			}
			doc.CreatedAt = prev.CreatedAt
		}
		acc = doc.account(owner)
		return tx.Set(ref, doc)
	})
	if err != nil {
		return tuya.AppAccount{}, fmt.Errorf("link account: %w", err)
	}
	return acc, nil
}

func (s *AppAccountStore) Unlink(ctx context.Context, owner string) error {
	ref, err := s.doc(owner)
	if err != nil {
		return err
	}
	err = s.client.RunTransaction(ctx, func(_ context.Context, tx *firestore.Transaction) error {
		snap, err := tx.Get(ref)
		if status.Code(err) == codes.NotFound {
			return tuya.ErrAccountNotLinked
		}
		if err != nil {
			return err
		}
		var doc accountDoc
		if err := snap.DataTo(&doc); err != nil {
			return fmt.Errorf("decode %q: %w", owner, err)
		}
		if doc.DeletedAt != nil {
			return tuya.ErrAccountNotLinked
		}
		now := time.Now().UTC()
		return tx.Update(ref, []firestore.Update{
			{Path: "deleted_at", Value: now},
			{Path: "updated_at", Value: now},
		})
	})
	if errors.Is(err, tuya.ErrAccountNotLinked) {
		return tuya.ErrAccountNotLinked
	}
	if err != nil {
		return fmt.Errorf("unlink account: %w", err)
	}
	return nil
}

func (s *AppAccountStore) doc(owner string) (*firestore.DocumentRef, error) {
	if err := validateOwner(owner); err != nil {
		return nil, err
	}
	return s.client.Collection(s.collection).Doc(owner), nil
}

func validateOwner(owner string) error {
	switch {
	case owner == "":
		return errors.New("firestore: owner is empty")
	case owner == "." || owner == "..":
		return fmt.Errorf("firestore: owner %q is a reserved document ID", owner)
	case strings.Contains(owner, "/"):
		return fmt.Errorf("firestore: owner %q contains '/', not allowed in a document ID", owner)
	case len(owner) > 1500:
		return fmt.Errorf("firestore: owner exceeds Firestore's 1500-byte document ID limit (%d bytes)", len(owner))
	case len(owner) >= 4 && strings.HasPrefix(owner, "__") && strings.HasSuffix(owner, "__"):
		return fmt.Errorf("firestore: owner %q matches Firestore's reserved __*__ document ID pattern", owner)
	}
	return nil
}

var _ tuya.AppAccountStore = (*AppAccountStore)(nil)
