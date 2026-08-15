# tuya

[![Go Reference](https://pkg.go.dev/badge/go.naturallyfunny.dev/tuya.svg)](https://pkg.go.dev/go.naturallyfunny.dev/tuya)
![Go 1.25+](https://img.shields.io/badge/go-1.25%2B-00ADD8)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

A small, interface-first Go library for the [Tuya Cloud OpenAPI](https://developer.tuya.com/en/docs/cloud/).
The root package signs and authenticates requests, manages the app-level access token, and
exposes typed device and space operations. On top of it, two optional subpackages do the job
Tuya leaves to you: **mapping your own identity system onto Tuya's**. Both of the ways Tuya lets
a cloud project reach devices get a door — through app accounts you link, or through the
project's own tree of spaces.

It is **agnostic about who calls it**. An HTTP backend, a batch worker, a CLI, an agent
toolset — the library neither knows nor cares. It **answers questions about ownership; it does
not enforce them**, because "this device is not on the owner's account" is not always a reason
to refuse: a product with device sharing legitimately reaches across accounts, and a library
that hard-refused those calls would block the correct integration to protect the careless one.
Where an answer is expensive, [Design rationale](#design-rationale) states the cost instead of
assuming you won't ask often.

```go
devices, err := app.ListDevices(ctx, owner)        // by your own owner ID
ok, err := app.HasDevice(ctx, owner, id)           // a fact, for you to act on
err = c.SendCommands(ctx, id, []tuya.DataPoint{
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
out of that, and the library keeps them in separate packages so neither leaks into the other:

1. **Talking to Tuya** — signing, the project-level token, typed device and space calls. Pure
   Tuya, keyed by UID or space ID, no idea who "owns" anything. This is the root `tuya` package.
2. **Owning a device** — mapping *your* opaque owner ID to a Tuya handle, and answering what
   that mapping makes answerable. This is one subpackage per door: `appaccount` and `spatial`.

The payoff: the mapping lives in one place, and so does the only question it makes answerable —
*is this Tuya handle under the identity linked to this owner?* Nothing else in your stack holds
both halves, so nothing else can answer it. What the answer **means** is yours: refuse, allow,
or check it against your own sharing rules first.

That is also why the layers sit this way round in the module. Importing a module named `tuya`
should hand you Tuya; the owner mapping is something built *on* the API, not the API itself,
and a consumer that already has its own mapping should not have to import it to make a device
call. So the client is the root package and each door is an opt-in import.

### Two doors, two Tuya models

The doors are not two flavours of one thing, and their surfaces do not match. `appaccount`
anchors an owner to a **Tuya UID**, which owns devices directly. `spatial` anchors an owner to a
**space in your project's tree**, which owns devices by binding. Those are separate worlds inside
Tuya, not two views of one: an app account's own places live in the older *asset* family
(`/v1.0/iot-03/users/{uid}/assets`), and the ids it returns are rejected by every
`/v2.0/cloud/space` endpoint this library speaks — `40001900 No space permission`, the same answer
you get for a space in someone else's project. Nothing relates the two trees, and no endpoint
maps a space back to a user.

So there is no `appaccount.ChildSpaces`, and `spatial` has no "every device this owner has" with
full detail. Neither is an omission waiting to be filled; building either would mean inventing a
mapping Tuya does not have. Pick the door that matches how your devices are actually organized,
and treat the two id spaces as unrelated — they are both `int64` and `string`, and mixing them
buys you a runtime error, not a compile-time one.

## Concepts

Two composable tiers. Bind the one your caller needs:

| You have / need                                          | Use                    | Resolves an owner? |
| -------------------------------------------------------- | ---------------------- | ------------------ |
| A Tuya UID, device ID or space ID; or raw `Do`            | `tuya.Client`          | no                 |
| Your own owner ID, one Tuya app account per human        | `appaccount.Service`   | **yes**            |
| Your own owner ID, one space per owner                    | `spatial.Service`      | **yes**            |

- **`tuya.Client`** — the client. Speaks Tuya at the **project level**: one access ID/secret
  yields an access token it caches in memory and refreshes on its own, when Tuya rejects the
  cached one with code `1010`. Handles HMAC-SHA256 signing. Every other method is
  one Tuya endpoint: `UserDevices`, `SpaceDevices`, `DeviceStatus`, `SendCommands`,
  `DeviceChannelNames` for devices, and `CreateSpace`, `Space`, `ModifySpace`, `DeleteSpace`,
  `SpaceResources`, `ListSpaces`, `SpaceRelation` for spaces. One method per endpoint, so each
  call is one request and nothing is composed behind your back — including pagination, which is
  handed to you a page at a time rather than looped over silently. `Do` is a raw escape hatch
  for endpoints not yet wrapped. No notion of an owner at all; a holder can reach any device or
  space the project can. Device commands go through here — they are addressed by device ID,
  which is already a Tuya handle, so there is nothing for a door to resolve.
- **`appaccount.Service`** — resolves owner → Tuya UID through an `appaccount.Store`, lists that
  account's devices with channel names filled in, and answers `HasDevice`. The package owns
  `Account`, `ErrNotLinked`, and the `Store` / `Client` interfaces it drives — including the
  membership check itself, which is built here from plain root primitives rather than asked of
  the root package.

  The package name states **which of Tuya's two device models** it speaks. The app-account one:
  every human holds their own Tuya app account, you link that account to your cloud project, and
  the boundary is its Tuya UID — a `Store` maps owner → UID. A project can hold any number of
  linked app accounts and can unlink one at any time; each account belongs to the person, not to
  your project.

  The `app` half of the name does a second job: *account* alone is ambiguous here. A Tuya **app
  account** holds devices and is keyed by UID; a Tuya **project account** holds the
  `accessID` / `accessSecret`. Naming the first one precisely is what keeps the two apart.
  Inside the package nothing repeats it — `appaccount.Account`, not `appaccount.AppAccount` —
  and call sites stay short because the variable name is yours:
  `app := appaccount.NewService(...)` then `app.Account(ctx, owner)`.
- **`spatial.Service`** — the same door for Tuya's other model, the spatial one: devices sit in
  a tree of spaces that belongs to the cloud project itself, and no Tuya app account is involved
  anywhere in it. That tree is not something you link — a project has exactly one, bound to it,
  with nothing to attach or detach. A `spatial.Store` maps owner → space ID, and every call
  resolves the owner before it touches a space. Consumers typically hand each of their customers
  one space and treat everything below it as theirs — property, hotels, offices — but the library
  only knows the link.

  Its space operations do refuse a space outside the owner's subtree — not as a security policy
  but because the IDs they take are owner-relative by construction: a zero ID means *the owner's
  own space*, never the project's top level, and the door has no way to name someone else's space
  in the first place. That check is one question to Tuya, and containment is transitive, so one
  boolean also settles deletes. Devices are the opposite case: `ContainsDevice` reports rather
  than refuses, and its cost is worth reading before you build on it — see the
  [rationale](#design-rationale).

### Identifiers are plain types

Owner, Tuya UID, and device ID are `string`; space ID is `int64`. There are no named wrappers
around them, and that is deliberate: these doors are wrappers, and a wrapper has no business
inventing a parallel vocabulary for values it only passes through. `database/sql` takes a
`string` query; `net/http` takes a `string` URL.

Named types were tried here and taken back out before release. What they were supposed to buy
was a compiler check on argument order — but an untyped string constant converts to any
string-based type on its own, so a swapped literal still compiles and `go vet` stays quiet.
The protection only ever covered variables. What was left was a nicer-looking call site, which
is not enough to pay for the vocabulary.

Nothing is converted at the edges, so your own IDs go straight in and database columns are
unchanged.

Ready-made store adapters ship in-tree — pick one, or implement the interfaces yourself:

- **`postgres.AppAccountStore`** / **`postgres.SpaceStore`** — the owner → UID and owner → space
  mappings in PostgreSQL (`pgx`), with an embedded migration runner shared by both.
- **`firestore.AppAccountStore`** / **`firestore.SpaceStore`** — the same contracts on Cloud
  Firestore: one document per owner, no migrations.

Adapters are split **per backend**, not per door, and that is why they are the one place a name
still carries its door: a `postgres.Store` would have had nowhere to go once the same package
holds both, and the alternative — `appaccount/postgres` beside `spatial/postgres` — is two
packages with the same name that force an alias at every import, with `Querier`, `Option` and
the migration runner homeless between them.

Dependency direction is acyclic and points inward — adapters depend on the doors, the doors
depend on the root, and the root depends on nothing of ours:

```
postgres ──┬─▶ appaccount ─┐
           │               ├─▶ tuya
firestore ─┴─▶ spatial ────┘
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

// 2. The Tuya client. baseURL selects the region.
c, err := tuya.New(accessID, accessSecret, "https://openapi.tuyaus.com")
if err != nil {
    log.Fatal(err)
}

// 3. The owner-scoped door, for the app-account tenancy model.
app := appaccount.NewService(c, store) // postgres.AppAccountStore satisfies appaccount.Store
```

For the spatial model, swap the store and the door — the client is the same:

```go
spaces, err := postgres.NewSpaceStore(ctx, pool, postgres.WithAutoMigrate())
if err != nil {
    log.Fatal(err)
}
hotel := spatial.NewService(c, spaces) // the same client, taken as spatial.Client
```

`tuya.New` prefetches an access token, so a bad credential or unreachable region fails here,
at wiring time, not on the first device call. Pass `tuya.WithHTTPClient` to control timeouts
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

List devices by *your* owner ID, and ask about ownership when you need to know:

```go
devices, err := app.ListDevices(ctx, owner)         // typed devices + per-channel names
if errors.Is(err, appaccount.ErrNotLinked) {
    // route the human into the account-linking flow
}

acc, err := app.Account(ctx, owner)                 // the linked owner ↔ UID mapping

ok, err := app.HasDevice(ctx, owner, id)            // one request, flat
```

`HasDevice` returns a fact, not a verdict. A `false` means the device is not listed under that
owner's Tuya account — which is exactly what you'd expect for a device someone shared with them,
so the decision is yours to make:

```go
switch {
case err != nil:
    return err                                  // the lookup failed; not the same as "no"
case ok, myShareRules.Allow(owner, id):
    err = c.SendCommands(ctx, id, []tuya.DataPoint{{Code: "switch_1", Value: true}})
default:
    return ErrForbidden                         // your rule, your error
}
```

Commands themselves go through the client. A device ID is already a Tuya handle, so there is
nothing about it for a door to resolve:

```go
acc, _ := store.Get(ctx, owner)

// Each call is exactly one Tuya request — no hidden fan-out, no enrichment.
devices, err := c.UserDevices(ctx, acc.TuyaUID)          // UID-addressed
status, err := c.DeviceStatus(ctx, deviceID)             // device-addressed
channels, err := c.DeviceChannelNames(ctx, deviceID)     // multi-gang labels, if you want them

// Space-addressed: up to 5 space IDs, optional product/category filters, page by last device ID.
devices, err := c.SpaceDevices(ctx, []int64{spaceID}, true, nil, nil, "", 20)
```

`SpaceDevices` returns only the devices bound to the space IDs you name. The `recursive`
argument is Tuya's `is_recursion`, and on 15 August 2026 it had no effect we could observe:
a device bound to a space stayed invisible from that space's parent and from the root, and
asking with `true` or `false` at the space itself returned the same list. The parameter is
passed through as Tuya documents it, but do not plan a subtree walk on it — `SpaceResources`
*is* transitive, and that is what `spatial.ContainsDevice` scans.

`DeviceChannelNames` is worth a request only for devices that have several channels.
`appaccount.IsMultiGang(category)` is the same judgement `ListDevices` makes internally, exported
so you can ask it before spending the request — it reads category `kg` and the `cz*` family.

> `tuya.Client` knows nothing about owners. A holder can reach every device in the project.
> That is the point — the doors tell you whose a handle is, and you decide what follows.

### Spaces

With `spatial.Service`, each owner is linked to one space. A zero space ID always means *that*
space, so the common calls need no ID at all, and nothing reachable here can name a space
belonging to another owner:

```go
// The last argument is the page you want. The zero Page is the first one, at Tuya's
// own page size; hand the returned page back to get the next, once you have checked
// there is one — a zero LastRowKey means that was the end, not "start over".
// onlySub: true lists the direct children, false the whole subtree.
rooms, next, err := hotel.ChildSpaces(ctx, owner, 0, true, tuya.Page{})
if next.LastRowKey != 0 {
    rooms, next, err = hotel.ChildSpaces(ctx, owner, 0, true, next)
}

room, err := hotel.CreateSpace(ctx, owner, "Room 201", 0, "twin") // under the owner's space

things, next, err := hotel.SpaceResources(ctx, owner, room, false, tuya.Page{})
if errors.Is(err, spatial.ErrNotOwned) {
    // the space is outside the owner's subtree
}

space, err := hotel.SpaceOf(ctx, owner) // which space is theirs

// Devices bound to one space, with names and online state. lastID is the id of the last
// device of the previous page and is exclusive; "" starts. pageSize 0 means Tuya's maximum
// of 20. An empty slice — not a short one — is the end.
devices, err := hotel.SpaceDevices(ctx, owner, room, "", 0)
```

`SpaceDevices` reports only the devices bound to that one space; it never descends. Tuya's
`is_recursion` has no effect on this endpoint, so the door does not offer a recursive form it
could not honour. For the whole subtree use `SpaceResources`, which *is* transitive but returns
identifiers only — that difference is Tuya's, not ours, and it is what `ContainsDevice` is built on.

The two questions the door can answer about a space and a device:

```go
ok, err := hotel.ContainsSpace(ctx, owner, spaceID)   // one request
ok, err := hotel.ContainsDevice(ctx, owner, deviceID) // scans the subtree — read the cost first
```

`ContainsDevice` is the expensive one, and deliberately a call you make rather than a check that
runs behind every command. If your own database already records which space a device sits in, it
will answer faster than this ever can, and you should ask it instead.

Listings take `only_sub` as a positional argument rather than an option, because Tuya's own
default for that parameter is undocumented, and a listing that quietly covers the wrong depth
is exactly what you must not build an ownership check on. It is always sent explicitly. Each call returns **one**
page plus a `tuya.Page` cursor; see [rationale](#design-rationale) for why the loop is yours.

Deleting the owner's own space is refused (`spatial.ErrOwnerSpaceProtected`): Tuya deletes a
space together with everything below it, so that one call would erase the owner's whole reach and
leave your mapping pointing at a space that is gone. Unlink it in the store instead, or delete
it deliberately through `tuya.Client`.

## Linking accounts

Both `AppAccountStore` adapters own the full lifecycle of the owner → Tuya-UID mapping.
`Link` is an upsert (re-linking refreshes the UID; re-linking a previously unlinked owner
revives the row rather than colliding on the key); `Unlink` is a soft-delete (`deleted_at`),
so `Get` stops returning it while the record is preserved for audit.

```go
acc, err := store.Link(ctx, owner, uid)   // upsert
err = store.Unlink(ctx, owner)            // soft-delete
```

The PostgreSQL store backs this with a `tuya_app_accounts` table (`owner` PK, `tuya_uid`,
timestamps, `deleted_at`); Firestore with a `tuya_app_accounts` collection (override via
`firestore.WithCollection`), one document per owner keyed by the owner string.

The `SpaceStore` adapters are the same lifecycle for the spatial model, keyed to a space instead
of a UID (`tuya_spaces` in both backends):

```go
space, err := spaces.Link(ctx, owner, 150000001)
err = spaces.Unlink(ctx, owner)
```

### Migrations (PostgreSQL)

A small custom runner, not `golang-migrate`. SQL files are embedded (`//go:embed`); versions
are tracked in `tuya_schema_migrations`. `WithAutoMigrate()` applies pending migrations on
startup. Without it, `NewAppAccountStore` validates the schema exists and fails fast if the
consumer forgot to migrate.

Each door owns its own migration directory — `migrations/appaccount/`, `migrations/spatial/` —
and each store runs only its own. Sequence numbers restart per door, and the version recorded in
`tuya_schema_migrations` is the door-qualified path (`appaccount/000001_init.up.sql`). A consumer
that only uses the spatial door never gets `tuya_app_accounts` created behind its back.

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

- **The doors report ownership; they do not enforce it.**
  Earlier versions guarded: `DeviceStatus` and `SendCommands` sat on both doors and refused any
  device that failed an ownership check. That is gone, for three reasons. A mandatory guard
  **blocks correct integrations** — device sharing across accounts is an ordinary product
  feature, and a shared device is by definition absent from the owner's listing, so that
  consumer was forced down to the raw client and lost the owner resolution that was the only
  thing it wanted here. A device ID arriving from somewhere untrustworthy is **the consumer's
  design decision**, not something a mapping library should police. And once the check is gone,
  `owner` on a device-addressed method resolves nothing at all — so the methods went with it
  rather than keeping a parameter that is read and discarded.

  What replaces them is a question: `HasDevice`, `ContainsSpace`, `ContainsDevice`, each
  returning a `bool`. Commands go through `tuya.Client`. If you want a guarded wrapper, it is
  three lines in your own code, written once, with your rules in the middle — and nobody else's
  product is bent around them.

- **There is no facade between the transport and the endpoints.**
  A `tuya.IoT` type used to sit in the middle: one field, a `*Client`, and no work of its own.
  It was there to make `Do` unreachable from the layer that made device calls, which it never
  really did — the type that owns `Do` was one field dereference away, and what actually limits
  a door to the endpoints it needs is the narrow `Client` interface the door declares, not a
  struct in the package it imports. A layer with no work of its own also has no honest name:
  `IoT`, `Service`, `API` and `Transport` each claim something the type did not do. So the
  endpoint methods sit on `Client`, and the one-method-per-endpoint rule is held by the same
  discipline that held it before — a type boundary was never what enforced it.

- **The root package exports concrete types; the *consumer* declares the interface.**
  `tuya.New` returns a concrete `*tuya.Client`. Each door declares its own interface —
  `appaccount.Client`, `spatial.Client` — as narrowly as that door needs, and `*tuya.Client`
  satisfies both structurally. Neither is a general facade: one is two methods wide, the other
  seven, and each lists only what that door calls. This is "accept interfaces, return structs"
  applied literally — mocking is the consumer's concern, so `appaccount/service_test.go` fakes
  `appaccount.Client` and `appaccount.Store` with zero test-only code in the root package.
  Exporting a speculative interface from the root would only add a compatibility burden that
  widens with every new domain.

- **`resolveChannelNames` uses `sync.WaitGroup.Go` + `errors.Join`, not `errgroup`.**
  The semantics are **collect-all**: the caller wants to know *every* channel that failed to
  resolve in one call. `errgroup.WithContext` is **fail-fast** — the first error cancels its
  siblings, which is exactly the information we don't want to lose. The "free context
  propagation" people reach for is precisely first-error cancellation. `errgroup` would be
  right if fail-fast were the goal; here it's a behavior change, not a cleanup.

- **The app-level token is cached in memory, with no store.**
  The Tuya credential is project-wide, not per-user, so there is nothing per-user to persist.
  A token store would add a dependency and a failure mode for state that is trivially re-fetched.

  There is exactly one refresh path, and Tuya starts it. `Do` spends whatever token is cached;
  when Tuya answers code `1010` the client refreshes and replays the request once. It does not
  check the expiry first, and `expire_time` is not even kept.

  That was tried and removed. A clock check cannot make the token valid — Tuya will refuse
  tokens our clock still likes, after a credential rotation or a little drift, and it can refuse
  one a millisecond after the check passed. So the reactive path has to exist and has to be
  correct no matter what; the check on top of it is pure optimization. What it optimizes is one
  wasted request every two hours. What it costs is an exclusive `tokenLock.Lock()` on **every**
  `Do`, a serialization point on the hottest path in the library. Defending it needs an argument
  about how often callers call — which is the one argument this library does not make.

- **`HasDevice` answers by list-then-contains, and lives in the door.**
  It lists the account's devices and checks membership. The cost is **one request, flat** no
  matter how many devices the account has. Nothing is cached: an ownership cache has to be
  invalidated when a device is added, removed or re-linked, and the library has no way to learn
  about any of those. A consumer that can learn about them is in a better position to cache than
  this library is.

  The listing comes from `tuya.Client.UserDevices`. The root package deliberately exposes no
  equivalent: a method whose reason to exist is the layer above would shape the lower layer
  around the upper one. The root answers "what is on this UID"; the door is what knows that UID
  belongs to your owner. `HasDevice` never asks for channel labels either — it needs identity,
  not names, and should not pay one request per multi-gang device. A test asserts that.

  A device that is absent is a plain `false`; only a failed lookup is an error. "Not found" and
  "could not look" must not arrive as the same answer to code that branches on it.

- **The spatial door refuses foreign *spaces* but only reports on *devices*, because Tuya prices
  the two questions very differently — and only one of them is owner-relative.**
  Checking a *space* costs one request: `GET /v2.0/cloud/space/relation` answers whether a space
  sits inside the owner's, and containment is transitive — confirmed against the live API, where
  a space answers `true` for its grandchild. That also settles deletes, since everything
  under a space you own is yours too. Two edges of that endpoint shape the guard: a space
  compared against *itself* answers `false`, so the door grants an owner their own space without
  asking (an optimization that is also a correctness fix), and a space the project cannot see at
  all is refused outright with code `40001900` rather than answered `false` — which the door
  translates to `spatial.ErrNotOwned`, because for the owner it means the same thing.

  The space operations keep that refusal because their IDs are owner-relative by construction —
  a zero means the owner's own space, and the door offers no vocabulary for anyone else's. There
  is no correct call it can block.

  A *device* gets no such shortcut. Tuya has no "which space holds this device" lookup at all:
  `GET /v2.0/cloud/thing/{device_id}` returns product, status and location and no space or asset
  ID. The only route is to enumerate the subtree's resources and look for the ID, paginated.
  `ContainsDevice` does exactly that and **know the cost before you build on it**: it stops at
  the first match, so a lucky call is one request, but the page holding your device is not
  guaranteed to be the first, and the ceiling is 50 pages of 200 resources. Unlike `HasDevice`,
  this **grows with the size of the estate**.

  That is precisely why it is a call you make and not a check that runs behind every command. If
  your own database already records which space each device sits in, it will answer faster than
  this ever can, and you should ask it instead. Giving up at the page cap is an error, never a
  `false` — "not there" and "stopped looking" are different answers.

- **`only_sub` is a required argument, not an option with a default.**
  Tuya's `only_sub` parameter decides whether a listing covers direct children (`true`) or the
  whole subtree (`false`), and Tuya documents no default. An ownership check built on a listing
  that quietly covered the wrong depth would be wrong in a way nothing in the code would show,
  so `SpaceResources` and `ListSpaces` take it positionally and always send it explicitly.
  Tuya's default never enters the picture.

- **The page is an ordinary argument, and it is the same type coming back.**
  Listings used to take `...tuya.PageOption` — `WithPageSize`, `WithLastRowKey`, over a struct
  of two pointer fields. The pointers existed to tell "unset" from zero, a distinction this
  domain does not have: `page_size=0` is not a request anyone means, and `last_row_key=0` is
  precisely how Tuya says there is no next page. Meanwhile the call *returned* a `tuya.Page`
  that no caller could hand back, so every walk unpacked it and rebuilt it as options. Now one
  type travels both ways, a zero field is simply not sent, and the page you get is the page you
  pass to get the one after it — once you have checked its `LastRowKey` is not zero, because a
  zero cursor means the walk is over, not that it should start again.

  `SpaceDevices` stands outside this, and the reason is Tuya's rather than ours. Tested against
  the Singapore data center on 15 August 2026: the `thing/space/device` response envelope holds
  only `success`, `t`, `tid` and `result`, and `result` is a bare array. **There is no cursor to
  hand back** — returning a `Page` there would mean inventing one. Its cursor is `last_id`, the
  ID of the last device you received, and it is exclusive; an ID the space does not hold is
  rejected with `40000903`. The walk ends on an **empty** page, not a short one: six devices at
  `page_size=2` came back as 2, 2, 2 and then `[]`. A full page never means there is another,
  and a caller who stops early on a short page is relying on something the API never promised.
  `page_size` itself is mandatory — omit it and Tuya answers `1110` — and caps at 20, so
  `SpaceDevices` sends `SpaceDevicePageSizeMax` when you pass 0 rather than shipping a request
  that is certain to be refused.

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

- **Space IDs are `int64`, never `any` or `float64`.**
  A Tuya space ID is a `Long`; routing one through `any` or `float64` would corrupt IDs above
  2^53 in silence, and a test pins that it doesn't. The live API answers with numbers. Tuya's
  reference shows them quoted (`"1500****"`), which an earlier `UnmarshalJSON` accepted too —
  that went out with the named types, so a quoted ID now fails to decode instead of passing
  quietly.

- **`result: false` is an error for modify and delete, and data for `SpaceRelation`.**
  `Do` hands back the raw result as soon as Tuya says `success: true`, so a delete that answers
  `{"success":true,"result":false}` would otherwise read as a deletion that never happened. A
  *missing* result is treated the same way, because it is not a confirmation either — which is
  not hypothetical: asking for a space that no longer exists returns `success: true` with no
  result at all, and `Space` reports that as `tuya.ErrSpaceNotFound` rather than letting a JSON
  parse error stand in for "gone".
  `ModifySpace` and `DeleteSpace` therefore return `error` alone and translate `false` into
  `tuya.ErrNotApplied` — that is translating a vendor protocol into a Go idiom, the same job
  `Do` does for `code`, not composition. `SpaceRelation` returns `(bool, error)` because there
  the boolean *is* the answer.

- **Paging is never looped inside the client, and the one loop in the library is bounded.**
  Tuya documents how to fetch the next page but never how to know there isn't one, and a loop
  written from a guess is an infinite loop that burns quota. Live, the last page arrives as an
  empty `data` with the cursor field absent altogether. The client hands you that page and the
  cursor and lets you compose; the device scan, which has no choice but to walk, stops on the
  documented signal *and* on a cursor that stopped moving, with a cap on pages read. The extra
  stops cost one comparison and turn any future surprise into a refusal rather than a hang.

- **`ListSpaces` takes a space ID of zero to mean the whole project, and a test keeps the door
  away from it.**
  `GET /v2.0/cloud/space/child` without a `space_id` returns the top-level spaces of the entire
  cloud project. That is one endpoint with an optional parameter, so it is one method — Tuya
  defines the zero case itself, and splitting it in two produced a name (`RootSpaces`) that no
  endpoint had.

  It is still legitimate for an operator and catastrophic on an owner-scoped path, and it used
  to be the interface that ruled it out: the door could not call what it could not name. Now the
  interface names it, so the guarantee moved into code that is asserted rather than typed.
  `spatial.Service` reaches `ListSpaces` only through `resolve`, which turns a caller's zero into
  the owner's own space, and `ownerSpace` refuses both an unlinked owner and a link whose space
  ID is zero. A test drives the door with each of those and fails if a zero ever reaches the
  client — and it was checked against a deliberately broken guard, not just a passing run.

- **Every method on `tuya.Client` is one Tuya endpoint, except the protocol itself.**
  If a method can't be pointed at exactly one endpoint, it is composing behavior, and
  composition belongs to whoever wants it. Two methods used to break this and were moved into
  the doors: `HasDevice` (born to serve the layer above, and now living where the owner mapping
  actually is) and `enrichDevices`, which carried the judgment that categories `kg` and `cz*`
  are the multi-gang ones — an opinion about Tuya's catalogue, not something its API states.
  What the root offers instead is the primitive: `DeviceChannelNames` wraps
  `GET /v1.0/devices/{id}/multiple-names` and nothing more; `appaccount.Service` fans it out
  across the devices it judges worth labelling. That judgement is exported as
  `appaccount.IsMultiGang` — the door already acts on it, so the opinion is public either way,
  and a caller who wants the primitive should be able to ask the same question first.

  The **field** followed the behavior, one release late. The old `Device` type carried a
  `CodeNameMapping` slot that no Tuya response ever filled — only `resolveChannelNames` wrote to
  it, from a different endpoint. A struct field that exists so the layer above has somewhere to
  put its results is the same violation as a method, just quieter. `tuya.UserDevice` now holds
  only fields `GET /v1.0/users/{uid}/devices` actually sends, and `appaccount.Device` embeds it
  and adds `Channels`.

  The exceptions are `Do`, the token lifecycle and the retry on code `1010` — protocol
  correctness, not domain composition.

  The cost is real and worth stating: a consumer binding the client on its own gets no
  batteries. Wanting channel labels means writing the fan-out. That is the trade for a layer
  you can audit against the vendor's API docs line by line.

- **One result type per endpoint, named after its method. There is no `tuya.Device`.**
  Tuya's device APIs fall into two families that disagree about their own field names, and the
  disagreement is not just cosmetic. Verified on a live Singapore project, same devices, same
  day:

  | | *thing* family | *app* family |
  |---|---|---|
  | endpoints | device detail, devices in space, devices in project | user device list, devices in home |
  | casing | detail snake, the listings **camelCase** | snake |
  | `name` | the **factory** name | the **user's rename** |
  | rename lives in | `custom_name` / `customName` | nowhere — it *is* `name` |
  | online flag | `is_online` / `isOnline` | `online` |
  | extras | `bind_space_id` | `uid`, `owner_id`, `biz_type`, `node_id`, `status` |

  One device makes it concrete. `ebb5cf…czab` is `{name: "Smart plug", custom_name: "Lampu Tidur
  Mama dan Acy"}` in device detail, and `{name: "Lampu Tidur Mama dan Acy"}` in the user list.
  The same key, two meanings, and **both decode without error** — a consumer that switches
  listings silently starts showing factory model numbers to end users.

  So the type carries the endpoint in its name: `UserDevices` returns `[]UserDevice`,
  `SpaceDevices` returns `[]SpaceDevice`. A generic `Device` would be an invitation to assume the
  two are interchangeable. Worth stating in advance, because it will be tempting the day
  `Query Device Details` gets wrapped: on the wire its fields are *identical* to the
  space listing's and differ only in casing, and it still gets **its own struct**. No shared
  type, and no `UnmarshalJSON` that accepts both casings — that would hide which endpoint
  answered, and would go quiet on the day Tuya fixes one of them.

- **The device types are a subset of the wire, and the line is identity over presentation.**
  Both listings send around twenty fields. The types keep seven and eight of them: the device's
  own identifiers, the two flags and the `status` that would otherwise cost one request per
  device to recover, and the `category` that `IsMultiGang` reads. `product_id` stays because it
  is an identifier and because `SpaceDevices` takes `product_ids` as a filter — a field this
  package asks for on the way in has to be available on the way out.

  Dropped: `icon`, `model`, `product_name`, `lat`, `lon`, `ip`, `time_zone`, `uuid`, `node_id`,
  `biz_type`, and the activate/create/update timestamps. Those are labels and presentation, and
  this library maps identity. Also dropped are `uid`, which the caller passed in to get the list,
  and `owner_id`, which is a **space ID** despite the name — the spatial door already answers
  that question.

  **`local_key` is dropped because it is a secret.** It is the device's LAN encryption key, this
  package has no LAN feature that needs it, and `appaccount.Device` carries JSON tags — so a
  consumer who returns it straight from an HTTP handler publishes every device's key. Note that
  trimming buys almost no memory: measured on a real device, the whole struct plus its strings is
  ~960 bytes, of which `status` alone is 423 and everything dropped here is about 370. Memory was
  never the argument.

  A consumer that genuinely needs the dropped fields is not stuck: `tuya.Client.Do` is a public
  escape hatch, and decoding this endpoint into their own struct is a dozen lines.

  Why Tuya does this is a guess, and it stays a guess: two services generated from one model
  where only one set a snake_case naming strategy. What is not a guess is that both shapes are
  public contract now, so neither can be assumed to converge.

- **`tuya.New` prefetches a token with `context.Background()`.**
  Construction-time prefetch turns a bad credential or unreachable region into a wiring-time
  error. A caller that needs to bound it configures timeouts on the `*http.Client` via
  `WithHTTPClient` — the right lever for transport-level control.

- **The Firestore store writes timestamps client-side, not with `ServerTimestamp` sentinels.**
  `Link` returns the exact `appaccount.Account` it just stored. A `ServerTimestamp` sentinel's
  value is unknown until *after* the commit resolves, which would force a second read to learn
  what was written. Stamping `time.Now().UTC()` inside the transaction lets `Link` return the
  stored account from the one round trip it already makes. The trade-off is trusting the client
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
- **Wrap the whole Tuya Cloud OpenAPI.** Two domains are typed so far: device operations and
  Tuya's seven space-management endpoints. `tuya.Client.Do` is the raw escape hatch for
  everything else — deliberately unguarded.
- **List the spaces of an app account, or the users of a space.** Tuya keeps an app account's own
  places in a different family from your project's space tree, and gives no endpoint that crosses
  between them — see [Two doors, two Tuya models](#two-doors-two-tuya-models). The only honest
  version would be a brute-force scan billed to you quietly, so there is none.
- **Cache ownership answers.** Every `HasDevice` / `ContainsDevice` asks Tuya afresh. Not
  because the library assumes you ask rarely, but because invalidating that cache needs events
  (device added, removed, re-linked) that Tuya never tells us about. Guessing a TTL on your
  behalf would trade a stated cost for an unstated staleness window. The costs are written down
  in [Design rationale](#design-rationale) so you can cache at a layer that knows better.
- **Enforce ownership.** The doors answer the question; acting on the answer is yours. This is
  deliberate — see the rationale above — and it is what keeps consumers with device sharing,
  delegated access or their own permission model from being locked out of the doors.
- **Decide who may hold a `*tuya.Client`.** That layer has no notion of an owner by design.

## Testing

```sh
go test ./...
```

The doors — where the owner mapping lives, and where a wrong answer misleads whatever
authorization you build on it — are unit-tested against fake `Client` and `Store`
implementations: happy paths, `appaccount.ErrNotLinked` / `spatial.ErrNotLinked` /
`spatial.ErrNotOwned`, and the distinction the ownership answers depend on — an absent device is
a plain `false` while a failed lookup is an error, never the two collapsed together. `appaccount`
also pins the cost rule (an ownership answer must never trigger channel-name requests) and covers
**95.7%** of its statements. `spatial` covers **86.0%**, including a zero space ID resolving to
the owner's own space without asking Tuya, the owner's own space being undeletable through the
door, a space the project cannot see being reported as unowned rather than surfacing a raw API
error, and the paginated device scan terminating on a stalled cursor *and* on a cursor that keeps
advancing forever.

The root package is tested against an `httptest` server (**64.3%** of its statements) — real HTTP
round trips over a real socket, not mocks. Two behaviours are worth pinning because they are
invisible from the outside. The token retry: the stub answers code `1010` and the test asserts
the retry carried a *different* access token, since a retry that silently replays the rejected
token looks identical to one that works until a credential is rotated in production. And the
space layer's wire contract, each case taken from a live response: the field names Tuya really
sends, a last page arriving with no cursor at all, an ID surviving beyond 2^53, `result:false`
becoming an error, only the parameter spellings Tuya binds going out — and the query string
staying ASCII-sorted, which nothing but a `1004` would otherwise tell you about.

The stores are integration-shaped by nature. `postgres` reaches **82.5%** against a fake
`Querier` that replays scripted rows, which is enough to pin the SQL each store sends and that
each one migrates only its own door. `firestore` sits at **39.6%**: faking a Firestore
transaction would exercise the fake, not the behaviour, so only the pure part is unit-tested —
`validateOwner`, table-tested at **100%** of that function. Signing against Tuya's live service
and running both stores against a real database or the Firestore emulator is on the
[roadmap](#status--roadmap).

## Compatibility

- **Go 1.25+**, per `go.mod`. The concurrent fan-out in `resolveChannelNames` uses
  `sync.WaitGroup.Go`, added in Go 1.25.
- **Breaking in v0.8.0: the layers swapped places.** The `cloud` subpackage is now the root
  `tuya` package, and each owner-scoped door moved into its own subpackage. Mechanically:
  `cloud.X` → `tuya.X`; `tuya.NewAppAccountClient` → `appaccount.NewService`;
  `tuya.NewSpaceClient` → `spatial.NewService`. Inside a door, names no longer repeat it —
  `tuya.AppAccount` → `appaccount.Account`, `tuya.ErrAccountNotLinked` →
  `appaccount.ErrNotLinked`, `tuya.ErrSpaceNotLinked` / `ErrSpaceNotOwned` →
  `spatial.ErrNotLinked` / `spatial.ErrNotOwned`, and the facade interfaces
  `tuya.AppAccountIoT` / `tuya.SpaceIoT` are now `appaccount.Client` / `spatial.Client`. The
  store adapters keep their names.
- **Breaking in v0.8.0: `cloud.IoT` is gone.** Every endpoint method now sits on `*tuya.Client`,
  so `cloud.NewIoT(transport)` drops out of your wiring and `iot.SendCommands(...)` becomes
  `c.SendCommands(...)`. See the rationale above for why the middle layer had no honest name.
- **Breaking in v0.8.0: each door migrates only its own schema.** PostgreSQL migrations moved
  into `migrations/appaccount/` and `migrations/spatial/`, and the version recorded in
  `tuya_schema_migrations` is now the door-qualified path (`appaccount/000001_init.up.sql`). An
  existing database recorded the old flat versions, so the new ones read as unapplied and run
  again — each is a `CREATE TABLE IF NOT EXISTS`, a no-op on a table that is already there. The
  new version rows land beside the old ones, which are then dead but harmless.
- **New in v0.8.0: `spatial.SpaceDevices`.** The door could already list a subtree's device *ids*
  through `SpaceResources`; this lists one space's devices with names, category and online state.
  It also adds a method to the `spatial.Client` interface, which is breaking only if you
  implemented that interface yourself — a fake in your tests, most likely.
- **Fixed in v0.8.0: `SpaceDevices` with `pageSize` 0.** The parameter used to be dropped from
  the query, and `thing/space/device` rejects a request without `page_size` outright with
  `1110 illegal param` — so that argument named a value that could never work. It now stands
  for `SpaceDevicePageSizeMax`, the 20 the endpoint caps at. Callers already passing a size are
  unaffected.
- **Breaking in v0.7.0: identifiers are plain `string` and `int64`.** `SpaceID`, `Scope` and its
  `DirectChildren` / `Subtree` constants are gone; listings take `only_sub` as a `bool`
  (`DirectChildren` → `true`, `Subtree` → `false`). Stored data and DB columns are untouched.
  See [Concepts](#identifiers-are-plain-types).
- **Breaking in v0.7.0, alongside the spatial door.** `postgres.Option` and `firestore.Option`
  are now `func(*options)` rather than functions over one store type, so `WithAutoMigrate` and
  `WithCollection` serve both stores; call sites that just pass `postgres.WithAutoMigrate()` or
  `firestore.WithCollection("…")` are unaffected. `firestore.DefaultCollection` is now
  `firestore.DefaultAppAccountCollection`, beside the new `DefaultSpaceCollection`.
- **The dependency cost is opt-in.** The root `tuya` package and both doors import **only the
  standard library** — bind those, bring your own stores, and you add nothing to your module
  graph. The external dependencies (`jackc/pgx` for `postgres`; `cloud.google.com/go/firestore`
  and gRPC for `firestore`) are compiled only if you import that store subpackage.

## Layout

The root is split by Tuya domain, one file per family of endpoints. Each door is split **per
door**, not per kind of declaration: what differs between the two is how each maps an owner onto
Tuya's handles, so that should be readable in one file rather than assembled from a types file
and a behavior file. The store adapters follow the same rule, which is why they are
`app_account.go` and not `store.go`.

```
client.go        tuya.Client: token cache/refresh, Do + retry on 1010, and the business-request
                 signing that reads the cached token, beside the mutex guarding it.
                 No endpoints of its own.
auth.go          the HMAC-SHA256 signer, the auth headers, and the token lifecycle. A token
                 request is signed without a token; every other request carries one.
device.go        UserDevice/SpaceDevice/DataPoint/Channel, one type per endpoint + its method.
space.go         Space/Resource/Page + one method per space endpoint.
appaccount/
  service.go     The app-account door, end to end: Account, Device, ErrNotLinked, Store,
                 Client, Service — ListDevices, HasDevice, and the channel-name fan-out
                 with the multi-gang judgement it needs.
spatial/
  service.go     The spatial door, end to end: Space, ErrNotLinked, ErrNotOwned,
                 ErrOwnerSpaceProtected, Store, Client, Service — the space operations
                 with their containment check, plus ContainsSpace and the bounded
                 subtree scan of ContainsDevice.
postgres/
  app_account.go AppAccountStore on PostgreSQL + the migration runner both stores share.
  spatial.go     SpaceStore on PostgreSQL.
  migrations/    embedded .up.sql / .down.sql, one directory per door.
firestore/
  app_account.go AppAccountStore on Cloud Firestore (schemaless, no migrations).
  spatial.go     SpaceStore on Cloud Firestore.
```

## Status & roadmap

The public API above is stable and in use — the client, the owner-scoped doors, and the
stores. Remaining work is additive:

- [x] MIT `LICENSE`.
- [x] Unit tests on both owner mappings and Firestore owner validation.
- [x] `spatial.Service` + its store — the door for Tuya's spatial model, alongside the
      app-account one, over the root `space.go`.
- [x] Settle Tuya's self-contradicting reference against the live API: parameter spelling,
      response casing, space-ID JSON type, end-of-listing signal, and whether `/space/relation`
      is transitive. It is — see [Design rationale](#design-rationale).
- [x] Drop the named identifier types again, back to plain `string` and `int64`: the compiler
      check they promised never covered literals, and a wrapper should not invent a parallel
      vocabulary for values it only passes through.
- [x] Replace the mandatory ownership guards with reportable answers (`HasDevice`,
      `ContainsSpace`, `ContainsDevice`), so consumers with device sharing or their own
      permission model are not locked out. See [Design rationale](#design-rationale).
- [x] Put the Tuya API at the root and each door in its own subpackage, so importing the module
      hands you Tuya and the owner mapping is opt-in.
- [ ] Integration tests for the root package / `postgres` / `firestore` behind a build tag and
      live infra.
- [ ] Further Tuya domains beyond device and space control (`home.go`), added as new files with
      their methods on `tuya.Client`.

## License

[MIT](LICENSE) © 2026 Ardian
