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
	if err := c.resolveChannelNames(ctx, devices); err != nil {
		return nil, fmt.Errorf("resolve channel names: %w", err)
	}
	return devices, nil
}

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

func isMultiGang(category string) bool {
	c := strings.ToLower(category)
	return c == "kg" || strings.HasPrefix(c, "cz")
}

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
				return
			}
			device.CodeNameMapping = channels
		})
	}
	wg.Wait()
	return errors.Join(errs...)
}
