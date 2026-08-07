// Package tuya is the owner-scoped door of the library.
package tuya

import (
	"context"
	"errors"

	"go.naturallyfunny.dev/tuya/cloud"
)

var ErrDeviceNotOwned = errors.New("tuya: device does not belong to owner")

type IoT interface {
	ListDevices(ctx context.Context, tuyaUID string) ([]cloud.Device, error)
	DeviceStatus(ctx context.Context, deviceID string) ([]cloud.DataPoint, error)
	SendCommands(ctx context.Context, deviceID string, commands []cloud.DataPoint) error
	DeviceChannelNames(ctx context.Context, deviceID string) ([]cloud.Channel, error)
}
