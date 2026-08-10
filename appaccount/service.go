package appaccount

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.naturallyfunny.dev/tuya"
)

type AppAccount struct {
	Owner     string    `json:"owner"`
	TuyaUID   string    `json:"tuya_uid"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Device struct {
	tuya.UserDevice
	Channels []tuya.Channel `json:"channels"`
}

var ErrAccountNotLinked = errors.New("tuya: no tuya account linked to owner")

type AppAccountStore interface {
	Get(ctx context.Context, owner string) (AppAccount, error)
	Link(ctx context.Context, owner, tuyaUID string) (AppAccount, error)
	Unlink(ctx context.Context, owner string) error
}

type Client interface {
	UserDevices(ctx context.Context, tuyaUID string) ([]tuya.UserDevice, error)
	DeviceChannelNames(ctx context.Context, deviceID string) ([]tuya.Channel, error)
}

type Service struct {
	iot   Client
	store AppAccountStore
}

func NewService(iot Client, store AppAccountStore) *Service {
	return &Service{iot: iot, store: store}
}

func (s *Service) Account(ctx context.Context, owner string) (AppAccount, error) {
	return s.store.Get(ctx, owner)
}

func (s *Service) ListDevices(ctx context.Context, owner string) ([]Device, error) {
	acc, err := s.store.Get(ctx, owner)
	if err != nil {
		return nil, err
	}
	found, err := s.iot.UserDevices(ctx, acc.TuyaUID)
	if err != nil {
		return nil, err
	}
	devices := make([]Device, len(found))
	for idx, device := range found {
		devices[idx] = Device{UserDevice: device, Channels: []tuya.Channel{}}
	}
	if err := s.resolveChannelNames(ctx, devices); err != nil {
		return nil, fmt.Errorf("resolve channel names: %w", err)
	}
	return devices, nil
}

func (s *Service) HasDevice(ctx context.Context, owner, deviceID string) (bool, error) {
	acc, err := s.store.Get(ctx, owner)
	if err != nil {
		return false, err
	}
	devices, err := s.iot.UserDevices(ctx, acc.TuyaUID)
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

func (s *Service) resolveChannelNames(ctx context.Context, devices []Device) error {
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
			channels, err := s.iot.DeviceChannelNames(ctx, device.ID)
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
