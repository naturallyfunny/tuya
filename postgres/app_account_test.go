package postgres

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"go.naturallyfunny.dev/tuya/appaccount"
)

var linkedAt = time.Date(2026, 8, 7, 9, 0, 0, 0, time.UTC)

func accountRow() []any {
	return []any{"owner-1", "uid-1", linkedAt, linkedAt}
}

func newAccountStore(t *testing.T, db *fakeDB) *AppAccountStore {
	t.Helper()
	store, err := NewAppAccountStore(context.Background(), db)
	if err != nil {
		t.Fatalf("NewAppAccountStore: unexpected error: %v", err)
	}
	db.queries = nil
	db.execs = nil
	return store
}

func TestAppAccountGet(t *testing.T) {
	db := &fakeDB{row: accountRow()}
	store := newAccountStore(t, db)
	acc, err := store.Get(context.Background(), "owner-1")
	if err != nil {
		t.Fatalf("Get: unexpected error: %v", err)
	}
	if acc.Owner != "owner-1" || acc.TuyaUID != "uid-1" || !acc.CreatedAt.Equal(linkedAt) {
		t.Errorf("Get = %+v, want owner-1 linked to uid-1 at %v", acc, linkedAt)
	}
	if len(db.queries) != 1 {
		t.Fatalf("Get ran %d queries, want 1", len(db.queries))
	}
	if got := db.queries[0].args; len(got) != 1 || got[0] != "owner-1" {
		t.Errorf("Get queried with %v, want owner-1", got)
	}
}

func TestAppAccountGetUnlinkedOwner(t *testing.T) {
	db := &fakeDB{}
	store := newAccountStore(t, db)
	if _, err := store.Get(context.Background(), "owner-1"); !errors.Is(err, appaccount.ErrNotLinked) {
		t.Errorf("Get error = %v, want ErrNotLinked", err)
	}
}

func TestAppAccountGetFailedLookupIsNotAnAnswer(t *testing.T) {
	db := &fakeDB{row: accountRow()}
	store := newAccountStore(t, db)
	db.queryErr = errors.New("connection refused")
	_, err := store.Get(context.Background(), "owner-1")
	if err == nil {
		t.Fatal("Get: got nil error for a failed lookup")
	}
	if errors.Is(err, appaccount.ErrNotLinked) {
		t.Errorf("Get error = %v, want the lookup failure, not ErrNotLinked", err)
	}
}

func TestAppAccountLinkReplacesTheUIDOfAnOwnerAlreadyOnFile(t *testing.T) {
	db := &fakeDB{row: accountRow()}
	store := newAccountStore(t, db)
	acc, err := store.Link(context.Background(), "owner-1", "uid-1")
	if err != nil {
		t.Fatalf("Link: unexpected error: %v", err)
	}
	if acc.Owner != "owner-1" || acc.TuyaUID != "uid-1" {
		t.Errorf("Link = %+v, want owner-1 linked to uid-1", acc)
	}
	sql := db.queries[0].sql
	if !strings.Contains(sql, "ON CONFLICT") {
		t.Errorf("Link fails on an owner that already has a row:\n%s", sql)
	}
}

func TestAppAccountUnlink(t *testing.T) {
	db := &fakeDB{row: accountRow(), tag: "DELETE 1"}
	store := newAccountStore(t, db)
	if err := store.Unlink(context.Background(), "owner-1"); err != nil {
		t.Fatalf("Unlink: unexpected error: %v", err)
	}
	if !strings.Contains(db.execs[0].sql, "DELETE FROM") {
		t.Errorf("Unlink leaves the row behind:\n%s", db.execs[0].sql)
	}
}

func TestAppAccountUnlinkOwnerThatWasNeverLinked(t *testing.T) {
	db := &fakeDB{row: accountRow(), tag: "DELETE 0"}
	store := newAccountStore(t, db)
	if err := store.Unlink(context.Background(), "owner-1"); !errors.Is(err, appaccount.ErrNotLinked) {
		t.Errorf("Unlink error = %v, want ErrNotLinked", err)
	}
}

func TestNewAppAccountStoreChecksTheSchemaWithoutCreatingIt(t *testing.T) {
	db := &fakeDB{queryErr: errors.New(`relation "tuya_app_accounts" does not exist`)}
	if _, err := NewAppAccountStore(context.Background(), db); err == nil {
		t.Fatal("NewAppAccountStore: got nil error for a missing table")
	}
	if len(db.execs) != 0 {
		t.Errorf("NewAppAccountStore wrote to the database without WithAutoMigrate: %v", db.execs)
	}
}

func TestAppAccountAutoMigrateCreatesTheTable(t *testing.T) {
	db := &fakeDB{applied: map[string]bool{}}
	if _, err := NewAppAccountStore(context.Background(), db, WithAutoMigrate()); err != nil {
		t.Fatalf("NewAppAccountStore: unexpected error: %v", err)
	}
	if !db.ran("tuya_app_accounts") {
		t.Error("auto-migrate did not create tuya_app_accounts")
	}
	if db.ran("DROP TABLE") {
		t.Error("auto-migrate executed a .down.sql")
	}
	want := []string{"000001_init.up.sql"}
	if got := db.recordedMigrations(); !slices.Equal(got, want) {
		t.Errorf("recorded migrations = %v, want %v", got, want)
	}
}

func TestAppAccountAutoMigrateSkipsWhatIsAlreadyRecorded(t *testing.T) {
	db := &fakeDB{applied: map[string]bool{"000001_init.up.sql": true}}
	if _, err := NewAppAccountStore(context.Background(), db, WithAutoMigrate()); err != nil {
		t.Fatalf("NewAppAccountStore: unexpected error: %v", err)
	}
	if db.ran("tuya_app_accounts") {
		t.Error("auto-migrate re-ran a migration already recorded")
	}
	if got := db.recordedMigrations(); len(got) != 0 {
		t.Errorf("recorded migrations = %v, want none", got)
	}
}

func TestNewAppAccountStoreRejectsANilQuerier(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("NewAppAccountStore(nil): did not panic")
		}
	}()
	NewAppAccountStore(context.Background(), nil)
}
