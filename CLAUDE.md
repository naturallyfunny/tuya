# tuya

Module Go `go.naturallyfunny.dev/tuya` — library publik untuk Tuya Cloud OpenAPI. Tugasnya satu: **memetakan identitas developer (`owner`) ke identifier Tuya** (Tuya UID, space ID,
device ID). **Ia menjawab, ia tidak memutuskan** — kepemilikan disajikan sebagai pertanyaan
(`HasDevice`/`ContainsSpace`/`ContainsDevice` → `bool`), bukan guard yang menolak; guard wajib
memblokir consumer yang benar, karena share device lintas akun itu sah. `false` = fakta, `error`
hanya untuk pencarian yang gagal. **Agnostic terhadap consumer**: tidak ada keputusan yang boleh
bersandar pada tebakan siapa pemanggilnya atau seberapa sering. Turunannya, **biaya sebuah operasi
adalah fakta yang harus ditulis** bukan diredakan dengan "toh jarang dipanggil"; dan library
menjawab pertanyaan identitas kalau Tuya memberi primitifnya, kalau tidak ia tidak mengarangnya
dengan brute force lalu menagihkannya diam-diam.

## Struktur

Root = layer Tuya murni: trusted, tanpa konsep owner. Tiap pintu owner-scoped punya package sendiri,
adapter store dipecah per backend. Dependency acyclic: **postgres/firestore → pintu → root**. Nama
file = nama pintunya, bukan `store.go`/`service.go` polos. Domain baru → file baru; method endpoint
ditulis di file domainnya, bukan di client.go.

```
client.go       Client: cache/refresh token, Do + retry-on-1010, signBusinessRequest (dekat tokenLock).
auth.go         hmacSign + auth header + token lifecycle; token-request ditandatangani tanpa token.
device.go       Tipe domain + method 1:1 endpoint. space.go idem (helper satu-satunya: listQuery).
appaccount/     Pintu app-account utuh: tipe, error, Store + Client interface, Service,
                + IsMultiGang (kategori kg/cz*, publik) dan resolveChannelNames (fan-out + errors.Join).
spatial/        Pintu spatial, pola sama: resolve → assertSpaceOwned → delegate.
                ID 0 = space owner, bukan top level project.
postgres/       app_account.go (+ Querier, Option/WithAutoMigrate, migrate), spatial.go,
                migrations/appaccount/ + migrations/spatial/ (//go:embed, satu folder per pintu)
firestore/      app_account.go (+ Option/WithCollection, validateOwner), spatial.go. Satu dokumen
                per owner (doc ID = owner), soft-delete, Link/Unlink transactional.
```

## Cara Pakai

Dua tier: `cloud.Client` (`Do` + method device/uid/space-addressed; perintah device lewat sini,
tak ada yang bisa diresolusi pintu) → dua pintu owner-scoped, tiap pintu satu package. Namanya
menyebut **model device Tuya**: app-account (tiap orang punya akun app, di-link ke project, batasnya
Tuya UID) dan spatial (device tinggal di pohon space milik project, satu project tepat satu pohon).

```go
c, err := cloud.New(accessID, accessSecret, baseURL)
app := appaccount.NewService(c, store) // postgres.NewAppAccountStore(ctx, pool, opts...)

devices, err := app.ListDevices(ctx, owner)    // resolve owner→uid + channel-name
ok, err := app.HasDevice(ctx, owner, deviceID) // pertanyaan, bukan vonis
if !ok && !myShareRules.Allow(owner, deviceID) {
    return ErrForbidden // punya consumer, bukan punya library
}
err = c.SendCommands(ctx, deviceID, []cloud.DataPoint{{Code: "switch_1", Value: true}})

// Pintu spatial sama, cuma store-nya beda. id 0 = space owner; onlySub true = anak langsung;
// cloud.Page{} = halaman pertama, halaman yang dikembalikan diumpankan balik.
hotel := spatial.NewService(c, spaces)
rooms, next, err := hotel.ChildSpaces(ctx, owner, 0, true, cloud.Page{})
ok, err = hotel.ContainsDevice(ctx, owner, deviceID) // mahal, lihat Design Decisions
```

## Region & Migrations

`baseURL` di-inject saat `tuya.New()`: `openapi.tuyaus.com` (W America), `openapi-ueaz.tuyaus.com`
(E America), `openapi.tuyaeu.com` (C Europe), `openapi-weaz.tuyaeu.com` (W Europe),
`openapi.tuyacn.com` (China), `openapi.tuyain.com` (India), **`openapi-sg.iotbing.com`** (Singapore
— domainnya beda sendiri). Salah data center tetap bisa terbitkan token, lalu semua endpoint isi
ditolak `28841107 "data center is suspended"` — gejalanya mirip kredensial mati.
Migration runner custom, bukan `golang-migrate`. `migrations/<pintu>/000N_deskripsi.up.sql`/`.down.sql`,
hanya `.up.sql` dieksekusi, key `tuya_schema_migrations` = `<pintu>/<file>`, wajib `IF NOT EXISTS`/
`IF EXISTS`, jangan pernah edit yang sudah di-commit. **Tiap store cuma menaikkan migrasi pintunya.**

## Design Decisions — yang mudah "dibersihkan" lalu rusak diam-diam

- **Dua jalur refresh token, jangan disatukan** (dijaga `cloud/client_test.go`). `ensureValidToken`
  (lazy) percaya expiry cache; `forceRefreshToken` (reaktif, saat Tuya balas 1010) mengabaikannya —
  Tuya otoritas atas tokennya. Disatukan = retry mati: reaktif return nil, token ditolak dipakai ulang.
- **`cloud.Client` = satu method satu endpoint, kecuali `Do`/token/retry — dijaga disiplin, bukan
  batas tipe.** Facade `cloud.IoT` dicabut: satu field `*Client`, tanpa kerja, dan `Do` tetap
  terjangkau pemiliknya — yang membatasi `Do` itu konvensi pintu menerima interface lokal. Lapisan
  tanpa pekerjaan tak punya nama jujur: `IoT`/`Service`/`API`/`Transport` sama-sama gagal. **Nama
  tipe = nama method; tipe device subset wire, identitas bukan presentasi**; komposisi (`HasX`,
  list-di-enrich) milik pintu, tak ada `cloud.Device` polos. Keep = identifier + `status`/flag yang
  jadi N request kalau dibuang; `local_key` rahasia.
- **Nama hanya boleh memuat kata yang kodenya cek atau lakukan.** Library tidak pernah tahu owner itu
  "tenant" atau space itu puncak apa pun. Tafsir bisnis consumer boleh di README, tidak pernah di
  identifier, tabel, atau kolom.
- **Wrapper tidak bikin vocabulary paralel.** Identifier Tuya dioper telanjang: `string` untuk
  owner/uid/device id, `int64` untuk space id. Sudah dicoba dan dicabut (v0.7.0) karena tiap
  pembelaannya runtuh saat dicek: `String()` di path URL sama saja dengan `%d` (`encoding/json`
  mengabaikan `fmt.Stringer`); untyped string constant otomatis dikonversi, jadi literal tertukar
  tetap lolos dan `go vet` diam; parameter posisional tidak bisa dilupakan, jadi guard nilai-nol
  cuma kena kasus sempit. **Tesnya: klaim sebuah tipe harus bertahan setelah dicek, bukan setelah
  diucapkan** — kosmetik tidak cukup. Pengecualian satu, `cloud.SpaceResourceType`: `const
  ResourceDevice = 0` terbaca "0 adalah sebuah resource device" padahal 0 itu *tipe*-nya, `res_type`
  milik Tuya, dan `Resource` cuma mengalir keluar. Prefiks `Space` wajib karena Tuya memakai istilah
  itu di beberapa modul dengan arti berbeda. Konstantanya cuma `SpaceResourceDevice = 0`, satu-satunya
  yang pernah terlihat di response — nilai lain (group/asset/scene) beredar di doc & jawaban LLM tapi
  **belum diverifikasi**; `res_type` tak dikenal toh ter-decode aman.
- **`HasDevice` = list-then-contains di root.** Satu request, flat berapa pun jumlah device. Tidak
  di-cache: invalidasi butuh tahu kapan device ditambah/dicabut/di-relink, Tuya tidak mengabari satu
  pun. Sengaja tidak minta `DeviceChannelNames` — butuh identitas, bukan label.
- **Biaya `ContainsDevice` disebut angkanya.** Berhenti di match pertama; batas atas
  `deviceScanPageSize` × `deviceScanMaxPages` = 200 × 50 resource. Biayanya **tumbuh seiring subtree**,
  beda kelas dari `HasDevice`. Menyerah karena kena cap = **error**, bukan `false`.
- **Operasi space menolak space di luar jangkauan owner — bukan pengecualian dari "menjawab, bukan
  memutuskan".** ID-nya owner-relative by construction; pintu ini tidak punya cara mengekspresikan
  operasi atas space orang lain. Guard `assertSpaceOwned` satu request via `SpaceRelation`, dan karena
  containment transitif ia sekaligus menyelesaikan cascade pada delete. Space yang di-link owner tidak
  bisa dihapus (`ErrOwnerSpaceProtected`): Tuya menghapus subspace bersama induknya. Rename boleh.
- **`cloud.ListSpaces(ctx, 0, …)` = top level seluruh project; yang menahannya tes, bukan tipe.**
  `ownerSpace()` menolak owner tanpa link dan link ber-space_id 0; `resolve()` memetakan id 0 jadi
  space owner, jadi `target` tidak pernah 0 — dijaga `TestTheDoorNeverListsTheWholeProject`.
  Method baru di `SpaceClient` yang meneruskan space id ke `ListSpaces` **wajib** lewat `resolve()`.
- **`result: false` → `cloud.ErrNotApplied` untuk modify & delete.** `Do` mengembalikan `result`
  mentah begitu `success: true`, jadi tanpa ini "terhapus" bisa berarti tidak terhapus. `result` yang
  tidak ada = `false` (nyata: space yang sudah dihapus dijawab `success:true` tanpa `result`, jadi
  `Space` → `ErrSpaceNotFound`). Untuk `SpaceRelation` boolean-nya justru datanya — query, bukan
  command. **`onlySub` juga argumen posisional, bukan option**: default Tuya untuk `only_sub` tidak
  terdokumentasi dan listing salah kedalaman adalah bahan baku guard yang salah.
- **`Do(...body []byte)` bukan `io.Reader`** — signing wajib `SHA256(body)` dan retry me-replay
  body; reader sekali-pakai tidak bisa di-replay. `Do` juga escape hatch publik untuk endpoint yang
  belum dibungkus, tanpa guard. Dan **`resolveChannelNames` pakai `sync.WaitGroup.Go` +
  `errors.Join`, bukan `errgroup`** — collect-all vs fail-fast, kontrak berbeda bukan cleanup.

## Fakta API — doc Tuya kontradiktif, ini hasil uji sungguhan (DC Singapore, 7–8 Agustus 2026)

1. **Query param selalu snake_case; casing response beda per endpoint.** `pageSize=3` **diabaikan
   diam-diam** (default 200) tanpa error. `thing/space/device` camelCase; device detail & `space/*` snake.
2. **Space ID biasanya number** (`int64` polos), tapi `bindSpaceId` di endpoint itu **string**.
3. **`name` beda arti antar keluarga device.** *thing* (detail, space/project): `name` = nama pabrik,
   `customName` = rename user. *app* (user list, home): `name` **itu** rename user. Detail di README.
4. **Halaman terakhir = `data: []` dan `last_row_key` hilang** (ter-decode 0). `ContainsDevice`
   berhenti di situ, plus deteksi cursor macet, plus cap.
5. **`relation` transitif** (`relation(root,cucu)=true`) jadi `assertSpaceOwned` sah; tapi
   **`relation(X,X)`=false**, jadi short-circuit `target == root` itu syarat kebenaran — tanpa itu
   owner ditolak dari space-nya sendiri. Space project lain **error** `40001900` → `spatial.ErrNotOwned`.
6. **Query param wajib urut ASCII**, kalau tidak `1004 sign invalid`. Semua query dibangun lewat
   `url.Values.Encode()` yang mengurutkan sendiri — jangan merakit query string dengan tangan.

## Conventions

- `tuya.New(...)` → `*tuya.Client`; `NewService(client, store)` tiap pintu terima `client` sebagai
  interface lokal, bukan implementor. `spatial.Space` sengaja senama `tuya.Space`.
- Nama package pintu = nama model device Tuya; di dalamnya nama tidak mengulang package-nya
  (`appaccount.Account`/`.Store`/`.ErrNotLinked`), tapi adapter tetap `AppAccountStore`/`SpaceStore`
  karena satu package menampung dua store. Penjawab kepemilikan `(bool, error)` dan namanya kata
  kerja bertanya — bukan `AssertX`/`MustX`.
- Adapter punya `var _ appaccount.Store = (*AppAccountStore)(nil)` supaya drift ketahuan saat
  compile; konstruktornya terima `Querier`, bukan `*pgxpool.Pool`. `WithAutoMigrate()` cuma menaikkan
  migrasi pintunya sendiri. Kolom tetap `text`/`bigint`, tanpa konversi.
- Adapter dipecah **per backend**, bukan per pintu — `appaccount/postgres` + `spatial/postgres` = dua
  package senama yang memaksa alias di tiap impor, dan `Querier`/`Option`/`migrate` kehilangan rumahnya.
- Conventional commits: `feat:`, `fix:`, `chore(migrate):` dst; tidak ada `pkg/`.
