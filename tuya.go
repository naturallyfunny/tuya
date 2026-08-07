// Package tuya is the owner-scoped door of the library: callers drive Tuya
// devices by their own opaque owner key instead of a Tuya identifier. It is the
// surface an AI agent goes through — low traffic, one action per user intent
// (list devices, read a device's state, send it a command).
//
// The owner concept is foreign to Tuya. The cloud subpackage speaks the Tuya
// Cloud OpenAPI and is keyed the way Tuya keys things; an owner is whatever
// opaque string the consumer identifies a human by (a user ID, an email — the
// library does not care). This package quarantines that foreign concept,
// including the ownership guard: cloud.IoT is a trusted, device-addressed layer
// with no tenant check, and a door in this package is the single entrance an
// untrusted agent goes through — one that cannot be opened without resolving an
// owner first.
//
// There is a door per Tuya tenancy model, and its type name says which model it
// speaks. AppAccountClient is the app-account model: every human holds their own
// Tuya app account, the tenant boundary is a Tuya UID, and an AppAccountStore
// maps owner -> UID (ready-made ones live in the postgres and firestore
// subpackages). Tuya's second model is spatial — the boundary is a root space,
// devices live in the subtree beneath it, and there is no per-tenant UID at all;
// its door will land here as SpaceClient, with a SpaceStore beside it.
//
// The names are long on purpose. With two models in one package "Client" would no
// longer tell a reader which one they hold, and "account" alone is ambiguous here:
// a Tuya app account (holds devices, keyed by UID) is not a Tuya project account
// (holds the accessID/accessSecret). Call sites stay short, because the variable
// name belongs to the caller: app := tuya.NewAppAccountClient(...) reads as
// app.Account(ctx, owner).
//
// Dependency direction is acyclic: postgres -> tuya -> cloud.
package tuya

import (
	"context"
	"errors"

	"go.naturallyfunny.dev/tuya/cloud"
)

// ErrDeviceNotOwned indicates the targeted device does not belong to the owner's
// tenant. Returned by device commands before anything is sent, so an agent can
// never drive a device that isn't the human's.
var ErrDeviceNotOwned = errors.New("tuya: device does not belong to owner")

// IoT is the device-addressed Tuya facade this package drives, narrowed to the
// methods its doors need. *cloud.IoT satisfies it. It is defined here, on the
// consumer side, so a door can be unit-tested against a fake and so the cloud
// package stays free of speculative interfaces.
//
// Every method here is one plain Tuya endpoint. Nothing this package needs is
// asked of the layer below: the ownership guard and the channel-name resolution
// are both composed here, from these primitives (see app_account.go), so the
// cloud package never has to know that owners or multi-gang categories exist.
type IoT interface {
	ListDevices(ctx context.Context, tuyaUID string) ([]cloud.Device, error)
	DeviceStatus(ctx context.Context, deviceID string) ([]cloud.DataPoint, error)
	SendCommands(ctx context.Context, deviceID string, commands []cloud.DataPoint) error
	DeviceChannelNames(ctx context.Context, deviceID string) ([]cloud.Channel, error)
}
