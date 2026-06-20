// Package tuya is the owner-scoped door of the library: callers drive Tuya
// devices by their own opaque owner ID instead of a Tuya UID. It is the surface
// an AI agent goes through — low traffic, one action per user intent (list
// devices, read a device's state, send it a command).
//
// The owner concept is foreign to Tuya. The cloud subpackage speaks the Tuya
// Cloud OpenAPI and is keyed by Tuya UID; an owner ID is the consumer's
// app-domain identity. This package quarantines that foreign concept, including
// the ownership guard: cloud.IoTClient is a trusted, device-addressed layer with
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
	"fmt"
	"time"

	"go.naturallyfunny.dev/tuya/cloud"
)

// Account links an opaque owner ID (whatever the consumer uses to identify a
// human) to that human's Tuya account UID. Devices are listed and controlled
// under the UID.
type Account struct {
	OwnerID   string    `json:"owner_id"`
	TuyaUID   string    `json:"tuya_uid"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ErrAccountNotLinked indicates the owner has no Tuya account linked, i.e. there
// is no owner-ID -> Tuya-UID mapping. Stores return it when no mapping exists and
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
	Get(ctx context.Context, ownerID string) (Account, error)
	Link(ctx context.Context, ownerID, tuyaUID string) (Account, error)
	Unlink(ctx context.Context, ownerID string) error
}

// IoT is the device-addressed Tuya facade this package drives, narrowed to the
// methods Client needs. *cloud.IoTClient satisfies it. It is defined here, on the
// consumer side, so Client can be unit-tested against a fake and so the cloud
// package stays free of speculative interfaces.
type IoT interface {
	ListDevices(ctx context.Context, tuyaUID string) ([]cloud.Device, error)
	DeviceStatus(ctx context.Context, deviceID string) ([]cloud.DataPoint, error)
	SendCommands(ctx context.Context, deviceID string, commands []cloud.DataPoint) error
	HasDevice(ctx context.Context, tuyaUID, deviceID string) (bool, error)
}

// Client drives Tuya devices for an owner, resolving owner -> UID via the store
// and delegating device work to the IoT facade. It is the ownership boundary:
// every mutating call asserts the device belongs to the resolved account before
// delegating to the (trust-all) IoT layer.
type Client struct {
	iot   IoT
	store AccountStore
}

// New builds a Client over the IoT facade and an account store. cloud.NewIoTClient
// returns a *cloud.IoTClient that satisfies IoT, and postgres.Store satisfies
// AccountStore, but any implementations of the interfaces work.
func New(iot IoT, store AccountStore) *Client {
	return &Client{iot: iot, store: store}
}

// Account returns the linked Tuya account for the owner. Useful for surfaces
// that need to surface the owner-ID / Tuya-UID mapping (e.g. a get_account tool).
func (c *Client) Account(ctx context.Context, ownerID string) (Account, error) {
	return c.store.Get(ctx, ownerID)
}

// ListDevices resolves the owner then lists their devices, each with its current
// status. Returns ErrAccountNotLinked (from the store) if the owner has no linked
// account.
func (c *Client) ListDevices(ctx context.Context, ownerID string) ([]cloud.Device, error) {
	acc, err := c.store.Get(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	return c.iot.ListDevices(ctx, acc.TuyaUID)
}

// DeviceStatus resolves the owner, asserts the device belongs to them, then
// reads one device's status. Returns ErrAccountNotLinked if the owner has no
// linked account, or ErrDeviceNotOwned if the device isn't on the resolved account.
func (c *Client) DeviceStatus(ctx context.Context, ownerID, deviceID string) ([]cloud.DataPoint, error) {
	acc, err := c.store.Get(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	if err := c.assertOwned(ctx, acc.TuyaUID, deviceID); err != nil {
		return nil, err
	}
	return c.iot.DeviceStatus(ctx, deviceID)
}

// SendCommands resolves the owner, asserts the device belongs to them, then sends
// DP commands to it. Returns ErrAccountNotLinked if the owner has no linked
// account, or ErrDeviceNotOwned if the device isn't on the resolved account.
func (c *Client) SendCommands(ctx context.Context, ownerID, deviceID string, cmds []cloud.DataPoint) error {
	acc, err := c.store.Get(ctx, ownerID)
	if err != nil {
		return err
	}
	if err := c.assertOwned(ctx, acc.TuyaUID, deviceID); err != nil {
		return err
	}
	return c.iot.SendCommands(ctx, deviceID, cmds)
}

// assertOwned checks that deviceID appears in the Tuya account's device list,
// returning ErrDeviceNotOwned if not. Uses IoT.HasDevice — a lean, unenriched
// check — so no channel-name fetches happen on ownership verification.
func (c *Client) assertOwned(ctx context.Context, tuyaUID, deviceID string) error {
	owned, err := c.iot.HasDevice(ctx, tuyaUID, deviceID)
	if err != nil {
		return fmt.Errorf("verify device ownership: %w", err)
	}
	if !owned {
		return ErrDeviceNotOwned
	}
	return nil
}
