package tuya

import (
	"context"

	"go.naturallyfunny.dev/tuya/cloud"
)

type Owner string

type IoT interface {
	ListDevices(ctx context.Context, tuyaUID cloud.TuyaUID) ([]cloud.Device, error)
	DeviceChannelNames(ctx context.Context, deviceID cloud.DeviceID) ([]cloud.Channel, error)
}
