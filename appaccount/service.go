// Package appaccount bridges an application's own user identity to Tuya, for
// integrations where one of your users owns one Tuya app account — the "connect
// your Tuya account" shape. An owner is whatever your application calls a user;
// the door resolves it to a Tuya UID and answers what that account holds.
//
// Unlike the root package this one may offer what Tuya has no single endpoint
// for, as long as it is useful and its cost is written down.
package appaccount

import (
	"context"
	"errors"
	"fmt"
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
	UserDevices(ctx context.Context, tuyaUID string, opts ...tuya.DeviceOption) ([]tuya.UserDevice, error)
	UserHasDevice(ctx context.Context, tuyaUID, deviceID string) (bool, error)
	ChannelNames(ctx context.Context, devices []tuya.Device) (map[string][]tuya.Channel, error)
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

func (s *Service) uid(ctx context.Context, owner string) (string, error) {
	acc, err := s.store.Get(ctx, owner)
	if err != nil {
		return "", err
	}
	if acc.TuyaUID == "" {
		return "", ErrNotLinked
	}
	return acc.TuyaUID, nil
}

func (s *Service) ListDevices(ctx context.Context, owner string) ([]Device, error) {
	uid, err := s.uid(ctx, owner)
	if err != nil {
		return nil, err
	}
	found, err := s.client.UserDevices(ctx, uid)
	if err != nil {
		return nil, err
	}
	base := make([]tuya.Device, len(found))
	for idx, device := range found {
		base[idx] = device.Device
	}
	named, err := s.client.ChannelNames(ctx, base)
	if err != nil {
		return nil, fmt.Errorf("resolve channel names: %w", err)
	}
	devices := make([]Device, len(found))
	for idx, device := range found {
		channels := named[device.ID]
		if channels == nil {
			channels = []tuya.Channel{}
		}
		devices[idx] = Device{UserDevice: device, Channels: channels}
	}
	return devices, nil
}

func (s *Service) HasDevice(ctx context.Context, owner, deviceID string) (bool, error) {
	uid, err := s.uid(ctx, owner)
	if err != nil {
		return false, err
	}
	return s.client.UserHasDevice(ctx, uid, deviceID)
}
