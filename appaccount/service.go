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

type Account struct {
	Owner     string    `json:"owner"`
	TuyaUID   string    `json:"tuya_uid"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Device struct {
	tuya.UserDevice
	Channels []tuya.Channel `json:"channels"`
}

var ErrNotLinked = errors.New("tuya: no tuya account linked to owner")

type Store interface {
	Get(ctx context.Context, owner string) (Account, error)
	Link(ctx context.Context, owner, tuyaUID string) (Account, error)
	Unlink(ctx context.Context, owner string) error
}

type Client interface {
	UserDevices(ctx context.Context, tuyaUID string) ([]tuya.UserDevice, error)
	DeviceChannelNames(ctx context.Context, deviceID string) ([]tuya.Channel, error)
}

type Service struct {
	client Client
	store  Store
}

func NewService(client Client, store Store) *Service {
	return &Service{client: client, store: store}
}

func (s *Service) Account(ctx context.Context, owner string) (Account, error) {
	return s.store.Get(ctx, owner)
}

func (s *Service) ListDevices(ctx context.Context, owner string) ([]Device, error) {
	tuyaUID, err := s.tuyaUID(ctx, owner)
	if err != nil {
		return nil, err
	}
	found, err := s.client.UserDevices(ctx, tuyaUID)
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
	tuyaUID, err := s.tuyaUID(ctx, owner)
	if err != nil {
		return false, err
	}
	devices, err := s.client.UserDevices(ctx, tuyaUID)
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

func IsMultiGang(category string) bool {
	c := strings.ToLower(category)
	return c == "kg" || strings.HasPrefix(c, "cz")
}

func (s *Service) tuyaUID(ctx context.Context, owner string) (string, error) {
	acc, err := s.store.Get(ctx, owner)
	if err != nil {
		return "", err
	}
	if acc.TuyaUID == "" {
		return "", ErrNotLinked
	}
	return acc.TuyaUID, nil
}

func (s *Service) resolveChannelNames(ctx context.Context, devices []Device) error {
	var targets []*Device
	for idx := range devices {
		device := &devices[idx]
		if IsMultiGang(device.Category) && device.ID != "" {
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
			channels, err := s.client.DeviceChannelNames(ctx, device.ID)
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
