package tuya

import (
	"context"

	"go.naturallyfunny.dev/tuya/cloud"
)

type IoT interface {
	ListDevices(ctx context.Context, tuyaUID string) ([]cloud.Device, error)
	DeviceChannelNames(ctx context.Context, deviceID string) ([]cloud.Channel, error)
}
