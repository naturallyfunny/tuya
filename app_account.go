package tuya

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.naturallyfunny.dev/tuya/cloud"
)

// The app-account tenancy model, end to end: the owner -> Tuya-UID mapping it is
// keyed by, the door itself, and every device operation behind it. One file per
// door, because what differs between doors is the guard, and whoever audits
// tenancy should read one file rather than assemble it from two.

// Account links an opaque owner (whatever the consumer uses to identify a
// human) to that human's Tuya app-account UID. Devices are listed and controlled
// under the UID.
type Account struct {
	Owner     string    `json:"owner"`
	TuyaUID   string    `json:"tuya_uid"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ErrAccountNotLinked indicates the owner has no Tuya app account linked, i.e.
// there is no owner -> Tuya-UID mapping. Stores return it when no mapping exists
// and AppAccountClient surfaces it, so consumers can route the human into the
// linking flow.
var ErrAccountNotLinked = errors.New("tuya: no tuya account linked to owner")

// AccountStore resolves and manages the owner -> Tuya UID mapping. Get reads it,
// Link creates or refreshes it, and Unlink removes it. postgres.Store satisfies
// this interface implicitly; consumers may supply any backend.
type AccountStore interface {
	Get(ctx context.Context, owner string) (Account, error)
	Link(ctx context.Context, owner, tuyaUID string) (Account, error)
	Unlink(ctx context.Context, owner string) error
}

// AppAccountClient drives Tuya devices for an owner under the app-account
// tenancy model: each human holds their own Tuya app account, so the tenant
// boundary is a Tuya UID. It resolves owner -> UID via the store and delegates
// device work to the IoT facade. It is the ownership boundary: every call
// asserts the device belongs to the resolved account before delegating to the
// (trust-all) IoT layer.
type AppAccountClient struct {
	iot   IoT
	store AccountStore
}

// NewAppAccountClient builds an AppAccountClient over the IoT facade and an
// account store. cloud.NewIoT returns a *cloud.IoT that satisfies IoT, and
// postgres.Store satisfies AccountStore, but any implementations of the
// interfaces work.
func NewAppAccountClient(iot IoT, store AccountStore) *AppAccountClient {
	return &AppAccountClient{iot: iot, store: store}
}

// Account returns the linked Tuya app account for the owner. Useful for surfaces
// that need to surface the owner / Tuya-UID mapping (e.g. a get_account tool).
func (c *AppAccountClient) Account(ctx context.Context, owner string) (Account, error) {
	return c.store.Get(ctx, owner)
}

// ListDevices resolves the owner then lists their devices, each with its current
// status and, for multi-gang switches/outlets, the human's per-channel labels.
// Returns ErrAccountNotLinked (from the store) if the owner has no linked
// account.
func (c *AppAccountClient) ListDevices(ctx context.Context, owner string) ([]cloud.Device, error) {
	acc, err := c.store.Get(ctx, owner)
	if err != nil {
		return nil, err
	}
	devices, err := c.iot.ListDevices(ctx, acc.TuyaUID)
	if err != nil {
		return nil, err
	}
	if len(devices) == 0 {
		return []cloud.Device{}, nil
	}
	// Labels are worth their extra requests on this path: the result is read by
	// a human, or an agent speaking for one, who needs "Kitchen light" rather
	// than "switch_1". assertOwned deliberately skips them.
	if err := c.resolveChannelNames(ctx, devices); err != nil {
		return nil, fmt.Errorf("resolve channel names: %w", err)
	}
	return devices, nil
}

// DeviceStatus resolves the owner, asserts the device belongs to them, then
// reads one device's status. Returns ErrAccountNotLinked if the owner has no
// linked account, or ErrDeviceNotOwned if the device isn't on the resolved account.
func (c *AppAccountClient) DeviceStatus(ctx context.Context, owner, deviceID string) ([]cloud.DataPoint, error) {
	acc, err := c.store.Get(ctx, owner)
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
func (c *AppAccountClient) SendCommands(ctx context.Context, owner, deviceID string, cmds []cloud.DataPoint) error {
	acc, err := c.store.Get(ctx, owner)
	if err != nil {
		return err
	}
	if err := c.assertOwned(ctx, acc.TuyaUID, deviceID); err != nil {
		return err
	}
	return c.iot.SendCommands(ctx, deviceID, cmds)
}

// assertOwned checks that deviceID appears in the Tuya account's device list,
// returning ErrDeviceNotOwned if not.
//
// The whole guard is here rather than behind an IoT method: ownership is this
// package's concept, and asking cloud to answer it would shape that layer
// around a caller's need. What cloud provides is the primitive — a device list
// keyed by Tuya UID — and the account is the authority on what it contains.
//
// It resolves no channel names on purpose: the guard needs identity only, and
// labels would add one Tuya request per multi-gang device to every guarded call.
//
// A failed lookup returns the error, never ErrDeviceNotOwned: an unreachable
// Tuya must not be reported as a device the owner does not have.
func (c *AppAccountClient) assertOwned(ctx context.Context, tuyaUID, deviceID string) error {
	devices, err := c.iot.ListDevices(ctx, tuyaUID)
	if err != nil {
		return fmt.Errorf("verify device ownership: %w", err)
	}
	for _, d := range devices {
		if d.ID == deviceID {
			return nil
		}
	}
	return ErrDeviceNotOwned
}

// isMultiGang reports whether a device category ships in variants where each
// switch/outlet is a separately labelled channel. "kg" is Tuya's switch
// category; "cz" and its suffixed variants are outlets.
//
// This is a judgement about Tuya's catalogue, not a fact its API states, which
// is why it lives here and not in the cloud package. A category Tuya adds later
// is a change to this line, and to nothing else.
func isMultiGang(category string) bool {
	c := strings.ToLower(category)
	return c == "kg" || strings.HasPrefix(c, "cz")
}

// resolveChannelNames fills CodeNameMapping for multi-gang switches/outlets by
// asking Tuya for each one's labels concurrently. Devices the mapping does not
// apply to get an empty, non-nil slice, so the shape does not depend on category.
//
// Errors are collected rather than fail-fast: a caller listing a home wants
// every device that could be resolved, plus the full list of what failed.
// errgroup.WithContext would cancel the siblings on the first error, discarding
// exactly the information that makes a partial failure actionable.
func (c *AppAccountClient) resolveChannelNames(ctx context.Context, devices []cloud.Device) error {
	var targets []*cloud.Device
	for idx := range devices {
		device := &devices[idx]
		if device.CodeNameMapping == nil {
			device.CodeNameMapping = []cloud.Channel{}
		}
		if isMultiGang(device.Category) && device.ID != "" {
			targets = append(targets, device)
		}
	}
	if len(targets) == 0 {
		return nil
	}
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)
	for _, device := range targets {
		wg.Go(func() {
			channels, err := c.iot.DeviceChannelNames(ctx, device.ID)
			if err != nil {
				mu.Lock()
				errs = append(errs, fmt.Errorf("device %s: %w", device.ID, err))
				mu.Unlock()
				return
			}
			if channels == nil {
				return // keep the empty, non-nil slice set above
			}
			device.CodeNameMapping = channels
		})
	}
	wg.Wait()
	return errors.Join(errs...)
}
