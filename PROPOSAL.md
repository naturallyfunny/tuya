# Proposal: relocate ownership — device-addressed `IoTClient`, owner-scoped `app.Client`

Status: **Draft / for review**
Author: Ardian (with Claude)
Date: 2026-06-20
Supersedes: the prior `tuya/app` proposal (now implemented — `app.Client`,
`app.AccountStore`, `app.Account`, `app.ErrAccountNotLinked` exist). This builds
on it.

## 1. The realization

`IoTClient` currently takes a `tuyaUID` on **every** device method and bolts an
ownership check onto operations that the Tuya API itself addresses purely by
device:

```go
// today
func (c *IoTClient) DeviceStatus(ctx, tuyaUID, deviceID string) ([]DataPoint, error) {
    if err := c.assertDeviceOwned(ctx, tuyaUID, deviceID); err != nil { ... } // list-then-contains
    // GET /v1.0/iot-03/devices/{deviceID}/status   ← no uid in the path
}
func (c *IoTClient) SendCommands(ctx, tuyaUID, deviceID string, cmds []DataPoint) error {
    if err := c.assertDeviceOwned(ctx, tuyaUID, deviceID); err != nil { ... }
    // POST /v1.0/iot-03/devices/{deviceID}/commands ← no uid in the path
}
```

Two concerns are conflated:

1. **The Tuya operation** — `DeviceStatus`/`SendCommands` are *device-addressed*
   (`/v1.0/iot-03/devices/{id}/...`). The uid is not part of the request.
2. **The ownership guard** — "does this device belong to this account?" — an
   *owner-scoping* concern. It exists so one human's agent can't drive another
   human's device inside the same Tuya project. That boundary is about **owner
   identity**, not about talking to Tuya.

The uid only appears in `DeviceStatus`/`SendCommands` to feed concern (2). Concern
(2) does not belong in the pure-Tuya transport layer — it belongs where the owner
concept lives: `app.Client`.

`ListDevices` is the honest exception: `/v1.0/users/{uid}/devices` **is**
uid-addressed. There the uid is the Tuya resource path, not an added guard, so it
stays a parameter.

## 2. Proposed split

**`IoTClient` becomes pure, device-addressed Tuya. Library stays context-free —
identity is always an explicit parameter, never read from a context key.**

```go
// IoTClient — pure Tuya
func (c *IoTClient) ListDevices(ctx, tuyaUID string) ([]Device, error)          // uid = resource path
func (c *IoTClient) DeviceStatus(ctx, deviceID string) ([]DataPoint, error)      // device-addressed
func (c *IoTClient) SendCommands(ctx, deviceID string, cmds []DataPoint) error   // device-addressed
// assertDeviceOwned and ErrDeviceNotOwned LEAVE this package.
```

**`app.Client` becomes the home of ownership.** Its methods keep their owner-ID
parameter (as already implemented), resolve owner → uid, assert ownership, then
delegate to the now-device-addressed `IoTClient`:

```go
// app.Client — owner-scoped
func (c *Client) ListDevices(ctx, ownerID string) ([]tuya.Device, error) {
    acc, err := c.store.Get(ctx, ownerID); if err != nil { return nil, err }
    return c.iot.ListDevices(ctx, acc.TuyaUID)
}
func (c *Client) DeviceStatus(ctx, ownerID, deviceID string) ([]tuya.DataPoint, error) {
    acc, err := c.store.Get(ctx, ownerID); if err != nil { return nil, err }
    if err := c.assertOwned(ctx, acc.TuyaUID, deviceID); err != nil { return nil, err }
    return c.iot.DeviceStatus(ctx, deviceID)
}
func (c *Client) SendCommands(ctx, ownerID, deviceID string, cmds []tuya.DataPoint) error {
    acc, err := c.store.Get(ctx, ownerID); if err != nil { return err }
    if err := c.assertOwned(ctx, acc.TuyaUID, deviceID); err != nil { return err }
    return c.iot.SendCommands(ctx, deviceID, cmds)
}

// assertOwned lists the account's devices and checks membership — the logic that
// used to live in IoTClient.assertDeviceOwned, now owner-scoped.
func (c *Client) assertOwned(ctx, tuyaUID, deviceID string) error {
    devices, err := c.iot.ListDevices(ctx, tuyaUID)
    if err != nil { return fmt.Errorf("verify device ownership: %w", err) }
    for _, d := range devices { if d.ID == deviceID { return nil } }
    return ErrDeviceNotOwned // now defined in app
}

// New: lets the agent layer confirm linkage (feeds adk's get_account tool).
func (c *Client) Account(ctx, ownerID string) (Account, error) {
    return c.store.Get(ctx, ownerID)
}
```

`ErrDeviceNotOwned` moves to `app` (joining `Account` / `ErrAccountNotLinked`).

## 3. Why this is correct

- **Honest layering.** `IoTClient` now mirrors the Tuya API one-to-one:
  device-addressed where Tuya is, uid-addressed only where Tuya is
  (`ListDevices`). No invented parameters.
- **Ownership sits with the owner concept.** The guard is multi-tenant scoping
  bound to owner identity; `app.Client` is the only layer that knows owners. The
  guarantee is *stronger* here, not weaker: `app.Client` is the single door an
  untrusted agent goes through, and it cannot be opened without resolving an
  owner first.
- **The library never touches `context` for identity.** Identity is a parameter
  at every layer. Consumers that want context-propagated identity (e.g. the ADK
  toolset reading `toolCtx.UserID()`) bind it themselves at their edge — see the
  companion `adk-go` proposal. This keeps `tuya` testable and free of any
  context-key convention.
- **Method shapes converge by design.** With ownership gone from `IoTClient`,
  `DeviceStatus(ctx, deviceID)` / `SendCommands(ctx, deviceID, cmds)` are
  identical in `IoTClient` and (modulo the leading `ownerID`) in `app.Client`.
  The remaining identity difference is one explicit parameter the consumer
  supplies — no hidden string whose meaning flips per implementation.

## 4. Consequences (conscious, not silent)

- **This reverses a documented decision.** CLAUDE.md currently states the
  ownership guarantee is a property of `IoTClient.DeviceStatus`/`SendCommands`.
  After this change, raw `IoTClient` is a **trusted, device-addressed layer with
  no tenant guard** — a caller holding it can act on any device the *project* can
  reach. The guard becomes the defining purpose of `app.Client`. CLAUDE.md
  "Design Decisions" and "Slop History" must be updated to say so.
- **Breaking API:** `IoTClient.DeviceStatus`/`SendCommands` drop the `tuyaUID`
  parameter. `ErrDeviceNotOwned` moves `tuya` → `app`.
- Non-breaking: `IoTClient.ListDevices(ctx, tuyaUID)` unchanged; `app.Client`
  method signatures unchanged (they already take `ownerID`); `app.Client` gains
  `Account`.

## 5. Migration impact

| File | Change |
|---|---|
| `device.go` | `DeviceStatus`/`SendCommands`: drop `tuyaUID` param + the assert call; bodies are already device-addressed. `ListDevices(ctx, tuyaUID)` unchanged. Delete `assertDeviceOwned` and `ErrDeviceNotOwned`. |
| `app/client.go` | Add `assertOwned` + `ErrDeviceNotOwned`; call it in `DeviceStatus`/`SendCommands` before delegating. Add `Account(ctx, ownerID)`. |
| `client.go` | No context plumbing. No `With…FromContext`. |
| `CLAUDE.md` | Flip "ownership lives in `IoTClient`" → "ownership lives in `app`"; refresh Slop History accordingly. |
| `README.md` | Show device-addressed `IoTClient`, owner-scoped `app.Client`; note context-binding is the consumer's edge. |
| tests | `app.Client`: fake `AccountStore`, assert ownership + `ErrDeviceNotOwned`. `IoTClient`: device-addressed calls. |

`Client` (transport), signing, token lifecycle, migrations: untouched.

## 6. Open questions

1. **Does `IoTClient` keep an internal `listDevices` helper, or is `ListDevices`
   the only list path?** `app.assertOwned` needs to list by uid; it can call the
   exported `IoTClient.ListDevices(ctx, uid)` directly (it returns enriched
   devices — slightly more work than needed for a membership check). Acceptable
   for low traffic, or expose a lean unexported list? Lean toward reuse.
2. **`app.Client.Account` return type.** Returns `app.Account`. Fine — confirms
   the toolset's `get_account` can map `OwnerID`/`TuyaUID` without importing
   `postgres`.
3. **Deprecation.** Library is young and (per the earlier decision) we took the
   clean break for the `postgres.Account` move. Same posture here: no shims for
   the `IoTClient` signature change.
```
