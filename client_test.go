package tuya_test

import (
	"context"
	"errors"
	"testing"

	"go.naturallyfunny.dev/tuya"
	"go.naturallyfunny.dev/tuya/cloud"
)

// fakeStore is an in-memory AccountStore. Only Get is exercised; Link/Unlink
// panic so an accidental call is loud.
type fakeStore struct {
	acc tuya.Account
	err error

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

// fakeIoT is a recording stub for the tuya.IoT facade.
type fakeIoT struct {
	devices []cloud.Device
	status  []cloud.DataPoint
	owned   bool

	listErr   error
	statusErr error
	sendErr   error
	hasErr    error

	listCalled   bool
	statusCalled bool
	sendCalled   bool
	hasUID       string
	sentDeviceID string
	sentCmds     []cloud.DataPoint
}

func (f *fakeIoT) ListDevices(_ context.Context, _ string) ([]cloud.Device, error) {
	f.listCalled = true
	return f.devices, f.listErr
}

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

func (f *fakeIoT) HasDevice(_ context.Context, tuyaUID, _ string) (bool, error) {
	f.hasUID = tuyaUID
	return f.owned, f.hasErr
}

func linkedAccount() tuya.Account {
	return tuya.Account{Owner: "owner-1", TuyaUID: "uid-1"}
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
}

func TestListDevicesAccountNotLinked(t *testing.T) {
	store := &fakeStore{err: tuya.ErrAccountNotLinked}
	iot := &fakeIoT{}
	c := tuya.New(iot, store)

	_, err := c.ListDevices(context.Background(), "owner-1")
	if !errors.Is(err, tuya.ErrAccountNotLinked) {
		t.Fatalf("ListDevices: got %v, want ErrAccountNotLinked", err)
	}
	if iot.listCalled {
		t.Error("ListDevices delegated to IoT despite unlinked account")
	}
}

func TestDeviceStatusOwned(t *testing.T) {
	store := &fakeStore{acc: linkedAccount()}
	iot := &fakeIoT{owned: true, status: []cloud.DataPoint{{Code: "switch_1", Value: true}}}
	c := tuya.New(iot, store)

	got, err := c.DeviceStatus(context.Background(), "owner-1", "dev-1")
	if err != nil {
		t.Fatalf("DeviceStatus: unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Code != "switch_1" {
		t.Fatalf("DeviceStatus: got %+v, want switch_1", got)
	}
	if iot.hasUID != "uid-1" {
		t.Errorf("HasDevice called with uid %q, want uid-1", iot.hasUID)
	}
}

func TestDeviceStatusNotOwned(t *testing.T) {
	store := &fakeStore{acc: linkedAccount()}
	iot := &fakeIoT{owned: false}
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
	iot := &fakeIoT{owned: true}
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
	iot := &fakeIoT{owned: false}
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

func TestAssertOwnedWrapsHasDeviceError(t *testing.T) {
	sentinel := errors.New("boom")
	store := &fakeStore{acc: linkedAccount()}
	iot := &fakeIoT{hasErr: sentinel}
	c := tuya.New(iot, store)

	_, err := c.DeviceStatus(context.Background(), "owner-1", "dev-1")
	if !errors.Is(err, sentinel) {
		t.Fatalf("DeviceStatus: got %v, want wrapped sentinel", err)
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
