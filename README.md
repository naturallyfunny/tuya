# tuya

[![Go Reference](https://pkg.go.dev/badge/go.naturallyfunny.dev/tuya.svg)](https://pkg.go.dev/go.naturallyfunny.dev/tuya)
![Go 1.25+](https://img.shields.io/badge/go-1.25%2B-00ADD8)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

A small, interface-first Go library for the [Tuya Cloud OpenAPI](https://developer.tuya.com/en/docs/cloud/).
The root package signs and authenticates requests, manages the app-level access token, and
exposes typed device and space operations. On top of it, an optional subpackage does the job Tuya
leaves to you: **mapping your own identity system onto Tuya's**. There is one per integration
shape, and today that is `appaccount` — your users each link a Tuya app account — meant to be the
whole Tuya surface for the shape it serves.

It is **agnostic about who calls it**. An HTTP backend, a batch worker, a CLI, an agent
toolset — the library neither knows nor cares. It **answers questions about ownership; it does
not enforce them**, because "this device is not on the owner's account" is not always a reason
to refuse: a product with device sharing legitimately reaches across accounts, and a library
that hard-refused those calls would block the correct integration to protect the careless one.
Where an answer is expensive, [Design rationale](#design-rationale) states the cost instead of
assuming you won't ask often.

```go
devices, err := app.Devices(ctx, owner)        // the root call, by your own owner ID
ok, err := app.HasDevice(ctx, owner, id)       // a fact, for you to act on
err = app.SendCommands(ctx, id, []tuya.DataPoint{
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
2. **Owning a device** — mapping *your* opaque owner ID to a Tuya handle, so the calls above can
   be made in your own vocabulary. This is one subpackage per integration shape; the one that
   exists today is `appaccount`, and it is free to use as much of the root package as its shape
   needs.

The payoff: the mapping lives in one place, and so does the only question it makes answerable —
*is this Tuya handle under the identity linked to this owner?* Nothing else in your stack holds
both halves, so nothing else can answer it. What the answer **means** is yours: refuse, allow,
or check it against your own sharing rules first.

That is also why the layers sit this way round in the module. Importing a module named `tuya`
should hand you Tuya; the owner mapping is something built *on* the API, not the API itself,
and a consumer that already has its own mapping should not have to import it to make a device
call. So the client is the root package and the door is an opt-in import.

### What a door is

A door is not a slice of Tuya's API surface; it is one way your product relates to Tuya, served
end to end. `appaccount` serves the shape where your user holds a Tuya app account: they sign in
to Tuya Smart, Smart Life or your OEM app, connect it to your product, and your product then
reaches what that account holds. The link is the whole relationship, and the door anchors an
owner to the **Tuya UID** that account is keyed by.

It is not "the package with the user endpoints". A door carries whatever its shape needs from the
root — commands and channel names included — and its only limits are that it invents no
capability the root lacks, takes no `owner` it does not use, and decides nothing about what an
ownership answer means.

There is no `appaccount.ChildSpaces`, and there is no way to reach a space through this door.
That is not an omission waiting to be filled: an app account's own places live in the older
*asset* family (`/v1.0/iot-03/users/{uid}/assets`), and the ids it returns are rejected by every
`/v2.0/cloud/space` endpoint this library speaks — `40001900 No space permission`
(`tuya.CodeNoSpacePermission`), the same answer you get for a space in someone else's project.
Nothing relates the two trees, and no endpoint maps a space back to a user, so building the
bridge would mean inventing a mapping Tuya does not have. The root package speaks both families;
mixing their ids buys you a runtime error, not a compile-time one.

## Concepts

Two composable tiers. Bind the one your caller needs:

| You have / need                                          | Use                    | Resolves an owner? |
| -------------------------------------------------------- | ---------------------- | ------------------ |
| A Tuya UID, device ID or space ID; or raw `Do`            | `tuya.Client`          | no                 |
| Your own owner ID, one Tuya app account per human        | `appaccount.Service`   | **yes**            |

- **`tuya.Client`** — the client. Speaks Tuya at the **project level**: one access ID/secret
  yields an access token it caches in memory and refreshes on its own, when Tuya rejects the
  cached one with code `1010`. Handles HMAC-SHA256 signing. Most other methods are exactly
  one Tuya endpoint: `UserDevices`, `SpaceDevices`, `DeviceStatus`, `SendCommands`,
  `DeviceChannelNames` for devices, and `CreateSpace`, `Space`, `ModifySpace`, `DeleteSpace`,
  `SpaceResources`, `ListSpaces`, `SpaceRelation` for spaces. Four things compose more than one
  request — `ChannelNames`, the `WithChannelNames()` option both listings take, `UserHasDevice`
  and `SpaceHasDevice` — and each is shaped so the cost is visible where you type it rather than
  wired in once at construction. Everything else is one call, one request, including pagination,
  which is handed to you a page at a time rather than looped over silently. `Do` is a raw escape
  hatch for endpoints not yet wrapped. No notion of an owner at all; a holder can reach any
  device or space the project can. Device commands live here too — they are addressed by device
  ID, which is already a Tuya handle, so there is nothing for a door to resolve.
- **`appaccount.Service`** — resolves owner → Tuya UID through an `appaccount.Store`, and then
  makes the root call. `Devices` is `tuya.Client.UserDevices` and `HasDevice` is `UserHasDevice`,
  each taking your owner where the root takes a UID, with options and result types passed
  straight through. The `User` prefix drops because it is what separates the root's two trees and
  this package *is* one of them. `Get`, `Link` and `Unlink` are the mapping itself, forwarded to
  the `Store` so the connect flow and the device flows hold one handle. The device-addressed calls —
  `DeviceStatus`, `SendCommands`, `DeviceChannelNames`, `ChannelNames` — are on the door too,
  forwarded verbatim: no owner argument, no ownership check, nothing added. They are there
  because they are part of this integration, not because they need resolving. The package owns
  `Account`, `ErrNotLinked`, and the `Store` / `Client` interfaces it drives.

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
  `app := appaccount.NewService(...)` then `app.Devices(ctx, owner)`.

### Identifiers are plain types

Owner, Tuya UID, and device ID are `string`; space ID is `int64`. There are no named wrappers
around them, and that is deliberate: a door is a wrapper, and a wrapper has no business
inventing a parallel vocabulary for values it only passes through. `database/sql` takes a
`string` query; `net/http` takes a `string` URL.

Named types were tried here and taken back out before release. What they were supposed to buy
was a compiler check on argument order — but an untyped string constant converts to any
string-based type on its own, so a swapped literal still compiles and `go vet` stays quiet.
The protection only ever covered variables. What was left was a nicer-looking call site, which
is not enough to pay for the vocabulary.

Nothing is converted at the edges, so your own IDs go straight in and database columns are
unchanged.

Ready-made store adapters ship in-tree — pick one, or implement the interface yourself:

- **`postgres.AppAccountStore`** — the owner → UID mapping in PostgreSQL (`pgx`), with an
  embedded migration runner.
- **`firestore.AppAccountStore`** — the same contract on Cloud Firestore: one document per owner,
  no migrations.

Adapters are split **per backend**, not per door, and that is why they are the one place a name
still carries its door: a plain `postgres.Store` would have nowhere to go the moment a second
door arrives, and the alternative — `appaccount/postgres` beside a sibling — is two packages with
the same name that force an alias at every import, with `Querier`, `Option` and the migration
runner homeless between them.

Dependency direction is acyclic and points inward — adapters depend on the door, the door depends
on the root, and the root depends on nothing of ours:

```
postgres ──┬─▶ appaccount ──▶ tuya
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

// 2. The Tuya client. baseURL selects the region.
c, err := tuya.New(accessID, accessSecret, "https://openapi.tuyaus.com")
if err != nil {
    log.Fatal(err)
}

// 3. The owner-scoped door, for the app-account integration shape.
app := appaccount.NewService(c, store) // postgres.AppAccountStore satisfies appaccount.Store
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
devices, err := app.Devices(ctx, owner)             // []tuya.UserDevice, same as the root call
if errors.Is(err, appaccount.ErrNotLinked) {
    // route the human into the account-linking flow
}

// The options are the root's too, and they mean the same thing here.
devices, err = app.Devices(ctx, owner, tuya.WithChannelNames())

acc, err := app.Get(ctx, owner)                     // the linked owner ↔ UID mapping

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
    err = app.SendCommands(ctx, id, []tuya.DataPoint{{Code: "switch_1", Value: true}})
default:
    return ErrForbidden                         // your rule, your error
}
```

The command itself takes no owner. A device ID is already a Tuya handle, so there is nothing
about it for a door to resolve — `app.SendCommands` and `c.SendCommands` are the same call, and
the door carries it because commands are part of this integration, not because it adds anything.
Nothing on either path checks ownership; the `switch` above is where that decision lives.

If you hold the raw client instead, they read:

```go
acc, _ := store.Get(ctx, owner)

// One Tuya request each.
devices, err := c.UserDevices(ctx, acc.TuyaUID)          // UID-addressed
status, err := c.DeviceStatus(ctx, deviceID)             // device-addressed
channels, err := c.DeviceChannelNames(ctx, deviceID)     // one device's channel labels

// Space-addressed: up to 5 space IDs, optional product/category filters, page by last device ID.
devices, err := c.SpaceDevices(ctx, []int64{spaceID}, 20, true, nil, nil, "")

// 1 + N: the listing, then one request per device in it that could have numbered channels.
devices, err := c.UserDevices(ctx, acc.TuyaUID, tuya.WithChannelNames())

// Membership, asked of either tree. Flat on a UID; a bounded subtree walk on a space.
ok, err := c.UserHasDevice(ctx, acc.TuyaUID, deviceID)
ok, err = c.SpaceHasDevice(ctx, spaceID, deviceID)
```

The `recursive` argument is Tuya's `is_recursion`: `true` recurses into subspaces, `false` (or
absent) queries only the space itself. The wrapper always sends it explicitly rather than relying
on the absent-means-false default. `SpaceResources` is transitive as well — confirmed against the
live API — and that is the one `SpaceHasDevice` scans, because it pages 200 resources at a time
where this endpoint caps at 20 devices.

`DeviceChannelNames` is worth a request only for a device that has several channels.
`WithChannelNames()` is that judgement applied to a whole listing: it asks `/multiple-names` for
every device whose category can carry numbered channels and fills `Channels` on the ones that
answer. `ChannelNames` is the same fan-out over a `[]tuya.Device` you already hold, for when the
devices did not come from a listing. Thirteen categories qualify, and the list is the library's own
reading of Tuya's catalogue rather than something the API states — see the
[rationale](#design-rationale) before you lean on it.

The category codes are exported as **134 constants**, `tuya.DeviceCategorySwitch` through
`tuya.DeviceCategoryWirelessSwitch`, so the `categories` filter of `SpaceDevices` and any branch
you write on `device.Category` can name one instead of spelling `"kg"`.

> `tuya.Client` knows nothing about owners. A holder can reach every device in the project.
> That is the point — the door tells you whose a handle is, and you decide what follows.

### Spaces

The space endpoints sit on `tuya.Client` and take a space ID, with no notion of an owner:

```go
// lastRowKey 0 starts the walk and pageSize 0 takes Tuya's own default. What comes back
// is the cursor for the next call; a zero means that was the end, not "start over".
// onlySub: true lists the direct children, false the whole subtree.
children, next, err := c.ListSpaces(ctx, spaceID, true, 0, 0)
if next != 0 {
    children, next, err = c.ListSpaces(ctx, spaceID, true, next, 0)
}

room, err := c.CreateSpace(ctx, "Room 201", spaceID, "twin")
things, next, err := c.SpaceResources(ctx, room, false, 0, 0)
contains, err := c.SpaceRelation(ctx, spaceID, room)
```

`SpaceResources` is transitive and returns identifiers only; `SpaceDevices` returns full devices
but never descends. That difference is Tuya's, not ours, and `SpaceHasDevice` is built on the
first of the two — read its cost in the [rationale](#design-rationale) before you build on it.

Listings take `only_sub` as a positional argument rather than an option, because Tuya's own
default for that parameter is undocumented, and a listing that quietly covers the wrong depth is
exactly what you must not build an ownership check on. It is always sent explicitly. Each call
returns **one** page plus the cursor for the next; see [rationale](#design-rationale) for why the
loop is yours.

Deleting a space deletes everything below it. Tuya offers no shallow form, so `DeleteSpace` is
one call with a subtree-sized consequence.

## Linking accounts

`Link` and `Unlink` sit on the door, so the flow that connects a Tuya account and the flow that
lists its devices hold the same handle:

```go
acc, err := app.Link(ctx, owner, uid)   // upsert
err = app.Unlink(ctx, owner)            // soft-delete
```

Both are the store's own operations, forwarded. `Link` is an upsert (re-linking refreshes the
UID; re-linking a previously unlinked owner revives the row rather than colliding on the key);
`Unlink` is a soft-delete (`deleted_at`), so `Get` stops returning it while the record is
preserved for audit. The PostgreSQL store backs this with a `tuya_app_accounts` table (`owner`
PK, `tuya_uid`, timestamps, `deleted_at`); Firestore with a `tuya_app_accounts` collection
(override via `firestore.WithCollection`), one document per owner keyed by the owner string.

### Migrations (PostgreSQL)

A small custom runner, not `golang-migrate`. SQL files are embedded (`//go:embed`); versions
are tracked in `tuya_schema_migrations`. `WithAutoMigrate()` applies pending migrations on
startup. Without it, `NewAppAccountStore` validates the schema exists and fails fast if the
consumer forgot to migrate.

Each door owns its own migration directory — `migrations/appaccount/` — and each store runs only
its own. Sequence numbers restart per door, and the version recorded in `tuya_schema_migrations`
is the door-qualified path (`appaccount/000001_init.up.sql`), so a consumer never gets another
door's tables created behind its back.

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

- **The door reports ownership; it does not enforce it.**
  Earlier versions guarded: `DeviceStatus` and `SendCommands` sat on the door and refused any
  device that failed an ownership check. That is gone, for three reasons. A mandatory guard
  **blocks correct integrations** — device sharing across accounts is an ordinary product
  feature, and a shared device is by definition absent from the owner's listing, so that
  consumer was forced down to the raw client and lost the owner resolution that was the only
  thing it wanted here. A device ID arriving from somewhere untrustworthy is **the consumer's
  design decision**, not something a mapping library should police. And once the check is gone,
  `owner` on a device-addressed method resolves nothing at all — so the methods went with it
  rather than keeping a parameter that is read and discarded.

  What replaces them is a question: `HasDevice`, returning a `bool`. If you want a guarded
  wrapper, it is three lines in your own code, written once, with your rules in the middle — and
  nobody else's product is bent around them.

  The methods themselves came back later, without the guard and without the `owner`: the door
  forwards `DeviceStatus`, `SendCommands`, `DeviceChannelNames` and `ChannelNames` verbatim. What
  had been wrong was the check, not the location — see the next bullet.

- **A door is a whole integration, not a thin mapping.**
  A subpackage serves one way a product relates to Tuya, and it serves it completely: whatever a
  consumer of that shape needs from the root package, the door carries. That is why it sends
  commands and reads channel names even though it resolves nothing to do so — an app is no less
  an app-account consumer for wanting to turn a switch on.

  What a door may *not* do is invent. It cannot offer a capability the root package does not have,
  it cannot take an `owner` it does not use, and it cannot decide what an ownership answer means.
  Inside those three lines it is free. `appaccount.Devices` is `tuya.Client.UserDevices` with the
  owner resolved first; `appaccount.SendCommands` is `tuya.Client.SendCommands` unchanged; `Get`
  is the mapping itself, named after the `Store.Get` it forwards.

  The `User` prefix does not come along, because it has nothing to distinguish here. At the root
  it separates the two trees — a UID's devices from a space's. Inside a package called
  `appaccount` there is only one tree, and `appaccount.UserDevices` would repeat the package name
  the way `appaccount.AppAccount` would.

  The door used to invent, and what it invented is what the rule is written against.
  `ListDevices` was `UserDevices` welded to a channel-name fan-out: a caller who wanted the list
  without paying 1 + N could not say so, and a caller on the raw client had to write the fan-out
  again. Both disappeared when the fan-out moved to the root behind `WithChannelNames()` — the
  door forwards the option and the choice sits at the call site. `appaccount.Device` was
  `tuya.UserDevice` plus a `Channels` field, which stopped meaning anything the day `tuya.Device`
  grew its own: two fields of one name at two depths, the outer silently shadowing the inner.
  Deleting the type deleted the question of which one you were reading.

  Carrying more of the root is not inventing, and a door may rename what it carries where its own
  anchor makes the root's name wrong. What keeps "carries more" from becoming "reaches
  everywhere" is the narrow `Client` interface a door declares: it lists exactly the root methods
  that door forwards, and nothing else is reachable through it.

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
  `tuya.New` returns a concrete `*tuya.Client`. A door declares its own interface —
  `appaccount.Client` — as narrowly as it needs, and `*tuya.Client` satisfies it structurally.
  It is not a general facade: it is six methods wide and lists only what the door calls. Another
  door declares its own, overlapping or not. This is "accept interfaces, return structs"
  applied literally — mocking is the consumer's concern, so `appaccount/service_test.go` fakes
  `appaccount.Client` and `appaccount.Store` with zero test-only code in the root package.
  Exporting a speculative interface from the root would only add a compatibility burden that
  widens with every new domain.

- **`ChannelNames` uses `sync.WaitGroup.Go` + `errors.Join`, not `errgroup`.**
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
  `Do` — today the signer only takes the read half of that `sync.RWMutex`, so concurrent calls do
  not queue behind each other; a check that may refresh cannot. Defending the trade needs an
  argument about how often callers call, which is the one argument this library does not make.

- **Membership is answered by list-then-contains, and the walk lives at the root.**
  `tuya.Client.UserHasDevice` lists the UID's devices and checks the ID. The cost is **one
  request, flat** no matter how many devices the account has. `appaccount.HasDevice` is that
  method with the owner resolved to a UID first, and nothing else — the door contributes the
  mapping, which is the only part it knows.

  That pairing is why the walk sits beside `UserDevices` rather than in the door. "Is this device
  on this UID" is a question about Tuya, answerable by anyone holding a client; only the
  resolution of *owner* to UID is the door's own. `SpaceHasDevice` is the same question put to
  the other tree, over `SpaceResources`, and it is the expensive one — see the space bullet
  below.

  Nothing is cached: an ownership cache has to be invalidated when a device is added, removed or
  re-linked, and the library has no way to learn about any of those. A consumer that can learn
  about them is in a better position to cache than this library is. Neither method asks for
  channel labels either — they need identity, not names, and must not pay one request per
  multi-channel device. Tests at both layers assert that.

  A device that is absent is a plain `false`; only a failed lookup is an error. "Not found" and
  "could not look" must not arrive as the same answer to code that branches on it.

- **Tuya prices the two space questions very differently.**
  Asking about a *space* costs one request: `GET /v2.0/cloud/space/relation`, wrapped as
  `SpaceRelation`, answers whether one space sits inside another, and containment is transitive —
  confirmed against the live API, where a space answers `true` for its grandchild. One boolean
  therefore settles a whole subtree. Two edges of that endpoint are worth knowing before you
  build on it: a space compared against *itself* answers `false`, and a space the project cannot
  see at all is refused with `tuya.CodeNoSpacePermission` rather than answered `false`.

  A *device* gets no such shortcut. Tuya has no "which space holds this device" lookup at all:
  `GET /v2.0/cloud/thing/{device_id}` returns product, status and location and no space or asset
  ID. The only route is to enumerate the subtree's resources and look for the ID, paginated, and
  `SpaceHasDevice` does exactly that — so **know the cost before you build on it**: it stops at
  the first match, so a lucky call is one request, but the page holding your device is not
  guaranteed to be the first, and the ceiling is 50 pages of 200 resources. Unlike
  `UserHasDevice`, this **grows with the size of the estate**.

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

- **Paging is two plain arguments and one plain return, with no type in between.**
  Listings used to take `...tuya.PageOption` — `WithPageSize`, `WithLastRowKey`, over a struct
  of two pointer fields — and then a `tuya.Page` struct carrying both ways. Both are gone.
  `last_row_key` and `page_size` are what Tuya's own parameter table calls them, so
  `SpaceResources` and `ListSpaces` take them as `int64` and `int` in that order and hand back
  the next cursor as an `int64`. A zero going in means "not sent"; a zero coming back means the
  walk is over, not that it should start again. The struct only ever grouped two numbers that
  the endpoint documents separately, and the pointers it used to hold existed to tell "unset"
  from zero — a distinction this domain does not have, since `page_size=0` is not a request
  anyone means and `last_row_key=0` is precisely how Tuya says there is no next page.

  `SpaceDevices` pages differently, and the reason is Tuya's rather than ours. Tested against
  the Singapore data center on 15 August 2026: the `thing/space/device` response envelope holds
  only `success`, `t`, `tid` and `result`, and `result` is a bare array. **There is no cursor to
  hand back** — returning one there would mean inventing it. Its cursor is `last_id`, the
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

- **No listing pages itself, and the two loops the library does own are bounded.**
  Tuya documents how to fetch the next page but never how to know there isn't one, and a loop
  written from a guess is an infinite loop that burns quota. Live, the last page arrives as an
  empty `data` with the cursor field absent altogether. Every listing hands you that page and the
  cursor and lets you compose. The exception is `SpaceHasDevice`, which has no choice but to
  walk: it stops on the documented signal *and* on a cursor that stopped moving, with a cap on
  pages read. The extra stops cost one comparison and turn any future surprise into a refusal
  rather than a hang.

- **`ListSpaces` takes a space ID of zero to mean the whole project.**
  `GET /v2.0/cloud/space/child` without a `space_id` returns the top-level spaces of the entire
  cloud project. That is one endpoint with an optional parameter, so it is one method — Tuya
  defines the zero case itself, and splitting it in two produced a name (`RootSpaces`) that no
  endpoint had.

  It is legitimate for an operator and catastrophic on an owner-scoped path. Any door built over
  these endpoints owes its callers a guarantee that a zero never reaches this method by accident,
  and that guarantee belongs in code that is asserted by a test rather than assumed.

- **A root method is one Tuya endpoint by default; anything more has to show its cost at the call
  site.**
  The older rule was absolute — one method, one endpoint, composition belongs to whoever wants
  it — and it was dropped, because `ChannelNames`, `UserHasDevice`, `SpaceHasDevice` and
  `WithChannelNames()` are all useful and not one of them could be born under it. What the rule
  was protecting against is not composition but *hidden* composition, so that is what the
  replacement forbids. A fan-out is fine; a fan-out you cannot see from the line that pays for it
  is not.

  `WithChannelNames()` is the shape that follows. It is a per-call `DeviceOption`, and the
  alternative it beat was an option on `New` — which would have turned every `UserDevices` in the
  program into 1 + N forever, decided once in the wiring, invisible at each of the call sites
  billed for it. `Channels` stays empty unless a call asks, and empty even then for a device with
  one channel.

  Which categories can carry numbered channels is a fact about **Tuya**, not about a door, so the
  predicate and the fan-out it feeds sit beside the endpoint they call rather than in
  `appaccount`. That relocation is also why there is no `appaccount.IsMultiGang` any more: the
  door no longer holds the opinion, and the two entry points that act on it — `WithChannelNames()`
  and `ChannelNames` — take devices rather than a category, so there is nothing left for a caller
  to ask in advance.

  The list itself is worth reading before you rely on it. It grew from `kg` plus the `cz*` prefix
  to thirteen categories in four channel-numbering shapes: `switch_N` (`kg`, `cz`, `pc`, `tdq`,
  `ggq`, `cjkg`, `ckmkzq`, `wkcz`), `switch_led_N` (`tgkg`, `tgq`), `control_N` (`cl`, `clkg`) and
  `switchN_value` (`wxkg`). Only `kg`, `cz` and `pc` are documented as answering
  `/multiple-names`; the other ten are in because their channels demonstrably exist, which is not
  free — a consumer with curtains, dimmers or wireless buttons now pays one request per such
  device whenever it asks for names, and may get an empty list back. `cz` also stopped matching by
  prefix; no other category begins with those two letters, so nothing changes today, but the loose
  form is gone.

  The **field** followed the behavior. The `Device` type once carried a `CodeNameMapping` slot
  that no Tuya response ever filled — a struct field existing so the layer above had somewhere to
  put its results. `Channels` is not that: the fan-out that fills it now lives in the same package
  as the field, and it is written only when the caller asked for it.

  `Do`, the token lifecycle and the retry on code `1010` sit outside the rule entirely — protocol
  correctness, not domain composition.

- **One result type per endpoint, named after its method. `tuya.Device` holds only what both
  families spell the same way.**
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
  `SpaceDevices` returns `[]SpaceDevice`. A type covering both would be an invitation to assume
  the two are interchangeable. Worth stating in advance, because it will be tempting the day
  `Query Device Details` gets wrapped: on the wire its fields are *identical* to the
  space listing's and differ only in casing, and it still gets **its own struct**. No merged
  type, and no `UnmarshalJSON` that accepts both casings — that would hide which endpoint
  answered, and would go quiet on the day Tuya fixes one of them.

  `tuya.Device` exists, but it is the opposite of that: `ID`, `Category`, `Channels`, embedded in
  both. Only the first two are spelled identically on both wires — `product_id` versus
  `productId` differ in casing, and `name` differs in *meaning*, which is exactly the confusion a
  shared field would bury — and `Channels` is not wire data at all. What it buys is
  `ChannelNames(ctx, []Device)`: one fan-out that answers for either family. Go slices are not
  covariant, so a caller with `[]UserDevice` still builds the `[]Device` itself; that loop is the
  price of the shape, and it is cheaper than a second method per type or a generic function,
  which could not be a method and so could not travel through a door's local `Client`
  interface.

- **The device types are a subset of the wire, and the line is identity over presentation.**
  Both listings send around twenty fields. The types keep seven and eight of them (plus
  `Channels`, which is not on the wire): the device's own identifiers, the two flags and the
  `status` that would otherwise cost one request per device to recover, and the `category` the
  channel-name fan-out reads. `product_id` stays because it is an identifier and because
  `SpaceDevices` takes `product_ids` as a filter — a field this package asks for on the way in has
  to be available on the way out. `bindSpaceId` is a `string`: it is the one space ID in this API
  that does not arrive as a number, and it is decoded as what it is rather than coerced.

  Dropped: `icon`, `model`, `product_name`, `lat`, `lon`, `ip`, `time_zone`, `uuid`, `node_id`,
  `biz_type`, and the activate/create/update timestamps. Those are labels and presentation, and
  this library maps identity. Also dropped are `uid`, which the caller passed in to get the list,
  and `owner_id`, which is a **space ID** despite the name and belongs to the space tree rather
  than to the device.

  **`local_key` is dropped because it is a secret.** It is the device's LAN encryption key, this
  package has no LAN feature that needs it, and these types carry JSON tags — so a consumer who
  returns one straight from an HTTP handler publishes every device's key. Note that
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
  between them — see [What a door is](#what-a-door-is). The only honest version would be a
  brute-force scan billed to you quietly, so there is none.
- **Cache ownership answers.** Every `HasDevice` asks Tuya afresh. Not because the library
  assumes you ask rarely, but because invalidating that cache needs events (device added,
  removed, re-linked) that Tuya never tells us about. Guessing a TTL on your behalf would trade a
  stated cost for an unstated staleness window. The costs are written down in
  [Design rationale](#design-rationale) so you can cache at a layer that knows better.
- **Enforce ownership.** The door answers the question; acting on the answer is yours. This is
  deliberate — see the rationale above — and it is what keeps consumers with device sharing,
  delegated access or their own permission model from being locked out of the door.
- **Decide who may hold a `*tuya.Client`.** That layer has no notion of an owner by design.

## Testing

```sh
go test ./...
```

The door — where the owner mapping lives, and where a wrong answer misleads whatever
authorization you build on it — is unit-tested against fake `Client` and `Store`
implementations: happy paths, `appaccount.ErrNotLinked`, and the distinction the ownership answer
depends on — an absent device is a plain `false` while a failed lookup is an error, never the two
collapsed together. It covers **100%** of its statements, and what it pins is the rule the
package is written to: an unlinked or empty UID never reaches Tuya, the caller's options arrive
at the client untouched, and the device-addressed calls pass through with the store deliberately
returning `ErrNotLinked` — a guard smuggled back into that path fails the test.

The root package is tested against an `httptest` server (**76.5%** of its statements) — real HTTP
round trips over a real socket, not mocks. Three groups are worth pinning because they are
invisible from the outside. The token retry: the stub answers code `1010` and the test asserts
the retry carried a *different* access token, since a retry that silently replays the rejected
token looks identical to one that works until a credential is rotated in production. The space
layer's wire contract, each case taken from a live response: the field names Tuya really
sends, a last page arriving with no cursor at all, an ID surviving beyond 2^53, `result:false`
becoming an error, only the parameter spellings Tuya binds going out — and the query string
staying ASCII-sorted, which nothing but a `1004` would otherwise tell you about. And the cost of
the device methods, now that some of them compose: a listing without `WithChannelNames()` makes
exactly one request, the fan-out asks only about categories that can carry channels, one failing
device does not cancel its siblings, and the space scan gives up with an error rather than
paging forever.

The stores are integration-shaped by nature. `postgres` reaches **82.5%** against a fake
`Querier` that replays scripted rows, which is enough to pin the SQL each store sends and that
each one migrates only its own door. `firestore` sits at **39.6%**: faking a Firestore
transaction would exercise the fake, not the behaviour, so only the pure part is unit-tested —
`validateOwner`, table-tested at **100%** of that function. Signing against Tuya's live service
and running both stores against a real database or the Firestore emulator is on the
[roadmap](#status--roadmap).

## Compatibility

- **Go 1.25+**, per `go.mod`. The concurrent fan-out in `ChannelNames` uses
  `sync.WaitGroup.Go`, added in Go 1.25.
- **Breaking in v0.9.0: the `spatial` door and its stores are gone.** `spatial.Service`,
  `postgres.SpaceStore` and `firestore.SpaceStore` were removed pending a redesign; the space
  endpoints they called are unchanged on `tuya.Client`, and `migrations/spatial/` is left in
  place. Pin v0.8.x if you depend on that door.
- **New in v0.9.0: the door carries the device-addressed calls.** `DeviceStatus`, `SendCommands`,
  `DeviceChannelNames` and `ChannelNames` are now on `appaccount.Service`, forwarded to the root
  with no owner argument and no ownership check, so an integration of that shape needs one handle
  instead of two. `c.SendCommands(...)` keeps working unchanged.
- **New in v0.9.0: `appaccount.Service` owns the link lifecycle.** `Link` and `Unlink` forward to
  the `Store`, so the flow that connects a Tuya account holds the same handle as the rest of the
  integration. Calling the store directly still works.
- **Breaking in v0.9.0: `SpaceDevices` argument order follows Tuya's own.** `pageSize` moves up
  beside `spaceIDs` — the two parameters Tuya marks required — and `recursive`, `productIDs`,
  `categories`, `lastID` follow in the order the endpoint documents them. Mechanically,
  `c.SpaceDevices(ctx, ids, true, nil, nil, "", 20)` becomes
  `c.SpaceDevices(ctx, ids, 20, true, nil, nil, "")`. `bool` and `int` do not convert to each
  other, so every stale call site fails to compile rather than silently swapping the two.
- **Breaking in v0.9.0: `tuya.Page` is gone.** `SpaceResources` and `ListSpaces` now take
  `lastRowKey int64, pageSize int` — Tuya's own parameter names, in Tuya's own order — and return
  the next cursor as a plain `int64`. Mechanically, `c.SpaceResources(ctx, id, false, tuya.Page{})`
  becomes `c.SpaceResources(ctx, id, false, 0, 0)`, and `next.LastRowKey != 0` becomes
  `next != 0`.
- **New in v0.9.0: `tuya.CodeNoSpacePermission`.** The `40001900` a space endpoint answers for a
  space the project cannot see, exported so callers can branch on it instead of spelling the
  number.
- **Breaking in v0.9.0: `appaccount` invents nothing of its own.** `ListDevices` → `Devices`,
  which now returns `[]tuya.UserDevice` — the root's own type — and takes the root's
  `...tuya.DeviceOption`; `Account` → `Get`; `HasDevice` keeps its name. The `appaccount.Device`
  wrapper is gone: its `Channels` field duplicated `tuya.Device.Channels`, which now holds it.
  Mechanically, `app.ListDevices(ctx, owner)` becomes `app.Devices(ctx, owner,
  tuya.WithChannelNames())` if you want the labels, or without the option if you do not — the
  request the old call had no way to make.
- **Breaking in v0.9.0: `appaccount.IsMultiGang` is gone, and channel names are asked for per
  call.** The judgement it exported moved into the root package, where it is no longer a category
  predicate you can call: ask `UserDevices` / `SpaceDevices` with `tuya.WithChannelNames()`, or
  call `tuya.ChannelNames` with the devices you hold.
- **Breaking in v0.9.0: `appaccount.Client` changed.** `UserDevices` gained a variadic
  `...tuya.DeviceOption`, and the interface gained `DeviceStatus`, `SendCommands`,
  `DeviceChannelNames` and `ChannelNames`. `*tuya.Client` still satisfies it; only your own fakes
  need the extra methods.
- **New in v0.9.0: `tuya.Device`, the category constants, and the two membership questions.**
  `Device` is `ID` + `Category` + `Channels`, embedded in `UserDevice` and `SpaceDevice`, so one
  fan-out serves both. 134 `tuya.DeviceCategory*` constants name Tuya's catalogue.
  `UserHasDevice` and `SpaceHasDevice` answer membership against a UID and against a space —
  the first flat, the second a bounded scan.
- **Breaking in v0.8.0: the layers swapped places.** The `cloud` subpackage is now the root
  `tuya` package, and each owner-scoped door moved into its own subpackage. Mechanically:
  `cloud.X` → `tuya.X`; `tuya.NewAppAccountClient` → `appaccount.NewService`. Inside a door,
  names no longer repeat it — `tuya.AppAccount` → `appaccount.Account`,
  `tuya.ErrAccountNotLinked` → `appaccount.ErrNotLinked`, and the facade interface
  `tuya.AppAccountIoT` is now `appaccount.Client`. The store adapters keep their names.
- **Breaking in v0.8.0: `cloud.IoT` is gone.** Every endpoint method now sits on `*tuya.Client`,
  so `cloud.NewIoT(transport)` drops out of your wiring and `iot.SendCommands(...)` becomes
  `c.SendCommands(...)`. See the rationale above for why the middle layer had no honest name.
- **Breaking in v0.8.0: each door migrates only its own schema.** PostgreSQL migrations moved
  into per-door directories, and the version recorded in `tuya_schema_migrations` is now the
  door-qualified path (`appaccount/000001_init.up.sql`). An existing database recorded the old
  flat versions, so the new ones read as unapplied and run again — each is a
  `CREATE TABLE IF NOT EXISTS`, a no-op on a table that is already there. The new version rows
  land beside the old ones, which are then dead but harmless.
- **Fixed in v0.8.0: `SpaceDevices` with `pageSize` 0.** The parameter used to be dropped from
  the query, and `thing/space/device` rejects a request without `page_size` outright with
  `1110 illegal param` — so that argument named a value that could never work. It now stands
  for `SpaceDevicePageSizeMax`, the 20 the endpoint caps at. Callers already passing a size are
  unaffected.
- **Breaking in v0.7.0: identifiers are plain `string` and `int64`.** `SpaceID`, `Scope` and its
  `DirectChildren` / `Subtree` constants are gone; listings take `only_sub` as a `bool`
  (`DirectChildren` → `true`, `Subtree` → `false`). Stored data and DB columns are untouched.
  See [Concepts](#identifiers-are-plain-types).
- **Breaking in v0.7.0: store options are package-wide.** `postgres.Option` and
  `firestore.Option` are now `func(*options)` rather than functions over one store type, so
  `WithAutoMigrate` and `WithCollection` serve any store in the package; call sites that just
  pass `postgres.WithAutoMigrate()` or `firestore.WithCollection("…")` are unaffected.
  `firestore.DefaultCollection` is now `firestore.DefaultAppAccountCollection`.
- **The dependency cost is opt-in.** The root `tuya` package and the door import **only the
  standard library** — bind those, bring your own stores, and you add nothing to your module
  graph. The external dependencies (`jackc/pgx` for `postgres`; `cloud.google.com/go/firestore`
  and gRPC for `firestore`) are compiled only if you import that store subpackage.

## Layout

The root is split by Tuya domain, one file per family of endpoints. A door is split **per door**,
not per kind of declaration: what makes it a door is how it maps an owner onto Tuya's handles, so
that should be readable in one file rather than assembled from a types file and a behavior file.
The store adapters follow the same rule, which is why they are `app_account.go` and not
`store.go`.

```
client.go        tuya.Client: token cache/refresh, Do + retry on 1010, and the business-request
                 signing that reads the cached token, beside the mutex guarding it.
                 No endpoints of its own.
auth.go          the HMAC-SHA256 signer, the auth headers, and the token lifecycle. A token
                 request is signed without a token; every other request carries one.
device.go        Device/UserDevice/SpaceDevice/DataPoint/Channel and their endpoints, then the
                 membership walks (UserHasDevice, SpaceHasDevice), the category constants, and
                 the channel-name fan-out with the judgement it reads.
space.go         Space/Resource + one method per space endpoint.
appaccount/
  service.go     The app-account door, end to end: Account, ErrNotLinked, Store, Client,
                 Service — owner → UID, then Devices and HasDevice calling the root
                 with it, the link lifecycle, and the device-addressed calls forwarded
                 as they are.
postgres/
  app_account.go AppAccountStore on PostgreSQL + the migration runner.
  migrations/    embedded .up.sql / .down.sql, one directory per door.
firestore/
  app_account.go AppAccountStore on Cloud Firestore (schemaless, no migrations).
```

## Status & roadmap

The public API above is stable and in use — the client, the app-account door, and its stores.
Remaining work:

- [x] MIT `LICENSE`.
- [x] Unit tests on the owner mapping and Firestore owner validation.
- [x] Settle Tuya's self-contradicting reference against the live API: parameter spelling,
      response casing, space-ID JSON type, end-of-listing signal, and whether `/space/relation`
      is transitive. It is — see [Design rationale](#design-rationale).
- [x] Drop the named identifier types again, back to plain `string` and `int64`: the compiler
      check they promised never covered literals, and a wrapper should not invent a parallel
      vocabulary for values it only passes through.
- [x] Replace the mandatory ownership guards with a reportable answer (`HasDevice`), so consumers
      with device sharing or their own permission model are not locked out. See
      [Design rationale](#design-rationale).
- [x] Put the Tuya API at the root and each door in its own subpackage, so importing the module
      hands you Tuya and the owner mapping is opt-in.
- [x] Give the root what is a fact about Tuya rather than about a door: the category constants,
      the shared `Device`, the channel-name fan-out behind a per-call option, and membership
      against a UID and against a space.
- [x] Cut `appaccount` back to what it does not invent: one root call per method, an owner
      instead of a UID, and no type or field of its own beyond the mapping.
- [x] Make the door the whole surface for its integration shape — the device-addressed calls and
      the link lifecycle on it, forwarded without an owner and without a guard.
- [ ] Redesign the door for the other integration shape, where a user holds a space rather than a
      Tuya account. The previous `spatial` package and its stores were removed to start from the
      shape rather than from Tuya's endpoint list.
- [ ] Probe `/multiple-names` against the ten channel categories that are in on evidence rather
      than on Tuya's documentation, and drop any that never answer.
- [ ] Integration tests for the root package / `postgres` / `firestore` behind a build tag and
      live infra.
- [ ] Further Tuya domains beyond device and space control (`home.go`), added as new files with
      their methods on `tuya.Client`.

## License

[MIT](LICENSE) © 2026 Ardian
