package appaccount

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"go.naturallyfunny.dev/tuya"
)

type fakeStore struct {
	acc      AppAccount
	err      error
	gotOwner string
}

func (f *fakeStore) Get(_ context.Context, owner string) (AppAccount, error) {
	f.gotOwner = owner
	return f.acc, f.err
}

func (f *fakeStore) Link(context.Context, string, string) (AppAccount, error) {
	panic("Link not expected in these tests")
}

func (f *fakeStore) Unlink(context.Context, string) error {
	panic("Unlink not expected in these tests")
}

type fakeIoT struct {
	devices    []tuya.UserDevice
	channels   map[string][]tuya.Channel
	listErr    error
	channelErr error
	mu         sync.Mutex
	listUIDs   []string
	channelIDs []string
}

func (f *fakeIoT) UserDevices(_ context.Context, tuyaUID string) ([]tuya.UserDevice, error) {
	f.listUIDs = append(f.listUIDs, tuyaUID)
	out := make([]tuya.UserDevice, len(f.devices))
	copy(out, f.devices)
	return out, f.listErr
}

func (f *fakeIoT) DeviceChannelNames(_ context.Context, deviceID string) ([]tuya.Channel, error) {
	f.mu.Lock()
	f.channelIDs = append(f.channelIDs, deviceID)
	f.mu.Unlock()
	if f.channelErr != nil {
		return nil, f.channelErr
	}
	return f.channels[deviceID], nil
}

func (f *fakeIoT) listCalled() bool { return len(f.listUIDs) > 0 }

func linkedAccount() AppAccount {
	return AppAccount{Owner: "owner-1", TuyaUID: "uid-1"}
}

func ownedDevices() []tuya.UserDevice {
	return []tuya.UserDevice{{ID: "dev-1", Category: "kg"}}
}

func TestListDevices(t *testing.T) {
	store := &fakeStore{acc: linkedAccount()}
	iot := &fakeIoT{devices: []tuya.UserDevice{{ID: "dev-1"}}}
	c := NewService(iot, store)
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
	if len(iot.channelIDs) != 0 {
		t.Errorf("channel names requested for %v, want none", iot.channelIDs)
	}
	if got[0].Channels == nil {
		t.Error("Channels is nil, want an empty non-nil slice")
	}
}

func TestListDevicesResolvesMultiGangChannelNames(t *testing.T) {
	store := &fakeStore{acc: linkedAccount()}
	iot := &fakeIoT{
		devices: []tuya.UserDevice{
			{ID: "switch-1", Category: "kg"},
			{ID: "outlet-1", Category: "cz"},
			{ID: "sensor-1", Category: "wsdcg"},
		},
		channels: map[string][]tuya.Channel{
			"switch-1": {{Identifier: "switch_1", Name: "Kitchen light"}},
			"outlet-1": {{Identifier: "switch_1", Name: "Fridge"}},
		},
	}
	c := NewService(iot, store)
	got, err := c.ListDevices(context.Background(), "owner-1")
	if err != nil {
		t.Fatalf("ListDevices: unexpected error: %v", err)
	}
	byID := map[string][]tuya.Channel{}
	for _, d := range got {
		byID[d.ID] = d.Channels
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

func TestListDevicesReportsChannelNameFailure(t *testing.T) {
	store := &fakeStore{acc: linkedAccount()}
	iot := &fakeIoT{
		devices:    []tuya.UserDevice{{ID: "switch-1", Category: "kg"}},
		channelErr: errors.New("boom"),
	}
	c := NewService(iot, store)
	_, err := c.ListDevices(context.Background(), "owner-1")
	if err == nil {
		t.Fatal("ListDevices: got nil error, want the channel-name failure")
	}
	if !strings.Contains(err.Error(), "switch-1") {
		t.Errorf("error %q does not name the failing device", err)
	}
}

func TestListDevicesAccountNotLinked(t *testing.T) {
	store := &fakeStore{err: ErrAccountNotLinked}
	iot := &fakeIoT{}
	c := NewService(iot, store)
	_, err := c.ListDevices(context.Background(), "owner-1")
	if !errors.Is(err, ErrAccountNotLinked) {
		t.Fatalf("ListDevices: got %v, want ErrAccountNotLinked", err)
	}
	if iot.listCalled() {
		t.Error("ListDevices delegated to Client despite unlinked account")
	}
}

func TestHasDevice(t *testing.T) {
	store := &fakeStore{acc: linkedAccount()}
	iot := &fakeIoT{devices: ownedDevices()}
	c := NewService(iot, store)
	ok, err := c.HasDevice(context.Background(), "owner-1", "dev-1")
	if err != nil {
		t.Fatalf("HasDevice: unexpected error: %v", err)
	}
	if !ok {
		t.Error("HasDevice: got false for a device the account lists")
	}
	if len(iot.listUIDs) != 1 || iot.listUIDs[0] != "uid-1" {
		t.Errorf("HasDevice listed uids %v, want one call with uid-1", iot.listUIDs)
	}
	if len(iot.channelIDs) != 0 {
		t.Errorf("HasDevice requested channel names for %v, want none: it needs identity, not labels", iot.channelIDs)
	}
}

func TestHasDeviceAbsentIsFalseNotError(t *testing.T) {
	store := &fakeStore{acc: linkedAccount()}
	iot := &fakeIoT{devices: []tuya.UserDevice{{ID: "someone-elses-device"}}}
	c := NewService(iot, store)
	ok, err := c.HasDevice(context.Background(), "owner-1", "dev-1")
	if err != nil {
		t.Fatalf("HasDevice: got error %v, want a plain false", err)
	}
	if ok {
		t.Error("HasDevice: got true for a device the account does not list")
	}
}

func TestHasDeviceAccountNotLinked(t *testing.T) {
	store := &fakeStore{err: ErrAccountNotLinked}
	iot := &fakeIoT{}
	c := NewService(iot, store)
	if _, err := c.HasDevice(context.Background(), "owner-1", "dev-1"); !errors.Is(err, ErrAccountNotLinked) {
		t.Fatalf("HasDevice: got %v, want ErrAccountNotLinked", err)
	}
	if iot.listCalled() {
		t.Error("HasDevice listed devices despite an unlinked account")
	}
}

func TestHasDeviceSurfacesListError(t *testing.T) {
	sentinel := errors.New("boom")
	store := &fakeStore{acc: linkedAccount()}
	iot := &fakeIoT{listErr: sentinel}
	c := NewService(iot, store)
	ok, err := c.HasDevice(context.Background(), "owner-1", "dev-1")
	if !errors.Is(err, sentinel) {
		t.Fatalf("HasDevice: got %v, want wrapped sentinel", err)
	}
	if ok {
		t.Error("HasDevice: got true alongside an error")
	}
}

func TestAccount(t *testing.T) {
	store := &fakeStore{acc: linkedAccount()}
	c := NewService(&fakeIoT{}, store)
	acc, err := c.Account(context.Background(), "owner-1")
	if err != nil {
		t.Fatalf("Account: unexpected error: %v", err)
	}
	if acc.TuyaUID != "uid-1" {
		t.Errorf("Account: got uid %q, want uid-1", acc.TuyaUID)
	}
}
