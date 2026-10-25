# Media Center

Server streaming media pribadi. Satu binary Go, jalan di Docker, tanpa database.
Arahkan ke folder berisi film/serial — aplikasi memindai, membuat thumbnail, dan
menyajikannya lewat web player yang nyaman dipakai di desktop maupun mobile.

## Fitur

- **Scan otomatis** folder `library/` (rekursif). Folder = playlist otomatis.
- **Metadata via ffprobe**: durasi, resolusi, codec.
- **Thumbnail via ffmpeg**, dibuat malas (on-demand) lalu di-cache.
- **Streaming** file asli langsung (HTTP Range, bisa seek).
- **Subtitle** sidecar `.vtt` langsung, `.srt` dikonversi ke WebVTT otomatis.
- **Resume tonton** dan bar kemajuan berbasis `localStorage` (tanpa login).
- **Cari episode** dari nama berkas (`E01`, `E16`, …) untuk urutan playlist.
- **Meta & ikon** rapi: description, Open Graph/Twitter, canonical, favicon SVG/PNG,
  dan apple-touch-icon (halaman ditandai `noindex`).
- UI gelap modern, responsif, aman dari XSS / path traversal.

## Kebutuhan

- Docker + Docker Compose v2 (untuk menjalankan).
- Go 1.19+ (opsional, hanya untuk pengembangan lokal).
- `ffmpeg` + `ffprobe` (sudah termasuk di image Docker).

## Mulai cepat (Docker)

```bash
# 1. Taruh video di folder library/ (boleh sub-folder)
mkdir -p library
cp film.mp4 library/
# library/Nama Serial/E01.mp4  -> playlist "Nama Serial"

# 2. Jalankan
docker compose up -d --build

# 3. Buka
#    http://127.0.0.1:8080
```

Ubah berkas video kapan saja (tambah/hapus) — perubahan terdeteksi otomatis lewat
perbandingan `size:mtime`. Untuk memaksa prewarm metadata + poster sekaligus:

```bash
curl -X POST http://127.0.0.1:8080/rescan
```

## Konfigurasi

Semua lewat variabel lingkungan (lihat `docker-compose.yml`).

| Variabel         | Default      | Keterangan                                            |
|------------------|--------------|-------------------------------------------------------|
| `LIB_DIR`        | `./library`  | Folder sumber video.                                  |
| `CACHE_DIR`      | `./.cache`   | Folder cache metadata (`library.json`).               |
| `THUMB_DIR`      | `./.thumbs`  | Folder cache poster (`.jpg`).                         |
| `PORT`           | `8080`       | Port HTTP di dalam container.                         |
| `FF_CONCURRENCY` | jumlah CPU   | Batas proses ffmpeg/ffprobe paralel.                  |
| `RESCAN_TOKEN`   | kosong       | Bila diisi, `POST /rescan` wajib `?token=<nilai>`.    |

Di `docker-compose.yml` sudah ada limit resource (`deploy.resources`) dan binding
port. Sesuaikan dengan kebutuhan host.

## Format berkas

- **Video**: `mp4`, `mkv`, `webm`, `m4v`, `mov`, `avi`.
- **Subtitle sidecar**: nama sama dengan video, ekstensi `.srt` atau `.vtt`.
  Contoh: `E01.mp4` + `E01.srt`.
- Berkas tersembunyi (`._*`, `.*`) dan symlink diabaikan.

## Rute HTTP

| Rute                                   | Fungsi                                  |
|----------------------------------------|-----------------------------------------|
| `GET /`                                | Beranda: hero, playlist, semua video.   |
| `GET /playlist?pl=<id>`                | Daftar episode sebuah playlist.         |
| `GET /watch?id=<id>&pl=<id>`           | Pemutar video + navigasi prev/next.     |
| `GET /thumb?id=<id>`                   | Poster JPEG (fallback SVG bila gagal).  |
| `GET /subs?id=<id>`                    | Subtitle WebVTT (`.srt` dikonversi).    |
| `GET /library/<path>`                  | Berkas media asli (Range/seek).         |
| `GET /assets/*`                        | CSS & JS (ter-embed di binary).         |
| `POST /rescan`                         | Prewarm metadata + poster.              |

`<id>` adalah `base64url` dari path relatif terhadap `library/`.

## Struktur proyek

```
.
├── main.go             # server HTTP, routing, konfigurasi
├── media.go            # scan library, cache, ffprobe, thumbnail, subtitle
├── security.go         # validasi path, header keamanan, semaphore
├── render.go           # fungsi template + view model
├── web/                # template HTML (embed ke binary)
├── assets/             # style.css + app.js (embed ke binary)
├── Dockerfile          # multi-stage: build Go -> alpine + ffmpeg
├── docker-compose.yml
└── go.mod
```

Template dan aset di-`embed`, jadi image akhir hanya berisi binary + ffmpeg.

## Keamanan

Aplikasi **tanpa autentikasi** — batasi akses di level jaringan (bind ke IP privat
atau taruh di belakang reverse proxy). Yang sudah ditangani di kode:

- **XSS**: seluruh output lewat `html/template` (auto-escape); tanpa inline script.
- **CSP ketat**: `default-src 'self'` (tanpa `'unsafe-inline'`), `object-src 'none'`,
  `frame-ancestors 'none'`, plus `X-Content-Type-Options`, `X-Frame-Options`,
  `Referrer-Policy`, `Cross-Origin-Resource-Policy`.
- **Path traversal**: id di-decode ketat (`base64url`), ditolak `..`/absolut/NUL,
  hasil dipastikan tetap di dalam `LIB_DIR` setelah `EvalSymlinks` (symlink keluar
  ditolak).
- **Command injection**: `ffmpeg`/`ffprobe` dijalankan via `exec` dengan argumen
  terpisah (tanpa shell), memakai path absolut.
- **DoS**: jumlah proses ffmpeg dibatasi semaphore; `/rescan` hanya `POST` +
  cek same-origin.

## Pengembangan lokal

```bash
# butuh ffmpeg/ffprobe terpasang
go run . \
  LIB_DIR=./library CACHE_DIR=./.cache THUMB_DIR=./.thumbs PORT=8080

go build -o mediacenter .   # atau build manual
go vet ./...
```

Cache metadata disimpan di `.cache/library.json` dan divalidasi dengan `size:mtime`,
sehingga pemindaian ulang hanya memproses berkas yang berubah.
