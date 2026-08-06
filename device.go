package tuya

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"go.naturallyfunny.dev/tuya/cloud"
)

// Device operations for an owner. Everything the cloud package refuses to
// decide is decided here: whether a device belongs to the caller, and which
// devices are worth an extra request to label.

// ListDevices resolves the owner then lists their devices, each with its current
// status and, for multi-gang switches/outlets, the human's per-channel labels.
// Returns ErrAccountNotLinked (from the store) if the owner has no linked
// account.
func (c *Client) ListDevices(ctx context.Context, owner string) ([]cloud.Device, error) {
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
func (c *Client) DeviceStatus(ctx context.Context, owner, deviceID string) ([]cloud.DataPoint, error) {
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
func (c *Client) SendCommands(ctx context.Context, owner, deviceID string, cmds []cloud.DataPoint) error {
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
func (c *Client) assertOwned(ctx context.Context, tuyaUID, deviceID string) error {
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
func (c *Client) resolveChannelNames(ctx context.Context, devices []cloud.Device) error {
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
