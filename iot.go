package tuya

import (
	"context"

	"go.naturallyfunny.dev/tuya/cloud"
)

type IoT interface {
	UserDevices(ctx context.Context, tuyaUID string) ([]cloud.UserDevice, error)
	DeviceChannelNames(ctx context.Context, deviceID string) ([]cloud.Channel, error)
}
