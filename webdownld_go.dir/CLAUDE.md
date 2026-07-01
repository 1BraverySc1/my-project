# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build and Run Commands

```bash
# Build the main server
go build ./cmd/server

# Build the storage node
go build ./cmd/storagenode

# Build the Raft node
cd raftImpl && go build -o bin/raftnode ./cmd/raftnode

# Run all tests
go test ./...

# Run Raft tests
cd raftImpl && go test ./...

# Static analysis
go vet ./...

# Run server (no MySQL = admin-only auth; payment disabled)
go run ./cmd/server
```

All test files live in dedicated directories: `test/` for the main module and `raftImpl/test/` for the nested Raft module. Keep `_test.go` files out of production source directories.

## Architecture

This is a distributed cloud drive system: browser → Nginx → Go web server (Gin) → Raft metadata cluster + gRPC storage cluster.

### Three Services

| Service | Entry | Default Port | Purpose |
|---------|-------|-------------|---------|
| Web server | `cmd/server/main.go` | `:8188` | HTTP API + static files, orchestrates upload/download |
| Storage node | `cmd/storagenode/main.go` | `:9001-9003` | gRPC chunk storage (SaveChunk/ReadChunk/DeleteChunk) |
| Raft node | `raftImpl/cmd/raftnode/main.go` | `:8080-8082` (HTTP), `:9000-9002` (gRPC) | Consistent KV for metadata |

### Request Flow (Upload)
1. Browser POST `/api/uploads/init` → server creates `UploadSession` in Raft
2. Browser POST chunk to `/api/uploads/:id/chunks/:index` → chunk buffer from `sync.Pool` → `ChunkWorkerPool` (64 fixed workers, bounded queue) → gRPC to storage node
3. Storage node writes chunk via temp file + atomic hardlink, returns SHA256 hash
4. Chunk state written to Raft key `uploadchunk:<uploadID>:<index>`
5. Browser POST `/api/uploads/:id/complete` → server assembles `FileMeta` → Raft

### Request Flow (Download)
1. Browser GET `/api/files/:id/manifest` → Raft read
2. Browser parallel-fetches chunks via `/api/files/:id/chunks/:index` → server opens gRPC stream from storage node → HTTP streaming response (`io.ReadCloser` adapter)
3. Browser merges chunks into Blob and triggers download

### Key Design Decisions
- **Metadata in Raft, data in gRPC storage nodes** — Raft handles small JSON metadata (KB), storage nodes handle large chunk data (MB).
- **FNV-1a routing** — `fnv1a(chunkHash) % len(nodes)` routes chunks, O(1) dedup+locate without a central index.
- **Per-chunk Raft keys** — Each chunk gets its own `uploadchunk:<uploadID>:<index>` key instead of rewriting the entire session, preventing concurrent overwrite.
- **Content-addressed storage** — Chunks named by SHA256 hash; dedup via Bloom filter pre-check + gRPC `ChunkExists` confirmation + atomic hardlink.
- **Exponential backoff with jitter** — RaftClient retries 10× with `50ms * 2^attempt` ±25% jitter, max 2s, redirecting on `409 not leader`.
- **Strict JWT token types** — Drive and payment business APIs require `token_type=access`; `/api/auth/refresh` only accepts `token_type=refresh`.

### Startup Order
1. Start Raft cluster (3 nodes, first with `-bootstrap`)
2. Start storage nodes (3 instances)
3. Start web server (`go run ./cmd/server`)

The web server is the only component that must wait for the others. Without Raft/storage nodes, it starts but upload/download APIs return errors. Without MySQL, it keeps admin-only JWT login enabled and disables database-backed registration/payment.

## Code Conventions

- **Pointer style**: All struct allocation uses `new(T)` with field assignment, never `&T{}`.
- **Logging**: Use `api.INFO(msg, args...)` (unified macro in `internal/api/logger.go`), not `slog.Info()`. `slog.Error()` is used directly for errors.
- **Comments**: All struct/field/function comments are in Chinese. Exported names use English for cross-package access; unexported helpers may use Chinese names.
- **Model types**: `model.User`, `model.Order`, `model.MemberPlan` — use English exported names with Chinese JSON comment.
- **Every new struct or function must have a Chinese comment** explaining its purpose. Format: `// TypeName 说明文字。` for types, `// fieldName 说明文字。` for fields.
- **Every code change must sync to all MD files** in the project root: `README.md` and `完整执行流程报告.md`. If a new feature, API, config option, or architectural change is made, both files must reflect it.

## Module Dependencies

All external dependencies come from the module proxy cache (offline environment):
- `github.com/gin-gonic/gin` — HTTP framework
- `github.com/go-sql-driver/mysql` — MySQL driver (v1.8.1 in cache)
- `golang.org/x/crypto` — bcrypt for password hashing
- `google.golang.org/grpc` + `google.golang.org/protobuf` — gRPC storage communication

JWT is self-implemented (`internal/auth/jwt.go`, HMAC-SHA256), Alipay signing is self-implemented (`internal/payment/alipay.go`, RSA-SHA256), and messaging uses a real RabbitMQ Topic exchange through `amqp091-go` (`internal/mq/rabbitmq.go`).

## Correctness Invariants

- File ownership comes from JWT claims, never request JSON. Non-admin users only access their own files.
- File IDs and instant-upload indexes are owner-scoped; physical chunks remain globally deduplicated.
- File completion/deletion use `files:mutation`; upload/delete of the same content use `chunk:mutation:<hash>`. Chunks are deleted only when no file or active upload references them.
- Redis watchdog renewal verifies the lock token through Lua.
- RabbitMQ publishes persistent messages and waits for publisher confirms.
- Storage reads/deletes use recorded `storage_id`; Raft HTTP leader promotion maps `leader_id` to `RAFT_HTTP_NODES`.

## Environment Variables

See `.env.example` for all config. Key ones:
- `APP_ADDR` (default `:8188`), `DATA_ROOT` (default `./data`)
- `STORAGE_NODE_ADDRS` (comma-separated gRPC addresses), `RAFT_HTTP_NODES`
- `CHUNK_SIZE_MB` (default 4), `MAX_CONCURRENT_CHUNK_WRITES` (default 64)
- `MYSQL_DSN` — MySQL connection string; if MySQL is unreachable, admin-only auth remains available and payment is disabled
- `JWT_SECRET`, `JWT_ACCESS_TTL_HOURS`, `JWT_REFRESH_TTL_DAYS`
- `ALIPAY_APP_ID` (empty = skip payment), `ALIPAY_IS_PRODUCTION` (false = sandbox)
