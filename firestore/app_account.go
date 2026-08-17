// Package firestore stores the links a door depends on, implementing
// appaccount.Store over Cloud Firestore. It is a persistence layer only: it
// never talks to Tuya and holds no ownership rules.
package firestore

import (
	"context"
	"fmt"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"go.naturallyfunny.dev/tuya/appaccount"
)

const defaultAppAccountCollection = "tuya_app_accounts"

type account struct {
	TuyaUID   string    `firestore:"tuya_uid"`
	CreatedAt time.Time `firestore:"created_at"`
	UpdatedAt time.Time `firestore:"updated_at"`
}

type AppAccountStore struct {
	client     *firestore.Client
	collection string
}

type AppAccountStoreOption func(*AppAccountStore)

func WithCollection(name string) AppAccountStoreOption {
	return func(s *AppAccountStore) {
		s.collection = name
	}
}

func NewAppAccountStore(client *firestore.Client, opts ...AppAccountStoreOption) *AppAccountStore {
	if client == nil {
		panic("firestore: NewAppAccountStore called with nil client")
	}
	store := &AppAccountStore{client: client, collection: defaultAppAccountCollection}
	for _, opt := range opts {
		opt(store)
	}
	return store
}

func (s *AppAccountStore) Get(ctx context.Context, owner string) (appaccount.Account, error) {
	snap, err := s.client.Collection(s.collection).Doc(owner).Get(ctx)
	if status.Code(err) == codes.NotFound {
		return appaccount.Account{}, appaccount.ErrNotLinked
	}
	if err != nil {
		return appaccount.Account{}, fmt.Errorf("get account: %w", err)
	}
	var acc account
	if err := snap.DataTo(&acc); err != nil {
		return appaccount.Account{}, fmt.Errorf("get account: decode %q: %w", owner, err)
	}
	return appaccount.Account{
		Owner:     owner,
		TuyaUID:   acc.TuyaUID,
		CreatedAt: acc.CreatedAt,
		UpdatedAt: acc.UpdatedAt,
	}, nil
}

func (s *AppAccountStore) Link(ctx context.Context, owner, tuyaUID string) (appaccount.Account, error) {
	ref := s.client.Collection(s.collection).Doc(owner)
	var acc appaccount.Account
	err := s.client.RunTransaction(ctx, func(_ context.Context, tx *firestore.Transaction) error {
		now := time.Now().UTC()
		createdAt := now
		snap, err := tx.Get(ref)
		if err != nil && status.Code(err) != codes.NotFound {
			return err
		}
		if err == nil {
			var prev account
			if err := snap.DataTo(&prev); err != nil {
				return fmt.Errorf("decode %q: %w", owner, err)
			}
			createdAt = prev.CreatedAt
		}
		acc = appaccount.Account{
			Owner:     owner,
			TuyaUID:   tuyaUID,
			CreatedAt: createdAt,
			UpdatedAt: now,
		}
		return tx.Set(ref, account{TuyaUID: tuyaUID, CreatedAt: createdAt, UpdatedAt: now})
	})
	if err != nil {
		return appaccount.Account{}, fmt.Errorf("link account: %w", err)
	}
	return acc, nil
}

func (s *AppAccountStore) Unlink(ctx context.Context, owner string) error {
	_, err := s.client.Collection(s.collection).Doc(owner).Delete(ctx, firestore.Exists)
	if status.Code(err) == codes.NotFound {
		return appaccount.ErrNotLinked
	}
	if err != nil {
		return fmt.Errorf("unlink account: %w", err)
	}
	return nil
}
