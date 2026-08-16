// Package appaccount bridges an application's own user identity to Tuya, for
// integrations where one of your users owns one Tuya app account — the "connect
// your Tuya account" shape. An owner is whatever your application calls a user;
// the door resolves it to a Tuya UID.
//
// Service is the whole Tuya surface for that shape: Link and Unlink for the
// mapping itself, the owner-addressed calls that resolve the owner first and
// drop the root's User prefix — that prefix separates the two trees, and this
// package is one of them — and the device-addressed calls forwarded as they
// are. It invents nothing: no capability the root package lacks, no owner
// argument it does not use, and no check on ownership before acting. HasDevice
// answers that question; what the answer means is yours.
package appaccount

import (
	"context"
	"errors"
	"time"

	"go.naturallyfunny.dev/tuya"
)

type Account struct {
	Owner     string    `json:"owner"`
	TuyaUID   string    `json:"tuya_uid"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Store interface {
	Get(ctx context.Context, owner string) (Account, error)
	Link(ctx context.Context, owner, tuyaUID string) (Account, error)
	Unlink(ctx context.Context, owner string) error
}

type Client interface {
	UserDevices(ctx context.Context, tuyaUID string, opts ...tuya.DeviceOption) ([]tuya.UserDevice, error)
	UserHasDevice(ctx context.Context, tuyaUID, deviceID string) (bool, error)
	DeviceStatus(ctx context.Context, deviceID string) ([]tuya.DataPoint, error)
	SendCommands(ctx context.Context, deviceID string, commands []tuya.DataPoint) error
	DeviceChannelNames(ctx context.Context, deviceID string) ([]tuya.Channel, error)
	ChannelNames(ctx context.Context, devices []tuya.Device) (map[string][]tuya.Channel, error)
}

type Service struct {
	client Client
	store  Store
}

func NewService(client Client, store Store) *Service {
	return &Service{client: client, store: store}
}

func (s *Service) Get(ctx context.Context, owner string) (Account, error) {
	return s.store.Get(ctx, owner)
}

func (s *Service) Link(ctx context.Context, owner, tuyaUID string) (Account, error) {
	return s.store.Link(ctx, owner, tuyaUID)
}

func (s *Service) Unlink(ctx context.Context, owner string) error {
	return s.store.Unlink(ctx, owner)
}

var ErrNotLinked = errors.New("appaccount: no tuya account linked to owner")

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

func (s *Service) Devices(ctx context.Context, owner string, opts ...tuya.DeviceOption) ([]tuya.UserDevice, error) {
	uid, err := s.uid(ctx, owner)
	if err != nil {
		return nil, err
	}
	return s.client.UserDevices(ctx, uid, opts...)
}

func (s *Service) HasDevice(ctx context.Context, owner, deviceID string) (bool, error) {
	uid, err := s.uid(ctx, owner)
	if err != nil {
		return false, err
	}
	return s.client.UserHasDevice(ctx, uid, deviceID)
}

func (s *Service) DeviceStatus(ctx context.Context, deviceID string) ([]tuya.DataPoint, error) {
	return s.client.DeviceStatus(ctx, deviceID)
}

func (s *Service) SendCommands(ctx context.Context, deviceID string, commands []tuya.DataPoint) error {
	return s.client.SendCommands(ctx, deviceID, commands)
}

func (s *Service) DeviceChannelNames(ctx context.Context, deviceID string) ([]tuya.Channel, error) {
	return s.client.DeviceChannelNames(ctx, deviceID)
}

func (s *Service) ChannelNames(ctx context.Context, devices []tuya.Device) (map[string][]tuya.Channel, error) {
	return s.client.ChannelNames(ctx, devices)
}
