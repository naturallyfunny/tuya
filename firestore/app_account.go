// Package firestore stores the links a door depends on, implementing
// appaccount.Store over Cloud Firestore. It is a persistence layer only: it
// never talks to Tuya and holds no ownership rules.
package firestore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"go.naturallyfunny.dev/tuya/appaccount"
)

const appAccountCollection = "tuya_app_accounts"

type account struct {
	TuyaUID   string     `firestore:"tuya_uid"`
	CreatedAt time.Time  `firestore:"created_at"`
	UpdatedAt time.Time  `firestore:"updated_at"`
	DeletedAt *time.Time `firestore:"deleted_at"`
}

type AppAccountStore struct {
	client *firestore.Client
}

func NewAppAccountStore(client *firestore.Client) *AppAccountStore {
	if client == nil {
		panic("firestore: NewAppAccountStore called with nil client")
	}
	return &AppAccountStore{client: client}
}

func (s *AppAccountStore) Get(ctx context.Context, owner string) (appaccount.Account, error) {
	snap, err := s.client.Collection(appAccountCollection).Doc(owner).Get(ctx)
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
	if acc.DeletedAt != nil {
		return appaccount.Account{}, appaccount.ErrNotLinked
	}
	return appaccount.Account{
		Owner:     owner,
		TuyaUID:   acc.TuyaUID,
		CreatedAt: acc.CreatedAt,
		UpdatedAt: acc.UpdatedAt,
	}, nil
}

func (s *AppAccountStore) Link(ctx context.Context, owner, tuyaUID string) (appaccount.Account, error) {
	ref := s.client.Collection(appAccountCollection).Doc(owner)
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
	ref := s.client.Collection(appAccountCollection).Doc(owner)
	err := s.client.RunTransaction(ctx, func(_ context.Context, tx *firestore.Transaction) error {
		snap, err := tx.Get(ref)
		if status.Code(err) == codes.NotFound {
			return appaccount.ErrNotLinked
		}
		if err != nil {
			return err
		}
		var acc account
		if err := snap.DataTo(&acc); err != nil {
			return fmt.Errorf("decode %q: %w", owner, err)
		}
		if acc.DeletedAt != nil {
			return appaccount.ErrNotLinked
		}
		now := time.Now().UTC()
		return tx.Update(ref, []firestore.Update{
			{Path: "deleted_at", Value: now},
			{Path: "updated_at", Value: now},
		})
	})
	if err != nil && !errors.Is(err, appaccount.ErrNotLinked) {
		return fmt.Errorf("unlink account: %w", err)
	}
	return err
}
