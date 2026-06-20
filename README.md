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

The root `tuya` package is **pure Tuya** (keyed by Tuya UID) and composes from two
orthogonal pieces:

- **`Client`** — the transport. Speaks Tuya at the **app (project) level**: a single access
  ID/secret yields an access token that `Client` caches in memory and refreshes on its own
  (lazily on expiry, reactively on Tuya code 1010). No per-user token store. Handles HMAC
  signing and exposes `Do`, a raw escape hatch for endpoints not yet wrapped.
- **`IoTClient`** — a trusted, device-addressed facade over a `Client`. `ListDevices` is
  UID-addressed (the uid is the Tuya resource path). `DeviceStatus` and `SendCommands` are
  pure device-addressed calls — no ownership guard. A caller holding `IoTClient` can act on
  any device the project can reach; ownership is `app.Client`'s job.

The **owner ID** (whatever you use to identify a human) is not a Tuya concept, so it lives in
subpackages — additive and optional, leaving the root pure Tuya:

- **`app`** — the owner-scoped client. `app.Client` is the **ownership boundary**: it resolves
  owner → UID, asserts the device belongs to that account (via a lean `IoTClient.HasDevice`
  check), then delegates to the device-addressed `IoTClient`. `app` owns `Account`,
  `ErrAccountNotLinked`, `ErrDeviceNotOwned`, and the `AccountStore` interface it consumes.
  It is the single door an untrusted agent goes through.
- **`postgres.Store`** — an adapter that implements `app.AccountStore` (implicitly), backing
  the owner → UID mapping with PostgreSQL. A consumer links an account once, then drives
  devices by owner ID via `app.Client`.

Dependency direction is acyclic: **`postgres → app → tuya`**. The root never imports downward.

## Setup

```go
// WithAutoMigrate is optional — runs pending migrations on startup.
store, err := postgres.NewAccountStore(ctx, pool, postgres.WithAutoMigrate())
if err != nil {
    log.Fatal(err)
}

// baseURL selects the regional endpoint; WithHTTPClient is optional.
client, err := tuya.New(accessID, accessSecret, "https://openapi.tuyaus.com")
if err != nil {
    log.Fatal(err)
}
iot := tuya.NewIoTClient(client)
```

`baseURL` selects the data-center region: `openapi.tuyaus.com` (US), `.tuyaeu.com` (EU),
`.tuyacn.com` (China), `.tuyain.com` (India). `tuya.New` prefetches an access token, so a
bad credential or unreachable region fails here at wiring time, not on the first call.

## Usage

Drive devices by owner ID with `app.Client` — it resolves owner → UID and enforces ownership:

```go
appClient := app.New(iot, store) // postgres.Store satisfies app.AccountStore

devices, err := appClient.ListDevices(ctx, ownerID)      // typed devices + per-channel names
if errors.Is(err, app.ErrAccountNotLinked) {
    // route the human into the account-linking flow
}
status, err := appClient.DeviceStatus(ctx, ownerID, id)  // asserts ownership, then reads status
err = appClient.SendCommands(ctx, ownerID, id, []tuya.DataPoint{
    {Code: "switch_1", Value: true},                     // asserts ownership, then sends
})
if errors.Is(err, app.ErrDeviceNotOwned) {
    // device doesn't belong to this owner
}
```

`app.Client.Account` returns the linked account (useful for surfaces that need to surface the
owner-ID / Tuya-UID mapping):

```go
acc, err := appClient.Account(ctx, ownerID)
```

Need raw `IoTClient` access? It is trusted and device-addressed — no ownership guard:

```go
acc, err := store.Get(ctx, ownerID)
devices, err := iot.ListDevices(ctx, acc.TuyaUID)   // uid-addressed
status, err := iot.DeviceStatus(ctx, deviceID)       // device-addressed, no guard
```

`Client.Do` is a raw escape hatch for endpoints not yet wrapped — it also carries no guard.
Don't expose `IoTClient` or `Client.Do` to an untrusted caller (e.g. an agent); route
everything through `app.Client` instead.

## Linking accounts

`postgres.Store` owns the full lifecycle of the owner → Tuya-UID mapping: `Get` reads it,
`Link` creates or refreshes it (upsert; reviving a soft-deleted row), and `Unlink`
soft-deletes it. The table is `tuya_app_accounts` (`owner_id` PK, `tuya_uid`, timestamps,
soft-delete `deleted_at`).

```go
acc, err := store.Link(ctx, ownerID, tuyaUID)   // upsert the mapping
err = store.Unlink(ctx, ownerID)                // soft-delete it
```
