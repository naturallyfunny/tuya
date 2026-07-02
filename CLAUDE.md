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
client.go      (root, package tuya) Owner-scoped door: Account, ErrAccountNotLinked,
               ErrDeviceNotOwned, AccountStore interface, IoT interface (di-satisfy
               *cloud.IoT), Client (resolves owner→uid via store, asserts ownership via
               assertOwned+HasDevice, lalu delegate ke IoT). Pintu tunggal yang dipakai
               agent — tidak bisa dibuka tanpa resolve owner lebih dulu.
client_test.go (package tuya_test) unit test Client lewat fake IoT + fake AccountStore:
               happy path + ErrAccountNotLinked / ErrDeviceNotOwned, dan short-circuit guard.
cloud/         Package cloud — layer Tuya murni (keyed by Tuya UID, tanpa konsep owner).
  client.go    cloud.Client (transport: token cache/refresh app-level, HMAC-SHA256 signing, Do
               dengan retry-on-1010) + IoT (facade, membungkus *Client) + NewIoT.
               Operasi domain menempel di IoT tapi DITULIS di file domain masing-masing.
  auth.go      Concern auth: request signing (signTokenRequest/signBusinessRequest, hmacSign,
               setAuthHeaders) + token lifecycle (token app-level grant_type=1, fetch/update/
               ensureValidToken). Tuya tanda-tangani token-request vs business-request berbeda.
  device.go    Domain device: tipe Device/DataPoint/Channel, dan method *IoT:
               ListDevices (uid-addressed, enriched), DeviceStatus/SendCommands (device-
               addressed, tanpa ownership guard), HasDevice (lean membership check untuk
               tuya.Client.assertOwned), enrichDevices (channel-name multi-gang kategori
               kg/cz*). Domain baru → file baru (home.go, space.go).
postgres/
  store.go     Account management (owner -> tuya_uid): Store.Get/Link/Unlink, migrate.
               Mengembalikan tuya.Account / tuya.ErrAccountNotLinked (import root tuya).
  migrations/  SQL files, di-embed via //go:embed (tabel tuya_app_accounts, soft-delete deleted_at)
firestore/
  store.go     Account management yang sama di atas Cloud Firestore: NewAccountStore(client,
               opts) — satu dokumen per owner (doc ID = owner, koleksi tuya_app_accounts,
               override via WithCollection), soft-delete deleted_at, Link/Unlink transactional,
               timestamp client-side agar Link bisa return Account tanpa re-read. Tanpa migrasi.
```

Dependency direction acyclic: **postgres → tuya → cloud**.

## Cara Pakai

Tiga tier yang dirangkai consumer: `cloud.Client` (transport), `cloud.IoT` (facade
operasi domain berbasis device/uid), dan `tuya.Client` (root, facade berbasis owner yang
memegang ownership guard). Default & cara termudah untuk agent: pakai `tuya.Client`.

```go
// postgres.WithAutoMigrate() opsional — jalankan migration saat startup
store, err := postgres.NewAccountStore(ctx, pool, postgres.WithAutoMigrate())

transport, err := cloud.New(accessID, accessSecret, "https://openapi.tuyaus.com")
iot := cloud.NewIoT(transport)
client := tuya.New(iot, store) // postgres.Store satisfies tuya.AccountStore

// Semua resolve owner→uid + ownership guard ditangani tuya.Client
devices, err := client.ListDevices(ctx, owner)
status, err := client.DeviceStatus(ctx, owner, deviceID)
err = client.SendCommands(ctx, owner, deviceID, []cloud.DataPoint{{Code: "switch_1", Value: true}})
```

`cloud.IoT` bisa dipakai langsung jika consumer sudah pegang tuyaUID dan tidak butuh
ownership guard (trusted context). `DeviceStatus`/`SendCommands` device-addressed — tidak butuh uid.

```go
acc, err := store.Get(ctx, owner)
devices, err := iot.ListDevices(ctx, acc.TuyaUID) // uid-addressed
status, err := iot.DeviceStatus(ctx, deviceID)    // device-addressed, tanpa ownership guard
```

## Region

Tuya punya endpoint per data-center. `baseURL` di-inject saat `cloud.New()`:
`https://openapi.tuyaus.com` (US), `.tuyaeu.com` (EU), `.tuyacn.com` (China), `.tuyain.com` (India).

## Migrations

Migration runner custom — tidak pakai `golang-migrate`. Naming: `000N_deskripsi.up.sql` / `.down.sql`.
Hanya `.up.sql` yang dieksekusi; `.down.sql` disimpan untuk rollback manual.
Version tracking via tabel `tuya_schema_migrations`. Semua statement wajib `IF NOT EXISTS` / `IF EXISTS`.
Jangan pernah edit migration yang sudah di-commit.

## Design Decisions (sengaja — bukan temuan audit)

- **Token app-level di-cache in-memory, tanpa store.** Kredensial Tuya bersifat project-wide, bukan
  per-user. `cloud.Client` me-refresh sendiri saat expiry / saat Tuya balas code 1010. Tidak perlu token store.
- **Layout: pintu owner-scoped di root, layer Tuya murni di `cloud/`.** Library ini "tool dipanggil
  AI agent", jadi surface utama = pintu agent (`tuya.Client`) dan ia hidup di root. Layer trusted
  device-addressed (`cloud.Client`/`cloud.IoT`) didemot ke subpackage `cloud`. Ini mengikuti
  pola sibling `go.naturallyfunny.dev/spotify` (Client owner-keyed di root, inner client resolved
  disembunyikan). Tuya *berhak* mengekspos `cloud` standalone karena token project-level membuatnya
  berguna di trusted context tanpa user (beda dgn spotify yang per-user token wajib).
- **Ownership guard hidup di `tuya.Client` (root), bukan `cloud.IoT`.** `cloud.IoT` adalah
  trusted, device-addressed layer tanpa tenant check — caller yang memegangnya bisa mengakses device
  manapun dalam project. `tuya.Client` adalah pintu tunggal untuk agent: setiap call melaluinya
  harus resolve owner lebih dulu, dan ownership diverifikasi via `HasDevice` (lean, tanpa enrichment)
  sebelum command diteruskan. Guard ini *lebih kuat* di root: tidak bisa di-bypass tanpa melewati
  `tuya.Client`.
- **Ownership check via list-then-contains** (`tuya.Client.assertOwned` → `cloud.IoT.HasDevice`).
  Tiap `SendCommands`/`DeviceStatus` melisting device akun (raw, tanpa enrichment) lalu cek keanggotaan.
  Aman & sederhana untuk traffic rendah; caching ditunda sampai ada kebutuhan throughput nyata.
- **Operasi domain diorganisir per-file di `cloud.IoT`.** Method `IoT` ditulis di file
  domainnya (cloud/device.go; nanti cloud/home.go, cloud/space.go). Struct `IoT` + `NewIoT`
  hidup di `cloud/client.go`, di samping transport.
- **`ErrDeviceNotOwned` dan `ErrAccountNotLinked` hidup di root `tuya`.** Keduanya adalah konsep
  multi-tenant / owner-scoping, bukan konsep Tuya API — tempatnya di layer yang memiliki owner.
- **Empat concern dipisah dengan jelas.** `cloud.Client` transport; `cloud.IoT` device-addressed
  Tuya facade; `tuya.Client` owner-scoped facade dengan ownership guard; `postgres.Store` account mapping.
  Interface `AccountStore` dan `IoT` didefinisikan di root `tuya` (consumer), bukan di implementor —
  sesuai idiom Go "accept interfaces, return structs".
- **`cloud.Client.Do` adalah escape hatch publik** untuk endpoint Tuya yang belum dibungkus. Tidak ada
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

- **`cloud` tidak mengekspor interface untuk `IoT` — itu benar, bukan kelalaian.** Idiom Go:
  "accept interfaces, return structs." `cloud.NewIoT` mengembalikan `*cloud.IoT` (concrete).
  Yang mendefinisikan interface adalah *consumer*: root `tuya` mendeklarasikan `tuya.IoT` sesempit
  kebutuhan `tuya.Client` (di-satisfy `*cloud.IoT`) — ini justru penerapan idiom yang sama, bukan
  pelanggaran. Jangan minta `cloud` mengekspor interface spekulatif: itu menanggung beban kompatibilitas
  seumur hidup dan melebar tiap domain baru (home.go, space.go). Mocking adalah concern consumer.

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

- **"Ownership guard ada di `IoT`" — sudah tidak benar sejak refactor Juni 2026.**
  `cloud.IoT.DeviceStatus`/`SendCommands` sekarang device-addressed murni, tanpa `tuyaUID`
  dan tanpa ownership check. Guard pindah ke `tuya.Client.assertOwned` (root). `cloud.IoT`
  adalah trusted layer — siapapun yang memegangnya bisa mengakses device apapun dalam project.
  Jangan flag ini sebagai kelemahan; itu keputusan sadar. Audit ownership → lihat root `client.go`.

- **`context.Background()` di `cloud.New()` bukan masalah.** `cloud.New()` menerima `WithHTTPClient(hc)` —
  caller yang butuh kontrol timeout/cancellation mengonfigurasinya di `*http.Client`. Itu mekanisme yang
  tepat untuk prefetch saat konstruksi.

## Conventions

- `cloud.New(...)` mengembalikan `*cloud.Client` (transport); `cloud.NewIoT(c)` membungkusnya jadi `*cloud.IoT` (facade domain)
- `tuya.New(iot, store)` mengembalikan `*tuya.Client` (root, pintu owner-scoped); `iot` diterima sebagai interface `tuya.IoT`
- `Account`, `ErrAccountNotLinked`, `ErrDeviceNotOwned`, interface `AccountStore` & `IoT` hidup di root `tuya` (consumer side)
- `postgres.Store` mengimplementasikan `tuya.AccountStore`, mengembalikan `tuya.Account` (import root `tuya`)
- `postgres.NewAccountStore(ctx, db, opts...)` — terima `Querier` interface, bukan concrete `*pgxpool.Pool`
- `postgres.WithAutoMigrate()` — option untuk jalankan migration saat startup
- Tiga package: root `tuya` (owner-scoped) + `cloud/` (Tuya murni) + `postgres/`; tidak ada `pkg/`
- Conventional commits: `feat:`, `fix:`, `chore(migrate):` dst
