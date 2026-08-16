# tuya

[![Go Reference](https://pkg.go.dev/badge/go.naturallyfunny.dev/tuya.svg)](https://pkg.go.dev/go.naturallyfunny.dev/tuya)
![Go 1.25+](https://img.shields.io/badge/go-1.25%2B-00ADD8)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

A Go library for the [Tuya Cloud OpenAPI](https://developer.tuya.com/en/docs/cloud/).

Tuya's API is keyed by a **Tuya UID** and knows nothing about *your* users. So the module has two
halves: one that talks to Tuya, and one that maps your own user IDs onto Tuya's.

```sh
go get go.naturallyfunny.dev/tuya
```

## The packages

| Package               | What it is                                              | Depends on |
| --------------------- | ------------------------------------------------------- | ---------- |
| `tuya`                | The Tuya client. Signing, tokens, devices, spaces.       | stdlib     |
| `tuya/appaccount`     | Your owner ID → Tuya UID, for the app-account shape.     | `tuya`     |
| `tuya/postgres`       | The owner → UID mapping stored in PostgreSQL (`pgx`).    | `appaccount` |
| `tuya/firestore`      | The same mapping on Cloud Firestore.                     | `appaccount` |

```
postgres ──┬─▶ appaccount ──▶ tuya
firestore ─┘
```

Only import what you need. `tuya` and `appaccount` are stdlib-only; `pgx` and the Firestore client
enter your module graph only if you import those store packages.

### `tuya` — the client

One access ID/secret for your whole cloud project. It fetches and caches an access token, refreshes
it when Tuya rejects one, and signs every request. Then it wraps endpoints:

- **Devices** — `UserDevices`, `SpaceDevices`, `DeviceStatus`, `SendCommands`,
  `DeviceChannelNames`, `ChannelNames`, `UserHasDevice`, `SpaceHasDevice`.
- **Spaces** — `CreateSpace`, `Space`, `ModifySpace`, `DeleteSpace`, `SpaceResources`,
  `ListSpaces`, `SpaceRelation`.
- **Anything else** — `Do`, a raw signed request for endpoints not wrapped yet.

It has no notion of ownership: whoever holds a `*tuya.Client` can reach anything in the project.

### `tuya/appaccount` — the owner mapping

For the integration where each of your users signs in to a Tuya app (Tuya Smart, Smart Life, your
OEM app) and connects it to your product. That link is a **Tuya UID**, and a `Store` remembers which
owner it belongs to.

`Service` is the whole surface for that shape: `Link` / `Unlink` / `Get` for the mapping, `Devices`
and `HasDevice` taking *your* owner ID where the client takes a UID, and the device-addressed calls
(`SendCommands`, `DeviceStatus`, `DeviceChannelNames`, `ChannelNames`) forwarded unchanged — a
device ID is already a Tuya handle, so there is nothing to resolve.

It **answers** ownership questions; it does not enforce them. `HasDevice` gives you a `bool`, and
what that means is your call — a device someone shared with your user is legitimately absent from
their listing, so a library that refused those calls would break the correct integration.

Spaces are not reachable through this door. An app account's own places live in Tuya's older *asset*
family, and its IDs are rejected by every `/v2.0/cloud/space` endpoint. Nothing relates the two
trees, so the bridge would have to be invented.

### `tuya/postgres` and `tuya/firestore` — stores

Ready-made `appaccount.Store` implementations, or write your own — it's three methods.

`postgres.AppAccountStore` uses a `tuya_app_accounts` table and ships embedded migrations;
`WithAutoMigrate()` runs them at startup, otherwise it checks the schema and fails fast.
`firestore.AppAccountStore` uses one document per owner in `tuya_app_accounts` (override with
`WithCollection`), nothing to migrate.

Both treat `Link` as an upsert and `Unlink` as a soft delete, so relinking revives the record and an
unlinked owner stays on file for audit.

## Wiring

```go
store, err := postgres.NewAppAccountStore(ctx, pool, postgres.WithAutoMigrate())

c, err := tuya.New(accessID, accessSecret, "https://openapi.tuyaus.com")

app := appaccount.NewService(c, store)
```

`tuya.New` fetches a token straight away, so bad credentials or the wrong region fail here rather
than on your first device call. `tuya.WithHTTPClient` sets timeouts and transport.

### Regions

`baseURL` picks the data center:

| Region           | `baseURL`                          |
| ---------------- | ---------------------------------- |
| Western America  | `https://openapi.tuyaus.com`       |
| Eastern America  | `https://openapi-ueaz.tuyaus.com`  |
| Central Europe   | `https://openapi.tuyaeu.com`       |
| Western Europe   | `https://openapi-weaz.tuyaeu.com`  |
| China            | `https://openapi.tuyacn.com`       |
| India            | `https://openapi.tuyain.com`       |
| Singapore        | `https://openapi-sg.iotbing.com`   |

Singapore is on `iotbing.com`, not `tuya*.com`. The wrong data center still hands you a token and
then refuses every call with `28841107` — if that's what you see, check the URL before the
credential.

## Using it

Through the door, by your own owner ID:

```go
devices, err := app.Devices(ctx, owner)
if errors.Is(err, appaccount.ErrNotLinked) {
    // send the human into your account-linking flow
}

acc, err := app.Get(ctx, owner)          // the owner ↔ UID mapping
ok, err := app.HasDevice(ctx, owner, id) // a fact, not a verdict
```

Then decide what the fact means, and act:

```go
switch {
case err != nil:
    return err                 // the lookup failed — not the same as "no"
case ok, myShareRules.Allow(owner, id):
    err = app.SendCommands(ctx, id, []tuya.DataPoint{{Code: "switch_1", Value: true}})
default:
    return ErrForbidden        // your rule, your error
}
```

Straight on the client, by Tuya handles:

```go
devices, err := c.UserDevices(ctx, tuyaUID)
status, err := c.DeviceStatus(ctx, deviceID)
devices, err := c.SpaceDevices(ctx, []int64{spaceID}, 20, true, nil, nil, "")

ok, err := c.UserHasDevice(ctx, tuyaUID, deviceID)  // one request, flat
ok, err = c.SpaceHasDevice(ctx, spaceID, deviceID)  // a paged scan — see below
```

### Channel names

A multi-gang switch labels its channels on a separate endpoint. Ask for them per call, so the extra
requests are visible where you pay for them:

```go
devices, err := c.UserDevices(ctx, tuyaUID, tuya.WithChannelNames()) // 1 + N requests
names, err := c.ChannelNames(ctx, devices)                          // same fan-out, devices you hold
labels, err := c.DeviceChannelNames(ctx, deviceID)                  // just one device
```

The fan-out only asks about the thirteen categories that can carry numbered channels. Three of them
are documented as answering; the other ten are in on observed evidence, so they may come back empty.
Category codes are exported as `tuya.DeviceCategorySwitch` and friends (134 of them) for that filter
and for your own branches on `device.Category`.

### Spaces

Space calls take a space ID and have no notion of an owner.

```go
children, next, err := c.ListSpaces(ctx, spaceID, true, 0, 0) // onlySub, lastRowKey, pageSize
room, err := c.CreateSpace(ctx, "Room 201", spaceID, "twin")
things, next, err := c.SpaceResources(ctx, room, false, 0, 0)
contains, err := c.SpaceRelation(ctx, spaceID, room)
```

Worth knowing:

- **Paging is yours.** Every listing hands back one page plus the cursor for the next; a zero cursor
  means the walk is over. Tuya never documents how to know there isn't a next page, so the library
  won't loop on a guess.
- **`onlySub` is a required argument.** `true` is direct children, `false` the whole subtree. Tuya
  documents no default, and a listing that quietly covered the wrong depth is not something to build
  an ownership check on.
- **`ListSpaces` with a space ID of `0`** lists the whole project's top-level spaces.
- **`DeleteSpace` deletes the subtree.** Tuya offers no shallow form.
- **`SpaceRelation` is transitive** — a space answers `true` for its grandchild — but `false` for
  itself.
- **A space id your project can't reach is an error, not an empty answer.** Deleted, wrong project,
  or never existed — Tuya refuses all three the same way, with code `40001900` (`No space
  permission`) in a `*tuya.APIError`. The library passes that through instead of inventing a
  "not found" of its own.
- **`SpaceHasDevice` is the expensive one.** Tuya has no "which space holds this device" lookup, so
  it enumerates the subtree's resources, up to 50 pages of 200. If your own database already records
  where a device sits, ask that instead.

Nothing is cached. Answers go stale when a device is added, removed or relinked, and Tuya never
tells us about any of those — a layer that *can* learn about them is in a better place to cache than
this library.

### Two device shapes

Tuya's device APIs come in two families that disagree about their own field names, so each endpoint
gets its own type: `UserDevices` returns `[]UserDevice`, `SpaceDevices` returns `[]SpaceDevice`.

The trap worth naming: in the *user* family `name` is the user's rename, and in the *space* family
`name` is the factory name with the rename in `customName`. Both decode without error, so a
merged type would silently start showing model numbers to end users. Both embed `tuya.Device`
(`ID`, `Category`, `Channels`) — the only fields that mean the same thing on both wires, and what
lets one `ChannelNames` fan-out serve either.

The types keep identifiers, flags and status, and drop labels and presentation (`icon`, `model`,
`lat`/`lon`, timestamps…). `local_key` is dropped because it is the device's LAN key — a secret, and
these structs carry JSON tags. Need the rest? `Do` decodes into your own struct.

## What this library doesn't do

- **Run the Tuya account-authorization flow.** Getting the UID — the human granting your project
  access — happens upstream. This library starts once you can call `Link(owner, tuyaUID)`.
- **Wrap all of Tuya.** Devices and spaces are typed; `Do` is the escape hatch for the rest.
- **Enforce ownership, or cache the answer.** Both explained above.

## Testing

```sh
go test ./...
```

`appaccount` is unit-tested against fake `Client` and `Store` implementations (100% of statements);
`tuya` against an `httptest` server over real HTTP (76.4%), pinning the token retry, the space wire
contract and the request cost of the device methods. `postgres` replays scripted rows through a fake
`Querier` (81.3%); `firestore` only unit-tests its pure part (44.7%). Live-infra integration tests
are on the roadmap.

## Compatibility

Go 1.25+, per `go.mod`. The API is stable and in use.

**v0.9.0 breaking changes:** the `spatial` door and its stores were removed pending a redesign (pin
v0.8.x if you need them); `SpaceDevices` argument order now follows Tuya's, with `pageSize` moved up
beside `spaceIDs`; `tuya.Page` is gone in favour of plain `lastRowKey int64, pageSize int` arguments
and an `int64` cursor; `appaccount.ListDevices` is now `Devices` returning `[]tuya.UserDevice`,
`Account` is now `Get`, the `appaccount.Device` wrapper and `appaccount.IsMultiGang` are gone, and
`appaccount.Client` gained methods. `tuya.ErrSpaceNotFound` and `tuya.CodeNoSpacePermission` are
also gone: a space id Tuya refuses now surfaces as the plain `*tuya.APIError` it always was.

**v0.8.0 breaking changes:** the layers swapped — the old `cloud` subpackage is now the root `tuya`
package, and each owner mapping moved into its own subpackage (`cloud.X` → `tuya.X`,
`tuya.NewAppAccountClient` → `appaccount.NewService`). `cloud.IoT` is gone; its methods sit on
`*tuya.Client`. PostgreSQL migrations are now per-door directories.

## Roadmap

- [ ] A door for the other integration shape, where a user holds a space rather than a Tuya account.
- [ ] Probe `/multiple-names` against the ten undocumented channel categories, and drop any that
      never answer.
- [ ] Integration tests behind a build tag, against live infra.
- [ ] Further Tuya domains beyond devices and spaces.

## License

[MIT](LICENSE) © 2026 Ardian
