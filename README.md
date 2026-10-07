# Omniroute WhatsApp Integration Service (GoWA / whatsmeow)

Layanan integrasi WhatsApp mandiri berbasis **Golang** menggunakan pustaka [whatsmeow](https://github.com/tulir/whatsmeow), terhubung ke **Omniroute AI API** dengan penyimpanan konteks riwayat percakapan otomatis pada database **PostgreSQL**.

Dibangun dengan arsitektur modular (*Clean Architecture / Standard Go Project Layout*), framework **Gin**, connection pool **pgxpool**, serta *Worker Pool* asinkron untuk menjamin responsivitas tinggi dan kehandalan di lingkungan produksi.

---

## 🌟 Fitur Utama

1. **Persistensi Sesi WhatsApp**:
   - Sesi terdaftar disimpan langsung di tabel internal PostgreSQL (`sqlstore`) atau file SQLite lokal.
   - Sesi tidak terputus saat aplikasi di-restart (*persistent login*).

2. **Dua Mode Tampilan QR Code**:
   - **Terminal Console**: Dicetak otomatis saat startup menggunakan representasi half-block UTF-8.
   - **Browser Web (`GET /qr`)**: Halaman dashboard HTML modern yang menampilkan gambar QR Code secara dinamis (base64 PNG) lengkap dengan *auto-refresh* dan polling status pairing secara otomatis. Tersedia juga endpoint gambar langsung di `GET /qr/image`.

3. **Manajemen Konteks Obrolan di PostgreSQL**:
   - Tabel `chat_histories` menyimpan riwayat percakapan (`user` & `assistant`).
   - Query mengambil `N` riwayat pesan terakhir dan mengurutkannya secara kronologis ascending untuk dimasukkan ke dalam prompt konteks AI.
   - Dilengkapi *composite index* `(phone_number, created_at DESC, id DESC)` untuk performa query sub-milidetik.

4. **Pemrosesan Asinkron (Worker Pool Pattern)**:
   - Event pesan masuk diterima via *event handler* whatsmeow, disaring dari pesan grup/broadcast dan pesan diri sendiri (*outbound*), lalu dimasukkan ke dalam antrean *buffered channel*.
   - Sekumpulan worker goroutine terkelola memproses obrolan, memanggil Omniroute AI, menyimpan ke DB, dan mengirimkan balasan kembali ke WhatsApp.

5. **Pemecahan Teks Pintar (*Smart Text Chunking*)**:
   - Pesan balasan AI yang melebihi batas WhatsApp (~4.000 karakter) dipecah secara cerdas berdasarkan paragraf (`\n\n`), baris baru (`\n`), akhir kalimat (`. `, `! `, `? `), atau spasi kata tanpa memotong kata atau karakter multi-byte/emoji.

6. **Graceful Shutdown**:
   - Menangani `SIGINT` dan `SIGTERM` secara tertib: menghentikan server HTTP Gin, menuntaskan tugas in-flight di worker pool (`sync.WaitGroup`), memutuskan koneksi WhatsApp, dan menutup PostgreSQL connection pool secara bersih.

---

## 🏗️ Struktur Direktori Proyek

```
omniroute-api-wa/
├── cmd/
│   └── api/
│       └── main.go                 # Entrypoint aplikasi & lifecycle orchestration
├── internal/
│   ├── config/
│   │   ├── config.go              # Parser & validator environment variable
│   │   └── config_test.go         # Unit test konfigurasi
│   ├── domain/
│   │   └── chat.go                # Entity domain, DTO Omniroute & WhatsApp
│   ├── repository/
│   │   └── postgres/
│   │       └── chat_repository.go # Query layer PostgreSQL (pgxpool)
│   ├── service/
│   │   ├── omniroute_service.go   # HTTP client integrasi Omniroute AI
│   │   ├── omniroute_service_test.go
│   │   └── gowa_service.go        # whatsmeow client, QR streamer, & sender
│   ├── delivery/
│   │   └── http/
│   │       ├── handler.go         # Gin controller (/health, /status, /qr)
│   │       └── middleware.go      # Logging, CORS, Panic Recovery
│   └── worker/
│       └── pool.go                # Bounded Worker Pool & pipeline AI
├── pkg/
│   └── textutil/
│       ├── chunker.go             # Helper pemecah string panjang WhatsApp
│       └── chunker_test.go        # Unit test text chunker
├── migrations/
│   └── 000001_create_chat_histories_table.up.sql # DDL skema DB PostgreSQL
├── docker-compose.yml              # Konfigurasi container PostgreSQL & App
├── Dockerfile                      # Multi-stage production build
├── .env.example                    # Template konfigurasi environment
├── go.mod
└── go.sum
```

---

## ⚙️ Variabel Lingkungan (.env)

> **Catatan:** Salin `.env.example` menjadi `.env` lalu isi nilai yang sesuai. File `.env` **tidak boleh** di-commit ke repositori (sudah terdaftar di `.gitignore`).

| Variabel | Tipe | Default | Wajib | Deskripsi |
|---|---|---|:---:|---|
| `APP_PORT` | String | `8080` | | Port HTTP Gin server. Menggunakan `APP_PORT` (bukan `PORT`) agar tidak bentrok dengan port default Omniroute CLI (`20128`) |
| `GIN_MODE` | String | `release` | | Mode Gin: `debug` atau `release` |
| `DB_HOST` | String | — | ✅ | Host server PostgreSQL |
| `DB_PORT` | String | — | ✅ | Port server PostgreSQL (biasanya `5432`) |
| `DB_USER` | String | — | ✅ | Username database PostgreSQL |
| `DB_PASSWORD` | String | — | ✅ | Password database PostgreSQL |
| `DB_NAME` | String | — | ✅ | Nama database PostgreSQL |
| `DB_SSLMODE` | String | `disable` | | SSL mode koneksi PostgreSQL (`disable`, `require`, `verify-full`) |
| `OMNIROUTE_API_BASE_URL` | String | — | ✅ | Endpoint base Omniroute AI. Port default Omniroute CLI: `http://localhost:20128/v1` |
| `OMNIROUTE_API_KEY` | String | — | | API Key autentikasi Omniroute (opsional jika tidak dikonfigurasi di server) |
| `OMNIROUTE_MODEL` | String | `auto` | | Nama model AI yang digunakan (`auto` untuk pemilihan otomatis) |
| `MAX_CONTEXT_MESSAGES` | Integer | `10` | | Jumlah pesan riwayat terakhir yang dikirim sebagai konteks ke AI |
| `WHATSAPP_SESSION_STORE` | String | `postgres` | | Media penyimpanan sesi WhatsApp: `postgres` atau `sqlite` |
| `WHATSAPP_SQLITE_PATH` | String | `whatsapp_session.db` | | Path file SQLite (hanya berlaku jika `WHATSAPP_SESSION_STORE=sqlite`) |
| `WORKER_POOL_SIZE` | Integer | `5` | | Jumlah goroutine worker pemrosesan pesan AI secara paralel |
| `WORKER_QUEUE_SIZE` | Integer | `100` | | Kapasitas antrean buffered channel untuk pesan masuk |

---

## 🚀 Panduan Menjalankan

### Prasyarat

- [Go](https://go.dev/dl/) 1.21+
- [Docker](https://www.docker.com/) & Docker Compose (untuk menjalankan PostgreSQL)
- Omniroute CLI berjalan di mesin lokal (default port `20128`)

### 1. Konfigurasi Environment

```bash
# Salin template konfigurasi
cp .env.example .env

# Edit .env dan isi nilai yang sesuai (DB_PASSWORD, OMNIROUTE_API_KEY, dll.)
```

### 2. Menjalankan Database PostgreSQL via Docker Compose

```bash
# Jalankan container PostgreSQL saja
docker compose up -d postgres

# Atau jalankan seluruh stack (PostgreSQL + App) sekaligus
docker compose up -d
```

> **Catatan:** Saat menjalankan `app` via Docker, `DB_HOST` otomatis di-set ke `postgres` (nama service dalam jaringan Docker). Pastikan `OMNIROUTE_API_BASE_URL` mengarah ke `http://host.docker.internal:20128/v1` agar container bisa mengakses Omniroute CLI di host.

### 3. Menjalankan Aplikasi Golang (Lokal)

```bash
# Unduh dependensi
go mod download

# Jalankan service
go run ./cmd/api/main.go
```

### 4. Autentikasi / Pairing WhatsApp

1. Saat service pertama kali berjalan, kode QR dicetak otomatis di terminal.
2. Buka browser dan akses:
   ```
   http://localhost:8080/qr
   ```
3. Pindai QR Code dari WhatsApp di smartphone Anda:
   - Buka **Pengaturan / Titik Tiga** → **Perangkat Tertaut** → **Tautkan Perangkat**.
   - Arahkan kamera ke kode QR yang tampil di browser atau terminal.
4. Setelah berhasil, halaman web otomatis memperbarui status menjadi **WhatsApp Terhubung** dan Anda akan diarahkan ke `/status`.

---

## 📡 Daftar Endpoint HTTP

| Metode | Endpoint | Deskripsi |
|---|---|---|
| `GET` | `/` | Redirect ke `/qr` atau `/status` sesuai status login |
| `GET` | `/health` | Health check komponen DB dan koneksi WhatsApp |
| `GET` | `/status` | Detail JSON status aplikasi, sesi JID, DB, dan konfigurasi |
| `GET` | `/qr` | Halaman Web HTML auto-refresh QR Code / Tampilan status terhubung |
| `GET` | `/qr?format=json` | Mengambil data mentah string QR dalam format JSON |
| `GET` | `/qr/image` | Mengambil gambar QR Code dalam format PNG murni |

---

## 🧪 Menjalankan Pengujian (Unit Tests)

```bash
go test -v ./...
```
Output:
```
=== RUN   TestConfigDefaults
--- PASS: TestConfigDefaults (0.00s)
=== RUN   TestConfigPostgresDSN
--- PASS: TestConfigPostgresDSN (0.00s)
=== RUN   TestOmnirouteService_GenerateResponse
--- PASS: TestOmnirouteService_GenerateResponse (0.00s)
=== RUN   TestOmnirouteService_FallbackResponse
--- PASS: TestOmnirouteService_FallbackResponse (0.00s)
=== RUN   TestChunkTextShort
--- PASS: TestChunkTextShort (0.00s)
=== RUN   TestChunkTextLongParagraph
--- PASS: TestChunkTextLongParagraph (0.00s)
=== RUN   TestChunkTextUnicodeRunes
--- PASS: TestChunkTextUnicodeRunes (0.00s)
PASS
```
Semua test lolos tanpa kesalahan.
