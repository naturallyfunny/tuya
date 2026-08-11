package postgres

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"go.naturallyfunny.dev/tuya/spatial"
)

const ownerSpace int64 = 15000001

func spaceRow() []any {
	return []any{"owner-1", ownerSpace, linkedAt, linkedAt}
}

func newSpaceStore(t *testing.T, db *fakeDB) *SpaceStore {
	t.Helper()
	store, err := NewSpaceStore(context.Background(), db)
	if err != nil {
		t.Fatalf("NewSpaceStore: unexpected error: %v", err)
	}
	db.queries = nil
	db.execs = nil
	return store
}

func TestSpaceGet(t *testing.T) {
	db := &fakeDB{row: spaceRow()}
	store := newSpaceStore(t, db)
	space, err := store.Get(context.Background(), "owner-1")
	if err != nil {
		t.Fatalf("Get: unexpected error: %v", err)
	}
	if space.Owner != "owner-1" || space.SpaceID != ownerSpace || !space.CreatedAt.Equal(linkedAt) {
		t.Errorf("Get = %+v, want owner-1 linked to space %d", space, ownerSpace)
	}
	if len(db.queries) != 1 {
		t.Fatalf("Get ran %d queries, want 1", len(db.queries))
	}
	if got := db.queries[0].args; len(got) != 1 || got[0] != "owner-1" {
		t.Errorf("Get queried with %v, want owner-1", got)
	}
	if !strings.Contains(db.queries[0].sql, "deleted_at IS NULL") {
		t.Errorf("Get reads unlinked rows too:\n%s", db.queries[0].sql)
	}
}

func TestSpaceGetUnlinkedOwner(t *testing.T) {
	db := &fakeDB{}
	store := newSpaceStore(t, db)
	if _, err := store.Get(context.Background(), "owner-1"); !errors.Is(err, spatial.ErrNotLinked) {
		t.Errorf("Get error = %v, want ErrNotLinked", err)
	}
}

func TestSpaceGetFailedLookupIsNotAnAnswer(t *testing.T) {
	db := &fakeDB{row: spaceRow()}
	store := newSpaceStore(t, db)
	db.queryErr = errors.New("connection refused")
	_, err := store.Get(context.Background(), "owner-1")
	if err == nil {
		t.Fatal("Get: got nil error for a failed lookup")
	}
	if errors.Is(err, spatial.ErrNotLinked) {
		t.Errorf("Get error = %v, want the lookup failure, not ErrNotLinked", err)
	}
}

func TestSpaceLinkRevivesAnUnlinkedOwner(t *testing.T) {
	db := &fakeDB{row: spaceRow()}
	store := newSpaceStore(t, db)
	space, err := store.Link(context.Background(), "owner-1", ownerSpace)
	if err != nil {
		t.Fatalf("Link: unexpected error: %v", err)
	}
	if space.Owner != "owner-1" || space.SpaceID != ownerSpace {
		t.Errorf("Link = %+v, want owner-1 linked to space %d", space, ownerSpace)
	}
	if got := db.queries[0].args; len(got) != 2 || got[1] != ownerSpace {
		t.Errorf("Link wrote %v, want the space id as an int64", got)
	}
	sql := db.queries[0].sql
	if !strings.Contains(sql, "ON CONFLICT") || !strings.Contains(sql, "deleted_at = NULL") {
		t.Errorf("Link leaves a previously unlinked owner unlinked:\n%s", sql)
	}
}

func TestSpaceUnlink(t *testing.T) {
	db := &fakeDB{row: spaceRow(), tag: "UPDATE 1"}
	store := newSpaceStore(t, db)
	if err := store.Unlink(context.Background(), "owner-1"); err != nil {
		t.Fatalf("Unlink: unexpected error: %v", err)
	}
	if !strings.Contains(db.execs[0].sql, "deleted_at = NOW()") {
		t.Errorf("Unlink is not a soft delete:\n%s", db.execs[0].sql)
	}
}

func TestSpaceUnlinkOwnerThatWasNeverLinked(t *testing.T) {
	db := &fakeDB{row: spaceRow(), tag: "UPDATE 0"}
	store := newSpaceStore(t, db)
	if err := store.Unlink(context.Background(), "owner-1"); !errors.Is(err, spatial.ErrNotLinked) {
		t.Errorf("Unlink error = %v, want ErrNotLinked", err)
	}
}

func TestNewSpaceStoreChecksTheSchemaWithoutCreatingIt(t *testing.T) {
	db := &fakeDB{queryErr: errors.New(`relation "tuya_spaces" does not exist`)}
	if _, err := NewSpaceStore(context.Background(), db); err == nil {
		t.Fatal("NewSpaceStore: got nil error for a missing table")
	}
	if len(db.execs) != 0 {
		t.Errorf("NewSpaceStore wrote to the database without WithAutoMigrate: %v", db.execs)
	}
}

func TestSpaceAutoMigrateRaisesOnlyItsOwnDoor(t *testing.T) {
	db := &fakeDB{applied: map[string]bool{}}
	if _, err := NewSpaceStore(context.Background(), db, WithAutoMigrate()); err != nil {
		t.Fatalf("NewSpaceStore: unexpected error: %v", err)
	}
	if !db.ran("tuya_spaces") {
		t.Error("auto-migrate did not create tuya_spaces")
	}
	if db.ran("tuya_app_accounts") {
		t.Error("auto-migrate raised the app-account door's migrations")
	}
	if db.ran("DROP TABLE") {
		t.Error("auto-migrate executed a .down.sql")
	}
	want := []string{"spatial/000001_init.up.sql"}
	if got := db.recordedMigrations(); !slices.Equal(got, want) {
		t.Errorf("recorded migrations = %v, want %v", got, want)
	}
}

func TestSpaceAutoMigrateSkipsWhatIsAlreadyRecorded(t *testing.T) {
	db := &fakeDB{applied: map[string]bool{"spatial/000001_init.up.sql": true}}
	if _, err := NewSpaceStore(context.Background(), db, WithAutoMigrate()); err != nil {
		t.Fatalf("NewSpaceStore: unexpected error: %v", err)
	}
	if db.ran("tuya_spaces") {
		t.Error("auto-migrate re-ran a migration already recorded")
	}
	if got := db.recordedMigrations(); len(got) != 0 {
		t.Errorf("recorded migrations = %v, want none", got)
	}
}

func TestNewSpaceStoreRejectsANilQuerier(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("NewSpaceStore(nil): did not panic")
		}
	}()
	NewSpaceStore(context.Background(), nil)
}
