package tuya_test

import (
	"context"
	"errors"
	"testing"

	"go.naturallyfunny.dev/tuya"
	"go.naturallyfunny.dev/tuya/cloud"
)

// The concrete facade has to keep satisfying the door's interface.
var _ tuya.SpaceIoT = (*cloud.IoT)(nil)

const (
	ownerSpace cloud.SpaceID = 15000001
	insideRoom cloud.SpaceID = 15000002
	foreign    cloud.SpaceID = 99000001
)

type fakeSpaceStore struct {
	space    tuya.Space
	err      error
	gotOwner string
}

func (f *fakeSpaceStore) Get(_ context.Context, owner string) (tuya.Space, error) {
	f.gotOwner = owner
	return f.space, f.err
}

func (f *fakeSpaceStore) Link(context.Context, string, cloud.SpaceID) (tuya.Space, error) {
	panic("Link not expected in these tests")
}

func (f *fakeSpaceStore) Unlink(context.Context, string) error {
	panic("Unlink not expected in these tests")
}

type resourcePage struct {
	resources []cloud.Resource
	cursor    int64
}

type fakeSpaceIoT struct {
	contains    map[cloud.SpaceID]bool
	containsErr error

	// pages is served one entry per call; endlessPages instead answers forever
	// with a cursor that keeps advancing, the shape that could loop.
	pages        []resourcePage
	endlessPages bool

	relationQueries [][2]cloud.SpaceID
	createdParent   cloud.SpaceID
	queried         cloud.SpaceID
	modified        cloud.SpaceID
	deleted         cloud.SpaceID
	listed          cloud.SpaceID
	resourcesOf     cloud.SpaceID
	resourceCalls   int
}

func (f *fakeSpaceIoT) SpaceContains(_ context.Context, parent, child cloud.SpaceID) (bool, error) {
	f.relationQueries = append(f.relationQueries, [2]cloud.SpaceID{parent, child})
	if f.containsErr != nil {
		return false, f.containsErr
	}
	return f.contains[child], nil
}

func (f *fakeSpaceIoT) CreateSpace(_ context.Context, _ string, parentID cloud.SpaceID, _ string) (cloud.SpaceID, error) {
	f.createdParent = parentID
	return 15000009, nil
}

func (f *fakeSpaceIoT) Space(_ context.Context, id cloud.SpaceID) (cloud.Space, error) {
	f.queried = id
	return cloud.Space{ID: id, Name: "Lobby"}, nil
}

func (f *fakeSpaceIoT) ModifySpace(_ context.Context, id cloud.SpaceID, _, _ string) error {
	f.modified = id
	return nil
}

func (f *fakeSpaceIoT) DeleteSpace(_ context.Context, id cloud.SpaceID) error {
	f.deleted = id
	return nil
}

func (f *fakeSpaceIoT) ChildSpaces(_ context.Context, id cloud.SpaceID, _ cloud.Scope, _ ...cloud.PageOption) ([]cloud.SpaceID, cloud.Page, error) {
	f.listed = id
	return []cloud.SpaceID{insideRoom}, cloud.Page{}, nil
}

func (f *fakeSpaceIoT) SpaceResources(_ context.Context, id cloud.SpaceID, _ cloud.Scope, _ ...cloud.PageOption) ([]cloud.Resource, cloud.Page, error) {
	f.resourcesOf = id
	f.resourceCalls++
	if f.endlessPages {
		return []cloud.Resource{{ID: "someone-elses-device", Type: cloud.ResourceDevice}},
			cloud.Page{LastRowKey: int64(f.resourceCalls)}, nil
	}
	if index := f.resourceCalls - 1; index < len(f.pages) {
		return f.pages[index].resources, cloud.Page{LastRowKey: f.pages[index].cursor}, nil
	}
	return nil, cloud.Page{}, nil
}

func newSpaceDoor(t *testing.T, iot *fakeSpaceIoT) *tuya.SpaceClient {
	t.Helper()
	store := &fakeSpaceStore{space: tuya.Space{Owner: "owner-1", SpaceID: ownerSpace}}
	return tuya.NewSpaceClient(iot, store)
}

func TestSpaceDoorReachesSpacesInsideTheOwnersSpace(t *testing.T) {
	iot := &fakeSpaceIoT{contains: map[cloud.SpaceID]bool{insideRoom: true}}
	door := newSpaceDoor(t, iot)

	space, err := door.Space(context.Background(), "owner-1", insideRoom)
	if err != nil {
		t.Fatalf("Space: unexpected error: %v", err)
	}
	if space.ID != insideRoom {
		t.Errorf("space id = %d, want %d", space.ID, insideRoom)
	}
	if len(iot.relationQueries) != 1 || iot.relationQueries[0] != [2]cloud.SpaceID{ownerSpace, insideRoom} {
		t.Errorf("relation queries = %v, want one asking whether %d holds %d", iot.relationQueries, ownerSpace, insideRoom)
	}
}

func TestSpaceDoorRefusesSpacesOutsideTheOwnersSpace(t *testing.T) {
	iot := &fakeSpaceIoT{contains: map[cloud.SpaceID]bool{}}
	door := newSpaceDoor(t, iot)
	ctx := context.Background()

	if _, err := door.Space(ctx, "owner-1", foreign); !errors.Is(err, tuya.ErrSpaceNotOwned) {
		t.Errorf("Space error = %v, want ErrSpaceNotOwned", err)
	}
	if err := door.ModifySpace(ctx, "owner-1", foreign, "Renamed", ""); !errors.Is(err, tuya.ErrSpaceNotOwned) {
		t.Errorf("ModifySpace error = %v, want ErrSpaceNotOwned", err)
	}
	if err := door.DeleteSpace(ctx, "owner-1", foreign); !errors.Is(err, tuya.ErrSpaceNotOwned) {
		t.Errorf("DeleteSpace error = %v, want ErrSpaceNotOwned", err)
	}
	if _, _, err := door.ChildSpaces(ctx, "owner-1", foreign, cloud.DirectChildren); !errors.Is(err, tuya.ErrSpaceNotOwned) {
		t.Errorf("ChildSpaces error = %v, want ErrSpaceNotOwned", err)
	}
	if _, err := door.CreateSpace(ctx, "owner-1", "Room 2", foreign, ""); !errors.Is(err, tuya.ErrSpaceNotOwned) {
		t.Errorf("CreateSpace error = %v, want ErrSpaceNotOwned", err)
	}
	if _, _, err := door.SpaceResources(ctx, "owner-1", foreign, cloud.Subtree); !errors.Is(err, tuya.ErrSpaceNotOwned) {
		t.Errorf("SpaceResources error = %v, want ErrSpaceNotOwned", err)
	}

	// A refused guard must stop before the operation reaches Tuya.
	if iot.queried != 0 || iot.modified != 0 || iot.deleted != 0 || iot.listed != 0 || iot.createdParent != 0 || iot.resourcesOf != 0 {
		t.Errorf("an operation ran past the guard: %+v", iot)
	}
}

func TestContainsDeviceScansTheWholeSubtree(t *testing.T) {
	lobbyLight := cloud.Resource{ID: "dev-lobby", Type: cloud.ResourceDevice}
	iot := &fakeSpaceIoT{pages: []resourcePage{{resources: []cloud.Resource{lobbyLight}}}}
	door := newSpaceDoor(t, iot)

	ok, err := door.ContainsDevice(context.Background(), "owner-1", "dev-lobby")
	if err != nil {
		t.Fatalf("ContainsDevice: unexpected error: %v", err)
	}
	if !ok {
		t.Error("ContainsDevice: got false for a device in the owner's subtree")
	}
	// The subtree, not one room: the device may sit anywhere inside the owner's space.
	if iot.resourcesOf != ownerSpace {
		t.Errorf("scanned space %d, want the owner's space %d", iot.resourcesOf, ownerSpace)
	}
}

// A device that is not in the subtree is a false, not an error. The door reports;
// whether that means "refuse" is the caller's rule, and a consumer running device
// sharing will answer differently from one that is not.
func TestContainsDeviceAbsentIsFalseNotError(t *testing.T) {
	iot := &fakeSpaceIoT{pages: []resourcePage{{resources: []cloud.Resource{{ID: "dev-other", Type: cloud.ResourceDevice}}}}}
	door := newSpaceDoor(t, iot)

	ok, err := door.ContainsDevice(context.Background(), "owner-1", "dev-foreign")
	if err != nil {
		t.Fatalf("ContainsDevice: got error %v, want a plain false", err)
	}
	if ok {
		t.Error("ContainsDevice: got true for a device outside the owner's subtree")
	}
}

func TestContainsDeviceReadsPagesUntilItFindsTheDevice(t *testing.T) {
	iot := &fakeSpaceIoT{pages: []resourcePage{
		{resources: []cloud.Resource{{ID: "dev-1", Type: cloud.ResourceDevice}}, cursor: 42},
		{resources: []cloud.Resource{{ID: "dev-2", Type: cloud.ResourceDevice}}, cursor: 43},
	}}
	door := newSpaceDoor(t, iot)

	ok, err := door.ContainsDevice(context.Background(), "owner-1", "dev-2")
	if err != nil || !ok {
		t.Fatalf("ContainsDevice = %v, %v; want true, nil", ok, err)
	}
	if iot.resourceCalls != 2 {
		t.Errorf("read %d pages, want 2 (the device is on the second)", iot.resourceCalls)
	}
}

func TestContainsDeviceStopsOnAStalledCursor(t *testing.T) {
	// Tuya never documents how a listing ends; a cursor that repeats is one of
	// the plausible signals, and must not be read as "there is another page".
	iot := &fakeSpaceIoT{pages: []resourcePage{
		{resources: []cloud.Resource{{ID: "dev-1", Type: cloud.ResourceDevice}}, cursor: 42},
		{resources: []cloud.Resource{{ID: "dev-1", Type: cloud.ResourceDevice}}, cursor: 42},
	}}
	door := newSpaceDoor(t, iot)

	ok, err := door.ContainsDevice(context.Background(), "owner-1", "dev-absent")
	if err != nil || ok {
		t.Fatalf("ContainsDevice = %v, %v; want false, nil", ok, err)
	}
	if iot.resourceCalls != 2 {
		t.Errorf("read %d pages, want 2 before the cursor stalled", iot.resourceCalls)
	}
}

func TestContainsDeviceGivesUpRatherThanPageForever(t *testing.T) {
	iot := &fakeSpaceIoT{endlessPages: true}
	door := newSpaceDoor(t, iot)

	// Giving up is an error, never a false: "not found" and "gave up looking"
	// must not be the same answer to a caller deciding on it.
	ok, err := door.ContainsDevice(context.Background(), "owner-1", "dev-absent")
	if err == nil {
		t.Fatal("ContainsDevice: got nil error, want the scan to give up")
	}
	if ok {
		t.Error("ContainsDevice: got true alongside an error")
	}
	if iot.resourceCalls > 100 {
		t.Errorf("read %d pages, want the scan capped", iot.resourceCalls)
	}
}

func TestContainsSpace(t *testing.T) {
	iot := &fakeSpaceIoT{contains: map[cloud.SpaceID]bool{insideRoom: true}}
	door := newSpaceDoor(t, iot)
	ctx := context.Background()

	ok, err := door.ContainsSpace(ctx, "owner-1", insideRoom)
	if err != nil || !ok {
		t.Fatalf("ContainsSpace(insideRoom) = %v, %v; want true, nil", ok, err)
	}
	// A space outside is a false, not ErrSpaceNotOwned: this is the question,
	// not the refusal.
	ok, err = door.ContainsSpace(ctx, "owner-1", foreign)
	if err != nil {
		t.Fatalf("ContainsSpace(foreign): got error %v, want a plain false", err)
	}
	if ok {
		t.Error("ContainsSpace(foreign) = true, want false")
	}
	// Zero still means the owner's own space, and needs no request to answer.
	before := len(iot.relationQueries)
	ok, err = door.ContainsSpace(ctx, "owner-1", 0)
	if err != nil || !ok {
		t.Fatalf("ContainsSpace(0) = %v, %v; want true, nil", ok, err)
	}
	if len(iot.relationQueries) != before {
		t.Error("ContainsSpace(0) asked Tuya about the owner's own space")
	}
}

func TestSpaceDoorStopsWhenTheOwnerHasNoSpace(t *testing.T) {
	iot := &fakeSpaceIoT{}
	store := &fakeSpaceStore{err: tuya.ErrSpaceNotLinked}
	door := tuya.NewSpaceClient(iot, store)

	if _, err := door.Space(context.Background(), "owner-1", insideRoom); !errors.Is(err, tuya.ErrSpaceNotLinked) {
		t.Errorf("Space error = %v, want ErrSpaceNotLinked", err)
	}
	if len(iot.relationQueries) != 0 {
		t.Errorf("asked Tuya %d times without a linked space, want none", len(iot.relationQueries))
	}
}

func TestZeroSpaceIDMeansTheOwnersSpace(t *testing.T) {
	iot := &fakeSpaceIoT{}
	door := newSpaceDoor(t, iot)
	ctx := context.Background()

	if _, err := door.Space(ctx, "owner-1", 0); err != nil {
		t.Fatalf("Space: unexpected error: %v", err)
	}
	if iot.queried != ownerSpace {
		t.Errorf("queried space %d, want the owner's space %d", iot.queried, ownerSpace)
	}
	if _, _, err := door.ChildSpaces(ctx, "owner-1", 0, cloud.DirectChildren); err != nil {
		t.Fatalf("ChildSpaces: unexpected error: %v", err)
	}
	if iot.listed != ownerSpace {
		t.Errorf("listed children of %d, want the owner's space %d", iot.listed, ownerSpace)
	}
	if _, err := door.CreateSpace(ctx, "owner-1", "Room 3", 0, ""); err != nil {
		t.Fatalf("CreateSpace: unexpected error: %v", err)
	}
	if iot.createdParent != ownerSpace {
		t.Errorf("created under %d, want the owner's space %d", iot.createdParent, ownerSpace)
	}

	// The owner's own space is theirs by definition — asking Tuya would be a
	// request per call, and Tuya answers false for a space compared against
	// itself, so asking would refuse it.
	if len(iot.relationQueries) != 0 {
		t.Errorf("asked Tuya about the owner's own space %d times, want none", len(iot.relationQueries))
	}
}

func TestTheOwnersSpaceCannotBeDeletedThroughTheDoor(t *testing.T) {
	iot := &fakeSpaceIoT{contains: map[cloud.SpaceID]bool{ownerSpace: true}}
	door := newSpaceDoor(t, iot)
	ctx := context.Background()

	// Tuya deletes subspaces along with their parent: this would erase everything
	// the owner can reach and leave the store pointing at a space that is gone.
	if err := door.DeleteSpace(ctx, "owner-1", ownerSpace); !errors.Is(err, tuya.ErrOwnerSpaceProtected) {
		t.Errorf("DeleteSpace(owner's space) error = %v, want ErrOwnerSpaceProtected", err)
	}
	if err := door.DeleteSpace(ctx, "owner-1", 0); !errors.Is(err, tuya.ErrOwnerSpaceProtected) {
		t.Errorf("DeleteSpace(0) error = %v, want ErrOwnerSpaceProtected", err)
	}
	if iot.deleted != 0 {
		t.Errorf("deleted space %d, want none", iot.deleted)
	}
}

func TestASpaceOutsideTheProjectIsRefusedAsUnowned(t *testing.T) {
	// Tuya does not answer false for a space it cannot see — it refuses the
	// question. For this door that means the same thing.
	iot := &fakeSpaceIoT{containsErr: &cloud.APIError{Code: cloud.CodeNoSpacePermission, Msg: "No space permission"}}
	door := newSpaceDoor(t, iot)

	if _, err := door.Space(context.Background(), "owner-1", foreign); !errors.Is(err, tuya.ErrSpaceNotOwned) {
		t.Errorf("Space error = %v, want ErrSpaceNotOwned", err)
	}
	if iot.queried != 0 {
		t.Errorf("queried space %d past the guard", iot.queried)
	}
}

func TestSpaceDoorReportsAFailedOwnershipCheck(t *testing.T) {
	iot := &fakeSpaceIoT{containsErr: errors.New("tuya unreachable")}
	door := newSpaceDoor(t, iot)

	// An unanswered guard is not an allowed guard.
	err := door.ModifySpace(context.Background(), "owner-1", insideRoom, "Renamed", "")
	if err == nil {
		t.Fatal("ModifySpace: got nil error, want the failed ownership check")
	}
	if iot.modified != 0 {
		t.Errorf("modified space %d despite the failed check", iot.modified)
	}
}
