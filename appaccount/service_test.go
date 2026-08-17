package appaccount

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"go.naturallyfunny.dev/tuya"
)

type fakeStore struct {
	acc           Account
	err           error
	gotOwner      string
	linked        [2]string
	unlinkedOwner string
}

func (f *fakeStore) Get(_ context.Context, owner string) (Account, error) {
	f.gotOwner = owner
	return f.acc, f.err
}

func (f *fakeStore) Link(_ context.Context, owner, tuyaUID string) (Account, error) {
	f.linked = [2]string{owner, tuyaUID}
	return Account{Owner: owner, TuyaUID: tuyaUID}, f.err
}

func (f *fakeStore) Unlink(_ context.Context, owner string) error {
	f.unlinkedOwner = owner
	return f.err
}

type fakeClient struct {
	devices      []tuya.UserDevice
	listErr      error
	listUIDs     []string
	listOpts     int
	statusOf     string
	propertiesOf string
	askedCodes   []string
	sentTo       string
	sent         []tuya.DataPoint
	namesOf      string
	namedDevices []tuya.Device
}

func (f *fakeClient) UserDevices(_ context.Context, tuyaUID string, opts ...tuya.DeviceOption) ([]tuya.UserDevice, error) {
	f.listUIDs = append(f.listUIDs, tuyaUID)
	f.listOpts = len(opts)
	out := make([]tuya.UserDevice, len(f.devices))
	copy(out, f.devices)
	return out, f.listErr
}

func (f *fakeClient) UserHasDevice(ctx context.Context, tuyaUID, deviceID string) (bool, error) {
	devices, err := f.UserDevices(ctx, tuyaUID)
	if err != nil {
		return false, fmt.Errorf("list devices of user %s: %w", tuyaUID, err)
	}
	for _, device := range devices {
		if device.ID == deviceID {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeClient) DeviceStatus(_ context.Context, deviceID string) ([]tuya.DataPoint, error) {
	f.statusOf = deviceID
	return []tuya.DataPoint{{Code: "switch_1", Value: true}}, nil
}

func (f *fakeClient) DeviceProperties(_ context.Context, deviceID string, codes []string) ([]tuya.Property, error) {
	f.propertiesOf = deviceID
	f.askedCodes = codes
	return []tuya.Property{{Code: "switch_led", Value: true}}, nil
}

func (f *fakeClient) SendCommands(_ context.Context, deviceID string, commands []tuya.DataPoint) error {
	f.sentTo = deviceID
	f.sent = commands
	return nil
}

func (f *fakeClient) DeviceChannelNames(_ context.Context, deviceID string) ([]tuya.Channel, error) {
	f.namesOf = deviceID
	return []tuya.Channel{{Identifier: "switch_1", Name: "Kitchen light"}}, nil
}

func (f *fakeClient) ChannelNames(_ context.Context, devices []tuya.Device) (map[string][]tuya.Channel, error) {
	f.namedDevices = devices
	return map[string][]tuya.Channel{}, nil
}

func (f *fakeClient) listCalled() bool { return len(f.listUIDs) > 0 }

func linkedAccount() Account {
	return Account{Owner: "owner-1", TuyaUID: "uid-1"}
}

func ownedDevices() []tuya.UserDevice {
	return []tuya.UserDevice{{Device: tuya.Device{ID: "dev-1", Category: "kg"}}}
}

func TestDevices(t *testing.T) {
	store := &fakeStore{acc: linkedAccount()}
	client := &fakeClient{devices: []tuya.UserDevice{{Device: tuya.Device{ID: "dev-1"}}}}
	c := NewService(client, store)
	got, err := c.Devices(context.Background(), "owner-1")
	if err != nil {
		t.Fatalf("Devices: unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].ID != "dev-1" {
		t.Fatalf("Devices: got %+v, want one device dev-1", got)
	}
	if store.gotOwner != "owner-1" {
		t.Errorf("store.Get called with %q, want owner-1", store.gotOwner)
	}
	if client.listOpts != 0 {
		t.Errorf("Devices passed %d options, want none: the door adds nothing of its own", client.listOpts)
	}
}

func TestDevicesPassesTheOptionsOn(t *testing.T) {
	store := &fakeStore{acc: linkedAccount()}
	client := &fakeClient{devices: ownedDevices()}
	c := NewService(client, store)
	if _, err := c.Devices(context.Background(), "owner-1", tuya.WithChannelNames()); err != nil {
		t.Fatalf("Devices: unexpected error: %v", err)
	}
	if client.listOpts != 1 {
		t.Errorf("client received %d options, want the one the caller passed", client.listOpts)
	}
}

func TestDevicesAccountNotLinked(t *testing.T) {
	store := &fakeStore{err: ErrNotLinked}
	client := &fakeClient{}
	c := NewService(client, store)
	_, err := c.Devices(context.Background(), "owner-1")
	if !errors.Is(err, ErrNotLinked) {
		t.Fatalf("Devices: got %v, want ErrNotLinked", err)
	}
	if client.listCalled() {
		t.Error("Devices delegated to Client despite unlinked account")
	}
}

func TestHasDevice(t *testing.T) {
	store := &fakeStore{acc: linkedAccount()}
	client := &fakeClient{devices: ownedDevices()}
	c := NewService(client, store)
	ok, err := c.HasDevice(context.Background(), "owner-1", "dev-1")
	if err != nil {
		t.Fatalf("HasDevice: unexpected error: %v", err)
	}
	if !ok {
		t.Error("HasDevice: got false for a device the account lists")
	}
	if len(client.listUIDs) != 1 || client.listUIDs[0] != "uid-1" {
		t.Errorf("HasDevice listed uids %v, want one call with uid-1", client.listUIDs)
	}
	if client.listOpts != 0 {
		t.Errorf("HasDevice asked for %d options, want none: it needs identity, not labels", client.listOpts)
	}
}

func TestHasDeviceAbsentIsFalseNotError(t *testing.T) {
	store := &fakeStore{acc: linkedAccount()}
	client := &fakeClient{devices: []tuya.UserDevice{{Device: tuya.Device{ID: "someone-elses-device"}}}}
	c := NewService(client, store)
	ok, err := c.HasDevice(context.Background(), "owner-1", "dev-1")
	if err != nil {
		t.Fatalf("HasDevice: got error %v, want a plain false", err)
	}
	if ok {
		t.Error("HasDevice: got true for a device the account does not list")
	}
}

func TestHasDeviceAccountNotLinked(t *testing.T) {
	store := &fakeStore{err: ErrNotLinked}
	client := &fakeClient{}
	c := NewService(client, store)
	if _, err := c.HasDevice(context.Background(), "owner-1", "dev-1"); !errors.Is(err, ErrNotLinked) {
		t.Fatalf("HasDevice: got %v, want ErrNotLinked", err)
	}
	if client.listCalled() {
		t.Error("HasDevice listed devices despite an unlinked account")
	}
}

func TestTheDoorNeverAsksTuyaAboutAnEmptyUID(t *testing.T) {
	ctx := context.Background()
	listing := &fakeClient{}
	c := NewService(listing, &fakeStore{acc: Account{Owner: "owner-1"}})
	if _, err := c.Devices(ctx, "owner-1"); !errors.Is(err, ErrNotLinked) {
		t.Errorf("Devices error = %v, want ErrNotLinked for an empty linked uid", err)
	}
	asking := &fakeClient{}
	c = NewService(asking, &fakeStore{acc: Account{Owner: "owner-1"}})
	if _, err := c.HasDevice(ctx, "owner-1", "dev-1"); !errors.Is(err, ErrNotLinked) {
		t.Errorf("HasDevice error = %v, want ErrNotLinked for an empty linked uid", err)
	}
	for name, client := range map[string]*fakeClient{"Devices": listing, "HasDevice": asking} {
		if client.listCalled() {
			t.Errorf("%s called UserDevices with %v — an empty uid there asks Tuya about /users//devices", name, client.listUIDs)
		}
	}
}

func TestTheDeviceAddressedCallsPassThroughWithoutOwnership(t *testing.T) {
	ctx := context.Background()
	client := &fakeClient{}
	c := NewService(client, &fakeStore{err: ErrNotLinked})

	if _, err := c.DeviceStatus(ctx, "dev-1"); err != nil {
		t.Fatalf("DeviceStatus: unexpected error: %v", err)
	}
	if _, err := c.DeviceProperties(ctx, "dev-1", []string{"switch_led"}); err != nil {
		t.Fatalf("DeviceProperties: unexpected error: %v", err)
	}
	commands := []tuya.DataPoint{{Code: "switch_1", Value: true}}
	if err := c.SendCommands(ctx, "dev-1", commands); err != nil {
		t.Fatalf("SendCommands: unexpected error: %v", err)
	}
	if _, err := c.DeviceChannelNames(ctx, "dev-1"); err != nil {
		t.Fatalf("DeviceChannelNames: unexpected error: %v", err)
	}
	if _, err := c.ChannelNames(ctx, []tuya.Device{{ID: "dev-1", Category: "kg"}}); err != nil {
		t.Fatalf("ChannelNames: unexpected error: %v", err)
	}

	if client.statusOf != "dev-1" || client.propertiesOf != "dev-1" || client.sentTo != "dev-1" || client.namesOf != "dev-1" {
		t.Errorf("device id reached the client as status=%q properties=%q send=%q names=%q, want dev-1 for each",
			client.statusOf, client.propertiesOf, client.sentTo, client.namesOf)
	}
	if len(client.askedCodes) != 1 || client.askedCodes[0] != "switch_led" {
		t.Errorf("DeviceProperties codes arrived as %v, want the caller's own", client.askedCodes)
	}
	if len(client.sent) != 1 || client.sent[0].Code != "switch_1" {
		t.Errorf("commands arrived as %+v, want the caller's own", client.sent)
	}
	if len(client.namedDevices) != 1 || client.namedDevices[0].ID != "dev-1" {
		t.Errorf("ChannelNames received %+v, want the caller's own devices", client.namedDevices)
	}
	if client.listCalled() {
		t.Error("a device-addressed call listed the owner's devices — it must not check ownership")
	}
}

func TestHasDeviceSurfacesListError(t *testing.T) {
	sentinel := errors.New("boom")
	store := &fakeStore{acc: linkedAccount()}
	client := &fakeClient{listErr: sentinel}
	c := NewService(client, store)
	ok, err := c.HasDevice(context.Background(), "owner-1", "dev-1")
	if !errors.Is(err, sentinel) {
		t.Fatalf("HasDevice: got %v, want wrapped sentinel", err)
	}
	if ok {
		t.Error("HasDevice: got true alongside an error")
	}
}

func TestLinkAndUnlink(t *testing.T) {
	ctx := context.Background()
	store := &fakeStore{}
	c := NewService(&fakeClient{}, store)

	acc, err := c.Link(ctx, "owner-1", "uid-1")
	if err != nil {
		t.Fatalf("Link: unexpected error: %v", err)
	}
	if store.linked != [2]string{"owner-1", "uid-1"} {
		t.Errorf("store.Link called with %v, want owner-1 and uid-1", store.linked)
	}
	if acc.Owner != "owner-1" || acc.TuyaUID != "uid-1" {
		t.Errorf("Link returned %+v, want the store's own account", acc)
	}

	if err := c.Unlink(ctx, "owner-1"); err != nil {
		t.Fatalf("Unlink: unexpected error: %v", err)
	}
	if store.unlinkedOwner != "owner-1" {
		t.Errorf("store.Unlink called with %q, want owner-1", store.unlinkedOwner)
	}
}

func TestLinkSurfacesTheStoreError(t *testing.T) {
	ctx := context.Background()
	sentinel := errors.New("boom")
	c := NewService(&fakeClient{}, &fakeStore{err: sentinel})
	if _, err := c.Link(ctx, "owner-1", "uid-1"); !errors.Is(err, sentinel) {
		t.Errorf("Link error = %v, want the store's own", err)
	}
	if err := c.Unlink(ctx, "owner-1"); !errors.Is(err, sentinel) {
		t.Errorf("Unlink error = %v, want the store's own", err)
	}
}

func TestGet(t *testing.T) {
	store := &fakeStore{acc: linkedAccount()}
	c := NewService(&fakeClient{}, store)
	acc, err := c.Get(context.Background(), "owner-1")
	if err != nil {
		t.Fatalf("Get: unexpected error: %v", err)
	}
	if acc.TuyaUID != "uid-1" {
		t.Errorf("Get: got uid %q, want uid-1", acc.TuyaUID)
	}
}
