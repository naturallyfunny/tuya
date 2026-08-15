# tuya

Module Go `go.naturallyfunny.dev/tuya` — library publik untuk Tuya Cloud OpenAPI. Tugasnya satu:
**memetakan identitas developer (`owner`) ke identifier Tuya** (Tuya UID, space ID, device ID).
**Ia menjawab, ia tidak memutuskan** — kepemilikan disajikan sebagai pertanyaan
(`HasDevice`/`ContainsSpace`/`ContainsDevice` → `bool`), bukan guard yang menolak; guard wajib
memblokir consumer yang benar, karena share device lintas akun itu sah. `false` = fakta, `error`
hanya untuk pencarian yang gagal. **Agnostic terhadap consumer**: tidak ada keputusan yang boleh
bersandar pada tebakan siapa pemanggilnya atau seberapa sering. Turunannya, **biaya sebuah operasi
adalah fakta yang harus ditulis**; dan library menjawab pertanyaan identitas kalau Tuya memberi
primitifnya, kalau tidak ia tidak mengarangnya dengan brute force lalu menagihkannya diam-diam.

## Struktur

Root = layer Tuya murni: trusted, tanpa konsep owner. Tiap pintu owner-scoped punya package sendiri,
adapter store dipecah per backend. Dependency acyclic: **postgres/firestore → pintu → root**. Nama
file = nama pintunya, bukan `store.go` polos; domain baru → file baru, method endpoint di file
domainnya bukan di client.go.

```
client.go       Client + accessToken, refreshToken (satu jalur), Do + retry-on-1010, signBusinessRequest.
auth.go         hmacSign + auth header + fetch/decode token; token-request ditandatangani tanpa token.
device.go       Tipe domain + method 1:1 endpoint. space.go idem (helper satu-satunya: listQuery).
appaccount/     Pintu app-account utuh: tipe, error, Store + Client interface, Service, IsMultiGang
                (kategori kg/cz*, publik), resolveChannelNames (fan-out + errors.Join).
spatial/        Pintu spatial, pola sama: resolve → assertSpaceOwned → delegate. ID 0 = space owner.
postgres/       app_account.go (+ Querier, Option/WithAutoMigrate, migrate), spatial.go,
                migrations/appaccount/ + migrations/spatial/ (//go:embed, satu folder per pintu)
firestore/      app_account.go (+ Option/WithCollection, validateOwner), spatial.go. Satu dokumen
                per owner (doc ID = owner), soft-delete, Link/Unlink transactional.
```

## Cara Pakai

Dua tier: `tuya.Client` (`Do` + method device/uid/space-addressed; perintah device lewat sini, tak
ada yang bisa diresolusi pintu) → dua pintu owner-scoped, tiap pintu satu package, namanya menyebut
**model device Tuya**: app-account (batasnya Tuya UID) dan spatial (pohon space milik project).

```go
c, err := tuya.New(accessID, accessSecret, baseURL)
app := appaccount.NewService(c, store) // postgres.NewAppAccountStore(ctx, pool, opts...)

devices, err := app.ListDevices(ctx, owner)    // resolve owner→uid + channel-name
ok, err := app.HasDevice(ctx, owner, deviceID) // pertanyaan, bukan vonis; consumer yang memutuskan
err = c.SendCommands(ctx, deviceID, []tuya.DataPoint{{Code: "switch_1", Value: true}})

// Pintu spatial sama, cuma store-nya beda. id 0 = space owner; onlySub true = anak langsung;
// tuya.Page{} = halaman pertama, yang dikembalikan diumpankan balik.
hotel := spatial.NewService(c, spaces)
rooms, next, err := hotel.ChildSpaces(ctx, owner, 0, true, tuya.Page{})
ok, err = hotel.ContainsDevice(ctx, owner, deviceID) // mahal, lihat Design Decisions
```

## Region & Migrations

`baseURL` di-inject saat `tuya.New()`: `openapi.tuyaus.com` (W America), `openapi-ueaz.tuyaus.com`
(E America), `openapi.tuyaeu.com` (C Europe), `openapi-weaz.tuyaeu.com` (W Europe),
`openapi.tuyacn.com` (China), `openapi.tuyain.com` (India), **`openapi-sg.iotbing.com`** (Singapore —
domainnya beda sendiri). Salah DC tetap bisa terbitkan token, lalu semua endpoint isi ditolak
`28841107 "data center is suspended"` — gejalanya mirip kredensial mati.
Migration runner custom, bukan `golang-migrate`. `migrations/<pintu>/000N_deskripsi.up.sql`/`.down.sql`,
hanya `.up.sql` dieksekusi, key `tuya_schema_migrations` = `<pintu>/<file>`, wajib `IF NOT EXISTS`/
`IF EXISTS`, jangan pernah edit yang sudah di-commit. **Tiap store cuma menaikkan migrasi pintunya.**

## Design Decisions — yang mudah "dibersihkan" lalu rusak diam-diam

- **Satu jalur refresh token: `1010` dari Tuya, titik** (dijaga `client_test.go`). Jangan tambahkan
  cek expiry sebelum request — **pernah ada dan dicabut**: tidak menjamin apa pun (token bisa mati
  semili-detik setelah lolos cek, jadi jalur reaktif tetap wajib), hemat satu request per dua jam,
  ditagih `tokenLock.Lock()` **eksklusif di tiap `Do`**. `Client` cuma simpan `accessToken`; field
  lain tak dibaca dan mengundang cek itu tumbuh lagi.
- **`tuya.Client` = satu method satu endpoint, kecuali `Do`/token/retry** — dijaga disiplin, bukan
  batas tipe; facade `IoT` dicabut karena lapisan tanpa pekerjaan tak punya nama jujur, dan yang
  membatasi `Do` itu konvensi pintu menerima interface lokal. **Nama tipe = nama method**; tipe
  device subset wire (identitas bukan presentasi), komposisi milik pintu, tak ada `Device` polos.
  Keep = identifier + `status`/flag yang jadi N request kalau dibuang; `local_key` rahasia.
- **Nama hanya boleh memuat kata yang kodenya cek atau lakukan.** Library tidak pernah tahu owner itu
  "tenant" atau space itu puncak apa pun. Tafsir bisnis consumer boleh di README, tidak pernah di
  identifier, tabel, atau kolom.
- **Wrapper tidak bikin vocabulary paralel.** Identifier Tuya telanjang: `string` untuk
  owner/uid/device id, `int64` untuk space id. Dicoba dan dicabut di v0.7.0 — tiap pembelaannya
  runtuh saat dicek (`String()` diabaikan `encoding/json`; untyped constant tetap lolos tertukar;
  guard nilai-nol cuma kena kasus sempit). **Klaim sebuah tipe harus bertahan setelah dicek, bukan
  setelah diucapkan.** Pengecualian satu, `SpaceResourceType`: 0 itu *tipe*-nya, bukan "sebuah
  resource device"; prefiks `Space` wajib karena Tuya memakai istilah itu beda-beda antar modul.
  Cuma `SpaceResourceDevice = 0` yang pernah terlihat di response — nilai lain **belum diverifikasi**;
  `res_type` tak dikenal toh ter-decode aman.
- **`HasDevice` = list-then-contains di root.** Satu request, flat berapa pun jumlah device. Tak
  di-cache: invalidasi butuh tahu kapan device ditambah/dicabut/di-relink, Tuya tak mengabari satu pun.
  Sengaja tidak minta `DeviceChannelNames` — butuh identitas, bukan label.
- **Biaya `ContainsDevice` disebut angkanya.** Berhenti di match pertama; batas atas
  `deviceScanPageSize` × `deviceScanMaxPages` = 200 × 50 resource — **tumbuh seiring subtree**, beda
  kelas dari `HasDevice`. Menyerah karena kena cap = **error**, bukan `false`.
- **Operasi space menolak space di luar jangkauan owner**, bukan pengecualian dari "menjawab, bukan
  memutuskan": ID-nya owner-relative by construction, pintu ini tak punya cara menyebut space orang
  lain. `assertSpaceOwned` satu request via `SpaceRelation`; containment transitif jadi cascade delete
  ikut beres. Space yang di-link owner tak bisa dihapus (`ErrOwnerSpaceProtected`); rename boleh.
- **`tuya.ListSpaces(ctx, 0, …)` = top level seluruh project; yang menahannya tes, bukan tipe.**
  `ownerSpace()` menolak owner tanpa link dan link ber-space_id 0; `resolve()` memetakan id 0 jadi
  space owner, jadi `target` tidak pernah 0. Padanannya `tuyaUID()` menolak link ber-uid kosong —
  `/users//devices` bukan pertanyaan tentang siapa pun. Dijaga `TestTheDoorNeverListsTheWholeProject`
  + `TestTheDoorNeverAsksTuyaAboutAnEmptyUID`; method baru yang meneruskan space id ke `ListSpaces`
  **wajib** lewat `resolve()`.
- **`result: false` → `ErrNotApplied` untuk modify & delete.** `Do` mengembalikan `result` mentah
  begitu `success: true`, jadi "terhapus" bisa berarti tidak terhapus; `result` tidak ada = `false`
  (space terhapus → `success:true` tanpa `result` → `ErrSpaceNotFound`). Di `SpaceRelation` boolean-nya
  justru datanya. **`onlySub` posisional bukan option**: default Tuya `only_sub` tak terdokumentasi.
- **`Do(...body []byte)` bukan `io.Reader`** — signing wajib `SHA256(body)` dan retry me-replay body;
  reader sekali-pakai tak bisa di-replay. `Do` juga escape hatch publik tanpa guard. Dan
  **`resolveChannelNames` pakai `sync.WaitGroup.Go` + `errors.Join`, bukan `errgroup`** — collect-all
  vs fail-fast, kontrak berbeda bukan cleanup.

## Fakta API — doc Tuya kontradiktif, ini hasil uji sungguhan (DC Singapore, 7–8 & 15 Agustus 2026)

1. **Query param selalu snake_case; casing response beda per endpoint.** `pageSize=3` **diabaikan
   diam-diam** tanpa error. `thing/space/device` camelCase; device detail & `space/*` snake. Space ID
   biasanya number (`int64` polos), tapi `bindSpaceId`/`bind_space_id` **string**.
2. **`name` beda arti antar keluarga device.** *thing* (detail, space/project): `name` = nama pabrik,
   `customName` = rename user. *app* (user list, home): `name` **itu** rename user. Detail di README.
3. **Halaman terakhir `space/*` = `data: []` dan `last_row_key` hilang** (ter-decode 0).
   `ContainsDevice` berhenti di situ, plus deteksi cursor macet, plus cap.
4. **`relation` transitif** (`relation(root,cucu)=true`) jadi `assertSpaceOwned` sah; tapi
   **`relation(X,X)`=false**, jadi short-circuit `target == root` itu syarat kebenaran — tanpa itu
   owner ditolak dari space-nya sendiri. Space project lain **error** `40001900` → `spatial.ErrNotOwned`.
5. **Query param wajib urut ASCII**, kalau tidak `1004 sign invalid`. Semua query dibangun lewat
   `url.Values.Encode()` yang mengurutkan sendiri — jangan merakit query string dengan tangan.
6. **`thing/space/device` paging-nya lain sendiri.** Envelope cuma `success/t/tid/result` dan
   `result` array telanjang — **tak ada cursor untuk dikembalikan**, makanya tanpa `Page`. Cursornya
   `last_id` = id device terakhir, **eksklusif**; ngawur → `40000903`. Habis = **halaman kosong**,
   bukan halaman pendek (6 device @ 2 → 2,2,2,`[]`). `page_size` **wajib** (hilang → `1110`) dan
   maks 20 (`21` → `40000904`), karena itu `SpaceDevicePageSizeMax` dipakai saat argumennya 0.
7. **`is_recursion` tidak berefek**; `SpaceResources` justru transitif. Device ber-`bindSpaceId` X tak
   terlihat dari induk X maupun root, dan di X sendiri `true`/`false` sama saja — sementara
   `space/{id}/resource` melaporkan device milik cucunya. Itu pijakan `ContainsDevice`.

## Conventions

- `tuya.New(...)` → `*tuya.Client`; `NewService(client, store)` tiap pintu terima `client` sebagai
  interface lokal, bukan implementor. `spatial.Space` sengaja senama `tuya.Space`.
- Nama package pintu = nama model device Tuya; di dalamnya nama tidak mengulang package-nya
  (`appaccount.Account`/`.Store`/`.ErrNotLinked`), tapi adapter tetap `AppAccountStore`/`SpaceStore`
  karena satu package menampung dua store. Penjawab kepemilikan `(bool, error)`, namanya kata kerja
  bertanya — bukan `AssertX`/`MustX`.
- Adapter punya `var _ appaccount.Store = (*AppAccountStore)(nil)` supaya drift ketahuan saat compile;
  konstruktornya terima `Querier`, bukan `*pgxpool.Pool`. `WithAutoMigrate()` cuma menaikkan migrasi
  pintunya sendiri. Kolom tetap `text`/`bigint`, tanpa konversi.
- Adapter dipecah **per backend**, bukan per pintu — `appaccount/postgres` + `spatial/postgres` = dua
  package senama yang memaksa alias di tiap impor, dan `Querier`/`Option`/`migrate` kehilangan rumahnya.
- Conventional commits: `feat:`, `fix:`, `chore(migrate):` dst; tidak ada `pkg/`.
