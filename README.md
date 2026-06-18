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

The library speaks Tuya at the **app (project) level**: a single access ID/secret yields an
access token that `Client` caches and refreshes on its own — there is no per-user token store.

What *is* per-user is the mapping from an opaque **owner ID** (whatever you use to identify a
human) to that human's **Tuya account UID**. That mapping lives behind the `Repository`
interface; a ready-made PostgreSQL implementation is in the `postgres` package.

`Client` takes two things:

- **`Repository`** — resolves owner ID → Tuya UID. Consumer-defined; `postgres.Store` provided.
- **access ID / secret / baseURL** — Tuya Cloud project credentials and the regional endpoint.

## Setup

```go
store := postgres.New(pool, dsn)
if err := store.Migrate(); err != nil {
    log.Fatal(err)
}

client, err := tuya.New(store, accessID, accessSecret, "https://openapi.tuyaus.com")
if err != nil {
    log.Fatal(err)
}
```

`baseURL` selects the data-center region: `openapi.tuyaus.com` (US), `.tuyaeu.com` (EU),
`.tuyacn.com` (China), `.tuyain.com` (India).

## Usage

```go
devices, err := client.ListDevices(ctx, ownerID)       // typed devices + per-channel names
status, err := client.DeviceStatus(ctx, ownerID, id)   // current DPs of one device
err = client.SendCommands(ctx, ownerID, id, []tuya.DataPoint{
    {Code: "switch_1", Value: true},
})
acc, err := client.Account(ctx, ownerID)               // the linked Tuya account
```

`SendCommands` and `DeviceStatus` verify the device belongs to the owner first, returning
`ErrDeviceNotOwned` otherwise. An owner who hasn't linked an account gets `ErrAccountNotLinked`.

## Linking accounts

Writing the owner → Tuya-UID rows (linking/unlinking) is the consumer's job; the `postgres`
store only reads the mapping the library needs. The table is `tuya_app_accounts`
(`owner_id` PK, `tuya_uid`, timestamps, soft-delete `deleted_at`).
