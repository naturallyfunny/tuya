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

Root dipecah **per pintu**, bukan per jenis deklarasi. Dependency acyclic: **postgres → tuya →
cloud**. Nama file store = nama pintunya, bukan `store.go`. Domain baru → file baru; method `IoT`
ditulis di file domainnya, bukan di client.go.

```
iot.go          package doc + interface IoT. Tanpa pintu.
app_account.go  Pintu app-account utuh: tipe, error, store interface, client, + isMultiGang
                (heuristik kategori kg/cz*) dan resolveChannelNames (fan-out + errors.Join).
space.go        Pintu spatial utuh, pola sama: resolve → assertSpaceOwned → delegate.
                ID 0 = space owner, bukan top level project.
cloud/          Layer Tuya murni, trusted, tanpa konsep owner. client.go: transport (token
                cache/refresh, HMAC-SHA256, Do + retry-on-1010) + IoT facade. auth.go: signing
                (token-request vs business-request beda) + token lifecycle. device.go & space.go:
                tipe domain + method 1:1 endpoint (helper satu-satunya: listQuery).
postgres/       app_account.go (+ Querier, Option/WithAutoMigrate, migrate), space.go, migrations/
                (//go:embed: 000001 tuya_app_accounts, 000002 tuya_spaces)
firestore/      app_account.go (+ Option/WithCollection, validateOwner), space.go. Satu dokumen
                per owner (doc ID = owner), soft-delete, Link/Unlink transactional.
```

## Cara Pakai

Tiga tier: `cloud.Client` (transport) → `cloud.IoT` (facade device/uid/space-addressed) → dua pintu
owner-scoped di root. Namanya menyebut **model device Tuya**: app-account (tiap orang punya akun
app, di-link ke project, batasnya Tuya UID) dan spatial (device tinggal di pohon space milik
project; satu project tepat satu pohon). Prefix `App` memisahkan akun app dari akun project.
Perintah device lewat `cloud.IoT` — device-addressed, tidak ada yang bisa diresolusi pintu root.

```go
iot := cloud.NewIoT(transport) // cloud.New(accessID, accessSecret, baseURL)
client := tuya.NewAppAccountClient(iot, store) // postgres.NewAppAccountStore(ctx, pool, opts...)

devices, err := client.ListDevices(ctx, owner)    // resolve owner→uid + channel-name
ok, err := client.HasDevice(ctx, owner, deviceID) // pertanyaan, bukan vonis
if !ok && !myShareRules.Allow(owner, deviceID) {
    return ErrForbidden // punya consumer, bukan punya library
}
err = iot.SendCommands(ctx, deviceID, []cloud.DataPoint{{Code: "switch_1", Value: true}})

// Pintu spatial sama, cuma store-nya beda. id 0 = space owner; onlySub true = anak langsung;
// cloud.Page{} = halaman pertama, halaman yang dikembalikan diumpankan balik.
hotel := tuya.NewSpaceClient(iot, spaces)
rooms, next, err := hotel.ChildSpaces(ctx, owner, 0, true, cloud.Page{})
ok, err = hotel.ContainsDevice(ctx, owner, deviceID) // mahal, lihat Design Decisions
```

## Region & Migrations

`baseURL` di-inject saat `cloud.New()`: `openapi.tuyaus.com` (W America), `openapi-ueaz.tuyaus.com`
(E America), `openapi.tuyaeu.com` (C Europe), `openapi-weaz.tuyaeu.com` (W Europe),
`openapi.tuyacn.com` (China), `openapi.tuyain.com` (India), **`openapi-sg.iotbing.com`** (Singapore
— domainnya beda sendiri). Salah data center tetap bisa terbitkan token, lalu semua endpoint isi
ditolak `28841107 "data center is suspended"` — gejalanya mirip kredensial mati.
Migration runner custom, bukan `golang-migrate`. `000N_deskripsi.up.sql`/`.down.sql`, hanya `.up.sql`
yang dieksekusi, version tracking di `tuya_schema_migrations`, semua statement wajib `IF NOT EXISTS`/
`IF EXISTS`. Jangan pernah edit migration yang sudah di-commit.

## Design Decisions — yang mudah "dibersihkan" lalu rusak diam-diam

- **Dua jalur refresh token, jangan disatukan** (dijaga `cloud/client_test.go`). `ensureValidToken`
  (lazy) percaya expiry cache; `forceRefreshToken` (reaktif, saat Tuya balas 1010) mengabaikannya —
  Tuya otoritas atas tokennya. Disatukan = retry mati: reaktif return nil, token ditolak dipakai ulang.
- **`cloud.IoT` = satu method satu endpoint; nama tipe = nama method; tipe device subset wire —
  identitas, bukan presentasi.** Komposisi (`HasX`, list-yang-di-enrich) milik pemanggil, di root;
  tak ada `cloud.Device` polos (Fakta 3). Keep = identifier + `status`/flag yang jadi N request
  kalau dibuang + yang dibaca kode kita; `local_key` rahasia. `Client`: refresh/retry dikecualikan.
- **Nama hanya boleh memuat kata yang kodenya cek atau lakukan.** Library tidak pernah tahu owner itu
  "tenant" atau space itu puncak apa pun. Tafsir bisnis consumer boleh di README, tidak pernah di
  identifier, tabel, atau kolom.
- **Wrapper tidak bikin vocabulary paralel.** Identifier Tuya dioper telanjang: `string` untuk
  owner/uid/device id, `int64` untuk space id. Sudah dicoba dan dicabut (v0.10.0) karena tiap
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
   owner ditolak dari space-nya sendiri. Space project lain **error** `40001900` → `ErrSpaceNotOwned`.
6. **Query param wajib urut ASCII**, kalau tidak `1004 sign invalid`. Semua query dibangun lewat
   `url.Values.Encode()` yang mengurutkan sendiri — jangan merakit query string dengan tangan.

## Conventions

- `cloud.New(...)` → `*cloud.Client`; `cloud.NewIoT(c)` → `*cloud.IoT`; `tuya.NewAppAccountClient` /
  `tuya.NewSpaceClient` terima `iot` sebagai interface yang didefinisikan di root, bukan implementor
- Nama pintu & store = nama model device Tuya, jangan `Client`/`Store` polos. Penjawab kepemilikan
  `(bool, error)` dan namanya kata kerja bertanya — bukan `AssertX`/`MustX`.
- `tuya.Space` sengaja senama dengan `cloud.Space`, `postgres.AppAccountStore` dengan
  `tuya.AppAccountStore` — selalu ada kualifikasi package. `tuya.Device` embed `cloud.UserDevice`.
- Adapter punya `var _ tuya.AppAccountStore = (*AppAccountStore)(nil)` supaya drift ketahuan saat
  compile; konstruktornya terima `Querier`, bukan `*pgxpool.Pool`. `WithAutoMigrate()` menaikkan
  seluruh schema, dipanggil dari store mana pun. Kolom tetap `text`/`bigint`, tanpa konversi.
- Empat package: root `tuya` + `cloud/` + `postgres/` + `firestore/`, tidak ada `pkg/`. Conventional
  commits: `feat:`, `fix:`, `chore(migrate):` dst
