# tuya

[![Go Reference](https://pkg.go.dev/badge/go.naturallyfunny.dev/tuya.svg)](https://pkg.go.dev/go.naturallyfunny.dev/tuya)
![Go 1.25+](https://img.shields.io/badge/go-1.25%2B-00ADD8)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

A small, interface-first Go library for the [Tuya Cloud OpenAPI](https://developer.tuya.com/en/docs/cloud/).
It signs and authenticates requests, manages the app-level access token, and exposes typed
device operations — behind an ownership boundary you can hand to an untrusted caller.

It is built to be used as a **tool invoked by an AI agent**: low-traffic, one action per user
intent (list devices, read a device's state, send it a command). The design leans on that
usage — see [Design rationale](#design-rationale) — rather than on the assumptions of a
high-throughput service.

```go
devices, err := client.ListDevices(ctx, owner)                 // by your own owner ID
err = client.SendCommands(ctx, owner, id, []cloud.DataPoint{   // ownership asserted first
    {Code: "switch_1", Value: true},
})
```

## Install

```sh
go get go.naturallyfunny.dev/tuya
```

Requires Go 1.25+ (the library uses `sync.WaitGroup.Go`).

## Why this shape

Tuya's API is keyed by a **Tuya UID** and has no notion of *your* users. Two concerns fall
out of that, and the library keeps them in separate layers so neither leaks into the other:

1. **Talking to Tuya** — signing, the project-level token, typed device calls. Pure Tuya,
   keyed by UID, no idea who "owns" a device. This is the `cloud` subpackage.
2. **Owning a device** — mapping *your* opaque owner ID to a human's Tuya UID and refusing
   any call that crosses accounts. This is the root `tuya` package.

The payoff: the ownership guard lives at a single door (`tuya.Client`) that *cannot* be
opened without first resolving an owner. The trusted, guard-free layer (`cloud.IoT`) still
exists for code that legitimately holds a UID — but an agent never touches it.

## Concepts

Three composable tiers. Bind the one your caller needs:

| You have / need                                          | Use            | Guarded? |
| -------------------------------------------------------- | -------------- | -------- |
| Just the transport (token lifecycle, signing, raw `Do`)  | `cloud.Client` | no       |
| A Tuya UID, want typed device ops                        | `cloud.IoT`    | no       |
| Your own owner ID, want ownership enforced               | `tuya.Client`  | **yes**  |

- **`tuya.Client`** — the owner-scoped door. Resolves owner → Tuya UID through an
  `AccountStore`, asserts the target device belongs to that account (a lean `HasDevice`
  membership check), then delegates. It owns `Account`, `ErrAccountNotLinked`,
  `ErrDeviceNotOwned`, and the `AccountStore` / `IoT` interfaces it drives.
- **`cloud.Client`** — the transport. Speaks Tuya at the **project level**: one access
  ID/secret yields an access token it caches in memory and refreshes on its own (lazily on
  expiry, reactively when Tuya returns code `1010`). Handles HMAC-SHA256 signing. `Do` is a
  raw escape hatch for endpoints not yet wrapped.
- **`cloud.IoT`** — a trusted, device-addressed facade over a `cloud.Client`
  (`ListDevices`, `DeviceStatus`, `SendCommands`). No ownership guard; a holder can reach any
  device the project can. Guarding it is `tuya.Client`'s job.

Two ready-made `AccountStore` adapters ship in-tree — pick one, or implement the interface
yourself:

- **`postgres.Store`** — the owner → UID mapping in PostgreSQL (`pgx`), with an embedded
  migration runner.
- **`firestore.Store`** — the same contract on Cloud Firestore: one document per owner,
  no migrations.

Dependency direction is acyclic and points inward — adapters depend on the root, the root
depends on `cloud`, and `cloud` depends on nothing of ours:

```
postgres ─┐
          ├─▶ tuya ──▶ cloud
firestore ─┘
```

## Setup

```go
// 1. An owner → Tuya-UID store. PostgreSQL:
store, err := postgres.NewAccountStore(ctx, pool, postgres.WithAutoMigrate())
if err != nil {
    log.Fatal(err)
}
// ...or Cloud Firestore (nothing to migrate):
//
//   import (
//       gcfs "cloud.google.com/go/firestore"
//       "go.naturallyfunny.dev/tuya/firestore"
//   )
//   fs, _ := gcfs.NewClient(ctx, projectID)
//   store := firestore.NewAccountStore(fs)

// 2. The Tuya transport + device facade. baseURL selects the region.
transport, err := cloud.New(accessID, accessSecret, "https://openapi.tuyaus.com")
if err != nil {
    log.Fatal(err)
}
iot := cloud.NewIoT(transport)

// 3. The owner-scoped door.
client := tuya.New(iot, store) // postgres.Store satisfies tuya.AccountStore
```

`cloud.New` prefetches an access token, so a bad credential or unreachable region fails here,
at wiring time, not on the first device call. Pass `cloud.WithHTTPClient` to control timeouts
and transport.

### Regions

`baseURL` selects the Tuya data-center endpoint:

| Region | `baseURL`                     |
| ------ | ----------------------------- |
| US     | `https://openapi.tuyaus.com`  |
| EU     | `https://openapi.tuyaeu.com`  |
| China  | `https://openapi.tuyacn.com`  |
| India  | `https://openapi.tuyain.com`  |

## Usage

Drive devices by *your* owner ID. Every mutating call resolves the owner and asserts
ownership before anything reaches Tuya:

```go
devices, err := client.ListDevices(ctx, owner)      // typed devices + per-channel names
if errors.Is(err, tuya.ErrAccountNotLinked) {
    // route the human into the account-linking flow
}

status, err := client.DeviceStatus(ctx, owner, id)  // asserts ownership, then reads

err = client.SendCommands(ctx, owner, id, []cloud.DataPoint{
    {Code: "switch_1", Value: true},                // asserts ownership, then sends
})
if errors.Is(err, tuya.ErrDeviceNotOwned) {
    // device doesn't belong to this owner
}

acc, err := client.Account(ctx, owner)              // the linked owner ↔ UID mapping
```

Both sentinel errors are comparable with `errors.Is`, so a caller can branch on "not linked"
(send the human into onboarding) versus "not owned" (a real authorization failure).

Holding a trusted UID and don't need the guard? Use `cloud.IoT` directly:

```go
acc, _ := store.Get(ctx, owner)
devices, err := iot.ListDevices(ctx, acc.TuyaUID)   // UID-addressed
status, err := iot.DeviceStatus(ctx, deviceID)      // device-addressed, no guard
```

> `cloud.IoT` and `cloud.Client.Do` carry **no** ownership guard by design. Don't hand them to
> an untrusted caller (e.g. an agent) — route that traffic through `tuya.Client`.

## Linking accounts

Both stores own the full lifecycle of the owner → Tuya-UID mapping. `Link` is an upsert
(re-linking refreshes the UID; re-linking a previously unlinked owner revives the row rather
than colliding on the key); `Unlink` is a soft-delete (`deleted_at`), so `Get` stops
returning it while the record is preserved for audit.

```go
acc, err := store.Link(ctx, owner, tuyaUID)   // upsert
err = store.Unlink(ctx, owner)                // soft-delete
```

The PostgreSQL store backs this with a `tuya_app_accounts` table (`owner` PK, `tuya_uid`,
timestamps, `deleted_at`); Firestore with a `tuya_app_accounts` collection (override via
`firestore.WithCollection`), one document per owner keyed by the owner string.

### Migrations (PostgreSQL)

A small custom runner, not `golang-migrate`. SQL files are embedded (`//go:embed`); versions
are tracked in `tuya_schema_migrations`. `WithAutoMigrate()` applies pending migrations on
startup. Without it, `NewAccountStore` validates the schema exists and fails fast if the
consumer forgot to migrate.

## Design rationale

The decisions below are deliberate, and several run *against* a generic idiom on purpose.
Each one is a domain or usage constraint, not an oversight.

- **`Do(ctx, method, path, body []byte)` takes `[]byte`, not `io.Reader`.**
  Two Tuya constraints make a byte slice the honest type: signing requires
  `SHA256(body)` computed *before* the request is sent (the body must be fully materialized),
  and the retry-on-`1010` path must **replay** the same body on the second attempt (a
  single-use `io.Reader` can't, without buffering to `[]byte` anyway). An `io.Reader` here
  would only move an `io.ReadAll` inside `Do` and advertise streaming that never happens.
  Callers already hold `[]byte` from `json.Marshal`.

- **The ownership guard lives at the root (`tuya.Client`), not in `cloud.IoT`.**
  `cloud.IoT` is a trusted, device-addressed layer with no tenant check — by design. Pushing
  the guard *up* to a single owner-scoped door makes it un-bypassable: you cannot reach a
  device without first resolving an owner. A guard buried in the device layer would have to
  thread a UID through every call and could still be sidestepped by a sibling method.

- **`cloud` exports concrete types; the *consumer* declares the interface.**
  `cloud.NewIoT` returns a concrete `*cloud.IoT`. The root package declares `tuya.IoT` as
  narrowly as `tuya.Client` needs, and `*cloud.IoT` satisfies it structurally. This is
  "accept interfaces, return structs" applied literally — mocking is the consumer's concern,
  so `client_test.go` fakes `tuya.IoT` and `tuya.AccountStore` with zero test-only code in
  `cloud`. Exporting a speculative interface from `cloud` would only add a
  compatibility burden that widens with every new domain.

- **`enrichDevices` uses `sync.WaitGroup.Go` + `errors.Join`, not `errgroup`.**
  The semantics are **collect-all**: an agent wants to know *every* channel that failed to
  enrich in one call. `errgroup.WithContext` is **fail-fast** — the first error cancels its
  siblings, which is exactly the information we don't want to lose. The "free context
  propagation" people reach for is precisely first-error cancellation. `errgroup` would be
  right if fail-fast were the goal; here it's a behavior change, not a cleanup.

- **The app-level token is cached in memory, with no store.**
  The Tuya credential is project-wide, not per-user, so there is nothing per-user to persist.
  `cloud.Client` refreshes lazily on expiry and reactively on code `1010`. A token store
  would add a dependency and a failure mode for state that is trivially re-fetched.

- **Ownership is checked by list-then-contains.**
  Each guarded call lists the account's devices (raw, unenriched, via `HasDevice`) and checks
  membership. Simple and correct for sporadic, one-intent-at-a-time traffic. Caching is
  deferred until there's a real throughput need to justify the invalidation complexity.

- **`cloud.New` prefetches a token with `context.Background()`.**
  Construction-time prefetch turns a bad credential or unreachable region into a wiring-time
  error. A caller that needs to bound it configures timeouts on the `*http.Client` via
  `WithHTTPClient` — the right lever for transport-level control.

## Testing

`tuya.Client` is unit-tested against fake `IoT` and `AccountStore` implementations — happy
paths plus `ErrAccountNotLinked` / `ErrDeviceNotOwned` and the short-circuit guards. The
Firestore owner-ID validation is table-tested.

```sh
go test ./...
```

## Layout

```
client.go        tuya.Client — owner-scoped door + ownership guard, Account, sentinel
                 errors, AccountStore / IoT interfaces (consumer-side).
cloud/
  client.go      cloud.Client transport (token cache/refresh, signing, Do) + IoT facade.
  auth.go        request signing + token lifecycle.
  device.go      typed Device/DataPoint/Channel + device operations, enrichment.
postgres/
  store.go       AccountStore on PostgreSQL + embedded migration runner.
  migrations/    embedded .up.sql / .down.sql.
firestore/
  store.go       AccountStore on Cloud Firestore (schemaless, no migrations).
```

## License

[MIT](LICENSE) © 2026 Ardian
