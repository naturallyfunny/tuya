// Package firestore provides a Cloud Firestore-backed app-account mapping for
// the Tuya library: the owner → Tuya-UID link a consumer resolves (via
// tuya.AppAccountClient) before driving devices.
//
// Each owner maps to one document in a single collection (DefaultCollection
// unless overridden with WithCollection); the owner string is the document ID,
// so lookups are direct reads rather than queries. Firestore is schemaless, so
// unlike the postgres sibling there are no migrations — the collection appears
// with the first linked account. Unlink soft-deletes (sets deleted_at) exactly
// like the postgres sibling, preserving the document for audit.
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

// DefaultCollection is the collection accounts live in unless WithCollection
// overrides it. It matches the table name used by the postgres sibling.
const DefaultCollection = "tuya_app_accounts"

// accountDoc is the stored shape of one owner's account document. The owner
// itself is the document ID, not a field.
type accountDoc struct {
	TuyaUID   string     `firestore:"tuya_uid"`
	CreatedAt time.Time  `firestore:"created_at"`
	UpdatedAt time.Time  `firestore:"updated_at"`
	DeletedAt *time.Time `firestore:"deleted_at"`
}

// account maps the document back to the domain type, restoring the owner that
// lives in the document ID.
func (d accountDoc) account(owner string) tuya.AppAccount {
	return tuya.AppAccount{
		Owner:     owner,
		TuyaUID:   d.TuyaUID,
		CreatedAt: d.CreatedAt,
		UpdatedAt: d.UpdatedAt,
	}
}

// AppAccountStore maps an owner to the human's Tuya app-account UID, backed by
// Cloud Firestore. It owns the full lifecycle of that mapping: Get reads it, Link
// creates or refreshes it, and Unlink soft-deletes it. A consumer links an
// account once (after the human authorizes Tuya), then drives devices by owner
// via tuya.AppAccountClient, which resolves owner -> UID with Get. That is the
// app-account tenancy model; Tuya's spatial model needs no such mapping, and
// will bring its own SpaceStore rather than widening this one.
type AppAccountStore struct {
	client     *firestore.Client
	collection string
}

// Option configures an AppAccountStore.
type Option func(*AppAccountStore)

// WithCollection stores accounts in the named collection instead of
// DefaultCollection. Use it when one Firestore database hosts several
// environments or apps.
func WithCollection(name string) Option {
	return func(s *AppAccountStore) { s.collection = name }
}

// NewAppAccountStore builds an AppAccountStore over an existing
// *firestore.Client the consumer already owns (and stays responsible for
// closing). No I/O happens here — Firestore needs no schema, so there is no
// migrate step and no error to return, unlike the postgres sibling.
func NewAppAccountStore(client *firestore.Client, opts ...Option) *AppAccountStore {
	if client == nil {
		panic("firestore: NewAppAccountStore called with nil client")
	}
	s := &AppAccountStore{client: client, collection: DefaultCollection}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Get returns the full Account linked to owner, or tuya.ErrAccountNotLinked if
// none is linked.
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

// Link records that owner maps to tuyaUID, returning the resulting Account. It
// is an upsert: linking an owner that is already linked refreshes the UID and
// updated_at, and re-linking a previously unlinked owner revives the document
// (clearing deleted_at). The read-then-write runs in a transaction so
// concurrent links serialize instead of clobbering each other's created_at.
func (s *AppAccountStore) Link(ctx context.Context, owner, tuyaUID string) (tuya.AppAccount, error) {
	ref, err := s.doc(owner)
	if err != nil {
		return tuya.AppAccount{}, err
	}
	var acc tuya.AppAccount
	err = s.client.RunTransaction(ctx, func(_ context.Context, tx *firestore.Transaction) error {
		// Timestamps are written client-side rather than as ServerTimestamp
		// sentinels: a sentinel's value is unknown until after commit, and Link
		// must return the exact Account it stored without a second read.
		now := time.Now().UTC()
		doc := accountDoc{TuyaUID: tuyaUID, CreatedAt: now, UpdatedAt: now}
		snap, err := tx.Get(ref)
		switch {
		case status.Code(err) == codes.NotFound:
			// First link: created_at = now, set above.
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
		// Set replaces the whole document; DeletedAt's zero value (nil) is what
		// revives a previously unlinked owner.
		return tx.Set(ref, doc)
	})
	if err != nil {
		return tuya.AppAccount{}, fmt.Errorf("link account: %w", err)
	}
	return acc, nil
}

// Unlink soft-deletes the mapping for owner (setting deleted_at), so Get stops
// returning it while the document is preserved for audit. Returns
// tuya.ErrAccountNotLinked if no live mapping exists.
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

// doc resolves owner to its document reference, rejecting owners that cannot
// be Firestore document IDs.
func (s *AppAccountStore) doc(owner string) (*firestore.DocumentRef, error) {
	if err := validateOwner(owner); err != nil {
		return nil, err
	}
	return s.client.Collection(s.collection).Doc(owner), nil
}

// validateOwner enforces Firestore's document-ID constraints on the opaque
// owner string. The owner is used verbatim as the document ID — no escaping —
// so the rare owner that violates a constraint is rejected loudly here instead
// of corrupting a document path or failing server-side with an opaque error.
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
