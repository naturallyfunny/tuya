# tuya

Module Go `go.naturallyfunny.dev/tuya` — reusable public library untuk integrasi Tuya Cloud OpenAPI.
Dirancang interface-based agar dapat dipakai lintas project, tidak terikat ke satu database atau satu aplikasi.

## Tujuan Pemakaian

Library ini dipakai sebagai **tool yang dipanggil oleh AI agent**, bukan backend high-throughput.
Pola traffic-nya: panggilan sporadik, satu aksi per intent user (list device, baca status, kirim command),
volume rendah. Beberapa keputusan (ownership check via list-then-contains, token in-memory, collect-all
error) mengoptimalkan untuk pola ini, bukan untuk throughput tinggi — rasionalnya ada di "Design Decisions"
dan di README (section "Design rationale"). Sebelum menukar sebuah keputusan dengan idiom generik ("prefer X"),
cek dulu apakah ia mengubah semantik atau bertabrakan dengan kendala domain (signing wajib hash full-body,
retry me-replay body, collect-all error).

## Struktur

Root dipecah **per pintu**, bukan per jenis deklarasi. Yang membedakan dua model tenancy
adalah guard-nya — bagian paling berisiko di library ini — jadi ia harus terbaca dalam satu
file, bukan dirakit dari file tipe + file perilaku.

```
iot.go         (root, package tuya) Yang dipakai semua pintu: package doc, interface IoT
               (di-satisfy *cloud.IoT), ErrDeviceNotOwned. Tidak ada pintu di sini.
app_account.go (root) Pintu app-account, utuh: AppAccount, ErrAccountNotLinked,
               AppAccountStore interface, struct AppAccountClient + NewAppAccountClient
               + Account() (receiver sudah bilang "App", method tidak mengulang), lalu
               operasi device-nya: ListDevices (resolve owner → list → resolveChannelNames),
               DeviceStatus/SendCommands (resolve → assertOwned → delegate), assertOwned
               (list-then-contains), isMultiGang (heuristik kategori kg/cz*),
               resolveChannelNames (fan-out concurrent + errors.Join). Semua yang cloud
               tolak putuskan, diputuskan di sini.
app_account_test.go
               (package tuya_test) unit test AppAccountClient lewat fake IoT + fake
               AppAccountStore: happy path + ErrAccountNotLinked / ErrDeviceNotOwned,
               short-circuit guard, resolusi channel-name, dan bukti guard tidak ikut
               menembak multiple-names.
space.go       (root) Pintu spatial, utuh: SpaceTenant, ErrSpaceNotLinked, ErrSpaceNotOwned,
               ErrRootSpaceProtected, interface SpaceStore + SpaceIoT, struct SpaceClient +
               NewSpaceClient + Tenant(), lalu operasinya: CreateSpace/Space/ModifySpace/
               DeleteSpace/ChildSpaces/SpaceResources (resolve owner → assertSpaceOwned →
               delegate) dan DeviceStatus/SendCommands (resolve → assertDeviceOwned →
               delegate). Dua guard karena dua biaya: assertSpaceOwned satu request
               (SpaceContains), assertDeviceOwned menelusuri resource seluruh subtree
               berhalaman dan berbatas. ID space 0 selalu berarti root milik tenant,
               tidak pernah root project.
space_test.go  (package tuya_test) unit test SpaceClient lewat fake SpaceIoT + fake
               SpaceStore: guard space & device, short-circuit sebelum operasi jalan,
               ID 0 = root tenant (tanpa nanya Tuya), root tenant tidak bisa dihapus,
               dan bukti walk device berhenti — cursor macet maupun cursor yang terus maju.
cloud/         Package cloud — layer Tuya murni, trusted, tanpa konsep owner. Sebagian besar
               device-addressed; hanya ListDevices yang butuh Tuya UID.
  client.go    cloud.Client (transport: token cache/refresh app-level, HMAC-SHA256 signing, Do
               dengan retry-on-1010) + IoT (facade, membungkus *Client) + NewIoT.
               Operasi domain menempel di IoT tapi DITULIS di file domain masing-masing.
  auth.go      Concern auth: request signing (signTokenRequest/signBusinessRequest, hmacSign,
               setAuthHeaders) + token lifecycle (token app-level grant_type=1, fetch/update/
               ensureValidToken). Tuya tanda-tangani token-request vs business-request berbeda.
  device.go    Domain device: tipe Device/DataPoint/Channel + empat method *IoT yang
               masing-masing satu endpoint: ListDevices (GET users/{uid}/devices),
               DeviceStatus, SendCommands, DeviceChannelNames (GET devices/{id}/
               multiple-names). Tanpa ownership, tanpa enrichment — keduanya milik root.
               Domain baru → file baru (home.go).
  space.go     Domain space: tipe SpaceID (terima JSON number MAUPUN string), Space,
               Resource, Page, Scope, ErrNotApplied, plus delapan method *IoT di atas tujuh
               endpoint /v2.0/cloud/space*: CreateSpace, Space, ModifySpace, DeleteSpace,
               SpaceResources, ChildSpaces, RootSpaces, SpaceContains. ChildSpaces dan
               RootSpaces berbagi satu endpoint (child) — dipisah supaya id 0 tidak diam-diam
               berubah jadi "lihat seluruh project". Tanpa guard, tanpa loop pagination.
postgres/
  app_account.go
               App-account management (owner -> tuya_uid): AppAccountStore.Get/Link/Unlink,
               NewAppAccountStore, migrate. Mengembalikan tuya.AppAccount /
               tuya.ErrAccountNotLinked (import root tuya). Memegang juga yang dipakai kedua
               store: Querier, Option/WithAutoMigrate, prepareSchema, migrate.
  space.go     Hal yang sama untuk model spatial (owner -> root_space_id): SpaceStore.
               Get/Link/Unlink, NewSpaceStore. root_space_id disimpan bigint dan di-scan
               lewat int64 sebelum jadi cloud.SpaceID — kolomnya milik driver, tipe
               bernamanya milik domain.
  migrations/  SQL files, di-embed via //go:embed (000001 tuya_app_accounts,
               000002 tuya_space_tenants; keduanya soft-delete deleted_at)
firestore/
  app_account.go
               Hal yang sama di atas Cloud Firestore: NewAppAccountStore(client,
               opts) — satu dokumen per owner (doc ID = owner, koleksi tuya_app_accounts,
               override via WithCollection), soft-delete deleted_at, Link/Unlink transactional,
               timestamp client-side agar Link bisa return AppAccount tanpa re-read. Tanpa migrasi.
               Memegang juga yang dipakai bersama: Option/WithCollection, collectionOr,
               validateOwner.
  space.go     SpaceStore di atas Firestore, koleksi tuya_space_tenants
               (DefaultSpaceCollection), pola dokumen & transaksi identik dengan
               app_account.go.
```

Nama file store = nama model tenancy juga, bukan `store.go`. Model spatial sudah masuk, jadi
tiap package adapter punya `space.go` di sebelah `app_account.go`; `store.go` yang polos
memang tidak punya tempat untuk itu.

Dependency direction acyclic: **postgres → tuya → cloud**.

## Cara Pakai

Tiga tier yang dirangkai consumer: `cloud.Client` (transport), `cloud.IoT` (facade
operasi domain berbasis device/uid/space), dan dua pintu owner-scoped di root yang memegang
ownership guard — `tuya.AppAccountClient` dan `tuya.SpaceClient`. Untuk agent, pakai salah
satu pintu root; yang mana tergantung model tenancy-nya.

Namanya menyebut **model tenancy**, bukan sekadar verbose. Tuya punya dua: app-account
(tiap manusia punya akun app sendiri, batas tenant = Tuya UID, butuh `AppAccountStore`) dan
spatial (batas tenant = root space, device di subtree di bawahnya, tanpa UID per tenant,
butuh `SpaceStore`). Keduanya sudah dibungkus.

Prefix `App` juga memisahkan dua "account" yang beda: **akun app** (punya device, di-key
uid) vs **akun project** Tuya (pemilik accessID/accessSecret). Call site tetap pendek karena
nama variabel milik pemanggil: `app := tuya.NewAppAccountClient(...)` lalu
`app.Account(ctx, owner)` — method-nya `Account()`, bukan `AppAccount()`, karena receiver
sudah membawa "App" dan tipe kembaliannya yang menyatakan presisi.

```go
// postgres.WithAutoMigrate() opsional — jalankan migration saat startup
store, err := postgres.NewAppAccountStore(ctx, pool, postgres.WithAutoMigrate())

transport, err := cloud.New(accessID, accessSecret, "https://openapi.tuyaus.com")
iot := cloud.NewIoT(transport)
client := tuya.NewAppAccountClient(iot, store) // postgres.AppAccountStore satisfies tuya.AppAccountStore

// Semua resolve owner→uid + ownership guard ditangani tuya.AppAccountClient
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

Pintu spatial dirangkai sama persis, cuma store-nya beda. Tenant = satu root space; semua
yang ada di subtree di bawahnya milik dia.

```go
tenants, err := postgres.NewSpaceStore(ctx, pool, postgres.WithAutoMigrate())
hotel := tuya.NewSpaceClient(iot, tenants) // iot yang sama, diterima sebagai tuya.SpaceIoT

_, err = tenants.Link(ctx, owner, rootSpaceID)

// id 0 = root milik tenant sendiri, tidak pernah root project
rooms, page, err := hotel.ChildSpaces(ctx, owner, 0, cloud.DirectChildren)
room, err := hotel.CreateSpace(ctx, owner, "Room 201", 0, "")
things, page, err := hotel.SpaceResources(ctx, owner, room, cloud.Subtree)

// device-nya dijaga assertDeviceOwned (telusur subtree), bukan SpaceContains
err = hotel.SendCommands(ctx, owner, deviceID, []cloud.DataPoint{{Code: "switch_1", Value: true}})
```

## Region

Tuya punya endpoint per data-center. `baseURL` di-inject saat `cloud.New()`:
`https://openapi.tuyaus.com` (Western America), `https://openapi-ueaz.tuyaus.com` (Eastern
America), `https://openapi.tuyaeu.com` (Central Europe), `https://openapi-weaz.tuyaeu.com`
(Western Europe), `https://openapi.tuyacn.com` (China), `https://openapi.tuyain.com` (India),
`https://openapi-sg.iotbing.com` (Singapore — perhatikan domainnya beda sendiri).

## Migrations

Migration runner custom — tidak pakai `golang-migrate`. Naming: `000N_deskripsi.up.sql` / `.down.sql`.
Hanya `.up.sql` yang dieksekusi; `.down.sql` disimpan untuk rollback manual.
Version tracking via tabel `tuya_schema_migrations`. Semua statement wajib `IF NOT EXISTS` / `IF EXISTS`.
Jangan pernah edit migration yang sudah di-commit.

## Design Decisions

(Rasional publik yang lebih lengkap ada di README, section "Design rationale".)

- **Token app-level di-cache in-memory, tanpa store.** Kredensial Tuya bersifat project-wide, bukan
  per-user. `cloud.Client` me-refresh sendiri. Tidak perlu token store.
  **Dua jalur refresh, dan jangan disatukan.** `ensureValidToken` (lazy, sebelum request) percaya
  pada expiry yang di-cache. `forceRefreshToken` (reaktif, saat Tuya balas 1010) mengabaikan expiry
  itu — Tuya adalah otoritas atas token yang dia terbitkan, dan dia bisa menolak token yang menurut
  jam kita masih hidup (kredensial dirotasi, clock skew). Menyatukan keduanya terlihat seperti
  cleanup tapi diam-diam mematikan retry: jalur reaktif akan return nil tanpa melakukan apa pun,
  lalu request diulang dengan token yang barusan ditolak. Ada test di `cloud/client_test.go`
  yang menjaga ini (dan sudah diverifikasi gagal pada perilaku lama).
- **Layout: pintu owner-scoped di root, layer Tuya murni di `cloud/`.** Library ini "tool dipanggil
  AI agent", jadi surface utama = pintu agent (`tuya.AppAccountClient`) dan ia hidup di root. Layer trusted
  device-addressed (`cloud.Client`/`cloud.IoT`) didemot ke subpackage `cloud`. Ini mengikuti
  pola sibling `go.naturallyfunny.dev/spotify` (Client owner-keyed di root, inner client resolved
  disembunyikan). Tuya *berhak* mengekspos `cloud` standalone karena token project-level membuatnya
  berguna di trusted context tanpa user (beda dgn spotify yang per-user token wajib).
- **Ownership guard hidup di root, bukan `cloud.IoT`.** `cloud.IoT` adalah
  trusted, device-addressed layer tanpa tenant check — caller yang memegangnya bisa mengakses device
  manapun dalam project. `tuya.AppAccountClient` adalah pintu tunggal untuk agent: setiap call melaluinya
  harus resolve owner lebih dulu, dan ownership diverifikasi di `assertOwned` (lean, tanpa
  enrichment) sebelum command diteruskan. Guard ini *lebih kuat* di root: tidak bisa di-bypass
  tanpa melewati pintu itu.
- **`cloud.IoT` = satu method, satu endpoint. Aturan keras.** Kalau sebuah method tidak bisa
  ditunjuk ke tepat satu endpoint Tuya, dia sedang menyusun perilaku, dan penyusunan itu milik
  pemanggil yang menginginkannya. Dua hal pernah melanggar ini dan sudah dipindah ke root:
  `HasDevice` (lahir khusus untuk melayani `assertOwned` — layer bawah dibentuk kebutuhan layer
  atas) dan `enrichDevices` (menyimpan heuristik kategori `kg`/`cz*`, yaitu opini tentang
  katalog Tuya, bukan fakta yang dinyatakan API-nya). Kalau nanti tergoda menambah `HasX` atau
  sebuah "list yang sekalian di-enrich" di `cloud`, itu tanda komposisinya yang harus dibangun
  di root dari primitif yang sudah ada.
  Batasnya berlaku untuk **`IoT`**, bukan `cloud.Client`: transport tetap boleh punya kebijakan
  (refresh token, retry-on-1010) karena itu kebenaran protokol, bukan komposisi domain.
- **Ownership check via list-then-contains, logikanya di root.** `tuya.AppAccountClient.assertOwned`
  memanggil `cloud.IoT.ListDevices(ctx, uid)` lalu meng-cek keanggotaan sendiri. Aman & sederhana
  untuk traffic rendah; caching ditunda sampai ada kebutuhan throughput nyata. Guard sengaja tidak
  memanggil `DeviceChannelNames` — dia butuh identitas, bukan label, dan label akan menambah satu
  request per device multi-gang di *setiap* call terjaga. Ada test yang menjaga ini.
- **Resolusi channel-name hidup di root** (`resolveChannelNames` + `isMultiGang` di
  app_account.go). Root `ListDevices` selalu me-resolve karena hasilnya dibaca manusia (butuh
  "Kitchen light", bukan "switch_1"); consumer yang memakai `cloud.IoT` langsung menyusun
  fan-out-nya sendiri kalau memang butuh.
- **Seam file: `cloud` per-domain, root + adapter per-model-tenancy.** Method `IoT` ditulis di
  file domainnya (cloud/device.go; nanti cloud/home.go, cloud/space.go), struct `IoT` +
  `NewIoT` di cloud/client.go. Root **tidak** mengikuti pola itu: sekali ada dua model
  tenancy, seam "tipe vs perilaku" berhenti berguna karena tiap pintu punya keduanya, dan
  yang benar-benar berbeda adalah guard-nya. Jadi satu file per pintu (app_account.go; nanti
  space.go), dengan iot.go hanya memegang yang dipakai bersama. Orang yang mengaudit tenancy
  membaca satu file. `postgres/` dan `firestore/` ikut aturan yang sama — makanya
  `app_account.go`, bukan `store.go`.
- **`ErrDeviceNotOwned` dan `ErrAccountNotLinked` hidup di root `tuya`.** Keduanya adalah konsep
  multi-tenant / owner-scoping, bukan konsep Tuya API — tempatnya di layer yang memiliki owner.
- **Empat concern dipisah dengan jelas.** `cloud.Client` transport; `cloud.IoT` device-addressed
  Tuya facade; `tuya.AppAccountClient` owner-scoped facade dengan ownership guard;
  `postgres.AppAccountStore` account mapping.
  Interface `AppAccountStore` dan `IoT` didefinisikan di root `tuya` (consumer), bukan di implementor —
  sesuai idiom Go "accept interfaces, return structs".
- **`cloud.Client.Do` adalah escape hatch publik** untuk endpoint Tuya yang belum dibungkus. Tidak ada
  ownership guard di sini — `Do` melewatinya. Tidak mengekspos `Do` ke caller tak-tepercaya (mis.
  agent) adalah tanggung jawab consumer.
- **Pintu spatial punya dua guard, karena biayanya dua kelas berbeda.** Menjaga *space*
  (`assertSpaceOwned`) cukup satu request: `SpaceContains(root, target)` — dan karena containment
  transitif, ia sekaligus menyelesaikan cascade pada delete (kalau `target` di dalam tenant, seluruh
  turunannya juga). Menjaga *device* (`assertDeviceOwned`) mahal: Tuya tidak punya endpoint "space
  mana yang memuat device ini" (sudah dicek: `GET /v2.0/cloud/thing/{device_id}` tidak membawa
  space/asset id sama sekali), jadi satu-satunya jalan adalah menelusuri resource seluruh subtree
  berhalaman. Asimetri ini disengaja dan tidak bisa dihilangkan dari sisi kita.
- **Device guard tetap hidup di library, bukan diserahkan ke consumer.** Alternatifnya pernah
  ditimbang: consumer yang sudah memirror struktur properti di databasenya bisa menjawab
  kepemilikan dengan satu query lokal, lebih murah. Tapi tanpa `DeviceStatus`/`SendCommands` di
  `SpaceClient`, agent yang cuma mau menyalakan lampu harus dipegangi `cloud.IoT` yang tanpa guard —
  persis yang README larang. Pintu yang tidak bisa menyalakan lampu bukan pintu.
- **`Scope` argumen wajib, bukan option.** `only_sub` menentukan kedalaman listing, dan default
  Tuya untuknya tidak terdokumentasi. Listing yang diam-diam salah kedalaman adalah bahan baku
  guard yang salah, jadi pemanggil harus menyebut `cloud.DirectChildren` atau `cloud.Subtree`;
  nilai nol `Scope` ditolak. Ini juga alasan `only_sub` selalu dikirim eksplisit.
- **Query param & response: snake_case, sudah dibuktikan ke API sungguhan** (detail + cara
  ujinya di "Design Notes"). Jangan percaya contoh request/response di doc Tuya yang camelCase —
  itu salah, dan salah ejaan tidak menghasilkan error, cuma diam-diam pakai default server.
- **`result: false` diterjemahkan jadi error (`cloud.ErrNotApplied`) untuk modify & delete.**
  `Do` mengembalikan `result` mentah begitu `success: true`, jadi tanpa ini "terhapus" bisa berarti
  tidak terhapus. Untuk `SpaceContains` boolean-nya justru datanya, jadi `false` dikembalikan apa
  adanya.
- **`RootSpaces` sengaja tidak masuk interface `SpaceIoT`.** Endpoint `child` tanpa `space_id`
  mengembalikan root seluruh cloud project — semua tenant. Ia ada di `cloud` (trusted) sebagai
  method tersendiri supaya `id` 0 tidak diam-diam berarti itu, dan tidak dapat dijangkau dari
  pintu owner-scoped karena interface-nya tidak menyebutnya.
- **Root space tenant tidak bisa dihapus lewat pintu** (`ErrRootSpaceProtected`). Tuya menghapus
  subspace bersama induknya, jadi menghapus root = menghapus seluruh tenancy dan menyisakan mapping
  yang menggantung. Rename root tetap boleh.

## Design Notes (catatan yang mudah salah baca)

Beberapa keputusan sengaja melawan idiom generik. Rasional lengkap ada di README ("Design rationale");
di bawah ini hanya penanda cepat + satu catatan sejarah yang tidak ada di README.

- **`Do(...body []byte)` bukan `io.Reader`** — signing wajib `SHA256(body)` sebelum kirim, dan retry-on-1010
  me-replay body; `io.Reader` sekali-pakai tidak bisa di-replay. `[]byte` adalah tipe yang jujur di sini.
- **`cloud` mengekspor concrete, consumer yang mendeklarasikan interface** (`tuya.IoT`) — "accept interfaces,
  return structs". Mocking adalah concern consumer; `cloud` tidak menanggung interface spekulatif.
- **`resolveChannelNames` (root) pakai `sync.WaitGroup.Go` + `errors.Join`, bukan `errgroup`** — semantiknya collect-all
  (kumpulkan semua error device), sedang `errgroup.WithContext` fail-fast (batalkan saat error pertama).
  Kontrak berbeda; errgroup di sini adalah perubahan perilaku, bukan cleanup.
- **Sejarah: ownership guard sudah pindah ke root sejak refactor Juni 2026.** `cloud.IoT.DeviceStatus`/
  `SendCommands` kini device-addressed murni tanpa `tuyaUID` dan tanpa ownership check; guard hidup di
  `assertOwned` di root (kini `app_account.go`). Kalau menelusuri ownership,
  mulai dari root, bukan `cloud`.
- **Sejarah: `cloud.IoT` dibersihkan jadi 1:1-dengan-endpoint pada Agustus 2026.** `HasDevice`
  dihapus (guard-nya jadi list-then-contains di root) dan `enrichDevices` dipindah ke root
  sebagai `resolveChannelNames`; `listDevices` yang private dipromosikan jadi `ListDevices`, dan
  `DeviceChannelNames` ditambahkan sebagai pembungkus `multiple-names`. Sebuah `ListOption`/
  `WithChannelNames` sempat ada di antara dua langkah itu — ia gugur sendiri begitu enrichment
  keluar dari `cloud`, karena tidak ada lagi yang perlu di-opt-out. Kalau menemukan referensi ke
  nama-nama lama di luar repo ini, itu sisa versi sebelumnya.
- **Sejarah: seluruh surface app-account diberi nama model tenancy-nya, Agustus 2026.** Nama
  lama → baru: `tuya.Client` → `tuya.AppAccountClient`, `tuya.New` →
  `tuya.NewAppAccountClient`, `tuya.Account` → `tuya.AppAccount`, `tuya.AccountStore` →
  `tuya.AppAccountStore`, `postgres.Store`/`firestore.Store` → `AppAccountStore` di kedua
  package, `NewAccountStore` → `NewAppAccountStore`. File `postgres/store.go` dan
  `firestore/store.go` → `app_account.go`; di root `client.go` + `device.go` → `tuya.go`
  (yang dipakai bersama) + `app_account.go`. `tuya.go` menyusul jadi `iot.go`, mengikuti
  interface `IoT` yang dipegangnya.
  Alasannya dua: memberi tempat pintu spatial (`SpaceClient` + `SpaceStore`, yang kemudian
  benar-benar datang) — begitu ada dua, `Client`/`Store` polos tidak lagi memberitahu yang
  mana — dan menghapus ambiguitas "account" antara akun app dan akun project Tuya.
  Method-nya tetap `Account()`, bukan `AppAccount()`: receiver sudah membawa "App", dan
  `app.AppAccount(...)` stutter. `postgres.AppAccountStore` sengaja senama dengan interface
  `tuya.AppAccountStore`; tabrakan hanya ada di prosa, di kode selalu ada kualifikasi package.
  Ini breaking change, rilis v0.7.0. Referensi ke nama-nama lama di luar repo ini adalah sisa
  versi ≤ v0.6.x — `go.naturallyfunny.dev/agentkit` salah satunya (pakai `tuya.Client` dan
  `tuya.Account` di `tuya/adk/toolset.go`), dan perlu disesuaikan saat versinya dinaikkan.
- **Sejarah: pintu spatial masuk Agustus 2026, v0.8.0.** `cloud/space.go` + `space.go` di root +
  `space.go` di kedua adapter + migration `000002_space_tenants`. Dua hal ikut berubah dan
  memutus kompatibilitas: `postgres.Option` sekarang `func(*options)` (bukan
  `func(*AppAccountStore)`) supaya `WithAutoMigrate` melayani dua store, begitu juga
  `firestore.Option`; dan `firestore.DefaultCollection` → `DefaultAppAccountCollection`,
  berdampingan dengan `DefaultSpaceCollection`. Pemakaian `postgres.WithAutoMigrate()` /
  `firestore.WithCollection(...)` di call site tidak berubah.
- **Kontradiksi doc Tuya sudah diuji ke API sungguhan (DC Singapore, 7 Agustus 2026).** Hasilnya,
  dan ini yang dipakai kode sekarang:
  1. **Query param = snake_case.** `page_size=3` mengembalikan 3 baris; `pageSize=3` **diabaikan
     diam-diam** dan server pakai default (`page_size: 200`). Tabel doc benar, contoh request di
     doc salah. Ini persis alasan tidak boleh menebak: salah ejaan tidak error, cuma diam.
  2. **Response = snake_case** (`res_id`, `res_type`, `last_row_key`, `page_size`, `id`,
     `root_id`). Contoh camelCase di doc salah. Struct tag biasa sudah cukup.
  3. **Space ID datang sebagai number**, bukan string — `"1500****"` di doc kemungkinan artefak
     masking. `SpaceID` tetap menerima keduanya (murah, satu method) tapi sekarang jelas mana
     yang normal.
  4. **Halaman terakhir = `data: []` dan field `last_row_key` hilang sama sekali** (jadi
     ter-decode 0). `assertDeviceOwned` berhenti di situ, plus cursor macet, plus cap.
  5. **`relation` transitif** — `relation(root, cucu) = true`. Guard `assertSpaceOwned` sah.
- **Dua sifat `relation` yang tidak ada di doc dan mengubah kode.** `relation(X, X)` menjawab
  **false**: sebuah space bukan turunan dirinya sendiri. Jadi short-circuit `target == root` di
  `assertSpaceOwned` bukan optimisasi, tapi syarat kebenaran — tanpa itu tenant ditolak masuk ke
  root-nya sendiri. Dan space yang bukan milik project **tidak** dijawab `false` melainkan error
  `40001900 "No space permission"`; pintu menerjemahkannya jadi `ErrSpaceNotOwned` (lewat
  `cloud.APIError` + `cloud.CodeNoSpacePermission`) supaya janji `errors.Is(err,
  ErrSpaceNotOwned)` tetap berlaku untuk semua space yang tidak boleh disentuh.
- **Query param wajib urut ASCII, kalau tidak `1004 sign invalid`.** Tuya mengurutkan query
  sebelum memverifikasi signature. `?space_id=..&only_sub=..&page_size=..` gagal; params yang
  sama dalam urutan terurut berhasil. Semua query di `cloud` dibangun lewat `url.Values.Encode()`
  yang mengurutkan sendiri — jangan pernah merakit query string dengan tangan untuk `Do`.
- **Data center Singapore beda domain: `https://openapi-sg.iotbing.com`**, bukan pola
  `openapi.tuya*.com`. Salah data center tetap bisa terbitkan token, lalu semua endpoint isi
  ditolak `28841107 "data center is suspended"` — gejalanya mirip kredensial mati padahal
  base URL-nya yang salah.

## Conventions

- `cloud.New(...)` mengembalikan `*cloud.Client` (transport); `cloud.NewIoT(c)` membungkusnya jadi `*cloud.IoT` (facade domain)
- `tuya.NewAppAccountClient(iot, store)` mengembalikan `*tuya.AppAccountClient` (root, pintu owner-scoped model app-account); `iot` diterima sebagai interface `tuya.IoT`
- `tuya.NewSpaceClient(iot, store)` mengembalikan `*tuya.SpaceClient` (root, pintu owner-scoped model spatial); `iot` diterima sebagai interface `tuya.SpaceIoT`
- Nama pintu & store = nama model tenancy Tuya: `AppAccountClient`/`AppAccountStore` dan `SpaceClient`/`SpaceStore`. Jangan pakai nama generik `Client` atau `Store`.
- `AppAccount`, `ErrAccountNotLinked`, interface `AppAccountStore` di `app_account.go`; `SpaceTenant`, `ErrSpaceNotLinked`, `ErrSpaceNotOwned`, `ErrRootSpaceProtected`, interface `SpaceStore` & `SpaceIoT` di `space.go`; `ErrDeviceNotOwned` + interface `IoT` di `iot.go` karena dipakai kedua pintu
- `postgres.AppAccountStore` & `firestore.AppAccountStore` mengimplementasikan `tuya.AppAccountStore`, mengembalikan `tuya.AppAccount` (import root `tuya`). Keduanya punya `var _ tuya.AppAccountStore = (*AppAccountStore)(nil)` supaya drift ketahuan saat compile. `SpaceStore` di kedua package ikut pola yang sama.
- `postgres.NewAppAccountStore(ctx, db, opts...)` / `postgres.NewSpaceStore(ctx, db, opts...)` — terima `Querier` interface, bukan concrete `*pgxpool.Pool`
- `postgres.WithAutoMigrate()` — option untuk jalankan migration saat startup; runner-nya satu untuk semua tabel, jadi option ini di store mana pun menaikkan seluruh schema
- Space ID lewat `cloud.SpaceID`, tidak pernah `int64`/`any` telanjang (presisi + Tuya kirim number maupun string). Listing space/resource wajib menyebut `cloud.Scope`.
- Empat package: root `tuya` (owner-scoped) + `cloud/` (Tuya murni) + `postgres/` + `firestore/`; tidak ada `pkg/`
- Conventional commits: `feat:`, `fix:`, `chore(migrate):` dst
