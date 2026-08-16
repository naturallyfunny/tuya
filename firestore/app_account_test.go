package firestore

import (
	"context"
	"strings"
	"testing"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func offlineClient(t *testing.T) *firestore.Client {
	t.Helper()
	t.Setenv("FIRESTORE_EMULATOR_HOST", "127.0.0.1:1")
	client, err := firestore.NewClient(context.Background(), "tuya-test")
	if err != nil {
		t.Fatalf("firestore.NewClient: %v", err)
	}
	t.Cleanup(func() { client.Close() })
	return client
}

func canceled() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func reachedFirestore(err error) bool {
	return status.Code(err) == codes.Canceled
}

var unnamableOwners = []string{"", ".", "owners/alice", "__reserved__"}

func TestValidateOwner(t *testing.T) {
	valid := []string{
		"user-123",
		"someone@example.com",
		"a",
		strings.Repeat("x", 1500),
		"__x", "x__", "___",
		".hidden", "..dots",
	}
	for _, owner := range valid {
		if err := validateOwner(owner); err != nil {
			t.Errorf("validateOwner(%q): unexpected error: %v", owner, err)
		}
	}
	invalid := []string{
		"",
		".", "..",
		"owners/alice",
		strings.Repeat("x", 1501),
		"__reserved__", "____",
	}
	for _, owner := range invalid {
		if err := validateOwner(owner); err == nil {
			t.Errorf("validateOwner(%q): want error, got nil", owner)
		}
	}
}

func TestAppAccountStoreRefusesOwnersFirestoreCannotName(t *testing.T) {
	store := NewAppAccountStore(offlineClient(t))
	for _, owner := range unnamableOwners {
		if _, err := store.Get(canceled(), owner); err == nil || reachedFirestore(err) {
			t.Errorf("Get(%q) error = %v, want the owner refused before Firestore", owner, err)
		}
		if _, err := store.Link(canceled(), owner, "uid-1"); err == nil || reachedFirestore(err) {
			t.Errorf("Link(%q) error = %v, want the owner refused before Firestore", owner, err)
		}
		if err := store.Unlink(canceled(), owner); err == nil || reachedFirestore(err) {
			t.Errorf("Unlink(%q) error = %v, want the owner refused before Firestore", owner, err)
		}
	}
	if _, err := store.Get(canceled(), "owner-1"); !reachedFirestore(err) {
		t.Fatalf("Get(owner-1) error = %v, want a cancelled RPC — a namable owner must reach Firestore", err)
	}
}

func TestAppAccountDocumentIsTheOwnerInItsCollection(t *testing.T) {
	client := offlineClient(t)
	ref, err := NewAppAccountStore(client).doc("owner-1")
	if err != nil {
		t.Fatalf("doc: unexpected error: %v", err)
	}
	if ref.ID != "owner-1" || ref.Parent.ID != DefaultAppAccountCollection {
		t.Errorf("doc = %s/%s, want %s/owner-1", ref.Parent.ID, ref.ID, DefaultAppAccountCollection)
	}
	ref, err = NewAppAccountStore(client, WithCollection("tenants")).doc("owner-1")
	if err != nil {
		t.Fatalf("doc: unexpected error: %v", err)
	}
	if ref.Parent.ID != "tenants" {
		t.Errorf("WithCollection: doc lives in %q, want tenants", ref.Parent.ID)
	}
	ref, err = NewAppAccountStore(client, WithCollection("")).doc("owner-1")
	if err != nil {
		t.Fatalf("doc: unexpected error: %v", err)
	}
	if ref.Parent.ID != DefaultAppAccountCollection {
		t.Errorf(`WithCollection(""): doc lives in %q, want the default %q`, ref.Parent.ID, DefaultAppAccountCollection)
	}
}

func TestNewAppAccountStoreRejectsANilClient(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("NewAppAccountStore(nil): did not panic")
		}
	}()
	NewAppAccountStore(nil)
}
