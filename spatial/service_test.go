package spatial

import (
	"context"
	"errors"
	"testing"

	"go.naturallyfunny.dev/tuya"
)

var _ Client = (*tuya.Client)(nil)

const (
	ownerSpace int64 = 15000001
	insideRoom int64 = 15000002
	foreign    int64 = 99000001
)

type fakeSpaceStore struct {
	space    Space
	err      error
	gotOwner string
}

func (f *fakeSpaceStore) Get(_ context.Context, owner string) (Space, error) {
	f.gotOwner = owner
	return f.space, f.err
}

func (f *fakeSpaceStore) Link(context.Context, string, int64) (Space, error) {
	panic("Link not expected in these tests")
}

func (f *fakeSpaceStore) Unlink(context.Context, string) error {
	panic("Unlink not expected in these tests")
}

type resourcePage struct {
	resources []tuya.Resource
	cursor    int64
}

type fakeSpaceClient struct {
	contains        map[int64]bool
	containsErr     error
	pages           []resourcePage
	endlessPages    bool
	relationQueries [][2]int64
	createdParent   int64
	queried         int64
	modified        int64
	deleted         int64
	listed          []int64
	resourcesOf     int64
	resourceCalls   int
	devicesOf       []int64
	devicesLastID   string
	devicesPageSize int
}

func (f *fakeSpaceClient) SpaceRelation(_ context.Context, parent, child int64) (bool, error) {
	f.relationQueries = append(f.relationQueries, [2]int64{parent, child})
	if f.containsErr != nil {
		return false, f.containsErr
	}
	return f.contains[child], nil
}

func (f *fakeSpaceClient) CreateSpace(_ context.Context, _ string, parentID int64, _ string) (int64, error) {
	f.createdParent = parentID
	return 15000009, nil
}

func (f *fakeSpaceClient) Space(_ context.Context, id int64) (tuya.Space, error) {
	f.queried = id
	return tuya.Space{ID: id, Name: "Lobby"}, nil
}

func (f *fakeSpaceClient) ModifySpace(_ context.Context, id int64, _, _ string) error {
	f.modified = id
	return nil
}

func (f *fakeSpaceClient) DeleteSpace(_ context.Context, id int64) error {
	f.deleted = id
	return nil
}

func (f *fakeSpaceClient) ListSpaces(_ context.Context, id int64, _ bool, _ tuya.Page) ([]int64, tuya.Page, error) {
	f.listed = append(f.listed, id)
	return []int64{insideRoom}, tuya.Page{}, nil
}

func (f *fakeSpaceClient) SpaceResources(_ context.Context, id int64, _ bool, _ tuya.Page) ([]tuya.Resource, tuya.Page, error) {
	f.resourcesOf = id
	f.resourceCalls++
	if f.endlessPages {
		return []tuya.Resource{{ID: "someone-elses-device", Type: tuya.SpaceResourceDevice}},
			tuya.Page{LastRowKey: int64(f.resourceCalls)}, nil
	}
	if index := f.resourceCalls - 1; index < len(f.pages) {
		return f.pages[index].resources, tuya.Page{LastRowKey: f.pages[index].cursor}, nil
	}
	return nil, tuya.Page{}, nil
}

func (f *fakeSpaceClient) SpaceDevices(_ context.Context, spaceIDs []int64, _ bool, _, _ []string, lastID string, pageSize int) ([]tuya.SpaceDevice, error) {
	f.devicesOf = spaceIDs
	f.devicesLastID = lastID
	f.devicesPageSize = pageSize
	return []tuya.SpaceDevice{{Device: tuya.Device{ID: "vdevo-1"}, Name: "Televisi"}}, nil
}

func newSpaceDoor(t *testing.T, client *fakeSpaceClient) *Service {
	t.Helper()
	store := &fakeSpaceStore{space: Space{Owner: "owner-1", SpaceID: ownerSpace}}
	return NewService(client, store)
}

func TestSpaceDoorReachesSpacesInsideTheOwnersSpace(t *testing.T) {
	client := &fakeSpaceClient{contains: map[int64]bool{insideRoom: true}}
	door := newSpaceDoor(t, client)
	space, err := door.Space(context.Background(), "owner-1", insideRoom)
	if err != nil {
		t.Fatalf("Space: unexpected error: %v", err)
	}
	if space.ID != insideRoom {
		t.Errorf("space id = %d, want %d", space.ID, insideRoom)
	}
	if len(client.relationQueries) != 1 || client.relationQueries[0] != [2]int64{ownerSpace, insideRoom} {
		t.Errorf("relation queries = %v, want one asking whether %d holds %d", client.relationQueries, ownerSpace, insideRoom)
	}
}

func TestSpaceDoorRefusesSpacesOutsideTheOwnersSpace(t *testing.T) {
	client := &fakeSpaceClient{contains: map[int64]bool{}}
	door := newSpaceDoor(t, client)
	ctx := context.Background()
	if _, err := door.Space(ctx, "owner-1", foreign); !errors.Is(err, ErrNotOwned) {
		t.Errorf("Space error = %v, want ErrNotOwned", err)
	}
	if err := door.ModifySpace(ctx, "owner-1", foreign, "Renamed", ""); !errors.Is(err, ErrNotOwned) {
		t.Errorf("ModifySpace error = %v, want ErrNotOwned", err)
	}
	if err := door.DeleteSpace(ctx, "owner-1", foreign); !errors.Is(err, ErrNotOwned) {
		t.Errorf("DeleteSpace error = %v, want ErrNotOwned", err)
	}
	if _, _, err := door.ChildSpaces(ctx, "owner-1", foreign, true, tuya.Page{}); !errors.Is(err, ErrNotOwned) {
		t.Errorf("ChildSpaces error = %v, want ErrNotOwned", err)
	}
	if _, err := door.CreateSpace(ctx, "owner-1", "Room 2", foreign, ""); !errors.Is(err, ErrNotOwned) {
		t.Errorf("CreateSpace error = %v, want ErrNotOwned", err)
	}
	if _, _, err := door.SpaceResources(ctx, "owner-1", foreign, false, tuya.Page{}); !errors.Is(err, ErrNotOwned) {
		t.Errorf("SpaceResources error = %v, want ErrNotOwned", err)
	}
	if _, err := door.SpaceDevices(ctx, "owner-1", foreign, "", 0); !errors.Is(err, ErrNotOwned) {
		t.Errorf("SpaceDevices error = %v, want ErrNotOwned", err)
	}
	if client.queried != 0 || client.modified != 0 || client.deleted != 0 || len(client.listed) != 0 || client.createdParent != 0 || client.resourcesOf != 0 || client.devicesOf != nil {
		t.Errorf("an operation ran past the guard: %+v", client)
	}
}

func TestSpaceDevicesListsOneResolvedSpace(t *testing.T) {
	client := &fakeSpaceClient{contains: map[int64]bool{insideRoom: true}}
	door := newSpaceDoor(t, client)
	ctx := context.Background()

	devices, err := door.SpaceDevices(ctx, "owner-1", insideRoom, "vdevo-0", 5)
	if err != nil {
		t.Fatalf("SpaceDevices: unexpected error: %v", err)
	}
	if len(devices) != 1 || devices[0].ID != "vdevo-1" {
		t.Errorf("devices = %+v, want the one the client returned", devices)
	}
	if len(client.devicesOf) != 1 || client.devicesOf[0] != insideRoom {
		t.Errorf("asked for spaces %v, want only %d", client.devicesOf, insideRoom)
	}
	if client.devicesLastID != "vdevo-0" || client.devicesPageSize != 5 {
		t.Errorf("paging = (%q, %d), want it passed through untouched", client.devicesLastID, client.devicesPageSize)
	}

	if _, err := door.SpaceDevices(ctx, "owner-1", 0, "", 0); err != nil {
		t.Fatalf("SpaceDevices: unexpected error: %v", err)
	}
	if len(client.devicesOf) != 1 || client.devicesOf[0] != ownerSpace {
		t.Errorf("asked for spaces %v, want the owner's space %d", client.devicesOf, ownerSpace)
	}
}

func TestContainsDeviceScansTheWholeSubtree(t *testing.T) {
	lobbyLight := tuya.Resource{ID: "dev-lobby", Type: tuya.SpaceResourceDevice}
	client := &fakeSpaceClient{pages: []resourcePage{{resources: []tuya.Resource{lobbyLight}}}}
	door := newSpaceDoor(t, client)
	ok, err := door.ContainsDevice(context.Background(), "owner-1", "dev-lobby")
	if err != nil {
		t.Fatalf("ContainsDevice: unexpected error: %v", err)
	}
	if !ok {
		t.Error("ContainsDevice: got false for a device in the owner's subtree")
	}
	if client.resourcesOf != ownerSpace {
		t.Errorf("scanned space %d, want the owner's space %d", client.resourcesOf, ownerSpace)
	}
}

func TestContainsDeviceAbsentIsFalseNotError(t *testing.T) {
	client := &fakeSpaceClient{pages: []resourcePage{{resources: []tuya.Resource{{ID: "dev-other", Type: tuya.SpaceResourceDevice}}}}}
	door := newSpaceDoor(t, client)
	ok, err := door.ContainsDevice(context.Background(), "owner-1", "dev-foreign")
	if err != nil {
		t.Fatalf("ContainsDevice: got error %v, want a plain false", err)
	}
	if ok {
		t.Error("ContainsDevice: got true for a device outside the owner's subtree")
	}
}

func TestContainsDeviceReadsPagesUntilItFindsTheDevice(t *testing.T) {
	client := &fakeSpaceClient{pages: []resourcePage{
		{resources: []tuya.Resource{{ID: "dev-1", Type: tuya.SpaceResourceDevice}}, cursor: 42},
		{resources: []tuya.Resource{{ID: "dev-2", Type: tuya.SpaceResourceDevice}}, cursor: 43},
	}}
	door := newSpaceDoor(t, client)
	ok, err := door.ContainsDevice(context.Background(), "owner-1", "dev-2")
	if err != nil || !ok {
		t.Fatalf("ContainsDevice = %v, %v; want true, nil", ok, err)
	}
	if client.resourceCalls != 2 {
		t.Errorf("read %d pages, want 2 (the device is on the second)", client.resourceCalls)
	}
}

func TestContainsDeviceStopsOnAStalledCursor(t *testing.T) {
	client := &fakeSpaceClient{pages: []resourcePage{
		{resources: []tuya.Resource{{ID: "dev-1", Type: tuya.SpaceResourceDevice}}, cursor: 42},
		{resources: []tuya.Resource{{ID: "dev-1", Type: tuya.SpaceResourceDevice}}, cursor: 42},
	}}
	door := newSpaceDoor(t, client)
	ok, err := door.ContainsDevice(context.Background(), "owner-1", "dev-absent")
	if err != nil || ok {
		t.Fatalf("ContainsDevice = %v, %v; want false, nil", ok, err)
	}
	if client.resourceCalls != 2 {
		t.Errorf("read %d pages, want 2 before the cursor stalled", client.resourceCalls)
	}
}

func TestContainsDeviceGivesUpRatherThanPageForever(t *testing.T) {
	client := &fakeSpaceClient{endlessPages: true}
	door := newSpaceDoor(t, client)
	ok, err := door.ContainsDevice(context.Background(), "owner-1", "dev-absent")
	if err == nil {
		t.Fatal("ContainsDevice: got nil error, want the scan to give up")
	}
	if ok {
		t.Error("ContainsDevice: got true alongside an error")
	}
	if client.resourceCalls > 100 {
		t.Errorf("read %d pages, want the scan capped", client.resourceCalls)
	}
}

func TestContainsSpace(t *testing.T) {
	client := &fakeSpaceClient{contains: map[int64]bool{insideRoom: true}}
	door := newSpaceDoor(t, client)
	ctx := context.Background()
	ok, err := door.ContainsSpace(ctx, "owner-1", insideRoom)
	if err != nil || !ok {
		t.Fatalf("ContainsSpace(insideRoom) = %v, %v; want true, nil", ok, err)
	}
	ok, err = door.ContainsSpace(ctx, "owner-1", foreign)
	if err != nil {
		t.Fatalf("ContainsSpace(foreign): got error %v, want a plain false", err)
	}
	if ok {
		t.Error("ContainsSpace(foreign) = true, want false")
	}
	before := len(client.relationQueries)
	ok, err = door.ContainsSpace(ctx, "owner-1", 0)
	if err != nil || !ok {
		t.Fatalf("ContainsSpace(0) = %v, %v; want true, nil", ok, err)
	}
	if len(client.relationQueries) != before {
		t.Error("ContainsSpace(0) asked Tuya about the owner's own space")
	}
}

func TestSpaceDoorStopsWhenTheOwnerHasNoSpace(t *testing.T) {
	client := &fakeSpaceClient{}
	store := &fakeSpaceStore{err: ErrNotLinked}
	door := NewService(client, store)
	if _, err := door.Space(context.Background(), "owner-1", insideRoom); !errors.Is(err, ErrNotLinked) {
		t.Errorf("Space error = %v, want ErrNotLinked", err)
	}
	if len(client.relationQueries) != 0 {
		t.Errorf("asked Tuya %d times without a linked space, want none", len(client.relationQueries))
	}
}

func TestZeroSpaceIDMeansTheOwnersSpace(t *testing.T) {
	client := &fakeSpaceClient{}
	door := newSpaceDoor(t, client)
	ctx := context.Background()
	if _, err := door.Space(ctx, "owner-1", 0); err != nil {
		t.Fatalf("Space: unexpected error: %v", err)
	}
	if client.queried != ownerSpace {
		t.Errorf("queried space %d, want the owner's space %d", client.queried, ownerSpace)
	}
	if _, _, err := door.ChildSpaces(ctx, "owner-1", 0, true, tuya.Page{}); err != nil {
		t.Fatalf("ChildSpaces: unexpected error: %v", err)
	}
	if len(client.listed) != 1 || client.listed[0] != ownerSpace {
		t.Errorf("listed %v, want exactly the owner's space %d — a zero id here would list the whole project", client.listed, ownerSpace)
	}
	if _, err := door.CreateSpace(ctx, "owner-1", "Room 3", 0, ""); err != nil {
		t.Fatalf("CreateSpace: unexpected error: %v", err)
	}
	if client.createdParent != ownerSpace {
		t.Errorf("created under %d, want the owner's space %d", client.createdParent, ownerSpace)
	}
	if len(client.relationQueries) != 0 {
		t.Errorf("asked Tuya about the owner's own space %d times, want none", len(client.relationQueries))
	}
}

func TestTheDoorNeverListsTheWholeProject(t *testing.T) {
	ctx := context.Background()
	unlinked := &fakeSpaceClient{}
	door := NewService(unlinked, &fakeSpaceStore{err: ErrNotLinked})
	if _, _, err := door.ChildSpaces(ctx, "owner-1", 0, true, tuya.Page{}); !errors.Is(err, ErrNotLinked) {
		t.Errorf("ChildSpaces error = %v, want ErrNotLinked", err)
	}
	zeroLink := &fakeSpaceClient{}
	door = NewService(zeroLink, &fakeSpaceStore{space: Space{Owner: "owner-1"}})
	if _, _, err := door.ChildSpaces(ctx, "owner-1", 0, true, tuya.Page{}); !errors.Is(err, ErrNotLinked) {
		t.Errorf("ChildSpaces error = %v, want ErrNotLinked for a zero linked space", err)
	}
	for name, client := range map[string]*fakeSpaceClient{"unlinked owner": unlinked, "zero linked space": zeroLink} {
		if len(client.listed) != 0 {
			t.Errorf("%s: called ListSpaces with %v — a zero id there lists every top level space in the project", name, client.listed)
		}
	}
}

func TestTheOwnersSpaceCannotBeDeletedThroughTheDoor(t *testing.T) {
	client := &fakeSpaceClient{contains: map[int64]bool{ownerSpace: true}}
	door := newSpaceDoor(t, client)
	ctx := context.Background()
	if err := door.DeleteSpace(ctx, "owner-1", ownerSpace); !errors.Is(err, ErrOwnerSpaceProtected) {
		t.Errorf("DeleteSpace(owner's space) error = %v, want ErrOwnerSpaceProtected", err)
	}
	if err := door.DeleteSpace(ctx, "owner-1", 0); !errors.Is(err, ErrOwnerSpaceProtected) {
		t.Errorf("DeleteSpace(0) error = %v, want ErrOwnerSpaceProtected", err)
	}
	if client.deleted != 0 {
		t.Errorf("deleted space %d, want none", client.deleted)
	}
}

func TestASpaceOutsideTheProjectIsRefusedAsUnowned(t *testing.T) {
	client := &fakeSpaceClient{containsErr: &tuya.APIError{Code: CodeNoSpacePermission, Msg: "No space permission"}}
	door := newSpaceDoor(t, client)
	if _, err := door.Space(context.Background(), "owner-1", foreign); !errors.Is(err, ErrNotOwned) {
		t.Errorf("Space error = %v, want ErrNotOwned", err)
	}
	if client.queried != 0 {
		t.Errorf("queried space %d past the guard", client.queried)
	}
}

func TestSpaceDoorReportsAFailedOwnershipCheck(t *testing.T) {
	client := &fakeSpaceClient{containsErr: errors.New("tuya unreachable")}
	door := newSpaceDoor(t, client)
	err := door.ModifySpace(context.Background(), "owner-1", insideRoom, "Renamed", "")
	if err == nil {
		t.Fatal("ModifySpace: got nil error, want the failed ownership check")
	}
	if client.modified != 0 {
		t.Errorf("modified space %d despite the failed check", client.modified)
	}
}
