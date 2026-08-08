package tuya

import (
	"context"

	"go.naturallyfunny.dev/tuya/cloud"
)

type IoT interface {
	UserDevices(ctx context.Context, tuyaUID string) ([]cloud.Device, error)
	DeviceChannelNames(ctx context.Context, deviceID string) ([]cloud.Channel, error)
}
