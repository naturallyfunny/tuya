package firestore

import (
	"testing"
)

func TestSpaceStoreRejectsZeroSpaceID(t *testing.T) {
	store := NewSpaceStore(offlineClient(t))
	_, err := store.Link(canceled(), "owner-1", 0)
	if err == nil {
		t.Fatal("Link(0): got nil error, want space id zero refused")
	}
	if reachedFirestore(err) {
		t.Errorf("Link(0) reached Firestore before refusing: %v", err)
	}
}

func TestSpaceStoreRefusesOwnersFirestoreCannotName(t *testing.T) {
	store := NewSpaceStore(offlineClient(t))
	for _, owner := range unnamableOwners {
		if _, err := store.Get(canceled(), owner); err == nil || reachedFirestore(err) {
			t.Errorf("Get(%q) error = %v, want the owner refused before Firestore", owner, err)
		}
		if _, err := store.Link(canceled(), owner, 15000001); err == nil || reachedFirestore(err) {
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

func TestSpaceDocumentIsTheOwnerInItsCollection(t *testing.T) {
	client := offlineClient(t)
	ref, err := NewSpaceStore(client).doc("owner-1")
	if err != nil {
		t.Fatalf("doc: unexpected error: %v", err)
	}
	if ref.ID != "owner-1" || ref.Parent.ID != DefaultSpaceCollection {
		t.Errorf("doc = %s/%s, want %s/owner-1", ref.Parent.ID, ref.ID, DefaultSpaceCollection)
	}
	ref, err = NewSpaceStore(client, WithCollection("hotel_spaces")).doc("owner-1")
	if err != nil {
		t.Fatalf("doc: unexpected error: %v", err)
	}
	if ref.Parent.ID != "hotel_spaces" {
		t.Errorf("WithCollection: doc lives in %q, want hotel_spaces", ref.Parent.ID)
	}
	ref, err = NewSpaceStore(client, WithCollection("")).doc("owner-1")
	if err != nil {
		t.Fatalf("doc: unexpected error: %v", err)
	}
	if ref.Parent.ID != DefaultSpaceCollection {
		t.Errorf(`WithCollection(""): doc lives in %q, want the default %q`, ref.Parent.ID, DefaultSpaceCollection)
	}
}

func TestNewSpaceStoreRejectsANilClient(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("NewSpaceStore(nil): did not panic")
		}
	}()
	NewSpaceStore(nil)
}
