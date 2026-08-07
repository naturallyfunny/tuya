// Package tuya maps your own identity system onto Tuya's.
//
// Tuya identifies things by its own handles — a Tuya UID, a space ID, a device
// ID — and knows nothing about the users of the system integrating with it.
// This package holds the mapping from your opaque owner ID to those handles,
// and answers the questions that mapping makes answerable: which Tuya account
// or space an owner is linked to, and whether a given handle sits under it.
//
// It answers; it does not decide. A door here never refuses a device because it
// looks unowned, because "unowned" is not always wrong — a consumer building
// device sharing legitimately reaches across accounts, and a library that
// hard-refused those calls would block the correct consumer to protect the
// careless one. Ownership is exposed as a question you ask (AppAccountClient.HasDevice,
// SpaceClient.ContainsSpace, SpaceClient.ContainsDevice) and act on however your
// product requires.
//
// The cloud subpackage is the layer below: Tuya's API as-is, addressed by Tuya's
// own handles, with no notion of an owner.
package tuya

import (
	"context"

	"go.naturallyfunny.dev/tuya/cloud"
)

// IoT is the slice of *cloud.IoT the app-account door drives, declared here on
// the consumer side so the door can be faked in tests and cloud carries no
// speculative interface.
type IoT interface {
	ListDevices(ctx context.Context, tuyaUID string) ([]cloud.Device, error)
	DeviceChannelNames(ctx context.Context, deviceID string) ([]cloud.Channel, error)
}
