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

type AppAccount struct {
	Owner     string    `json:"owner"`
	TuyaUID   string    `json:"tuya_uid"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Device struct {
	cloud.Device
	Channels []cloud.Channel `json:"channels"`
}

var ErrAccountNotLinked = errors.New("tuya: no tuya account linked to owner")

type AppAccountStore interface {
	Get(ctx context.Context, owner string) (AppAccount, error)
	Link(ctx context.Context, owner, tuyaUID string) (AppAccount, error)
	Unlink(ctx context.Context, owner string) error
}

type AppAccountClient struct {
	iot   IoT
	store AppAccountStore
}

func NewAppAccountClient(iot IoT, store AppAccountStore) *AppAccountClient {
	return &AppAccountClient{iot: iot, store: store}
}

func (c *AppAccountClient) Account(ctx context.Context, owner string) (AppAccount, error) {
	return c.store.Get(ctx, owner)
}

func (c *AppAccountClient) ListDevices(ctx context.Context, owner string) ([]Device, error) {
	acc, err := c.store.Get(ctx, owner)
	if err != nil {
		return nil, err
	}
	found, err := c.iot.UserDevices(ctx, acc.TuyaUID)
	if err != nil {
		return nil, err
	}
	devices := make([]Device, len(found))
	for idx, device := range found {
		devices[idx] = Device{Device: device, Channels: []cloud.Channel{}}
	}
	if err := c.resolveChannelNames(ctx, devices); err != nil {
		return nil, fmt.Errorf("resolve channel names: %w", err)
	}
	return devices, nil
}

func (c *AppAccountClient) HasDevice(ctx context.Context, owner, deviceID string) (bool, error) {
	acc, err := c.store.Get(ctx, owner)
	if err != nil {
		return false, err
	}
	devices, err := c.iot.UserDevices(ctx, acc.TuyaUID)
	if err != nil {
		return false, fmt.Errorf("list devices of owner %s: %w", owner, err)
	}
	for _, d := range devices {
		if d.ID == deviceID {
			return true, nil
		}
	}
	return false, nil
}

func isMultiGang(category string) bool {
	c := strings.ToLower(category)
	return c == "kg" || strings.HasPrefix(c, "cz")
}

func (c *AppAccountClient) resolveChannelNames(ctx context.Context, devices []Device) error {
	var targets []*Device
	for idx := range devices {
		device := &devices[idx]
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
				return
			}
			device.Channels = channels
		})
	}
	wg.Wait()
	return errors.Join(errs...)
}
