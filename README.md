# tuya

Go module `go.naturallyfunny.dev/tuya` — a reusable library for the Tuya Cloud OpenAPI.
Interface-based, not tied to a single database or application. Built to be used as a tool
invoked by an AI agent: low traffic, one action per user intent (list devices, read a
device's state, send it a command).

## Installation

```sh
go get go.naturallyfunny.dev/tuya
```

## Concepts

The root `tuya` package is the **owner-scoped door** — the surface an AI agent goes through.
It is keyed by your own opaque **owner ID** and owns the ownership guard:

- **`tuya.Client`** — resolves owner → Tuya UID via an `AccountStore`, asserts the target
  device belongs to that account (a lean `HasDevice` membership check), then delegates the
  actual call. It owns `Account`, `ErrAccountNotLinked`, `ErrDeviceNotOwned`, the
  `AccountStore` interface, and the `IoT` interface it drives. It is the single door an
  untrusted agent goes through — it cannot be opened without resolving an owner first.

The pure-Tuya layer lives in the **`tuya/cloud`** subpackage (keyed by Tuya UID, no owner
concept). The root composes over it:

- **`cloud.Client`** — the transport. Speaks Tuya at the **app (project) level**: a single
  access ID/secret yields an access token that `Client` caches in memory and refreshes on its
  own (lazily on expiry, reactively on Tuya code 1010). No per-user token store. Handles HMAC
  signing and exposes `Do`, a raw escape hatch for endpoints not yet wrapped.
- **`cloud.IoTClient`** — a trusted, device-addressed facade over a `cloud.Client`.
  `ListDevices` is UID-addressed (the uid is the Tuya resource path); `DeviceStatus` and
  `SendCommands` are pure device-addressed calls — no ownership guard. A caller holding it can
  act on any device the project can reach; ownership is `tuya.Client`'s job. It satisfies the
  consumer-side `tuya.IoT` interface.

The PostgreSQL adapter for the owner → UID mapping:

- **`postgres.Store`** — implements `tuya.AccountStore` (implicitly), backing the owner → UID
  mapping with PostgreSQL. A consumer links an account once, then drives devices by owner ID
  via `tuya.Client`.

Dependency direction is acyclic: **`postgres → tuya → cloud`**.

Three consumer tiers fall out of this layout — bind the one you need:

| You need | Use |
|---|---|
| transport only (token lifecycle, signing, `Do`) | `cloud.Client` |
| the IoT device wrapper (device-addressed, by UID) | `cloud.IoTClient` |
| owner ↔ account management + ownership guard | `tuya.Client` |

## Setup

```go
// WithAutoMigrate is optional — runs pending migrations on startup.
store, err := postgres.NewAccountStore(ctx, pool, postgres.WithAutoMigrate())
if err != nil {
    log.Fatal(err)
}

// baseURL selects the regional endpoint; WithHTTPClient is optional.
transport, err := cloud.New(accessID, accessSecret, "https://openapi.tuyaus.com")
if err != nil {
    log.Fatal(err)
}
iot := cloud.NewIoTClient(transport)
client := tuya.New(iot, store) // postgres.Store satisfies tuya.AccountStore
```

`baseURL` selects the data-center region: `openapi.tuyaus.com` (US), `.tuyaeu.com` (EU),
`.tuyacn.com` (China), `.tuyain.com` (India). `cloud.New` prefetches an access token, so a
bad credential or unreachable region fails here at wiring time, not on the first call.

## Usage

Drive devices by owner ID with `tuya.Client` — it resolves owner → UID and enforces ownership:

```go
devices, err := client.ListDevices(ctx, ownerID)      // typed devices + per-channel names
if errors.Is(err, tuya.ErrAccountNotLinked) {
    // route the human into the account-linking flow
}
status, err := client.DeviceStatus(ctx, ownerID, id)  // asserts ownership, then reads status
err = client.SendCommands(ctx, ownerID, id, []cloud.DataPoint{
    {Code: "switch_1", Value: true},                  // asserts ownership, then sends
})
if errors.Is(err, tuya.ErrDeviceNotOwned) {
    // device doesn't belong to this owner
}
```

`tuya.Client.Account` returns the linked account (useful for surfaces that need to surface the
owner-ID / Tuya-UID mapping):

```go
acc, err := client.Account(ctx, ownerID)
```

Need raw cloud access? `cloud.IoTClient` is trusted and device-addressed — no ownership guard:

```go
acc, err := store.Get(ctx, ownerID)
devices, err := iot.ListDevices(ctx, acc.TuyaUID)   // uid-addressed
status, err := iot.DeviceStatus(ctx, deviceID)       // device-addressed, no guard
```

`cloud.Client.Do` is a raw escape hatch for endpoints not yet wrapped — it also carries no
guard. Don't expose `cloud.IoTClient` or `cloud.Client.Do` to an untrusted caller (e.g. an
agent); route everything through `tuya.Client` instead.

## Linking accounts

`postgres.Store` owns the full lifecycle of the owner → Tuya-UID mapping: `Get` reads it,
`Link` creates or refreshes it (upsert; reviving a soft-deleted row), and `Unlink`
soft-deletes it. The table is `tuya_app_accounts` (`owner_id` PK, `tuya_uid`, timestamps,
soft-delete `deleted_at`).

```go
acc, err := store.Link(ctx, ownerID, tuyaUID)   // upsert the mapping
err = store.Unlink(ctx, ownerID)                // soft-delete it
```
