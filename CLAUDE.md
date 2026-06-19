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
client.go      Client (transport: token cache/refresh app-level, HMAC-SHA256 signing, Do
               dengan retry-on-1010) + IoTClient (facade, membungkus *Client) + NewIoTClient.
               Operasi domain menempel di IoTClient tapi DITULIS di file domain masing-masing.
auth.go        Concern auth: request signing (signTokenRequest/signBusinessRequest, hmacSign,
               setAuthHeaders) + token lifecycle (token app-level grant_type=1, fetch/update/
               ensureValidToken). Tuya tanda-tangani token-request vs business-request berbeda.
device.go      Domain device: tipe Device/DataPoint/Channel, ErrDeviceNotOwned, dan method
               *IoTClient: ListDevices/DeviceStatus/SendCommands (berbasis tuyaUID),
               assertDeviceOwned, enrichDevices (channel-name multi-gang kategori kg/cz*).
               Domain baru → file baru (home.go, space.go), tiap file punya assert-nya sendiri.
postgres/
  store.go     Account management (owner_id -> tuya_uid): Account, ErrAccountNotLinked,
               Store.Get, migrate. TANPA interface — tidak dikonsumsi paket tuya.
  migrations/  SQL files, di-embed via //go:embed (tabel tuya_app_accounts, soft-delete deleted_at)
```

## Cara Pakai

Tiga potong ortogonal yang dirangkai consumer: `Client` (transport), `IoTClient` (facade
operasi domain berbasis `tuyaUID`), dan `postgres.Store` (resolusi owner→uid). Consumer
me-resolve owner sendiri lalu memanggil `IoTClient`.

```go
// postgres.WithAutoMigrate() opsional — jalankan migration saat startup
store, err := postgres.NewAccountStore(ctx, pool, postgres.WithAutoMigrate())
if err != nil {
    log.Fatal(err)
}

// baseURL = endpoint region: tuyaus / tuyaeu / tuyacn / tuyain
// httpClient opsional via tuya.WithHTTPClient(...); default http.DefaultClient
client, err := tuya.New(accessID, accessSecret, "https://openapi.tuyaus.com")
// client, err := tuya.New(accessID, accessSecret, baseURL, tuya.WithHTTPClient(hc))
iot := tuya.NewIoTClient(client)

// resolve owner → uid sendiri (concern consumer), baru panggil IoT
acc, err := store.Get(ctx, ownerID)
if errors.Is(err, postgres.ErrAccountNotLinked) {
    // arahkan human ke linking flow
}

devices, err := iot.ListDevices(ctx, acc.TuyaUID)
status, err := iot.DeviceStatus(ctx, acc.TuyaUID, deviceID)
err = iot.SendCommands(ctx, acc.TuyaUID, deviceID, []tuya.DataPoint{{Code: "switch_1", Value: true}})
```

`ownerID` adalah identitas opaque milik consumer; consumer memetakannya ke Tuya UID lewat
`postgres.Store` (atau store backend lain miliknya) sebelum memanggil `IoT`.

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
- **Ownership check via list-then-contains** (`assertDeviceOwned`, device.go). Tiap `SendCommands`/`DeviceStatus`
  melisting device akun lalu cek keanggotaan. Aman & sederhana untuk traffic rendah; caching ditunda
  sampai ada kebutuhan throughput nyata.
- **Operasi domain diorganisir per-file, assert terlokalisasi.** Method `IoTClient` ditulis di file
  domainnya (device.go; nanti home.go, space.go), masing-masing membawa assert kepemilikannya sendiri
  (`assertDeviceOwned`, dst). Struct `IoTClient` + `NewIoTClient` hidup di `client.go`, di samping transport.
- **Tiga concern dipisah, tanpa interface AccountStore.** `Client` transport, `IoTClient` facade domain
  berbasis `tuyaUID`, account management seluruhnya di `postgres` (tanpa interface karena tak dikonsumsi
  paket `tuya`). Resolusi owner→uid + link/unlink akun adalah tanggung jawab consumer; ia memanggil
  `store.Get` sendiri lalu mengoper `tuyaUID` ke `IoTClient`.
- **`Client.Do` adalah escape hatch publik** untuk endpoint Tuya yang belum dibungkus. Jaminan ownership
  adalah properti method `IoTClient` (`DeviceStatus`/`SendCommands`), bukan properti `Client` — `Do`
  melewatinya. Tidak mengekspos `Do` ke caller tak-tepercaya (mis. agent) adalah tanggung jawab consumer.

## Conventions

- `tuya.New(...)` mengembalikan `*Client` (transport); `tuya.NewIoTClient(client)` membungkusnya jadi facade domain
- Account management hidup di `postgres` (tanpa interface): `Account`, `ErrAccountNotLinked`, `Store.Get`
- `postgres.NewAccountStore(ctx, db, opts...)` — terima `Querier` interface, bukan concrete `*pgxpool.Pool`
- `postgres.WithAutoMigrate()` — option untuk jalankan migration saat startup
- Flat structure, tidak ada `pkg/`
- Conventional commits: `feat:`, `fix:`, `chore(migrate):` dst
