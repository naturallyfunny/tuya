# tuya

[![Go Reference](https://pkg.go.dev/badge/go.naturallyfunny.dev/tuya.svg)](https://pkg.go.dev/go.naturallyfunny.dev/tuya)
![Go 1.25+](https://img.shields.io/badge/go-1.25%2B-00ADD8)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

A Go library for the [Tuya Cloud OpenAPI](https://developer.tuya.com/en/docs/cloud/).

Tuya's API is keyed by a **Tuya UID** and knows nothing about *your* users. So the module has two
halves: one that talks to Tuya, and one that maps your own IDs onto Tuya's.

```sh
go get go.naturallyfunny.dev/tuya
```

## The packages

| Package               | What it is                                              | Depends on |
| --------------------- | ------------------------------------------------------- | ---------- |
| `tuya`                | The complete Tuya API client. Signing, tokens, devices, spaces.       | stdlib     |
| `tuya/appaccount`     | Your app app's identity -> Tuya UID, for the app-account shape.     | `tuya`     |
| `tuya/postgres`       | The owner → UID mapping stored in PostgreSQL (`pgx`).    | `appaccount` |
| `tuya/firestore`      | The same mapping on Cloud Firestore.                     | `appaccount` |

```
postgres ──┬─▶ appaccount ──▶ tuya
firestore ─┘
```

Only import what you need. `tuya` and `appaccount` are stdlib-only; `pgx` and the Firestore client
enter your module graph only if you import those store packages.

## `package tuya`

So you have just made a Tuya cloud project but don't know how to work with the API? I got you.

Prepare your Access ID, Access Secret, and the base URL. The base URL is the data center your cloud
project lives in:

| Region           | Base URL                           |
| ---------------- | ---------------------------------- |
| Western America  | `https://openapi.tuyaus.com`       |
| Eastern America  | `https://openapi-ueaz.tuyaus.com`  |
| Central Europe   | `https://openapi.tuyaeu.com`       |
| Western Europe   | `https://openapi-weaz.tuyaeu.com`  |
| China            | `https://openapi.tuyacn.com`       |
| India            | `https://openapi.tuyain.com`       |
| Singapore        | `https://openapi-sg.iotbing.com`   |

Singapore is on `iotbing.com`, not `tuya*.com`. The wrong data center still hands you a token and
then refuses every call with code `28841107` — if that's what you see, check the URL before the
credential.

Hand those three to `New`:

```go
client, err := tuya.New(accessID, accessSecret, "https://openapi-sg.iotbing.com")
```

That is all the auth you ever have to think about. Signing every request with HMAC-SHA256, caching
the access token, refreshing it when Tuya rejects it — the most stressful part of this API — is done
for you. `New` fetches the first token straight away, so a bad credential or the wrong region fails
right here instead of on your first device call. Pass `tuya.WithHTTPClient` if you want your own
timeouts and transport.

Then just call the endpoint you want:

```go
devices, err := client.UserDevices(ctx, tuyaUID)
status, err := client.DeviceStatus(ctx, deviceID)
err = client.SendCommands(ctx, deviceID, []tuya.DataPoint{{Code: "switch_1", Value: true}})
```

`DeviceProperties` is the newer read of the same device, and it carries more than a code and a value
— the data type, the name, and when the device last reported it. Name the codes you want, or none
at all for every property the device reports:

```go
properties, err := client.DeviceProperties(ctx, deviceID, []string{"switch_1", "countdown_1"})
```

If the endpoint you need isn't wrapped yet — or you'd simply rather drive it yourself — `Do` is the
same signed request one level down. Give it a method, a path and a body, and you get Tuya's `result`
back as raw JSON to decode into whatever you like:

```go
raw, err := client.Do(ctx, http.MethodGet, "/v1.0/devices/"+deviceID, nil)
```

## `package appaccount`

So you have your own app, with your own users, and each of them connects their Tuya app account to
it? Then you are holding a **Tuya UID** per user — a string that means everything to Tuya and
nothing to your database — and every call above kept asking you for one. I got you here too.

`appaccount` manages that link — it stores an **owner** against their Tuya UID, resolves the UID
again on every call, and hands the call to the client. So you address Tuya in your own IDs. An owner
is whatever identity your side of the integration already has: a user ID, an email, a tenant row, a
device installation. The library never looks inside it.

It needs two things: the client you just built, and a store to keep the mapping in. Two stores come
ready-made, and only the driver you import ends up in your module.

PostgreSQL, over `pgx` — it takes anything that can `Exec` and `Query`, so a `*pgxpool.Pool` or a
single `*pgx.Conn` both fit:

```go
store, err := postgres.NewAppAccountStore(ctx, pool, postgres.WithAutoMigrate())
```

`WithAutoMigrate()` creates the `tuya_app_accounts` table at startup from migrations embedded in the
binary. Leave it off if migrations are your deploy's job — then the constructor only checks the
table is there and fails immediately if it isn't, rather than at 3am on a live call.

Or Firestore, one document per owner, keyed by the owner ID:

```go
store := firestore.NewAppAccountStore(fsClient)
```

No migrations, so no error to handle. The collection is `tuya_app_accounts`; rename it with
`firestore.WithCollection("...")`. Do note the import name collides with Google's own `firestore`
package — alias one of them.

Either one goes to the service, together with the client:

```go
app := appaccount.NewService(client, store)
```

Write the link down once, when a user finishes connecting their Tuya account:

```go
acc, err := app.Link(ctx, owner, tuyaUID)
```

From here on you never have to hold a Tuya UID again:

```go
devices, err := app.Devices(ctx, owner)
if errors.Is(err, appaccount.ErrNotLinked) {
    // no Tuya account connected yet — send them into that flow
}

ok, err := app.HasDevice(ctx, owner, deviceID)
```

Acting on a device needs no owner at all — a device ID is already a Tuya handle, so these go
straight through:

```go
err = app.SendCommands(ctx, deviceID, []tuya.DataPoint{{Code: "switch_1", Value: true}})
status, err := app.DeviceStatus(ctx, deviceID)
properties, err := app.DeviceProperties(ctx, deviceID, nil)
```

So `HasDevice` only answers, it doesn't block. It tells you if the device is in that owner's own
list, and you decide what to do with that answer. A device someone shared with them won't be in the
list even though they're allowed to use it, so if this library blocked the command by itself, it
would break that.

When the Tuya account is disconnected, `app.Unlink(ctx, owner)`. `app.Get(ctx, owner)` hands you the
mapping itself if you need to see it.

In both stores `Link` is an upsert — linking again just replaces the UID — and `Unlink` really
deletes the record, because keeping someone's Tuya UID after they disconnected is your call to make,
not this library's. Want a trail instead? Write your own store, buddy: `Store` is three methods (`Get`,
`Link`, `Unlink`), and the only contract that matters is returning `ErrNotLinked` when the owner has
no link.

Spaces are not reachable this way. An app account's own homes and rooms live in Tuya's older *asset*
family, and those IDs are rejected by every `/v2.0/cloud/space` endpoint, so there is nothing here
to bridge them with.

## What this library doesn't do

- **Run the Tuya account-authorization flow.** Honestly, I haven't figured that one out yet. So for
  now `Link` assumes you already got the Tuya UID from somewhere, however you did it.
- **Wrap all of Tuya.** Only the essentials are wrapped so far, and more get added over time. Use
  `Do` for the rest meanwhile — and open an issue for the endpoint you need, I'll add it. Pull
  requests are welcome too, though I'm fairly strict about how code is written here, so expect me to
  refactor yours before merging.
- **Enforce ownership, or cache the answer.** Both explained above.

## Roadmap

- [ ] `package spatial`, just like `appaccount` but the owner is linked to a space instead of a Tuya
      account — for multi-tenant products such as smart hotels, where the devices belong to spaces
      rather than to a guest's own Tuya account.
- [ ] Probe `/multiple-names` against the ten undocumented channel categories, and drop any that
      never answer.
- [ ] Integration tests behind a build tag, against live infra.
- [ ] Further Tuya domains beyond devices and spaces.

## License

[MIT](LICENSE) © 2026 Ardian
