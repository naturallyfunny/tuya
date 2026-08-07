# tuya

[![Go Reference](https://pkg.go.dev/badge/go.naturallyfunny.dev/tuya.svg)](https://pkg.go.dev/go.naturallyfunny.dev/tuya)
![Go 1.25+](https://img.shields.io/badge/go-1.25%2B-00ADD8)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

A small, interface-first Go library for the [Tuya Cloud OpenAPI](https://developer.tuya.com/en/docs/cloud/).
It signs and authenticates requests, manages the app-level access token, and exposes typed
device and space operations — behind an ownership boundary you can hand to an untrusted caller.
Both of Tuya's tenancy models get a door: one Tuya account per human, or one root space per
tenant.

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

## Contents

- [Install](#install)
- [Why this shape](#why-this-shape)
- [Concepts](#concepts)
- [Setup](#setup)
- [Usage](#usage)
- [Linking accounts](#linking-accounts)
- [Design rationale](#design-rationale)
- [Non-goals](#non-goals)
- [Testing](#testing)
- [Compatibility](#compatibility)
- [Layout](#layout)
- [Status & roadmap](#status--roadmap)
- [License](#license)

## Install

```sh
go get go.naturallyfunny.dev/tuya
```

Requires Go 1.25+ (see [Compatibility](#compatibility)).

## Why this shape

Tuya's API is keyed by a **Tuya UID** and has no notion of *your* users. Two concerns fall
out of that, and the library keeps them in separate layers so neither leaks into the other:

1. **Talking to Tuya** — signing, the project-level token, typed device calls. Pure Tuya,
   keyed by UID, no idea who "owns" a device. This is the `cloud` subpackage.
2. **Owning a device** — mapping *your* opaque owner ID to a human's Tuya UID and refusing
   any call that crosses accounts. This is the root `tuya` package.

The payoff: the ownership guard lives at a single door (`tuya.AppAccountClient`) that
*cannot* be opened without first resolving an owner. The trusted, guard-free layer
(`cloud.IoT`) still exists for code that legitimately holds a UID — but an agent never
touches it.

## Concepts

Three composable tiers. Bind the one your caller needs:

| You have / need                                          | Use                     | Guarded? |
| -------------------------------------------------------- | ----------------------- | -------- |
| Just the transport (token lifecycle, signing, raw `Do`)  | `cloud.Client`          | no       |
| A Tuya UID or space ID, want typed device and space ops  | `cloud.IoT`             | no       |
| Your own owner ID, one Tuya app account per human        | `tuya.AppAccountClient` | **yes**  |
| Your own owner ID, one root space per tenant             | `tuya.SpaceClient`      | **yes**  |

- **`tuya.AppAccountClient`** — the owner-scoped door. Resolves owner → Tuya UID through an
  `AppAccountStore`, asserts the target device belongs to that account (a lean, unenriched
  listing plus a membership check), then delegates. It owns `AppAccount`,
  `ErrAccountNotLinked`, `ErrDeviceNotOwned`, and the `AppAccountStore` / `IoT` interfaces it
  drives — including the ownership check itself, which is built here from plain `cloud`
  primitives rather than asked of `cloud`.

  The name states a **tenancy model**, not verbosity. Tuya has two, and they differ in what
  a tenant *is*. `AppAccountClient` is the app-account model: every human holds their own
  Tuya app account, so the boundary is a Tuya UID and an `AppAccountStore` maps owner → UID.
  A bare `Client` would no longer say which of the two you are holding.

  The `App` prefix does a second job: *account* alone is ambiguous here. A Tuya **app
  account** holds devices and is keyed by UID; a Tuya **project account** holds the
  `accessID` / `accessSecret`. Naming the first one precisely is what keeps the two apart.
  Call sites stay short, because the variable name is yours —
  `app := tuya.NewAppAccountClient(...)` then `app.Account(ctx, owner)`.
- **`tuya.SpaceClient`** — the same door for Tuya's other tenancy model, the spatial one: a
  tenant *is* a root space, everything it owns hangs in the subtree below it, and there is no
  per-tenant UID at all. It is what Tuya recommends for multi-tenant property — hotels,
  apartments, offices. A `SpaceStore` maps owner → root space ID, and every call resolves the
  owner before it touches a space.

  It carries **two** guards, because they cost different amounts. Space operations ask Tuya one
  question — *is this space inside that root?* — and containment is transitive, so one boolean
  also settles deletes: everything under a space you own is yours too. Device operations get no
  such shortcut; see the [rationale](#design-rationale). Throughout the door, a zero space ID
  means *the tenant's own root*, never Tuya's project root.
- **`cloud.Client`** — the transport. Speaks Tuya at the **project level**: one access
  ID/secret yields an access token it caches in memory and refreshes on its own (lazily on
  expiry, reactively when Tuya returns code `1010`). Handles HMAC-SHA256 signing. `Do` is a
  raw escape hatch for endpoints not yet wrapped.
- **`cloud.IoT`** — a trusted facade over a `cloud.Client`: `ListDevices`, `DeviceStatus`,
  `SendCommands`, `DeviceChannelNames` for devices, and `CreateSpace`, `Space`, `ModifySpace`,
  `DeleteSpace`, `SpaceResources`, `ChildSpaces`, `RootSpaces`, `SpaceContains` for spaces. One
  method per Tuya endpoint, so each call is one request and nothing is composed behind your
  back — including pagination, which is handed to you a page at a time rather than looped over
  silently. No ownership guard; a holder can reach any device or space the project can.
  Guarding it is the root package's job.

Ready-made store adapters ship in-tree — pick one, or implement the interfaces yourself:

- **`postgres.AppAccountStore`** / **`postgres.SpaceStore`** — the owner → UID and owner → root
  space mappings in PostgreSQL (`pgx`), with an embedded migration runner shared by both.
- **`firestore.AppAccountStore`** / **`firestore.SpaceStore`** — the same contracts on Cloud
  Firestore: one document per owner, no migrations.

Each adapter deliberately reuses the interface's name rather than a plain `Store`. Now that both
packages hold a `SpaceStore` as well, a `postgres.Store` would have had nowhere left to go. The
names only collide in prose; in code the package always qualifies them.

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
store, err := postgres.NewAppAccountStore(ctx, pool, postgres.WithAutoMigrate())
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
//   store := firestore.NewAppAccountStore(fs)

// 2. The Tuya transport + device facade. baseURL selects the region.
transport, err := cloud.New(accessID, accessSecret, "https://openapi.tuyaus.com")
if err != nil {
    log.Fatal(err)
}
iot := cloud.NewIoT(transport)

// 3. The owner-scoped door, for the app-account tenancy model.
client := tuya.NewAppAccountClient(iot, store) // postgres.AppAccountStore satisfies tuya.AppAccountStore
```

For the spatial model, swap the store and the door — the transport and facade are the same:

```go
tenants, err := postgres.NewSpaceStore(ctx, pool, postgres.WithAutoMigrate())
if err != nil {
    log.Fatal(err)
}
hotel := tuya.NewSpaceClient(iot, tenants) // the same iot, taken as tuya.SpaceIoT
```

`cloud.New` prefetches an access token, so a bad credential or unreachable region fails here,
at wiring time, not on the first device call. Pass `cloud.WithHTTPClient` to control timeouts
and transport.

### Regions

`baseURL` selects the Tuya data-center endpoint:

| Region           | `baseURL`                          |
| ---------------- | ---------------------------------- |
| Western America  | `https://openapi.tuyaus.com`       |
| Eastern America  | `https://openapi-ueaz.tuyaus.com`  |
| Central Europe   | `https://openapi.tuyaeu.com`       |
| Western Europe   | `https://openapi-weaz.tuyaeu.com`  |
| China            | `https://openapi.tuyacn.com`       |
| India            | `https://openapi.tuyain.com`       |
| Singapore        | `https://openapi-sg.iotbing.com`   |

Singapore sits on a different domain entirely — `iotbing.com`, not `tuya*.com`. Pointing at the
wrong data center still mints a token, then refuses every business call, so a credential that
"works" but answers `28841107` usually means the base URL, not the credential.

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

// Each call is exactly one Tuya request — no hidden fan-out, no enrichment.
devices, err := iot.ListDevices(ctx, acc.TuyaUID)          // UID-addressed
status, err := iot.DeviceStatus(ctx, deviceID)             // device-addressed, no guard
channels, err := iot.DeviceChannelNames(ctx, deviceID)     // multi-gang labels, if you want them
```

> `cloud.IoT` and `cloud.Client.Do` carry **no** ownership guard by design. Don't hand them to
> an untrusted caller (e.g. an agent) — route that traffic through `tuya.AppAccountClient` or
> `tuya.SpaceClient`.

### Spaces

With `tuya.SpaceClient`, the tenant is a root space. A zero space ID always means *that* root,
so the common calls need no ID at all, and nothing reachable here can name a space belonging to
another tenant:

```go
rooms, page, err := hotel.ChildSpaces(ctx, owner, 0, cloud.DirectChildren) // one page
room, err := hotel.CreateSpace(ctx, owner, "Room 201", 0, "twin")          // under my root

things, page, err := hotel.SpaceResources(ctx, owner, room, cloud.Subtree) // devices in it
err = hotel.SendCommands(ctx, owner, deviceID, []cloud.DataPoint{
    {Code: "switch_1", Value: true},                                       // ownership asserted
})
if errors.Is(err, tuya.ErrSpaceNotOwned) {
    // the space is outside this tenant's subtree
}
```

Listings take an explicit `cloud.Scope` — `cloud.DirectChildren` or `cloud.Subtree` — because
Tuya's own default for that parameter is undocumented, and a listing that quietly covers the
wrong depth is exactly what you must not build an ownership check on. Each call returns **one**
page plus a `cloud.Page` cursor; see [rationale](#design-rationale) for why the loop is yours.

Deleting the tenant's own root is refused (`tuya.ErrRootSpaceProtected`): Tuya deletes a space
together with its subspaces, so that one call would erase the whole tenancy and leave your
mapping pointing at nothing. Unlink it in the store instead, or delete it deliberately through
`cloud.IoT`.

## Linking accounts

Both `AppAccountStore` adapters own the full lifecycle of the owner → Tuya-UID mapping.
`Link` is an upsert (re-linking refreshes the UID; re-linking a previously unlinked owner
revives the row rather than colliding on the key); `Unlink` is a soft-delete (`deleted_at`),
so `Get` stops returning it while the record is preserved for audit.

```go
acc, err := store.Link(ctx, owner, tuyaUID)   // upsert
err = store.Unlink(ctx, owner)                // soft-delete
```

The PostgreSQL store backs this with a `tuya_app_accounts` table (`owner` PK, `tuya_uid`,
timestamps, `deleted_at`); Firestore with a `tuya_app_accounts` collection (override via
`firestore.WithCollection`), one document per owner keyed by the owner string.

`SpaceStore` is the same lifecycle for the spatial model, keyed to a root space instead of a
UID (`tuya_space_tenants` in both backends):

```go
tenant, err := tenants.Link(ctx, owner, cloud.SpaceID(150000001))
err = tenants.Unlink(ctx, owner)
```

### Migrations (PostgreSQL)

A small custom runner, not `golang-migrate`. SQL files are embedded (`//go:embed`); versions
are tracked in `tuya_schema_migrations`. `WithAutoMigrate()` applies pending migrations on
startup. Without it, `NewAppAccountStore` validates the schema exists and fails fast if the
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

- **The ownership guard lives at the root (`tuya.AppAccountClient`), not in `cloud.IoT`.**
  `cloud.IoT` is a trusted, device-addressed layer with no tenant check — by design. Pushing
  the guard *up* to a single owner-scoped door makes it un-bypassable: you cannot reach a
  device without first resolving an owner. A guard buried in the device layer would have to
  thread a UID through every call and could still be sidestepped by a sibling method.

- **`cloud` exports concrete types; the *consumer* declares the interface.**
  `cloud.NewIoT` returns a concrete `*cloud.IoT`. The root package declares `tuya.IoT` as
  narrowly as its doors need, and `*cloud.IoT` satisfies it structurally. This is
  "accept interfaces, return structs" applied literally — mocking is the consumer's concern,
  so `app_account_test.go` fakes `tuya.IoT` and `tuya.AppAccountStore` with zero test-only code in
  `cloud`. Exporting a speculative interface from `cloud` would only add a
  compatibility burden that widens with every new domain.

- **`resolveChannelNames` uses `sync.WaitGroup.Go` + `errors.Join`, not `errgroup`.**
  The semantics are **collect-all**: an agent wants to know *every* channel that failed to
  resolve in one call. `errgroup.WithContext` is **fail-fast** — the first error cancels its
  siblings, which is exactly the information we don't want to lose. The "free context
  propagation" people reach for is precisely first-error cancellation. `errgroup` would be
  right if fail-fast were the goal; here it's a behavior change, not a cleanup.

- **The app-level token is cached in memory, with no store.**
  The Tuya credential is project-wide, not per-user, so there is nothing per-user to persist.
  A token store would add a dependency and a failure mode for state that is trivially re-fetched.

  The two refresh paths are deliberately different. `ensureValidToken` is lazy and trusts the
  cached expiry — the right call before a request goes out. `forceRefreshToken` runs when Tuya
  answers code `1010` and ignores that expiry entirely, because Tuya is the authority on whether
  a token it issued is still good: it will reject tokens our clock still considers valid, after a
  credential rotation or clock drift. Sharing one refresh path between the two looks like
  cleanup and quietly disables the retry — the reactive call returns `nil` without doing
  anything, and the replay carries the token Tuya just refused. A test pins this.

- **Ownership is checked by list-then-contains, and the check lives in the root package.**
  Each guarded call lists the account's devices and checks membership. Simple and correct for
  sporadic, one-intent-at-a-time traffic; caching is deferred until there's a real throughput
  need to justify the invalidation complexity.

  The listing comes from `cloud.IoT.ListDevices` — one request — and the membership loop lives
  in `tuya.AppAccountClient.assertOwned`. `cloud` deliberately exposes no `HasDevice`-style helper: a
  method whose reason to exist is a caller's guard would shape the lower layer around the upper
  one. `cloud` answers "what is on this UID"; what that *means* is the root package's word. The
  guard also never asks for channel labels — it needs identity, not names, and should not pay
  one request per multi-gang device on every guarded call. A test asserts that.

- **The spatial door carries two guards, because Tuya prices them very differently.**
  Guarding a *space* costs one request: `GET /v2.0/cloud/space/relation` answers whether a space
  sits inside the tenant's root, and containment is transitive — confirmed against the live API,
  where a root answers `true` for its grandchild. That also settles deletes, since everything
  under a space you own is yours too. Two edges of that endpoint shape the guard: a space
  compared against *itself* answers `false`, so the door grants the tenant its own root without
  asking (an optimization that is also a correctness fix), and a space the project cannot see at
  all is refused outright with code `40001900` rather than answered `false` — which the door
  translates to `ErrSpaceNotOwned`, because for a tenant it means the same thing. Guarding a
  *device* gets no such shortcut. Tuya has no "which space holds this device" lookup at all: `GET
  /v2.0/cloud/thing/{device_id}` returns product, status and location and no space or asset ID.
  The only route is to walk the subtree's resources and look for the ID, paginated, before every
  guarded call.

  The library pays that cost rather than passing it on. The alternative — guard spaces only, let
  each consumer check devices against its own mirror of the property — is genuinely cheaper for
  a consumer that already stores which room each device is in. But it leaves an agent that only
  wants to switch a light holding `cloud.IoT`, which is precisely what this README tells you
  never to do. A door that cannot turn on a lamp is not a door. The walk stops at the first
  match, so the common case is one request; a consumer that would rather answer from its own
  database can still bind `cloud.IoT` and guard it itself.

- **`cloud.Scope` is a required argument, not an option with a default.**
  Tuya's `only_sub` parameter decides whether a listing covers direct children or the whole
  subtree, and Tuya documents no default. An ownership check built on a listing that quietly
  covered the wrong depth would be wrong in a way nothing in the code would show, so
  `SpaceResources` and `ChildSpaces` refuse the zero value and make the caller say
  `DirectChildren` or `Subtree`. The parameter is then always sent explicitly, and Tuya's
  default never enters the picture.

- **Query parameters are snake_case, and the query string is always ASCII-sorted.**
  Tuya's reference tables say `only_sub`, `last_row_key`, `page_size`, `space_id`; its example
  requests *on the same pages* say `onlySub`, `lastRowKey`, `pageSize`, `spaceId`. Probing the
  live API settled it: the tables are right and the examples are wrong. The failure mode was the
  reason to check rather than guess — a camelCase `pageSize=3` is not rejected, it is *ignored*,
  and the server's default quietly applies (`page_size: 200`). For `only_sub` that would mean
  querying a different depth than you asked for, under an ownership check.

  Sorting is a separate trap with the same shape. Tuya orders query parameters before it
  verifies the signature, so an unsorted query fails as code `1004`, "sign invalid" — an error
  that points nowhere near its cause. `url.Values.Encode` sorts, which is why every query here
  is built through it, and a test asserts the ordering survives.

- **`cloud.SpaceID` is a named `int64` that accepts a JSON number *or* a string.**
  The live API answers with numbers, but Tuya's reference shows space IDs quoted (`"1500****"`),
  so both decode. It is a `Long`: routing one through `any` or `float64` would corrupt IDs above
  2^53 in silence, and a test pins that it doesn't.

- **`result: false` is an error for modify and delete, and data for `SpaceContains`.**
  `Do` hands back the raw result as soon as Tuya says `success: true`, so a delete that answers
  `{"success":true,"result":false}` would otherwise read as a deletion that never happened.
  `ModifySpace` and `DeleteSpace` therefore return `error` alone and translate `false` into
  `cloud.ErrNotApplied` — that is translating a vendor protocol into a Go idiom, the same job
  `Do` does for `code`, not composition. `SpaceContains` returns `(bool, error)` because there
  the boolean *is* the answer.

- **Paging is never looped inside `cloud`, and the one loop in the library is bounded.**
  Tuya documents how to fetch the next page but never how to know there isn't one, and a loop
  written from a guess is an infinite loop that burns quota. Live, the last page arrives as an
  empty `data` with the cursor field absent altogether. `cloud` hands you that page and the
  cursor and lets you compose; the device guard, which has no choice but to walk, stops on the
  documented signal *and* on a cursor that stopped moving, with a cap on pages read. The extra
  stops cost one comparison and turn any future surprise into a refusal rather than a hang.

- **`RootSpaces` exists in `cloud`, and deliberately not in `SpaceIoT`.**
  `GET /v2.0/cloud/space/child` without a `space_id` returns the root spaces of the whole cloud
  project — every tenant's. It is legitimate for an operator and catastrophic on a tenant-scoped
  path, so it is a separate method rather than the zero-ID case of `ChildSpaces` (which rejects
  zero), and the interface the spatial door declares does not mention it. The door cannot call
  what it cannot name.

- **`cloud.IoT` is one method per Tuya endpoint, with no exceptions.**
  If a method can't be pointed at exactly one endpoint, it is composing behavior, and
  composition belongs to whoever wants it. Two methods used to break this and were moved to the
  root package: `HasDevice` (born to serve the guard above) and `enrichDevices`, which carried
  the judgment that categories `kg` and `cz*` are the multi-gang ones — an opinion about Tuya's
  catalogue, not something its API states. What `cloud` offers instead is the primitive:
  `DeviceChannelNames` wraps `GET /v1.0/devices/{id}/multiple-names` and nothing more;
  `tuya.AppAccountClient` fans it out across the devices it judges worth labelling.

  The rule binds `IoT`, not `cloud.Client`. The transport keeps its policy — token refresh,
  retry on code `1010` — because that is protocol correctness, not domain composition.

  The cost is real and worth stating: a consumer binding `cloud.IoT` on its own gets no
  batteries. Wanting channel labels means writing the fan-out. That is the trade for a layer
  you can audit against the vendor's API docs line by line.

- **`cloud.New` prefetches a token with `context.Background()`.**
  Construction-time prefetch turns a bad credential or unreachable region into a wiring-time
  error. A caller that needs to bound it configures timeouts on the `*http.Client` via
  `WithHTTPClient` — the right lever for transport-level control.

- **The Firestore store writes timestamps client-side, not with `ServerTimestamp` sentinels.**
  `Link` returns the exact `Account` it just stored. A `ServerTimestamp` sentinel's value is
  unknown until *after* the commit resolves, which would force a second read to learn what was
  written. Stamping `time.Now().UTC()` inside the transaction lets `Link` return the stored
  `Account` from the one round trip it already makes. The trade-off is trusting the client
  clock for `created_at` / `updated_at` — acceptable for an audit timestamp on a low-frequency
  linking action, not for a monotonic event log. (The PostgreSQL store has no such tension: its
  `RETURNING` clause hands back the server-set `NOW()` in the same statement.)

- **The opaque owner is validated as a Firestore document ID, up front.**
  The owner string is used *verbatim* as the document ID — no hashing, no escaping — so a lookup
  is a direct read, not a query. That subjects the owner to Firestore's document-ID rules
  (non-empty, no `/`, not `.` or `..`, ≤1500 bytes, not the reserved `__*__` pattern), so
  `validateOwner` rejects a violating owner loudly at the call site rather than letting it
  corrupt a document path or fail server-side with an opaque error. It is the one piece of store
  logic that is pure and [table-tested](#testing).

## Non-goals

To keep the surface honest, the library deliberately does **not**:

- **Run the Tuya account-authorization flow.** It maps an *already-authorized* Tuya UID to your
  owner; obtaining that UID — the human granting your Tuya project access to their account —
  happens upstream in your onboarding. The library's world begins once you can call
  `store.Link(owner, tuyaUID)`.
- **Wrap the whole Tuya Cloud OpenAPI.** Only what an agent needs is typed: the device
  operations and Tuya's seven space-management endpoints. `cloud.Client.Do` is the raw escape
  hatch for everything else — deliberately unguarded, and not something to hand an agent.
- **Optimize for throughput.** Ownership is re-checked on every call — a device listing in the
  app-account model, a subtree walk in the spatial one — and the token lives in memory. Both are
  right for sporadic, one-intent-at-a-time agent traffic and would only be rebuilt (caching,
  invalidation) if a real throughput need appeared. See
  [Design rationale](#design-rationale).
- **Verify trust for `cloud.IoT`.** That layer is device-addressed with no tenant check by design;
  deciding who may hold it is the consumer's job.

## Testing

```sh
go test ./...
```

The root `tuya` package — the ownership boundary, where a bug means a device reaches the wrong
owner — is unit-tested against fake `IoT`, `SpaceIoT` and store implementations: happy paths,
`ErrAccountNotLinked` / `ErrDeviceNotOwned` / `ErrSpaceNotLinked` / `ErrSpaceNotOwned`, the
short-circuit guards (a command must never reach an unowned device), and the cost guard (an
ownership check must never trigger channel-name requests). For the spatial door it also pins
that a zero space ID resolves to the tenant's own root without asking Tuya, that the tenant root
cannot be deleted through the door, that a space the project cannot see is refused as unowned
rather than surfacing a raw API error, and that the paginated device walk terminates on a
stalled cursor *and* on a cursor that keeps advancing forever. That suite covers **88.4%** of
the package's statements. The Firestore `validateOwner` rules are table-tested (100% of that
function).

`cloud` is tested against an `httptest` server (**71.4%** of the package) — real HTTP round trips
over a real socket, not mocks. Two behaviours are worth pinning because they are invisible from
the outside. The token retry: the stub answers code `1010` and the test asserts the retry
carried a *different* access token, since a retry that silently replays the rejected token looks
identical to one that works until a credential is rotated in production. And the space layer's
wire contract, each case taken from a live response: the field names Tuya really sends, a last
page arriving with no cursor at all, an ID surviving beyond 2^53, `result:false` becoming an
error, only the parameter spellings Tuya binds going out — and the query string staying
ASCII-sorted, which nothing but a `1004` would otherwise tell you about.

The rest is integration-shaped by nature: signing against Tuya's live service, and the
`postgres` / `firestore` stores talking to a real database or the Firestore emulator. Faking a
Firestore transaction would exercise the fake, not the behaviour, so their reported coverage is
honestly low. Wiring them to live infrastructure behind a build tag is on the
[roadmap](#status--roadmap).

## Compatibility

- **Go 1.25+**, per `go.mod`. The concurrent fan-out in `resolveChannelNames` uses
  `sync.WaitGroup.Go`, added in Go 1.25.
- **Breaking in v0.8.0, alongside the spatial door.** `postgres.Option` and `firestore.Option`
  are now `func(*options)` rather than functions over one store type, so `WithAutoMigrate` and
  `WithCollection` serve both stores; call sites that just pass `postgres.WithAutoMigrate()` or
  `firestore.WithCollection("…")` are unaffected. `firestore.DefaultCollection` is now
  `firestore.DefaultAppAccountCollection`, beside the new `DefaultSpaceCollection`.
- **The dependency cost is opt-in.** The root `tuya` package and the `cloud` layer import
  **only the standard library** — bind those, bring your own stores, and you add nothing
  to your module graph. The external dependencies (`jackc/pgx` for `postgres`;
  `cloud.google.com/go/firestore` and gRPC for `firestore`) are compiled only if you import that
  store subpackage.

## Layout

Every package is split **per tenancy model**, not per kind of declaration. What differs
between models is the guard — the riskiest code here — so it should be readable in one file
rather than assembled from a types file and a behavior file. The store adapters follow the
same rule, which is why they are `app_account.go` and not `store.go`.

```
iot.go           Package doc, the consumer-side IoT interface, ErrDeviceNotOwned.
                 What both doors share.
app_account.go   The app-account door, end to end: AppAccount, ErrAccountNotLinked,
                 AppAccountStore, AppAccountClient and its device operations —
                 the ownership guard, and the channel-name fan-out with the
                 multi-gang judgement it needs.
space.go         The spatial door, end to end: SpaceTenant, ErrSpaceNotLinked,
                 ErrSpaceNotOwned, ErrRootSpaceProtected, SpaceStore, SpaceIoT,
                 SpaceClient — and both guards, the cheap containment check and
                 the bounded subtree walk devices need.
cloud/
  client.go      cloud.Client transport (token cache/refresh, signing, Do) + IoT facade.
  auth.go        request signing + token lifecycle.
  device.go      typed Device/DataPoint/Channel + one method per device endpoint.
  space.go       SpaceID/Space/Resource/Page/Scope + one method per space endpoint.
postgres/
  app_account.go AppAccountStore on PostgreSQL + the migration runner both stores share.
  space.go       SpaceStore on PostgreSQL.
  migrations/    embedded .up.sql / .down.sql.
firestore/
  app_account.go AppAccountStore on Cloud Firestore (schemaless, no migrations).
  space.go       SpaceStore on Cloud Firestore.
```

## Status & roadmap

The public API above is stable and in use — the owner-scoped doors, the `cloud` split, and the
stores. Remaining work is additive:

- [x] MIT `LICENSE`.
- [x] Unit tests on both ownership boundaries and Firestore owner validation.
- [x] `SpaceClient` + `SpaceStore` — the door for Tuya's spatial tenancy model, alongside
      `AppAccountClient` / `AppAccountStore`, over `cloud/space.go`.
- [x] Settle Tuya's self-contradicting reference against the live API: parameter spelling,
      response casing, space-ID JSON type, end-of-listing signal, and whether `/space/relation`
      is transitive. It is — see [Design rationale](#design-rationale).
- [ ] Integration tests for `cloud` / `postgres` / `firestore` behind a build tag and live infra.
- [ ] Further Tuya domains beyond device and space control (`cloud/home.go`), added as new files
      on `cloud.IoT`.
- [ ] Ownership-check caching — deferred until a real throughput need justifies the invalidation
      cost.

## License

[MIT](LICENSE) © 2026 Ardian
