# tuya

Module Go `go.naturallyfunny.dev/tuya` — reusable public library untuk integrasi Tuya Cloud OpenAPI.
Dirancang interface-based agar dapat dipakai lintas project, tidak terikat ke satu database atau satu aplikasi.

## Ruang Lingkup

Library ini **agnostic terhadap consumer**. Ia tidak tahu dan tidak boleh peduli dipakai untuk
apa — backend HTTP, worker batch, CLI, tool agent, semuanya sama saja. Tidak ada keputusan di
sini yang boleh bersandar pada tebakan siapa pemanggilnya atau seberapa sering ia memanggil.

Tugasnya satu: **memetakan sistem identitas developer (`owner`) ke identifier milik Tuya**
(Tuya UID, space ID, device ID). Tuya tidak kenal user consumer; library ini yang memegang
jembatannya, dan karena itu ia satu-satunya yang bisa menjawab pertanyaan seperti "device ini
ada di bawah identitas Tuya milik owner?".

**Ia menjawab, ia tidak memutuskan.** Tidak ada pintu di sini yang menolak sebuah device karena
tampak bukan milik owner. Alasannya bukan kelonggaran, tapi karena "bukan miliknya" tidak selalu
berarti salah: consumer yang membangun fitur share device memang menjangkau lintas akun, dan
guard wajib akan **memblokir consumer yang benar demi mengasuh yang ceroboh**. Kepemilikan
disajikan sebagai pertanyaan (`AppAccountClient.HasDevice`, `SpaceClient.ContainsSpace`,
`SpaceClient.ContainsDevice`) yang consumer olah dengan aturannya sendiri. Device ID yang datang
dari sumber ngawur adalah keputusan produk consumer, bukan urusan library.

Sisanya dibentuk oleh **kendala API Tuya** — signing wajib hash full-body, retry me-replay body,
query wajib urut ASCII, tidak ada lookup device→space. Ini fakta, bukan preferensi. Turunannya
satu aturan: **library menjawab pertanyaan identitas kalau Tuya memberi primitifnya; kalau tidak,
ia tidak mengarang satu dengan brute force lalu menagihkannya diam-diam.** `HasDevice` punya
primitif (`ListDevices`, satu request). `ContainsSpace` punya (`SpaceRelation`, satu request).
`ContainsDevice` tidak punya — makanya ia method tersendiri yang biayanya ditulis, bukan guard
yang jalan diam-diam di tiap perintah.

Konsekuensi yang mudah dilanggar: **biaya sebuah operasi adalah fakta yang harus ditulis, bukan
dibenarkan oleh asumsi "toh jarang dipanggil".** Kalau sebuah guard mahal, dokumentasikan
berapa mahal dan beri consumer jalan menguranginya — jangan menyimpulkan mahalnya tidak
apa-apa. Consumer yang memanggil ribuan kali per menit sama sahnya dengan yang memanggil
sekali per menit.

Sebelum menukar sebuah keputusan dengan idiom generik ("prefer X"), cek dulu apakah ia mengubah
semantik atau bertabrakan dengan dua hal di atas (mis. collect-all error vs fail-fast errgroup).

## Struktur

Root dipecah **per pintu**, bukan per jenis deklarasi. Sekali ada dua pintu, seam "tipe vs
perilaku" berhenti berguna karena tiap pintu punya keduanya; yang benar-benar berbeda adalah
cara tiap pintu memetakan owner ke identifier Tuya. Itu harus terbaca dalam satu file.

```
iot.go         (root, package tuya) Yang dipakai semua pintu: package doc (menyatakan library
               ini pemeta identitas dan bahwa ia menjawab, bukan memutuskan) + tipe Owner
               (identitas milik consumer, dipakai kedua pintu) + interface IoT
               (di-satisfy *cloud.IoT; cuma ListDevices + DeviceChannelNames — pintu
               app-account tidak lagi menyentuh device-addressed endpoint). Tidak ada pintu
               di sini.
app_account.go (root) Pintu app-account, utuh: AppAccount, ErrAccountNotLinked,
               AppAccountStore interface, struct AppAccountClient + NewAppAccountClient
               + Account() (receiver sudah bilang "App", method tidak mengulang), lalu
               ListDevices (resolve owner → list → resolveChannelNames), HasDevice
               (list-then-contains, mengembalikan bool — jawaban, bukan vonis),
               isMultiGang (heuristik kategori kg/cz*), resolveChannelNames (fan-out
               concurrent + errors.Join).
app_account_test.go
               (package tuya_test) unit test AppAccountClient lewat fake IoT + fake
               AppAccountStore: happy path + ErrAccountNotLinked, resolusi channel-name,
               HasDevice (device asing = false polos bukan error, error listing tidak
               menyamar jadi false), dan bukti HasDevice tidak ikut menembak multiple-names.
space.go       (root) Pintu spatial, utuh: Space, ErrSpaceNotLinked, ErrSpaceNotOwned,
               ErrOwnerSpaceProtected, interface SpaceStore + SpaceIoT, struct SpaceClient +
               NewSpaceClient + SpaceOf(), lalu operasi space-nya: CreateSpace/Space/
               ModifySpace/DeleteSpace/ChildSpaces/SpaceResources (resolve owner →
               assertSpaceOwned → delegate; ChildSpaces di sini nama pintu root, yang
               dipanggilnya cloud.ListSpaces), plus dua penjawab publik: ContainsSpace
               (satu request, SpaceRelation) dan ContainsDevice (scan resource seluruh
               subtree, berhalaman dan berbatas — deviceScanPageSize/deviceScanMaxPages).
               ID space 0 selalu berarti space milik owner, tidak pernah top level project.
space_test.go  (package tuya_test) unit test SpaceClient lewat fake SpaceIoT + fake
               SpaceStore: guard space + short-circuit sebelum operasi jalan, ID 0 = space
               owner (tanpa nanya Tuya), space owner tidak bisa dihapus, ContainsSpace/
               ContainsDevice mengembalikan false polos untuk yang di luar, dan bukti scan
               device berhenti — cursor macet maupun cursor yang terus maju (yang terakhir
               error, bukan false).
cloud/         Package cloud — layer Tuya murni, trusted, tanpa konsep owner. Sebagian besar
               device-addressed; hanya ListDevices yang butuh Tuya UID.
  client.go    cloud.Client (transport: token cache/refresh app-level, HMAC-SHA256 signing, Do
               dengan retry-on-1010) + IoT (facade, membungkus *Client) + NewIoT.
               Operasi domain menempel di IoT tapi DITULIS di file domain masing-masing.
  auth.go      Concern auth: request signing (signTokenRequest/signBusinessRequest, hmacSign,
               setAuthHeaders) + token lifecycle (token app-level grant_type=1, fetch/update/
               ensureValidToken). Tuya tanda-tangani token-request vs business-request berbeda.
  device.go    Domain device: tipe DeviceID + TuyaUID (identifier Tuya; wajib di sini, bukan
               root, karena cloud tidak boleh mengimpor root) + Device/DataPoint/Channel +
               empat method *IoT yang
               masing-masing satu endpoint: ListDevices (GET users/{uid}/devices),
               DeviceStatus, SendCommands, DeviceChannelNames (GET devices/{id}/
               multiple-names). Tanpa ownership, tanpa enrichment — keduanya milik root.
               Domain baru → file baru (home.go).
  space.go     Domain space: tipe SpaceID (terima JSON number MAUPUN string), Space,
               Resource, Page, Scope, ErrNotApplied, ErrSpaceNotFound, plus delapan method *IoT di atas tujuh
               endpoint /v2.0/cloud/space*: CreateSpace, Space, ModifySpace, DeleteSpace,
               SpaceResources, ListSpaces, SpaceRelation. ListSpaces satu banding satu dengan
               endpoint child: id 0 = space_id tidak dikirim = top level cloud project, persis
               seperti yang Tuya dokumentasikan. Tiap method menulis request-nya sendiri sampai
               selesai (tanpa helper bersama) supaya terbaca lurus dari atas ke bawah, sama
               seperti device.go. Tanpa guard, tanpa loop pagination. Satu-satunya helper yang
               tersisa: listQuery (only_sub + cursor + page size).
postgres/
  app_account.go
               App-account management (owner -> tuya_uid): AppAccountStore.Get/Link/Unlink,
               NewAppAccountStore, migrate. Mengembalikan tuya.AppAccount /
               tuya.ErrAccountNotLinked (import root tuya). Memegang juga yang dipakai kedua
               store: Querier, Option/WithAutoMigrate, prepareSchema, migrate.
  space.go     Hal yang sama untuk model spatial (owner -> space_id): SpaceStore.
               Get/Link/Unlink, NewSpaceStore. space_id disimpan bigint dan di-scan
               lewat int64 sebelum jadi cloud.SpaceID — kolomnya milik driver, tipe
               bernamanya milik domain.
  migrations/  SQL files, di-embed via //go:embed (000001 tuya_app_accounts,
               000002 tuya_spaces; keduanya soft-delete deleted_at)
firestore/
  app_account.go
               Hal yang sama di atas Cloud Firestore: NewAppAccountStore(client,
               opts) — satu dokumen per owner (doc ID = owner, koleksi tuya_app_accounts,
               override via WithCollection), soft-delete deleted_at, Link/Unlink transactional,
               timestamp client-side agar Link bisa return AppAccount tanpa re-read. Tanpa migrasi.
               Memegang juga yang dipakai bersama: Option/WithCollection, collectionOr,
               validateOwner.
  space.go     SpaceStore di atas Firestore, koleksi tuya_spaces
               (DefaultSpaceCollection), pola dokumen & transaksi identik dengan
               app_account.go.
```

Nama file store = nama pintunya juga, bukan `store.go`. Model spatial sudah masuk, jadi
tiap package adapter punya `space.go` di sebelah `app_account.go`; `store.go` yang polos
memang tidak punya tempat untuk itu.

Dependency direction acyclic: **postgres → tuya → cloud**.

## Cara Pakai

Tiga tier yang dirangkai consumer: `cloud.Client` (transport), `cloud.IoT` (facade
operasi domain berbasis device/uid/space), dan dua pintu owner-scoped di root yang memegang
ownership guard — `tuya.AppAccountClient` dan `tuya.SpaceClient`. Kalau pemanggilnya menyebut
owner, pakai salah satu pintu root; yang mana tergantung model device-nya.

Namanya menyebut **model device Tuya**, bukan sekadar verbose. Tuya punya dua: app-account
(tiap manusia punya akun app sendiri, di-link ke cloud project, batasnya Tuya UID, butuh
`AppAccountStore` — satu project boleh me-link berapa pun akun dan bisa unlink kapan saja,
akunnya milik orangnya) dan spatial (device tinggal di pohon space milik cloud project itu
sendiri, tanpa akun app sama sekali; pohon itu tidak di-link — satu project punya tepat satu
dan terikat mati padanya, butuh `SpaceStore`). Keduanya sudah dibungkus.

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

// Identitas consumer masuk lewat konversi eksplisit; itu titik jembatannya
owner := tuya.Owner(userID)

// Resolve owner→uid + channel-name ditangani tuya.AppAccountClient
devices, err := client.ListDevices(ctx, owner)

// Kepemilikan itu pertanyaan. Consumer yang memutuskan artinya.
ok, err := client.HasDevice(ctx, owner, deviceID)
if !ok && !myShareRules.Allow(owner, deviceID) {
    return ErrForbidden // punya consumer, bukan punya library
}
err = iot.SendCommands(ctx, deviceID, []cloud.DataPoint{{Code: "switch_1", Value: true}})
```

Perintah device dikirim lewat `cloud.IoT` — ia device-addressed dan tidak butuh uid, jadi tidak
ada yang bisa diresolusi pintu root di situ. Kalau consumer memang tidak butuh scoping owner
sama sekali (trusted context), `cloud.IoT` dipakai langsung tanpa pintu:

```go
acc, err := store.Get(ctx, owner)
devices, err := iot.ListDevices(ctx, acc.TuyaUID) // uid-addressed
status, err := iot.DeviceStatus(ctx, deviceID)    // device-addressed
```

Pintu spatial dirangkai sama persis, cuma store-nya beda. Tiap owner di-link ke satu space;
semua yang ada di subtree di bawahnya miliknya.

```go
spaces, err := postgres.NewSpaceStore(ctx, pool, postgres.WithAutoMigrate())
hotel := tuya.NewSpaceClient(iot, spaces) // iot yang sama, diterima sebagai tuya.SpaceIoT

_, err = spaces.Link(ctx, owner, spaceID)

// id 0 = space milik owner sendiri, tidak pernah top level project
// cloud.Page{} = halaman pertama; halaman yang dikembalikan diumpankan balik untuk berikutnya
rooms, next, err := hotel.ChildSpaces(ctx, owner, 0, cloud.DirectChildren, cloud.Page{})
room, err := hotel.CreateSpace(ctx, owner, "Room 201", 0, "")
things, next, err := hotel.SpaceResources(ctx, owner, room, cloud.Subtree, cloud.Page{})

// ContainsDevice men-scan subtree — mahal, jadi ia method tersendiri yang consumer
// panggil sadar, bukan guard yang jalan diam-diam di tiap perintah
ok, err := hotel.ContainsDevice(ctx, owner, deviceID)
err = iot.SendCommands(ctx, deviceID, []cloud.DataPoint{{Code: "switch_1", Value: true}})
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
- **Layout: pintu owner-scoped di root, layer Tuya murni di `cloud/`.** Yang di root adalah yang
  memegang konsep owner — konsep milik library ini, bukan milik Tuya. Layer trusted
  device-addressed (`cloud.Client`/`cloud.IoT`) didemot ke subpackage `cloud`. Ini mengikuti
  pola sibling `go.naturallyfunny.dev/spotify` (Client owner-keyed di root, inner client resolved
  disembunyikan). Tuya *berhak* mengekspos `cloud` standalone karena token project-level membuatnya
  berguna di trusted context tanpa user (beda dgn spotify yang per-user token wajib).
- **Pintu root menjawab kepemilikan, tidak menegakkannya.** Ini pernah sebaliknya: `DeviceStatus`/
  `SendCommands` ada di kedua pintu dan menolak device yang tidak lolos guard. Dibongkar Agustus
  2026 karena tiga hal. **(a)** Guard wajib memblokir consumer yang benar — fitur share device
  lintas akun itu sah, dan device yang di-share memang tidak ada di listing owner; consumer itu
  jadi terpaksa turun ke `cloud.IoT` dan sekalian kehilangan resolusi owner, satu-satunya hal
  yang ia butuh dari library. **(b)** Device ID yang datang dari sumber ngawur adalah keputusan
  produk consumer; library tidak perlu mengasuhnya. **(c)** Setelah guard-nya dicabut, `owner` di
  method device-addressed tidak meresolusi apa pun — jadi method-nya ikut dicabut, bukan
  dipertahankan dengan parameter yang diam. Sekarang: `HasDevice`/`ContainsSpace`/`ContainsDevice`
  mengembalikan `bool`, consumer merangkai kebijakannya sendiri, perintah device lewat `cloud.IoT`.
  Kalau nanti tergoda menambahkan lagi "pintu yang aman", ingat bahwa yang dibeli bukan keamanan
  melainkan larangan buat consumer yang use case-nya tidak kita bayangkan.
- **`cloud.IoT` = satu method, satu endpoint. Aturan keras.** Kalau sebuah method tidak bisa
  ditunjuk ke tepat satu endpoint Tuya, dia sedang menyusun perilaku, dan penyusunan itu milik
  pemanggil yang menginginkannya. Dua hal pernah melanggar ini dan sudah dipindah ke root:
  `cloud.HasDevice` (lahir khusus untuk melayani guard di root — layer bawah dibentuk kebutuhan
  layer atas; nama itu kini dipakai di root, tempat pemetaan owner memang hidup) dan
  `enrichDevices` (menyimpan heuristik kategori `kg`/`cz*`, yaitu opini tentang
  katalog Tuya, bukan fakta yang dinyatakan API-nya). Kalau nanti tergoda menambah `HasX` atau
  sebuah "list yang sekalian di-enrich" di `cloud`, itu tanda komposisinya yang harus dibangun
  di root dari primitif yang sudah ada.
  Batasnya berlaku untuk **`IoT`**, bukan `cloud.Client`: transport tetap boleh punya kebijakan
  (refresh token, retry-on-1010) karena itu kebenaran protokol, bukan komposisi domain.
- **`HasDevice` via list-then-contains, logikanya di root.** Ia memanggil
  `cloud.IoT.ListDevices(ctx, uid)` lalu meng-cek keanggotaan sendiri. Biayanya **satu request,
  flat** berapa pun jumlah device. Tidak di-cache: invalidasi butuh tahu kapan device ditambah,
  dicabut, atau di-relink, dan Tuya tidak mengabari satu pun dari itu — consumer yang tahu jauh
  lebih pantas nge-cache daripada library. `HasDevice` sengaja tidak memanggil
  `DeviceChannelNames` — dia butuh identitas, bukan label. Ada test yang menjaga ini.
  Device yang tidak ada di listing = `false` polos, **bukan** error; error hanya untuk lookup yang
  gagal, supaya "tidak ketemu" dan "tidak bisa mencari" tidak jadi jawaban yang sama.
- **Resolusi channel-name hidup di root** (`resolveChannelNames` + `isMultiGang` di
  app_account.go). Root `ListDevices` selalu me-resolve karena hasilnya dibaca manusia (butuh
  "Kitchen light", bukan "switch_1"); consumer yang memakai `cloud.IoT` langsung menyusun
  fan-out-nya sendiri kalau memang butuh.
- **Seam file: `cloud` per-domain, root + adapter per-pintu.** Method `IoT` ditulis di
  file domainnya (cloud/device.go; nanti cloud/home.go, cloud/space.go), struct `IoT` +
  `NewIoT` di cloud/client.go. Root **tidak** mengikuti pola itu: sekali ada dua pintu,
  seam "tipe vs perilaku" berhenti berguna karena tiap pintu punya keduanya, dan
  yang benar-benar berbeda adalah cara tiap pintu memetakan owner. Jadi satu file per pintu
  (app_account.go, space.go), dengan iot.go hanya memegang yang dipakai bersama. Orang yang
  menelusuri kepemilikan membaca satu file. `postgres/` dan `firestore/` ikut aturan yang sama —
  makanya `app_account.go`, bukan `store.go`.
- **`ErrAccountNotLinked` dan kerabat space-nya hidup di root `tuya`.** Semuanya konsep
  owner-scoping, bukan konsep Tuya API — tempatnya di layer yang memiliki owner.

- **Nama hanya boleh memuat kata yang kodenya sendiri cek atau lakukan.** Library ini tidak
  pernah memverifikasi sebuah space itu puncak apa pun, dan tidak pernah tahu owner itu
  "tenant" — yang dia tahu cuma: owner di-link ke satu space, dan apa pun di dalam space itu
  boleh disentuh. Karena itu `SpaceTenant`/`root_space_id` diganti `Space`/`space_id`, dan
  `ErrRootSpaceProtected` jadi `ErrOwnerSpaceProtected`. Verb `Link`/`Unlink` tetap, karena
  merekatkan owner ke space memang operasi yang store ini lakukan; yang dilarang adalah
  *noun* karangan seperti "space link", yang tidak menunjuk objek apa pun di Tuya. Tafsir
  bisnis consumer ("1 space = 1 klien hotel") boleh hidup di README sebagai contoh pemakaian,
  tidak pernah di identifier, nama tabel, atau nama kolom. Tes untuk nama baru: adakah kode
  yang mengecek klaim yang dibawa nama itu?
- **Empat concern dipisah dengan jelas.** `cloud.Client` transport; `cloud.IoT` device-addressed
  Tuya facade; `tuya.AppAccountClient` pemetaan owner→uid + penjawab kepemilikan;
  `postgres.AppAccountStore` account mapping.
  Interface `AppAccountStore` dan `IoT` didefinisikan di root `tuya` (consumer), bukan di implementor —
  sesuai idiom Go "accept interfaces, return structs".
- **`cloud.Client.Do` adalah escape hatch publik** untuk endpoint Tuya yang belum dibungkus. Tidak ada
  ownership guard di sini — `Do` melewatinya. Kepada siapa `Do` boleh dipegangkan adalah
  keputusan consumer.
- **Operasi space tetap menolak space di luar jangkauan owner — dan itu bukan pengecualian dari
  "menjawab, bukan memutuskan".** ID yang diterima method-method itu *owner-relative by
  construction*: 0 berarti space owner, dan pintu ini memang tidak punya cara mengekspresikan
  operasi atas space orang lain. Guard-nya (`assertSpaceOwned`) satu request lewat `SpaceRelation`,
  dan karena containment transitif ia sekaligus menyelesaikan cascade pada delete. Consumer yang
  punya aturan sendiri tetap bisa bertanya duluan lewat `ContainsSpace`, atau memakai `cloud.IoT`
  yang tanpa konsep owner.
- **Biaya `ContainsDevice` harus disebut angkanya, bukan diredakan.** Berhenti di match pertama,
  jadi kasus beruntung satu request; batas atasnya `deviceScanPageSize` × `deviceScanMaxPages`
  = 200 × 50 resource, dan halaman yang memuat device-nya tidak dijamin halaman pertama. Yang
  penting: biaya ini **tumbuh seiring besarnya subtree**, beda kelas dengan `HasDevice` yang flat.
  Justru karena mahal ia berupa method yang dipanggil sadar, bukan guard yang jalan diam-diam —
  consumer yang sudah memirror lokasi device di databasenya menjawab jauh lebih cepat dan memang
  sebaiknya begitu. Menyerah karena kena cap = **error**, bukan `false`: "tidak ketemu" dan
  "berhenti mencari" tidak boleh jadi jawaban yang sama buat consumer yang memutuskan di atasnya.
- **`Scope` argumen wajib, bukan option.** `only_sub` menentukan kedalaman listing, dan default
  Tuya untuknya tidak terdokumentasi. Listing yang diam-diam salah kedalaman adalah bahan baku
  guard yang salah, jadi pemanggil harus menyebut `cloud.DirectChildren` atau `cloud.Subtree`;
  nilai nol `Scope` ditolak. Ini juga alasan `only_sub` selalu dikirim eksplisit.
- **Query param & response: snake_case, sudah dibuktikan ke API sungguhan** (detail + cara
  ujinya di "Design Notes"). Jangan percaya contoh request/response di doc Tuya yang camelCase —
  itu salah, dan salah ejaan tidak menghasilkan error, cuma diam-diam pakai default server.
- **`result: false` diterjemahkan jadi error (`cloud.ErrNotApplied`) untuk modify & delete.**
  `Do` mengembalikan `result` mentah begitu `success: true`, jadi tanpa ini "terhapus" bisa berarti
  tidak terhapus. Untuk `SpaceRelation` boolean-nya justru datanya, jadi `false` dikembalikan apa
  adanya. `result` yang **tidak ada** diperlakukan sama dengan `false` — dan itu nyata: menanyakan
  space yang sudah dihapus dijawab `success:true` tanpa `result` sama sekali, jadi `Space`
  mengembalikan `ErrSpaceNotFound`, bukan error parser JSON.
- **`cloud.ListSpaces(ctx, 0, …)` mengembalikan top level seluruh cloud project, dan yang
  menahannya sekarang tes, bukan tipe.** Dulu ini dua method (`ChildSpaces` yang menolak id 0 +
  `RootSpaces` yang sengaja tidak masuk interface `SpaceIoT`), supaya pintu owner-scoped
  *tidak bisa menyebut* operasi seluruh-project. Digabung Agustus 2026 karena memang satu
  endpoint dan Tuya sendiri mendefinisikan "tanpa space_id = root directory". Yang hilang
  jaminan struktural, yang tersisa jaminan perilaku: `ownerSpace()` menolak owner tanpa link
  **dan** link yang space_id-nya 0 dengan `ErrSpaceNotLinked`, lalu `resolve()` memetakan id 0
  dari pemanggil jadi space owner — sehingga `target` yang dioper ke `ListSpaces` tidak pernah
  0. `TestTheDoorNeverListsTheWholeProject` di space_test.go yang menjaganya, dan sudah
  diverifikasi gagal kalau guard `space.SpaceID == 0` dicabut. Kalau nanti ada method baru di
  `SpaceClient` yang meneruskan space id ke `ListSpaces`, ia wajib lewat `resolve()`.
- **Space yang di-link owner tidak bisa dihapus lewat pintu** (`ErrOwnerSpaceProtected`). Tuya
  menghapus subspace bersama induknya, jadi menghapusnya = menghapus seluruh jangkauan owner dan
  menyisakan mapping yang menunjuk space yang sudah tidak ada. Rename tetap boleh.

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
- **Sejarah: guard kepemilikan device dihapus seluruhnya, Agustus 2026.** Jalurnya panjang:
  Juni 2026 ia dipindah dari `cloud` ke root (`assertOwned`), Agustus 2026 pintu spatial datang
  membawa `assertDeviceOwned`, lalu keduanya dibongkar bersama `DeviceStatus`/`SendCommands` di
  kedua pintu root. Penggantinya `HasDevice`/`ContainsSpace`/`ContainsDevice` yang mengembalikan
  `bool`. Alasan lengkapnya di Design Decisions ("Pintu root menjawab kepemilikan, tidak
  menegakkannya"); ringkasnya, guard wajib memblokir consumer yang benar (share device lintas
  akun) demi mengasuh yang ceroboh. Kalau menemukan referensi ke `tuya.ErrDeviceNotOwned` atau
  `SpaceClient.SendCommands` di luar repo ini, itu sisa versi sebelum v0.8.0.
- **Sejarah: `cloud.IoT` dibersihkan jadi 1:1-dengan-endpoint pada Agustus 2026.** `HasDevice`
  dihapus (guard-nya jadi list-then-contains di root) dan `enrichDevices` dipindah ke root
  sebagai `resolveChannelNames`; `listDevices` yang private dipromosikan jadi `ListDevices`, dan
  `DeviceChannelNames` ditambahkan sebagai pembungkus `multiple-names`. Sebuah `ListOption`/
  `WithChannelNames` sempat ada di antara dua langkah itu — ia gugur sendiri begitu enrichment
  keluar dari `cloud`, karena tidak ada lagi yang perlu di-opt-out. Kalau menemukan referensi ke
  nama-nama lama di luar repo ini, itu sisa versi sebelumnya.
- **Sejarah: seluruh surface app-account diberi nama modelnya, Agustus 2026.** Nama
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
  `space.go` di kedua adapter + migration `000002_spaces`. Dua hal ikut berubah dan
  memutus kompatibilitas: `postgres.Option` sekarang `func(*options)` (bukan
  `func(*AppAccountStore)`) supaya `WithAutoMigrate` melayani dua store, begitu juga
  `firestore.Option`; dan `firestore.DefaultCollection` → `DefaultAppAccountCollection`,
  berdampingan dengan `DefaultSpaceCollection`. Pemakaian `postgres.WithAutoMigrate()` /
  `firestore.WithCollection(...)` di call site tidak berubah.
- **Halaman itu argumen `cloud.Page`, bukan functional option.** Dulu `SpaceResources`/
  `ChildSpaces`/`RootSpaces` (waktu itu masih dua method) menerima `...PageOption` di atas
  struct dua field pointer. Pointer itu membedakan "tidak diset" dari nol — padahal di domain ini
  nol memang sudah berarti tidak ada, di kedua arah: `page_size=0` bukan permintaan yang berarti,
  dan `last_row_key=0` justru cara Tuya bilang tidak ada halaman lagi. Yang lebih menusuk: API
  **mengembalikan** `Page` tapi pemanggil tidak bisa menyerahkannya kembali — `ContainsDevice`
  membongkarnya lalu merakit ulang jadi options tiap iterasi. Sekarang satu tipe untuk dua arah,
  field nol = tidak dikirim, halaman yang dikembalikan bisa langsung diumpankan balik (setelah
  `LastRowKey != 0` dicek — nol berarti habis, bukan mulai dari awal). Breaking, v0.9.0.
- **Sejarah: keempat identifier jadi tipe bernama, Agustus 2026, v0.10.0.** `tuya.Owner` di
  `iot.go`, `cloud.TuyaUID` + `cloud.DeviceID` di `cloud/device.go`, melengkapi `cloud.SpaceID`
  yang sudah ada. Alasannya bukan kerapian: memetakan identitas consumer ke identifier Tuya
  adalah satu-satunya tugas library ini, dan sebelum ini `iot.ListDevices(ctx, acc.Owner)`
  (harusnya `acc.TuyaUID`) dan `HasDevice(ctx, deviceID, owner)` (tertukar) sama-sama kompilasi
  tanpa keluhan. Sekarang keduanya error saat build — sudah diverifikasi, bukan diasumsikan.
  Dua turunan yang mudah dibongkar ulang kalau lupa alasannya: **(a)** `cloud.Resource.ID`
  sengaja tetap `string`. `ContainsDevice` membandingkannya dengan device id lewat konversi di
  titik banding (`cloud.DeviceID(res.ID) == deviceID`); menjadikan field itu `DeviceID` akan
  mengklaim semua resource adalah device, padahal `ResourceType` ada justru karena tidak — dan
  nama yang mengklaim hal yang kodenya tidak cek adalah persis yang dilarang di aturan penamaan
  di atas. **(b)** Kolom DB tidak berubah; konversi terjadi di store, dan `postgres/app_account.go`
  dapat `scanAppAccount` supaya sebangun dengan `scanSpace` yang sudah ada. Referensi ke
  signature ber-`string` di luar repo ini adalah sisa versi ≤ v0.9.x — `agentkit` termasuk.
- **`SpaceID.MarshalJSON` dihapus karena hasilnya persis sama dengan default.** `type SpaceID
  int64` sudah di-marshal sebagai JSON number tanpa bantuan siapa pun, termasuk di field
  ber-`omitempty` (`omitempty` melihat nilai Go-nya, bukan hasil marshal). `UnmarshalJSON` tetap
  ada karena ia benar-benar bekerja: terima number MAUPUN string.
  `TestCreateSpaceOmitsTheZeroParent` yang menjaga bentuk body-nya tetap number telanjang.
- **`cloud/space.go` mengikuti gaya `cloud/device.go`: tiap method utuh dari path sampai decode.**
  `decodePage` (yang mengoper `any`), `assertApplied`, dan helper `childSpaces` dilebur ke
  pemanggilnya masing-masing — dua sampai tiga call site, dan hasilnya tiap method terbaca lurus
  tanpa lompat ke helper, dengan struct decode yang bertipe (bukan `any`) dan pesan error yang
  menyebut operasinya sendiri. `listQuery` sengaja tetap: ia memegang penolakan `Scope` nol, satu
  aturan yang tidak boleh punya tiga salinan yang bisa berbeda diam-diam.
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
     ter-decode 0). `ContainsDevice` berhenti di situ, plus cursor macet, plus cap.
  5. **`relation` transitif** — `relation(root, cucu) = true`. Guard `assertSpaceOwned` sah.
- **Dua sifat `relation` yang tidak ada di doc dan mengubah kode.** `relation(X, X)` menjawab
  **false**: sebuah space bukan turunan dirinya sendiri. Jadi short-circuit `target == root` di
  `assertSpaceOwned` bukan optimisasi, tapi syarat kebenaran — tanpa itu owner ditolak masuk ke
  space-nya sendiri. Dan space yang bukan milik project **tidak** dijawab `false` melainkan error
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
- Nama pintu & store = nama model device Tuya: `AppAccountClient`/`AppAccountStore` dan `SpaceClient`/`SpaceStore`. Jangan pakai nama generik `Client` atau `Store`.
- Penjawab kepemilikan mengembalikan `(bool, error)` dan namanya kata kerja bertanya — `HasDevice`, `ContainsSpace`, `ContainsDevice` — bukan `AssertX` atau `MustX`. Namanya harus menyatakan bahwa ia melaporkan, bukan memvonis. `false` = fakta; `error` hanya untuk pencarian yang gagal
- `AppAccount`, `ErrAccountNotLinked`, interface `AppAccountStore` di `app_account.go`; `Space`, `ErrSpaceNotLinked`, `ErrSpaceNotOwned`, `ErrOwnerSpaceProtected`, interface `SpaceStore` & `SpaceIoT` di `space.go`; interface `IoT` di `iot.go`
- `tuya.Space` (baris store: owner + space id) sengaja senama dengan `cloud.Space` (objek space Tuya yang lengkap) — sama seperti `postgres.AppAccountStore` vs `tuya.AppAccountStore`, di kode selalu ada kualifikasi package
- `postgres.AppAccountStore` & `firestore.AppAccountStore` mengimplementasikan `tuya.AppAccountStore`, mengembalikan `tuya.AppAccount` (import root `tuya`). Keduanya punya `var _ tuya.AppAccountStore = (*AppAccountStore)(nil)` supaya drift ketahuan saat compile. `SpaceStore` di kedua package ikut pola yang sama.
- `postgres.NewAppAccountStore(ctx, db, opts...)` / `postgres.NewSpaceStore(ctx, db, opts...)` — terima `Querier` interface, bukan concrete `*pgxpool.Pool`
- `postgres.WithAutoMigrate()` — option untuk jalankan migration saat startup; runner-nya satu untuk semua tabel, jadi option ini di store mana pun menaikkan seluruh schema
- Keempat identifier lewat tipe bernama, tidak pernah `string`/`int64`/`any` telanjang:
  `tuya.Owner` (milik consumer, di `iot.go`) dan `cloud.TuyaUID`/`cloud.DeviceID`/`cloud.SpaceID`
  (milik Tuya, di `cloud`). `SpaceID` punya alasan tambahan (presisi + Tuya kirim number maupun
  string); tiga sisanya supaya menukar owner dengan uid atau owner dengan device id gagal saat
  kompilasi — itu inti pekerjaan library ini, jadi compiler yang mengeceknya. Konversi eksplisit
  di call site consumer memang tujuannya. Listing space/resource wajib menyebut `cloud.Scope`.
- Tipe bernama berhenti di tepi driver: kolom DB tetap `text`/`bigint`, argumen query dan hasil
  scan dikonversi di store (`string(owner)`, `tuya.Owner(owner)`) — persis pola `int64(spaceID)`
  yang sudah ada. Firestore beda: doc struct-nya memang wire format, jadi ia memegang tipe
  bernama langsung (`cloud.SpaceID`, `cloud.TuyaUID`), dan yang dikonversi cuma doc ID.
- Empat package: root `tuya` (owner-scoped) + `cloud/` (Tuya murni) + `postgres/` + `firestore/`; tidak ada `pkg/`
- Conventional commits: `feat:`, `fix:`, `chore(migrate):` dst
