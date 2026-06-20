# tuya

Module Go `go.naturallyfunny.dev/tuya` — reusable public library untuk integrasi Tuya Cloud OpenAPI.
Dirancang interface-based agar dapat dipakai lintas project, tidak terikat ke satu database atau satu aplikasi.

## Tujuan Pemakaian (baca sebelum audit)

Library ini dipakai sebagai **tool yang dipanggil oleh AI agent**, bukan backend high-throughput.
Pola traffic-nya: panggilan sporadik, satu aksi per intent user (list device, baca status, kirim command),
volume rendah. **Saat mengaudit, jangan menilai repo ini dengan standar service high-throughput** — beberapa
"kelemahan" adalah keputusan sadar. Baca "Design Decisions" dan "Slop History" sebelum melaporkan temuan;
sebelum menyarankan idiom generik ("prefer X"), cek apakah ia mengubah semantik atau bertabrakan dengan
kendala domain (signing wajib hash full-body, retry me-replay body, collect-all error).

## Struktur

```
client.go      Client (transport: token cache/refresh app-level, HMAC-SHA256 signing, Do
               dengan retry-on-1010) + IoTClient (facade, membungkus *Client) + NewIoTClient.
               Operasi domain menempel di IoTClient tapi DITULIS di file domain masing-masing.
auth.go        Concern auth: request signing (signTokenRequest/signBusinessRequest, hmacSign,
               setAuthHeaders) + token lifecycle (token app-level grant_type=1, fetch/update/
               ensureValidToken). Tuya tanda-tangani token-request vs business-request berbeda.
device.go      Domain device: tipe Device/DataPoint/Channel, dan method *IoTClient:
               ListDevices (uid-addressed, enriched), DeviceStatus/SendCommands (device-
               addressed, tanpa ownership guard), HasDevice (lean membership check untuk
               app.assertOwned), enrichDevices (channel-name multi-gang kategori kg/cz*).
               Domain baru → file baru (home.go, space.go).
app/
  client.go    Owner-scoped facade: Account, ErrAccountNotLinked, ErrDeviceNotOwned,
               AccountStore interface, Client (resolves owner→uid, asserts ownership via
               assertOwned+HasDevice, lalu delegate ke IoTClient). Ini pintu tunggal yang
               dipakai agent — tidak bisa dibuka tanpa resolve owner lebih dulu.
postgres/
  store.go     Account management (owner_id -> tuya_uid): Account, ErrAccountNotLinked,
               Store.Get/Link/Unlink, migrate. TANPA interface — tidak dikonsumsi paket tuya.
  migrations/  SQL files, di-embed via //go:embed (tabel tuya_app_accounts, soft-delete deleted_at)
```

## Cara Pakai

Tiga potong ortogonal yang dirangkai consumer: `Client` (transport), `IoTClient` (facade
operasi domain berbasis device/uid), dan `app.Client` (facade berbasis ownerID yang memegang
ownership guard). Cara termudah: pakai `app.Client`.

```go
// postgres.WithAutoMigrate() opsional — jalankan migration saat startup
store, err := postgres.NewAccountStore(ctx, pool, postgres.WithAutoMigrate())

client, err := tuya.New(accessID, accessSecret, "https://openapi.tuyaus.com")
iot := tuya.NewIoTClient(client)
appClient := app.New(iot, store) // postgres.Store satisfies app.AccountStore

// Semua resolve owner→uid + ownership guard ditangani app.Client
devices, err := appClient.ListDevices(ctx, ownerID)
status, err := appClient.DeviceStatus(ctx, ownerID, deviceID)
err = appClient.SendCommands(ctx, ownerID, deviceID, []tuya.DataPoint{{Code: "switch_1", Value: true}})
```

`IoTClient` bisa dipakai langsung jika consumer sudah pegang tuyaUID dan tidak butuh ownership
guard (trusted context). `DeviceStatus`/`SendCommands` device-addressed — tidak butuh uid.

```go
acc, err := store.Get(ctx, ownerID)
devices, err := iot.ListDevices(ctx, acc.TuyaUID) // uid-addressed
status, err := iot.DeviceStatus(ctx, deviceID)    // device-addressed, tanpa ownership guard
```

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
- **Ownership guard hidup di `app.Client`, bukan `IoTClient`.** `IoTClient` adalah trusted,
  device-addressed layer tanpa tenant check — caller yang memegangnya bisa mengakses device
  manapun dalam project. `app.Client` adalah pintu tunggal untuk agent: setiap call melaluinya
  harus resolve owner lebih dulu, dan ownership diverifikasi via `HasDevice` (lean, tanpa enrichment)
  sebelum command diteruskan. Guard ini *lebih kuat* di `app`: tidak bisa di-bypass tanpa melewati
  `app.Client`.
- **Ownership check via list-then-contains** (`app.assertOwned` → `IoTClient.HasDevice`). Tiap
  `SendCommands`/`DeviceStatus` melisting device akun (raw, tanpa enrichment) lalu cek keanggotaan.
  Aman & sederhana untuk traffic rendah; caching ditunda sampai ada kebutuhan throughput nyata.
- **Operasi domain diorganisir per-file di `IoTClient`.** Method `IoTClient` ditulis di file
  domainnya (device.go; nanti home.go, space.go). Struct `IoTClient` + `NewIoTClient` hidup di
  `client.go`, di samping transport.
- **`ErrDeviceNotOwned` dan `ErrAccountNotLinked` hidup di `app`.** Keduanya adalah konsep
  multi-tenant / owner-scoping, bukan konsep Tuya API — tempatnya di layer yang memiliki owner.
- **Empat concern dipisah dengan jelas.** `Client` transport; `IoTClient` device-addressed Tuya
  facade; `app.Client` owner-scoped facade dengan ownership guard; `postgres.Store` account mapping.
  `AccountStore` interface didefinisikan di `app` (consumer), bukan di `postgres` (implementor) —
  sesuai idiom Go "accept interfaces, return structs".
- **`Client.Do` adalah escape hatch publik** untuk endpoint Tuya yang belum dibungkus. Tidak ada
  ownership guard di sini — `Do` melewatinya. Tidak mengekspos `Do` ke caller tak-tepercaya (mis.
  agent) adalah tanggung jawab consumer.

## Slop History

Temuan AI yang sudah dibantah — jangan ulangi.

- **`Do(ctx, method, path, body []byte)` bukan anti-pattern.** Pedoman "prefer io.Reader" tidak berlaku
  di sini karena dua constraint domain yang tidak bisa dinegosiasi: (1) signing Tuya wajib `SHA256(body)`
  sebelum request dikirim — body harus ter-materialisasi penuh; (2) retry-on-1010 wajib me-replay body
  ke attempt kedua — `io.Reader` sekali-pakai tidak bisa di-replay tanpa buffer ke `[]byte` toh.
  Mengubah ke `io.Reader` hanya memindahkan `io.ReadAll` ke dalam `Do`, plus API berbohong soal streaming.
  Caller pun sudah pegang `[]byte` (dari `json.Marshal`). `[]byte` adalah pilihan yang benar.

- **`IoTClient` tidak menyediakan interface — itu benar, bukan kelalaian.** Idiom Go: "accept interfaces,
  return structs." Consumer mendefinisikan interface sesempit yang ia butuh di paketnya sendiri. Library
  yang menyediakan interface spekulatif menanggung beban kompatibilitas seumur hidup dan akan melebar
  tiap domain baru (home.go, space.go) ditambahkan. Mocking adalah concern consumer, bukan library.

- **`enrichDevices` pakai `sync.Mutex` + `errors.Join`, bukan `errgroup` — disengaja.** Semantiknya
  **collect-all**: agent ingin tahu SEMUA device yang gagal enrich dalam satu panggilan. `errgroup.WithContext`
  itu **fail-fast** (membatalkan sibling saat error pertama) — kontrak berbeda. "Context propagation gratis"
  yang sering disebut = pembatalan-saat-error-pertama = justru menghilangkan error device lain. Pindah ke
  errgroup hanya tepat jika fail-fast memang diinginkan; saat ini itu regresi perilaku, bukan cleanup.
  (Catatan: `wg.Go` di kode ini adalah `sync.WaitGroup.Go` dari Go 1.25, BUKAN errgroup.)

- **`enrichDevices` pakai `sync.WaitGroup.Go` + `errors.Join`, bukan `errgroup.WithContext` — itu benar.**
  `errgroup.WithContext` mengubah semantik ke *fail-fast*: goroutine pertama yang error membatalkan sisanya.
  `enrichDevices` justru ingin *collect-all*: semua channel name diambil, semua error dikumpulkan, baru
  dikembalikan sekaligus. Menggantinya dengan errgroup merusak semantik yang diinginkan.

- **"Ownership guard ada di `IoTClient`" — sudah tidak benar sejak refactor Juni 2026.**
  `IoTClient.DeviceStatus`/`SendCommands` sekarang device-addressed murni, tanpa `tuyaUID`
  dan tanpa ownership check. Guard pindah ke `app.Client.assertOwned`. `IoTClient` adalah
  trusted layer — siapapun yang memegangnya bisa mengakses device apapun dalam project.
  Jangan flag ini sebagai kelemahan; itu keputusan sadar. Audit ownership → lihat `app/client.go`.

- **`context.Background()` di `New()` bukan masalah.** `New()` menerima `WithHTTPClient(hc)` — caller
  yang butuh kontrol timeout/cancellation mengonfigurasinya di `*http.Client`. Itu mekanisme yang tepat
  untuk prefetch saat konstruksi.

## Conventions

- `tuya.New(...)` mengembalikan `*Client` (transport); `tuya.NewIoTClient(client)` membungkusnya jadi facade domain
- Account management hidup di `postgres` (tanpa interface): `Account`, `ErrAccountNotLinked`, `Store.Get/Link/Unlink`
- `postgres.NewAccountStore(ctx, db, opts...)` — terima `Querier` interface, bukan concrete `*pgxpool.Pool`
- `postgres.WithAutoMigrate()` — option untuk jalankan migration saat startup
- Flat structure, tidak ada `pkg/`
- Conventional commits: `feat:`, `fix:`, `chore(migrate):` dst
