// Package tuya is the owner-scoped door of the library: callers drive Tuya
// devices by their own opaque owner key instead of a Tuya UID. It is the surface
// an AI agent goes through — low traffic, one action per user intent (list
// devices, read a device's state, send it a command).
//
// The owner concept is foreign to Tuya. The cloud subpackage speaks the Tuya
// Cloud OpenAPI and is keyed by Tuya UID; an owner is whatever opaque string the
// consumer identifies a human by (a user ID, an email — the library does not
// care). This package quarantines that foreign concept, including
// the ownership guard: cloud.IoT is a trusted, device-addressed layer with
// no tenant check; Client is the single door an untrusted agent goes through, and
// it cannot be opened without resolving an owner first. Mapping owner -> Tuya UID
// is delegated to an AccountStore (a ready-made PostgreSQL one lives in the
// postgres subpackage).
//
// Dependency direction is acyclic: postgres -> tuya -> cloud.
package tuya

import (
	"context"
	"errors"
	"time"

	"go.naturallyfunny.dev/tuya/cloud"
)

// Account links an opaque owner (whatever the consumer uses to identify a
// human) to that human's Tuya account UID. Devices are listed and controlled
// under the UID.
type Account struct {
	Owner     string    `json:"owner"`
	TuyaUID   string    `json:"tuya_uid"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ErrAccountNotLinked indicates the owner has no Tuya account linked, i.e. there
// is no owner -> Tuya-UID mapping. Stores return it when no mapping exists and
// Client surfaces it, so consumers can route the human into the linking flow.
var ErrAccountNotLinked = errors.New("tuya: no tuya account linked to owner")

// ErrDeviceNotOwned indicates the targeted device does not belong to the owner's
// Tuya account. Returned by device commands before anything is sent, so an agent
// can never drive a device that isn't the human's.
var ErrDeviceNotOwned = errors.New("tuya: device does not belong to owner")

// AccountStore resolves and manages the owner -> Tuya UID mapping. Get reads it,
// Link creates or refreshes it, and Unlink removes it. postgres.Store satisfies
// this interface implicitly; consumers may supply any backend.
type AccountStore interface {
	Get(ctx context.Context, owner string) (Account, error)
	Link(ctx context.Context, owner, tuyaUID string) (Account, error)
	Unlink(ctx context.Context, owner string) error
}

// IoT is the device-addressed Tuya facade this package drives, narrowed to the
// methods Client needs. *cloud.IoT satisfies it. It is defined here, on the
// consumer side, so Client can be unit-tested against a fake and so the cloud
// package stays free of speculative interfaces.
//
// Every method here is one plain Tuya endpoint. Nothing this package needs is
// asked of the layer below: the ownership guard and the channel-name resolution
// are both composed here, from these primitives (see device.go), so the cloud
// package never has to know that owners or multi-gang categories exist.
type IoT interface {
	ListDevices(ctx context.Context, tuyaUID string) ([]cloud.Device, error)
	DeviceStatus(ctx context.Context, deviceID string) ([]cloud.DataPoint, error)
	SendCommands(ctx context.Context, deviceID string, commands []cloud.DataPoint) error
	DeviceChannelNames(ctx context.Context, deviceID string) ([]cloud.Channel, error)
}

// Client drives Tuya devices for an owner, resolving owner -> UID via the store
// and delegating device work to the IoT facade. It is the ownership boundary:
// every mutating call asserts the device belongs to the resolved account before
// delegating to the (trust-all) IoT layer.
type Client struct {
	iot   IoT
	store AccountStore
}

// New builds a Client over the IoT facade and an account store. cloud.NewIoT
// returns a *cloud.IoT that satisfies IoT, and postgres.Store satisfies
// AccountStore, but any implementations of the interfaces work.
func New(iot IoT, store AccountStore) *Client {
	return &Client{iot: iot, store: store}
}

// Account returns the linked Tuya account for the owner. Useful for surfaces
// that need to surface the owner / Tuya-UID mapping (e.g. a get_account tool).
func (c *Client) Account(ctx context.Context, owner string) (Account, error) {
	return c.store.Get(ctx, owner)
}
