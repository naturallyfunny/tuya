# tuya

Module Go `go.naturallyfunny.dev/tuya` — reusable public library untuk integrasi Tuya Cloud OpenAPI.
Dirancang interface-based agar dapat dipakai lintas project, tidak terikat ke satu database atau satu aplikasi.

## Tujuan Pemakaian (baca sebelum audit)

Library ini dipakai sebagai **tool yang dipanggil oleh AI agent**, bukan backend high-throughput.
Pola traffic-nya: panggilan sporadik, satu aksi per intent user (list device, baca status, kirim command),
volume rendah. **Saat mengaudit, jangan menilai repo ini dengan standar service high-throughput** — beberapa
"kelemahan" adalah keputusan sadar. Lihat "Design Decisions" sebelum melaporkan temuan.

## Struktur

```
client.go      Client (token cache/refresh app-level, HMAC-SHA256 signing, Do dengan retry-on-1010),
               AccountStore interface (consumer-defined), sentinel ErrAccountNotLinked / ErrDeviceNotOwned
token.go       Token, getToken (grant_type=1) — token app-level, bukan per-user
signature.go   generateSignature (HMAC-SHA256 ala Tuya Cloud)
account.go     Account, Client.Account
device.go      Device/DataPoint/Channel, ListDevices, DeviceStatus, SendCommands (ownership check),
               enrichDevices (channel-name multi-gang untuk kategori kg/cz*)
postgres/
  store.go     Store implements tuya.AccountStore (owner_id -> tuya_uid), Migrate()
  migrations/  SQL files, di-embed via //go:embed (tabel tuya_app_accounts, soft-delete deleted_at)
```

## Cara Pakai

```go
// postgres.WithAutoMigrate() opsional — jalankan migration saat startup
store, err := postgres.NewAccountStore(ctx, pool, postgres.WithAutoMigrate())
if err != nil {
    log.Fatal(err)
}

// baseURL = endpoint region: tuyaus / tuyaeu / tuyacn / tuyain
client, err := tuya.New(store, accessID, accessSecret, "https://openapi.tuyaus.com")

devices, err := client.ListDevices(ctx, ownerID)
status, err := client.DeviceStatus(ctx, ownerID, deviceID)
err = client.SendCommands(ctx, ownerID, deviceID, []tuya.DataPoint{{Code: "switch_1", Value: true}})
```

`ownerID` adalah identitas opaque milik consumer; library memetakannya ke Tuya UID lewat `AccountStore`.

## Region

Tuya punya endpoint per data-center. `baseURL` di-inject saat `New()`:
`https://openapi.tuyaus.com` (US), `.tuyaeu.com` (EU), `.tuyacn.com` (China), `.tuyain.com` (India).

## Migrations

Migration runner custom — tidak pakai `golang-migrate`. Naming: `000N_deskripsi.up.sql` / `.down.sql`.
Hanya `.up.sql` yang dieksekusi; `.down.sql` disimpan untuk rollback manual.
Version tracking via tabel `tuya_schema_migrations`. Semua statement wajib `IF NOT EXISTS` / `IF EXISTS`.
Jangan pernah edit migration yang sudah di-commit.

## Design Decisions (sengaja — bukan temuan audit)

- **Token app-level di-cache in-memory, tanpa store.** Kredensial Tuya bersifat project-wide, bukan
  per-user. `Client` me-refresh sendiri saat expiry / saat Tuya balas code 1010. Tidak perlu token store.
- **Ownership check via list-then-contains** (`assertOwned`, device.go). Tiap `SendCommands`/`DeviceStatus`
  melisting device akun lalu cek keanggotaan. Aman & sederhana untuk traffic rendah; caching ditunda
  sampai ada kebutuhan throughput nyata.
- **`AccountStore` consumer-defined interface.** Library tidak memaksakan storage; consumer menulis baris
  (link/unlink akun). `postgres.Store` hanya membaca mapping. Penulisan/enkripsi adalah tanggung jawab consumer.

## Conventions

- `AccountStore` interface didefinisikan di `client.go` — consumer-defined interface
- `postgres.NewAccountStore(ctx, db, opts...)` — terima `Querier` interface, bukan concrete `*pgxpool.Pool`
- `postgres.WithAutoMigrate()` — option untuk jalankan migration saat startup
- Flat structure, tidak ada `pkg/`
- Conventional commits: `feat:`, `fix:`, `chore(migrate):` dst
