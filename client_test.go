package tuya_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"go.naturallyfunny.dev/tuya"
	"go.naturallyfunny.dev/tuya/cloud"
)

// fakeStore is an in-memory AccountStore. Only Get is exercised; Link/Unlink
// panic so an accidental call is loud.
type fakeStore struct {
	acc      tuya.Account
	err      error
	gotOwner string
}

func (f *fakeStore) Get(_ context.Context, owner string) (tuya.Account, error) {
	f.gotOwner = owner
	return f.acc, f.err
}

func (f *fakeStore) Link(context.Context, string, string) (tuya.Account, error) {
	panic("Link not expected in these tests")
}

func (f *fakeStore) Unlink(context.Context, string) error {
	panic("Unlink not expected in these tests")
}

// fakeIoT is a recording stub for the tuya.IoT facade. It records which device
// IDs channel names were requested for, so tests can prove the ownership guard
// stays lean while the human-facing listing does resolve labels.
type fakeIoT struct {
	devices      []cloud.Device
	status       []cloud.DataPoint
	channels     map[string][]cloud.Channel
	listErr      error
	statusErr    error
	sendErr      error
	channelErr   error
	mu           sync.Mutex
	listUIDs     []string
	channelIDs   []string
	statusCalled bool
	sendCalled   bool
	sentDeviceID string
	sentCmds     []cloud.DataPoint
}

func (f *fakeIoT) ListDevices(_ context.Context, tuyaUID string) ([]cloud.Device, error) {
	f.listUIDs = append(f.listUIDs, tuyaUID)
	// Hand back a copy: Client writes CodeNameMapping into the slice it gets,
	// and a shared backing array would leak that between calls.
	out := make([]cloud.Device, len(f.devices))
	copy(out, f.devices)
	return out, f.listErr
}

// DeviceChannelNames is called concurrently by resolveChannelNames, so its
// recording is mutex-guarded.
func (f *fakeIoT) DeviceChannelNames(_ context.Context, deviceID string) ([]cloud.Channel, error) {
	f.mu.Lock()
	f.channelIDs = append(f.channelIDs, deviceID)
	f.mu.Unlock()
	if f.channelErr != nil {
		return nil, f.channelErr
	}
	return f.channels[deviceID], nil
}

// listCalled reports whether ListDevices was reached at all — used to prove a
// call short-circuited before touching Tuya.
func (f *fakeIoT) listCalled() bool { return len(f.listUIDs) > 0 }

func (f *fakeIoT) DeviceStatus(_ context.Context, _ string) ([]cloud.DataPoint, error) {
	f.statusCalled = true
	return f.status, f.statusErr
}

func (f *fakeIoT) SendCommands(_ context.Context, deviceID string, cmds []cloud.DataPoint) error {
	f.sendCalled = true
	f.sentDeviceID = deviceID
	f.sentCmds = cmds
	return f.sendErr
}

func linkedAccount() tuya.Account {
	return tuya.Account{Owner: "owner-1", TuyaUID: "uid-1"}
}

// ownedDevices is the account listing the guard sees when dev-1 belongs to the
// resolved owner. It is multi-gang so tests can prove the guard skips labels
// even when they would apply.
func ownedDevices() []cloud.Device {
	return []cloud.Device{{ID: "dev-1", Category: "kg"}}
}

func TestListDevices(t *testing.T) {
	store := &fakeStore{acc: linkedAccount()}
	iot := &fakeIoT{devices: []cloud.Device{{ID: "dev-1"}}}
	c := tuya.New(iot, store)
	got, err := c.ListDevices(context.Background(), "owner-1")
	if err != nil {
		t.Fatalf("ListDevices: unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].ID != "dev-1" {
		t.Fatalf("ListDevices: got %+v, want one device dev-1", got)
	}
	if store.gotOwner != "owner-1" {
		t.Errorf("store.Get called with %q, want owner-1", store.gotOwner)
	}
	// A device with no category is not multi-gang: no label request, but the
	// mapping is still a non-nil empty slice.
	if len(iot.channelIDs) != 0 {
		t.Errorf("channel names requested for %v, want none", iot.channelIDs)
	}
	if got[0].CodeNameMapping == nil {
		t.Error("CodeNameMapping is nil, want an empty non-nil slice")
	}
}

// Only multi-gang categories are worth the extra request, and their labels must
// land on the right device.
func TestListDevicesResolvesMultiGangChannelNames(t *testing.T) {
	store := &fakeStore{acc: linkedAccount()}
	iot := &fakeIoT{
		devices: []cloud.Device{
			{ID: "switch-1", Category: "kg"},
			{ID: "outlet-1", Category: "cz"},
			{ID: "sensor-1", Category: "wsdcg"},
		},
		channels: map[string][]cloud.Channel{
			"switch-1": {{Identifier: "switch_1", Name: "Kitchen light"}},
			"outlet-1": {{Identifier: "switch_1", Name: "Fridge"}},
		},
	}
	c := tuya.New(iot, store)
	got, err := c.ListDevices(context.Background(), "owner-1")
	if err != nil {
		t.Fatalf("ListDevices: unexpected error: %v", err)
	}
	byID := map[string][]cloud.Channel{}
	for _, d := range got {
		byID[d.ID] = d.CodeNameMapping
	}
	if len(byID["switch-1"]) != 1 || byID["switch-1"][0].Name != "Kitchen light" {
		t.Errorf("switch-1 mapping = %+v, want Kitchen light", byID["switch-1"])
	}
	if len(byID["outlet-1"]) != 1 || byID["outlet-1"][0].Name != "Fridge" {
		t.Errorf("outlet-1 mapping = %+v, want Fridge", byID["outlet-1"])
	}
	if len(byID["sensor-1"]) != 0 || byID["sensor-1"] == nil {
		t.Errorf("sensor-1 mapping = %+v, want empty non-nil", byID["sensor-1"])
	}
	if len(iot.channelIDs) != 2 {
		t.Errorf("channel names requested for %v, want only the two multi-gang devices", iot.channelIDs)
	}
}

// A device whose labels fail to resolve must not silently disappear from the
// error: the whole listing fails, naming the device.
func TestListDevicesReportsChannelNameFailure(t *testing.T) {
	store := &fakeStore{acc: linkedAccount()}
	iot := &fakeIoT{
		devices:    []cloud.Device{{ID: "switch-1", Category: "kg"}},
		channelErr: errors.New("boom"),
	}
	c := tuya.New(iot, store)
	_, err := c.ListDevices(context.Background(), "owner-1")
	if err == nil {
		t.Fatal("ListDevices: got nil error, want the channel-name failure")
	}
	if !strings.Contains(err.Error(), "switch-1") {
		t.Errorf("error %q does not name the failing device", err)
	}
}

func TestListDevicesAccountNotLinked(t *testing.T) {
	store := &fakeStore{err: tuya.ErrAccountNotLinked}
	iot := &fakeIoT{}
	c := tuya.New(iot, store)
	_, err := c.ListDevices(context.Background(), "owner-1")
	if !errors.Is(err, tuya.ErrAccountNotLinked) {
		t.Fatalf("ListDevices: got %v, want ErrAccountNotLinked", err)
	}
	if iot.listCalled() {
		t.Error("ListDevices delegated to IoT despite unlinked account")
	}
}

func TestDeviceStatusOwned(t *testing.T) {
	store := &fakeStore{acc: linkedAccount()}
	iot := &fakeIoT{devices: ownedDevices(), status: []cloud.DataPoint{{Code: "switch_1", Value: true}}}
	c := tuya.New(iot, store)
	got, err := c.DeviceStatus(context.Background(), "owner-1", "dev-1")
	if err != nil {
		t.Fatalf("DeviceStatus: unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Code != "switch_1" {
		t.Fatalf("DeviceStatus: got %+v, want switch_1", got)
	}
	if len(iot.listUIDs) != 1 || iot.listUIDs[0] != "uid-1" {
		t.Errorf("guard listed uids %v, want one call with uid-1", iot.listUIDs)
	}
	// The guard must stay lean: no channel-name fetches on ownership checks,
	// even though the device it guards is multi-gang.
	if len(iot.channelIDs) != 0 {
		t.Errorf("guard requested channel names for %v, want none", iot.channelIDs)
	}
}

func TestDeviceStatusNotOwned(t *testing.T) {
	store := &fakeStore{acc: linkedAccount()}
	iot := &fakeIoT{devices: []cloud.Device{{ID: "someone-elses-device"}}}
	c := tuya.New(iot, store)
	_, err := c.DeviceStatus(context.Background(), "owner-1", "dev-1")
	if !errors.Is(err, tuya.ErrDeviceNotOwned) {
		t.Fatalf("DeviceStatus: got %v, want ErrDeviceNotOwned", err)
	}
	if iot.statusCalled {
		t.Error("DeviceStatus read status despite failed ownership check")
	}
}

func TestSendCommandsOwned(t *testing.T) {
	store := &fakeStore{acc: linkedAccount()}
	iot := &fakeIoT{devices: ownedDevices()}
	c := tuya.New(iot, store)
	cmds := []cloud.DataPoint{{Code: "switch_1", Value: false}}
	if err := c.SendCommands(context.Background(), "owner-1", "dev-1", cmds); err != nil {
		t.Fatalf("SendCommands: unexpected error: %v", err)
	}
	if !iot.sendCalled || iot.sentDeviceID != "dev-1" {
		t.Fatalf("SendCommands not delegated correctly: called=%v device=%q", iot.sendCalled, iot.sentDeviceID)
	}
	if len(iot.sentCmds) != 1 || iot.sentCmds[0].Code != "switch_1" {
		t.Fatalf("SendCommands passed %+v, want switch_1", iot.sentCmds)
	}
}

func TestSendCommandsNotOwned(t *testing.T) {
	store := &fakeStore{acc: linkedAccount()}
	iot := &fakeIoT{devices: []cloud.Device{{ID: "someone-elses-device"}}}
	c := tuya.New(iot, store)
	err := c.SendCommands(context.Background(), "owner-1", "dev-1", nil)
	if !errors.Is(err, tuya.ErrDeviceNotOwned) {
		t.Fatalf("SendCommands: got %v, want ErrDeviceNotOwned", err)
	}
	if iot.sendCalled {
		t.Error("SendCommands sent commands despite failed ownership check")
	}
}

func TestSendCommandsAccountNotLinked(t *testing.T) {
	store := &fakeStore{err: tuya.ErrAccountNotLinked}
	iot := &fakeIoT{}
	c := tuya.New(iot, store)
	err := c.SendCommands(context.Background(), "owner-1", "dev-1", nil)
	if !errors.Is(err, tuya.ErrAccountNotLinked) {
		t.Fatalf("SendCommands: got %v, want ErrAccountNotLinked", err)
	}
	if iot.sendCalled {
		t.Error("SendCommands delegated despite unlinked account")
	}
}

// A listing that fails must surface as that failure, not as ErrDeviceNotOwned:
// an unreachable Tuya is not evidence about who owns what.
func TestAssertOwnedSurfacesListError(t *testing.T) {
	sentinel := errors.New("boom")
	store := &fakeStore{acc: linkedAccount()}
	iot := &fakeIoT{listErr: sentinel}
	c := tuya.New(iot, store)
	_, err := c.DeviceStatus(context.Background(), "owner-1", "dev-1")
	if !errors.Is(err, sentinel) {
		t.Fatalf("DeviceStatus: got %v, want wrapped sentinel", err)
	}
	if errors.Is(err, tuya.ErrDeviceNotOwned) {
		t.Error("a failed lookup was reported as ErrDeviceNotOwned")
	}
	if iot.statusCalled {
		t.Error("DeviceStatus read status despite a failed ownership check")
	}
}

func TestAccount(t *testing.T) {
	store := &fakeStore{acc: linkedAccount()}
	c := tuya.New(&fakeIoT{}, store)
	acc, err := c.Account(context.Background(), "owner-1")
	if err != nil {
		t.Fatalf("Account: unexpected error: %v", err)
	}
	if acc.TuyaUID != "uid-1" {
		t.Errorf("Account: got uid %q, want uid-1", acc.TuyaUID)
	}
}
