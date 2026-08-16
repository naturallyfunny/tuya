package firestore

import (
	"context"
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

func TestAppAccountStoreLetsFirestoreJudgeTheOwner(t *testing.T) {
	store := NewAppAccountStore(offlineClient(t))
	for _, owner := range []string{"owner-1", "", ".", "owners/alice", "__reserved__"} {
		if _, err := store.Get(canceled(), owner); !reachedFirestore(err) {
			t.Errorf("Get(%q) error = %v, want a cancelled RPC", owner, err)
		}
		if _, err := store.Link(canceled(), owner, "uid-1"); !reachedFirestore(err) {
			t.Errorf("Link(%q) error = %v, want a cancelled RPC", owner, err)
		}
		if err := store.Unlink(canceled(), owner); !reachedFirestore(err) {
			t.Errorf("Unlink(%q) error = %v, want a cancelled RPC", owner, err)
		}
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
