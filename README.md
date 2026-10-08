# Omniroute WhatsApp Integration Service (GoWA / whatsmeow)

[Indonesian](README.id.md) | [Chinese](README.zh-CN.md)

---

Self-hosted WhatsApp integration service built with **Golang** using the [whatsmeow](https://github.com/tulir/whatsmeow) library, connected to the **Omniroute AI API** with automatic conversation history context storage in **PostgreSQL** database.

Built with modular architecture (*Clean Architecture / Standard Go Project Layout*), **Gin** framework, **pgxpool** connection pool, and asynchronous *Worker Pool* to ensure high responsiveness and reliability in production environments.

---

## 🌟 Key Features

1. **WhatsApp Session Persistence**:
   - Registered sessions stored directly in PostgreSQL internal table (`sqlstore`) or local SQLite file.
   - Sessions remain connected across application restarts (*persistent login*).

2. **Two QR Code Display Modes**:
   - **Terminal Console**: Automatically printed on startup using UTF-8 half-block representation.
   - **Web Browser (`GET /qr`)**: Modern HTML dashboard displaying QR Code dynamically (base64 PNG) with auto-refresh and automatic pairing status polling. Direct image endpoint available at `GET /qr/image`.

3. **Chat Context Management in PostgreSQL**:
   - `chat_histories` table stores conversation history (`user` & `assistant` messages).
   - Query retrieves last `N` messages and sorts them chronologically ascending for AI context prompt.
   - Equipped with *composite index* `(phone_number, created_at DESC, id DESC)` for sub-millisecond query performance.

4. **Asynchronous Processing (Worker Pool Pattern)**:
   - Incoming message events received via whatsmeow event handler, filtered from group/broadcast messages and outbound messages.
   - Processed by managed goroutine worker pool: calls Omniroute AI, saves to database, and sends response back to WhatsApp.

5. **Smart Text Chunking**:
   - AI reply messages exceeding WhatsApp limit (~4,000 characters) are intelligently chunked based on paragraphs (`\n\n`), newlines (`\n`), sentence endings (`. `, `! `, `? `), or word spaces without breaking words or multi-byte/emoji characters.

6. **Graceful Shutdown**:
   - Handles `SIGINT` and `SIGTERM` signals properly: stops Gin HTTP server, completes in-flight worker tasks (`sync.WaitGroup`), disconnects WhatsApp connection, and cleanly closes PostgreSQL connection pool.

---

## 🏗️ Project Directory Structure

```
omniroute-api-wa/
├── cmd/
│   └── api/
│       └── main.go                 # Application entrypoint & lifecycle orchestration
├── internal/
│   ├── config/
│   │   ├── config.go              # Environment variable parser & validator
│   │   └── config_test.go         # Configuration unit tests
│   ├── domain/
│   │   └── chat.go                # Domain entities, Omniroute & WhatsApp DTOs
│   ├── repository/
│   │   └── postgres/
│   │       └── chat_repository.go # PostgreSQL query layer (pgxpool)
│   ├── service/
│   │   ├── omniroute_service.go   # Omniroute AI HTTP client integration
│   │   ├── omniroute_service_test.go
│   │   └── gowa_service.go        # whatsmeow client, QR streamer, & sender
│   ├── delivery/
│   │   └── http/
│   │       ├── handler.go         # Gin controller (/health, /status, /qr)
│   │       └── middleware.go      # Logging, CORS, Panic Recovery
│   └── worker/
│       └── pool.go                # Bounded Worker Pool & AI pipeline
├── pkg/
│   └── textutil/
│       ├── chunker.go             # Helper for splitting long WhatsApp messages
│       └── chunker_test.go        # Text chunker unit tests
├── migrations/
│   └── 000001_create_chat_histories_table.up.sql # PostgreSQL schema DDL
├── docker-compose.yml              # PostgreSQL & App container configuration
├── Dockerfile                      # Multi-stage production build
├── .env.example                    # Environment configuration template
├── go.mod
└── go.sum
```

---

## ⚙️ Environment Variables (.env)

> **Note:** Copy `.env.example` to `.env` and fill in appropriate values. The `.env` file **must not** be committed to the repository (already listed in `.gitignore`).

| Variable | Type | Default | Required | Description |
|---|---|---|:---:|---|
| `APP_PORT` | String | `8080` | | HTTP Gin server port. Using `APP_PORT` (not `PORT`) to avoid conflict with Omniroute CLI default port (`20128`) |
| `GIN_MODE` | String | `release` | | Gin mode: `debug` or `release` |
| `DB_HOST` | String | — | ✅ | PostgreSQL server host |
| `DB_PORT` | String | — | ✅ | PostgreSQL server port (usually `5432`) |
| `DB_USER` | String | — | ✅ | PostgreSQL database username |
| `DB_PASSWORD` | String | — | ✅ | PostgreSQL database password |
| `DB_NAME` | String | — | ✅ | PostgreSQL database name |
| `DB_SSLMODE` | String | `disable` | | PostgreSQL connection SSL mode (`disable`, `require`, `verify-full`) |
| `OMNIROUTE_API_BASE_URL` | String | — | ✅ | Omniroute AI base endpoint. Default Omniroute CLI port: `http://localhost:20128/v1` |
| `OMNIROUTE_API_KEY` | String | — | | Omniroute authentication API key (optional if not configured on server) |
| `OMNIROUTE_MODEL` | String | `auto` | | AI model name to use (`auto` for automatic selection) |
| `MAX_CONTEXT_MESSAGES` | Integer | `10` | | Number of recent messages sent as context to AI |
| `WHATSAPP_SESSION_STORE` | String | `postgres` | | WhatsApp session storage medium: `postgres` or `sqlite` |
| `WHATSAPP_SQLITE_PATH` | String | `whatsapp_session.db` | | SQLite file path (only applies if `WHATSAPP_SESSION_STORE=sqlite`) |
| `WORKER_POOL_SIZE` | Integer | `5` | | Number of goroutine workers processing AI messages in parallel |
| `WORKER_QUEUE_SIZE` | Integer | `100` | | Buffered channel capacity for incoming messages |

---

## 🚀 Getting Started

### Prerequisites

- [Go](https://go.dev/dl/) 1.21+
- [Docker](https://www.docker.com/) & Docker Compose (for running PostgreSQL)
- Omniroute CLI running on local machine (default port `20128`)

### 1. Environment Configuration

```bash
# Copy configuration template
cp .env.example .env

# Edit .env and fill in appropriate values (DB_PASSWORD, OMNIROUTE_API_KEY, etc.)
```

### 2. Run PostgreSQL Database via Docker Compose

```bash
# Run PostgreSQL container only
docker compose up -d postgres

# Or run entire stack (PostgreSQL + App) at once
docker compose up -d
```

> **Note:** When running `app` via Docker, `DB_HOST` is automatically set to `postgres` (service name in Docker network). Ensure `OMNIROUTE_API_BASE_URL` points to `http://host.docker.internal:20128/v1` so container can access Omniroute CLI on host.

### 3. Run Golang Application (Local)

```bash
# Download dependencies
go mod download

# Run service
go run ./cmd/api/main.go
```

### 4. WhatsApp Authentication / Pairing

1. When service starts for the first time, QR code is automatically printed to terminal.
2. Open browser and access:
   ```
   http://localhost:8080/qr
   ```
3. Scan QR Code from WhatsApp on your smartphone:
   - Open **Settings / Three Dots** → **Linked Devices** → **Link a Device**.
   - Point camera at QR code displayed in browser or terminal.
4. Upon successful pairing, web page automatically updates status to **WhatsApp Connected** and redirects to `/status`.

---

## 📡 HTTP Endpoint List

| Method | Endpoint | Description |
|---|---|---|
| `GET` | `/` | Redirects to `/qr` or `/status` based on login status |
| `GET` | `/health` | Health check for DB and WhatsApp connection components |
| `GET` | `/status` | JSON details of app status, session JID, DB, and configuration |
| `GET` | `/qr` | Web HTML auto-refresh QR Code / Connected status display |
| `GET` | `/qr?format=json` | Retrieve raw QR string data in JSON format |
| `GET` | `/qr/image` | Retrieve QR Code image in pure PNG format |

---

## 🧪 Running Tests (Unit Tests)

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
All tests pass without errors.
