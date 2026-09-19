# Distributed Wallet Service

A high-performance distributed wallet payment system built in Go, leveraging the actor model via Proto.Actor for concurrent, lock-free transaction processing. Exposes both an HTTP and a gRPC API over the same service layer.

---

## Table of Contents

- [Overview](#overview)
- [Architecture](#architecture)
- [Transaction Flow](#transaction-flow)
- [Concurrency Model](#concurrency-model)
- [Project Structure](#project-structure)
- [Data Models](#data-models)
- [Getting Started](#getting-started)
- [Configuration](#configuration)
- [API](#api)
- [Database](#database)

---

## Overview

Distributed Wallet Service is a financial transaction engine designed for high throughput and correctness. The system processes multi-party transfers — debits, credits, fees — as atomic units, with automatic reversal on failure and a full immutable ledger trail for every balance change.

Key characteristics:

- **Asynchronous processing** — initiation returns `202` once the transaction is persisted and handed to its actor
- **Lock-free balance updates** via the actor model — no `SELECT FOR UPDATE`, no deadlocks
- **Automatic reversal** when any transfer in a transaction fails
- **Immutable ledger** — every balance change is recorded with before/after balances
- **Tamper detection** via SHA-256 checksum on every wallet row, verified before every balance change
- **Idempotent wallet operations** — redelivered messages are detected by idempotency key and never double-apply
- **Optimistic concurrency** — every balance update is guarded by the wallet `version`
- **Exact money** — the API speaks decimals (`110.00`), storage is `int64` minor units; no floats anywhere
- **Dual API** — HTTP (Chi) and gRPC servers run side by side, both backed by the same service layer
- **Distributed** — runs as a single node or a multi-node Kubernetes cluster via Proto.Actor

---

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│            HTTP Layer            │      gRPC Layer      │
│         (Go Chi Router)          │ (Profile / Wallet /  │
│                                  │ Transaction Service) │
└────────────────────────┬────────────────────────────────┘
                         │
┌────────────────────────▼────────────────────────────────┐
│                    Service Layer                         │
│         ProfileService / WalletService /                 │
│                 TransactionService                       │
└────────────────────────┬────────────────────────────────┘
                         │
┌────────────────────────▼────────────────────────────────┐
│                   Actor System                           │
│              (Proto.Actor + Cluster)                     │
│                                                          │
│   ┌─────────────────────────────────────────────────┐   │
│   │             TransactionActor                     │   │
│   │   Orchestrates the full transaction lifecycle    │   │
│   └──────────┬──────────────────────┬───────────────┘   │
│              │                      │                    │
│   ┌──────────▼──────┐    ┌──────────▼──────┐            │
│   │  WalletActor(1) │    │  WalletActor(2) │  ...       │
│   │  wallet-uuid-1  │    │  wallet-uuid-2  │            │
│   └─────────────────┘    └─────────────────┘            │
└─────────────────────────────────────────────────────────┘
                         │
┌────────────────────────▼────────────────────────────────┐
│                    PostgreSQL                            │
│         profiles / wallets / transactions /              │
│                    ledgers                               │
│           (range-partitioned by created_at)              │
└─────────────────────────────────────────────────────────┘
```

---

## Transaction Flow

A transaction is a collection of transfers — each transfer is a debit or credit against a specific wallet. A typical peer-to-peer transfer with a fee looks like this:

```json
{
  "type": "transfer",
  "merchant_id": "5837b2c4-3532-45c6-a1e0-fb014cda76fb",
  "order_id": "ORD-2026-001",
  "provider_ref": "PSP-REF-001",
  "callback_url": "https://merchant.example.com/wallet/callback",
  "currency": "KES",
  "total_amount": 110.00,
  "fee": 10.00,
  "transfers": [
    { "wallet_id": "wallet-A", "action": "debit",  "amount": 100.00, "purpose": "initial debit",  "is_initial": true },
    { "wallet_id": "wallet-B", "action": "credit", "amount": 100.00, "purpose": "initial credit", "is_initial": true },
    { "wallet_id": "wallet-A", "action": "debit",  "amount": 10.00,  "purpose": "fee debit",      "is_fee": true },
    { "wallet_id": "wallet-C", "action": "credit", "amount": 10.00,  "purpose": "fee credit",     "is_fee": true }
  ]
}
```

Amounts are decimals in the currency's major units (a JSON number or a string). `total_amount` is the full amount moved, fee included; `fee` says how much of it is fee.

### Step-by-step lifecycle

```
1. HTTP handler receives POST /v1/transaction/initiate
        │
        ▼
2. TransactionService validates wallets (exist, active, currency) and the
   double-entry rules (debits == credits == total_amount + fee, order_id unused)
        │
        ▼
3. Cluster resolves the TransactionActor grain for the new transaction ID
        │
        ▼
4. Transaction row inserted into DB with status = pending
        │
        ▼
5. StartTransactionMsg sent to TransactionActor
        │
        ▼
6. HTTP handler returns 202 with the transaction in `pending` state ◄── client unblocked here
        │
        ▼  (async from this point)
7. TransactionActor updates transaction status → processing
        │
        ▼
8. TransactionActor fans out all transfers (non-blocking ctx.Request)
   ├── ctx.Request(WalletActor-A, DebitWalletMsg{amount: 10890})
   ├── ctx.Request(WalletActor-A, DebitWalletMsg{amount: 100})
   ├── ctx.Request(WalletActor-B, CreditWalletMsg{amount: 10890})
   └── ctx.Request(WalletActor-C, CreditWalletMsg{amount: 100})
        │
        ▼ (replies arrive asynchronously)
9. Each WalletActor processes its message:
   - Skips it if the idempotency key was already applied (replies with prior result)
   - Validates wallet status, checksum and available balance
   - Applies balance change + writes ledger row in one DB transaction,
     guarded by `WHERE version = ?` (retried on conflict)
   - Sends WalletResultMsg back to TransactionActor
        │
        ▼
10. TransactionActor collects replies
    - Matches reply to transfer by idempotency key
    - Marks transfer as applied or failed
        │
        ├─── ALL SUCCESS ──────────────────────────────────────────┐
        │                                                           ▼
        │                                         UPDATE transaction → success
        │                                         SET is_completed = true
        │                                         SET date_completed = NOW()
        │
        └─── ANY FAILURE ──────────────────────────────────────────┐
                                                                    ▼
                                             stage → reversing: send the opposite operation
                                             (fresh idempotency key) to every wallet that applied
                                                                    │
                                                                    ▼
                                             Collect reversal replies (once — no re-reversal)
                                                                    │
                                                                    ▼
                                             UPDATE transaction → failed
                                             SET narration = error message
                                             (flagged "reconciliation required" if a reversal failed)

Afterwards the actor POSTs the final transaction to `callback_url`
(3 attempts with backoff) and stops itself.
```

### Caching

Redis is optional (`REDIS_ADDR` empty disables it; unreachable Redis logs a warning and the service runs uncached). What is cached, and why it is safe:

| Data | Key | TTL | Invalidation |
|---|---|---|---|
| Profiles by id, email, phone | `profile:*` | 30 min | Immutable after registration |
| Wallet metadata used to validate a request (owner, merchant, currency, status; never balances) | `wallet:meta:{id}` | 5 min | Status is re-checked by the WalletActor at apply time |
| Wallet reads (`GET /wallet/{id}`, `/wallet/number/{n}`) including balances | `wallet:id:*`, `wallet:number:*` | 5 s | Deleted by the WalletActor after every balance change |
| Completed transactions (by id, RRN, order) with transfers and balances | `txn:*` | 15 min | Immutable once completed; pending ones are never cached |
| `order_id` already used | `txn:order-used:*` | 48 h | Replay check answered without a database round trip |

The WalletActor also keeps its wallet row in memory between messages. It is the only writer, so the copy is trustworthy; the version-guarded `UPDATE` still catches any out-of-band change, on which the copy is dropped and reloaded. This removes one `SELECT` per leg from the hot path. Hot-path queries select only the columns they use.

### Connection pooling with PgBouncer

Each node keeps up to 25 Postgres connections. With several replicas that adds up, and Postgres handles many idle connections poorly, so the service can talk to PgBouncer instead, toggled by `USE_PGBOUNCER`:

- `false` (default): the pool connects to `DB_HOST:DB_PORT` directly.
- `true`: the pool connects to `PGBOUNCER_HOST:PGBOUNCER_PORT`. Migrations still go direct to Postgres because golang-migrate takes a session advisory lock. In `transaction` pool mode (the default and the one that actually saves connections) pgx runs in `cache_describe` mode and GORM statement caching is off, since a server connection is only borrowed per transaction and named prepared statements would not survive. In `session` mode prepared statements stay on.

Minimal `pgbouncer.ini` entry for this service:

```ini
[databases]
wallet_database = host=127.0.0.1 port=5432 dbname=wallet_database

[pgbouncer]
pool_mode = transaction
max_client_conn = 1000
default_pool_size = 20
max_prepared_statements = 200   ; PgBouncer >= 1.21; lets pgx keep prepared statements even in transaction mode
```

### Failure handling and recovery

Every path that can lose a message has a resolution; nothing is only logged.

| Event | Resolution |
|---|---|
| Wallet does not reply within `TRANSACTION_TIMEOUT` | The silent leg is marked failed (`timeout`); applied legs are reversed; transaction → `failed` |
| Debit/Credit message dead-lettered (wallet actor unreachable) | The dead-letter handler answers the orchestrator with an `undeliverable` result, so it fails the leg and reverses the rest immediately |
| Start / reply / finalize / timeout message dead-lettered (orchestrator gone) | The recovery manager re-drives the transaction from its persisted plan (`transactions.transfers`) |
| Transaction incomplete and unchanged for `RECOVERY_STALE_AFTER` (node crash, lost final write) | The sweeper (every `RECOVERY_SWEEP_INTERVAL`) re-drives it the same way |
| Re-driven more than `RECOVERY_MAX_ATTEMPTS` times | Marked `failed`; if ledger rows exist the narration says "reconciliation required" and the callback is sent |

**How a re-drive works.** The plan stores each leg with its idempotency key, and the reversal key once a reversal was issued. On resume the actor reads the ledger rows for the transaction: a leg is *applied* if a row carries its key and *reversed* if a row carries its reversal key. Settled legs are not re-sent; the rest continue under the same keys, so even a late-arriving original message is absorbed idempotently by the wallet. If any reversal key exists, the transaction resumes directly in the reversing stage and finishes the reversal rather than re-applying anything.

---

## Concurrency Model

### Why no database locks?

Traditional payment systems use `SELECT FOR UPDATE` to prevent concurrent balance updates on the same wallet. Under high load this causes:

- Lock contention between competing transactions
- Deadlocks when two transactions touch the same wallets in different orders
- Serialised throughput — one transaction at a time per wallet

Katuva Wallet eliminates this entirely using the **actor model**.

### One actor per wallet

Every wallet has exactly one `WalletActor` running in the cluster, identified by its UUID. The actor's **mailbox** is a serial queue — messages are processed one at a time:

```
WalletActor(wallet-A) mailbox:
┌─────────────────────────────────────┐
│  DebitMsg  (txn-1, amount: 10890)   │  ← processing now
│  DebitMsg  (txn-1, amount: 100)     │  ← waiting
│  CreditMsg (txn-2, amount: 5000)    │  ← waiting
│  DebitMsg  (txn-3, amount: 2000)    │  ← waiting
└─────────────────────────────────────┘
```

This means:
- **Two transactions debiting the same wallet simultaneously are automatically serialised** — no locks needed
- **Different wallets process in parallel** — `WalletActor(wallet-A)` and `WalletActor(wallet-B)` run concurrently
- **1000 transactions involving 1000 different wallets process in parallel** with zero contention

### Non-blocking HTTP responses

The HTTP handler returns as soon as the transaction row is written and the `StartTransactionMsg` is dispatched to the cluster — it does not wait for a reply from any `WalletActor`. The client does not wait for wallet processing to complete. This means:

- HTTP threads are never blocked on DB operations inside the actor system
- The server can handle thousands of concurrent HTTP requests
- Transaction status is polled or delivered via webhook after completion

### Idempotency

Every transfer is assigned a UUID idempotency key before being sent to the wallet actor and stored on its ledger row. If a message is delivered twice (e.g. after a network retry), the wallet actor finds the existing ledger row for that key and replies with the original result without touching the balance. Reversals get a fresh key so they are applied as new work. At the API level `order_id` is the caller's idempotency handle: a repeat returns `409`.

### Optimistic concurrency

Every wallet row has a `version` counter. The `UPDATE` uses `WHERE id = ? AND version = ?`; zero rows affected means something else changed the wallet, and the actor re-reads and retries up to three times before failing the leg (which triggers reversal of the others).

### Checksum integrity

After every balance change, a SHA-256 checksum of `id:actual:available:processing:version` is written to the `checksum` column and verified on every read. Any out-of-band modification (direct SQL, migration bug) causes the leg to fail with `checksum_mismatch` instead of moving money from a corrupted row.

### Messages cross nodes as protobuf

Proto.Actor remoting only serialises `proto.Message` values, so every actor message is defined in `proto/actors/actors.proto`. Errors travel as string codes, never as Go `error` values.

---

## Project Structure

```
wallet/
├── cmd/
│   ├── main.go             # Entry point: logger → config → db → migrations → partitions → servers
│   │                       # Also `wallet migrate up|down`
│   └── servers/
│       ├── server.go       # Boots cluster, gRPC and HTTP; graceful shutdown on SIGTERM
│       ├── http.go         # Chi server: middleware, /healthz, /readyz, timeouts
│       └── grpc.go         # gRPC server: services, health, reflection
├── config/config.go        # Environment variable loading
├── proto/
│   ├── actors/             # Actor messages (must be protobuf for remoting)
│   ├── profile/            # ProfileService
│   ├── wallet/             # WalletService
│   └── transaction/        # TransactionService
├── dpk/
│   ├── logger/             # Leveled loggers → stdout + one file per day (rolls at midnight)
│   └── utils/              # RRN generation and shared utilities
├── internal/
│   ├── actors/
│   │   ├── wallet.go       # WalletActor — idempotency, checksum, versioned balance ops
│   │   ├── transaction.go  # TransactionActor — processing → reversing → finalized
│   │   ├── resolver.go     # WalletResolver (cluster lookup)
│   │   ├── dead_letter.go  # Dead letter resolution (answer sender / trigger recovery)
│   │   ├── plan.go         # Persisted transfer plan (legs + keys) for recovery
│   │   └── types.go        # Kind names and error codes
│   ├── cluster/
│   │   ├── cluster.go      # Proto.Actor cluster init (automanaged / k8s)
│   │   └── kinds.go        # Kind registration — Wallet, Transaction
│   ├── db/
│   │   ├── connection.go   # GORM PostgreSQL connection (pooled, pinged)
│   │   ├── migrator.go     # golang-migrate runner over embedded SQL
│   │   ├── partitions.go   # Monthly partition creation + daily maintenance
│   │   └── migrations/     # Ordered .up.sql / .down.sql files
│   ├── handlers/
│   │   ├── http/           # Chi HTTP handlers
│   │   └── grpc/           # gRPC handlers (same DTO validation as HTTP)
│   ├── routers/            # HTTP route registration
│   ├── models/             # GORM models — Profile, Wallet, Transaction, Ledger
│   ├── services/           # Business logic shared by HTTP and gRPC
│   ├── types/              # DTOs
│   ├── validation/         # Shared validator + field error formatting
│   └── webhook/            # callback_url delivery
├── k8/                     # kustomize manifests
└── .env
```

---

## Data Models

### Profile
Represents any entity that can own a wallet — individual customer or business merchant.

### Wallet
Belongs to a profile. Tracks three balance fields, all `int64` minor units:
- `available_balance` — funds the owner can spend right now
- `processing_balance` — funds held pending settlement (never touched by debits/credits)
- `actual_balance` — `available_balance + processing_balance`

### Transaction
Records a financial event. Partitioned by `created_at` for query performance at scale. Tracks sender/receiver names, wallet references, amount, fee, status, and completion metadata.

### Ledger
Immutable append-only record of every balance change. Each debit or credit writes one ledger row with `initial_balance` and `updated_balance`. Partitioned by `created_at`. The ledger is the authoritative source of truth — wallet balances can always be recomputed from the ledger.

---

## Getting Started

### Prerequisites

- Go 1.25+
- PostgreSQL 14+
- Docker (optional, for local cluster mode)

### Setup

```bash
# Clone the repository
git clone https://github.com/katuva/wallet.git
cd wallet

# Copy and configure environment
cp .env.example .env

# Run the application — migrations run automatically on startup.
# This starts both the HTTP server and the gRPC server.
make run
```

### Running with Docker Compose

`docker-compose.yml` brings up Postgres, Redis and the wallet service (built from the `Dockerfile`), with PgBouncer available behind a profile.

```bash
docker compose up -d --build          # postgres + redis + wallet on :3003 (HTTP) and :3004 (gRPC)
docker compose logs -f wallet
curl localhost:3003/readyz

docker compose --profile pooler up -d # add PgBouncer; pair with USE_PGBOUNCER=true
docker compose down                   # -v also drops the database volume
```

Defaults are development-only (`wallet`/`wallet` credentials). Override any value from a `.env` next to the compose file or the shell, e.g. `DB_PASSWORD=... HOST_HTTP_PORT=8080 docker compose up -d`. Note that Compose reads the project `.env`, so `DB_USER`, `DB_PASSWORD` and `DB_NAME` there also seed the Postgres container. Host ports are `HOST_HTTP_PORT` (3003), `HOST_GRPC_PORT` (3004), `HOST_POSTGRES_PORT` (5433) and `HOST_REDIS_PORT` (6380); Postgres and Redis are off their default ports so they do not collide with local installs. Migrations and partitions run automatically when the container starts.

### Running in cluster mode

```bash
CLUSTER_MODE=cluster make run
```

### Deploying to Kubernetes

Manifests live under [`k8/`](k8) and are assembled with `kustomize`. The stack is built for a multi-node actor cluster:

| File | What it is |
|---|---|
| `00-namespace`, `01-rbac` | Namespace and the ServiceAccount/Role the Proto.Actor Kubernetes provider needs (it discovers peers by reading and patching pod labels `cluster.proto.actor/*`) |
| `02-configmap`, `03-secret` | `CLUSTER_MODE=cluster`, `USE_PGBOUNCER=true`, Redis address, recovery tuning; placeholder passwords to replace |
| `04-postgres` | Self-contained tuned Postgres StatefulSet for dev/staging (`max_connections=300`, 1 GB shared buffers). For production use a managed HA Postgres and point `DB_HOST` at it |
| `04b-redis` | Cache-only Redis (LRU, no persistence) |
| `04c-pgbouncer` | 2 PgBouncer replicas in transaction mode, 100 server connections each, prepared statements enabled, own PDB |
| `05-deployment` | Wallet pods: pod IP injected as `ADVERTISED_HOST`, `GOMAXPROCS`/`GOMEMLIMIT` from limits, startup/readiness/liveness probes on `/readyz` and `/healthz`, preStop drain, zone and node spread, rolling update with zero unavailable |
| `06-service` | ClusterIP for HTTP, plus a headless `wallet-grpc` Service for client-side gRPC balancing |
| `07-ingress` | ingress-nginx for HTTP, and a TLS gRPC ingress that balances per RPC |
| `08-pdb` | At most one wallet pod voluntarily disrupted at a time |
| `09-hpa` | 3 to 30 pods on CPU 65 % / memory 80 %, fast scale-up, slow scale-down |

Cluster prerequisites: metrics-server (HPA), ingress-nginx, and a TLS secret or cert-manager for the gRPC ingress.

```bash
make k8s-render    # preview the rendered manifests
make k8s-apply     # kubectl apply -k k8
kubectl get pods -n wallet -w
kubectl -n wallet logs deploy/wallet | grep -i "cluster\|member"   # peers joining
make k8s-delete
```

**Real cluster**: push the image, update `image:`/`imagePullPolicy` in `k8/05-deployment.yaml`, and replace the placeholder secret:

```bash
kubectl create secret generic wallet-secrets --namespace wallet \
  --from-literal=DB_PASSWORD=... --from-literal=POSTGRES_PASSWORD=... \
  --dry-run=client -o yaml | kubectl apply -f -
```

**Capacity notes for 10k requests/second.** The application tier scales horizontally: one pod at 1 vCPU handled roughly 1000 initiations/s in local tests, so 10 to 15 pods carry the target with headroom and the HPA range covers it. Two things do not scale by adding pods:

- *The database.* A two-leg transaction is about eight statements, so 10k transactions/s is on the order of 80k statements/s: a large, NVMe-backed Postgres with the PgBouncer pools sized to it. Partition pruning, prepared statements and the cache keep the per-transaction cost low, but the database is the ceiling.
- *Hot wallets.* Every wallet is one actor with a serial mailbox, so a single wallet cannot absorb more than one Postgres row update at a time (roughly 1 to 2k/s). If every transaction credits the same fee or float wallet, that wallet is the bottleneck regardless of pod count. Spread such traffic across several wallets (for example N fee wallets chosen round-robin or by hash) and sum them for reporting.

---

## Configuration

| Variable | Default | Description |
|---|---|---|
| `PORT` | `3003` | HTTP server port |
| `GRPC_PORT` | `3004` | gRPC server port |
| `CORS_ALLOWED_ORIGINS` | `http://localhost:*` | Comma-separated allowed origins |
| `SHUTDOWN_TIMEOUT` | `15s` | Grace period for draining on SIGTERM |
| `TRANSACTION_TIMEOUT` | `30s` | Per-stage wait for wallet replies before failing silent legs |
| `RECOVERY_STALE_AFTER` | `2m` | Incomplete transactions unchanged this long are re-driven |
| `RECOVERY_SWEEP_INTERVAL` | `30s` | How often the sweeper looks for stuck transactions |
| `RECOVERY_MAX_ATTEMPTS` | `5` | Re-drives before a transaction is marked failed for reconciliation |
| `REDIS_ADDR` | `` (empty) | Redis `host:port`; empty disables caching (`REDIS_URL` accepted as an alias) |
| `REDIS_PASSWORD` | `` (empty) | Redis password |
| `REDIS_DB` | `0` | Redis logical database |
| `CACHE_TTL` | `5m` | Default TTL for entries that do not set their own |
| `LOG_TO_FILE` | `true` | Also write one log file per day: `LOG_DIR/<YYYY-MM-DD>-app.log`, rolled at midnight |
| `LOG_DIR` | `./logs` | Log file directory |
| `CLUSTER_MODE` | `single` | `single` for local, `cluster` for k8s |
| `CLUSTER_NAME` | `wallet-cluster` | Proto.Actor cluster name |
| `CLUSTER_PORT` | `8090` | Inter-node communication port |
| `ADVERTISED_HOST` | `127.0.0.1` | This node's IP — critical in k8s |
| `AUTOMANAGED_PORT` | `6330` | Discovery port in `single` mode; change it to run two instances on one machine |
| `K8S_NAMESPACE` | `default` | Kubernetes namespace for k8s provider |
| `DB_HOST` | `127.0.0.1` | PostgreSQL host |
| `DB_PORT` | `5432` | PostgreSQL port |
| `DB_NAME` | `wallet_database` | Database name |
| `DB_USER` | `postgres` | Database user |
| `DB_PASSWORD` | `` (empty) | Database password |
| `DB_SSLMODE` | `disable` | Postgres `sslmode` |
| `USE_PGBOUNCER` | `false` | Route the application pool through PgBouncer |
| `PGBOUNCER_HOST` | `127.0.0.1` | PgBouncer host |
| `PGBOUNCER_PORT` | `6432` | PgBouncer port |
| `PGBOUNCER_POOL_MODE` | `transaction` | Must match `pool_mode` in pgbouncer.ini; drives prepared-statement handling |

---

## API

Every resource is available over both HTTP and gRPC, backed by the same service layer and the same validation.

### HTTP

Mounted under `/v1`. Health: `GET /healthz` (liveness), `GET /readyz` (pings the database).

| Method | Endpoint | Description |
|---|---|---|
| `GET` | `/v1/currencies` | Supported currencies (code, name, exponent) |
| `POST` | `/v1/profile/register` | Register a profile → `201` |
| `GET` | `/v1/profile` | List profiles, paginated; filters `external_id`, `type`, `search` |
| `GET` | `/v1/profile/{id}` | Profile by id |
| `GET` | `/v1/profile/username/{username}` | Profile by email or phone |
| `POST` | `/v1/wallet/create` | Create a wallet → `201` |
| `GET` | `/v1/wallet` | List wallets, paginated; filters `merchant_id`, `profile_id`, `currency`, `status` |
| `GET` | `/v1/wallet/{id}` | Wallet with balances |
| `GET` | `/v1/wallet/number/{number}` | Wallet by number (e.g. `W100077`) |
| `GET` | `/v1/wallet/profile/{profileId}` | Wallets owned by a profile, paginated; optional `merchant_id`, `currency`, `status` |
| `POST` | `/v1/transaction/initiate` | Initiate a transaction → `202`, status `pending` |
| `GET` | `/v1/transaction` | List transactions, paginated; filters `merchant_id`, `profile_id`, `wallet_id`, `status`, `type`, `order_id`, `provider_ref`, `from`, `to` |
| `GET` | `/v1/transaction/{id}` | Transaction by id, with `transfers` and `balances` |
| `GET` | `/v1/transaction/rrn/{rrn}` | Transaction by RRN, with `transfers` and `balances` |
| `GET` | `/v1/transaction/order/{orderId}` | Transaction by caller `order_id`, with `transfers` and `balances` |
| `POST` | `/v1/transaction/rrn/{rrn}/resend-callback` | Re-deliver the completion webhook → `202 {"message": "callback queued for delivery"}` |

**Pagination.** Listings take `page` (1-based, default 1) and `page_size` (default 20, max 100) and return `{"data": [...], "page": n, "page_size": n, "total": n}`. Transaction listings default to the last 30 days; pass `from`/`to` (RFC3339 or `YYYY-MM-DD`) for another window. The window is what lets Postgres prune partitions, so keep it as narrow as you can.

### gRPC

Served on `GRPC_PORT`, with server reflection and the standard health service.

| Service | RPC | Description |
|---|---|---|
| `profile.ProfileService` | `Register` | Register a profile |
| `wallet.WalletService` | `Create`, `Get` | Create / read a wallet |
| `transaction.TransactionService` | `Initiate`, `Get` | Initiate / read a transaction |

Listing, lookup-by-number/username and resend-callback are HTTP-only for now.

Proto definitions live under `proto/{profile,wallet,transaction}/`. gRPC amounts are decimal strings (`"110.00"`), the same semantics as HTTP. Regenerate with `make proto`.

### Transaction request rules

- `merchant_id` is required and stored on every transaction (`NOT NULL`): the profile transacting. It must exist and own at least one wallet in `transfers`.
- Amounts (`total_amount`, `fee`, every `transfers[].amount`) are decimals in major units, e.g. `110.00` or `"110.00"`. They must fit the currency's minor units: KES allows 2 decimals, JPY 0, KWD 3. Internally everything is `int64` minor units.
- `transfers` needs at least two entries. Sum of debits must equal sum of credits, and both must equal `total_amount`. `fee` is part of `total_amount` (carried by its own legs) and may be `0`.
- `currency` must be a supported ISO 4217 code. Every wallet in `transfers` must hold that currency; a request whose wallets span two currencies is rejected with `400 transfers mix currencies`. There is no implicit FX.
- Every wallet must exist and be `active`. At least one debit must be flagged `is_initial` (it becomes `wallet_from`).
- `order_id` must be unused; a repeat returns `409` with the original transaction.
- Completed transactions (single-transaction GETs and the callback) also carry `balances`: one entry per affected wallet with `initial_balance` before the transaction first touched it and `updated_balance` after it last did, reversals included, plus the number of `entries`. The callback builds this from the balances the TransactionActor already received in wallet replies, so it costs no extra database read; GET and recovery derive the same data from the ledger. Pending transactions have an empty array.
- Single-transaction GETs (by id, RRN or order) include `transfers` (wallet, action, decimal amount, purpose, `is_fee`, `is_initial`), read from the plan persisted on the row. The initiate response, listings and the callback do not carry `transfers`.
- `callback_url` is required. It is echoed back with the transaction details, and the final transaction is POSTed to it when processing completes (3 attempts with backoff).
- Unknown JSON fields are rejected with `400`.

### Currencies

Each wallet holds exactly one currency, so a profile that deals in several currencies owns one wallet per currency. The supported list is the full set of active ISO 4217 currencies, stored as data in `internal/currency/currencies.json` (code, name, minor-unit exponent) and embedded into the binary. It is validated on wallet creation and transaction initiation over both HTTP and gRPC, case-insensitively (`usd` → `USD`), and served at `GET /v1/currencies`. API amounts are decimals in major units; storage is `int64` minor units per the currency exponent (KES `10.50` → `1050`; JPY has no decimals; KWD has three). Responses render balances and amounts with the currency's exponent (`"150.00"` → `150.00`). To add or remove a currency, edit the JSON file and rebuild.

### Response codes (HTTP)

| Code | Meaning |
|---|---|
| `200` / `201` | Success |
| `202` | Transaction accepted and processing asynchronously |
| `400` | Malformed JSON or business rule violation |
| `404` | Not found |
| `409` | Conflict (duplicate email/external_id/order_id) |
| `422` | Field validation failed (body lists fields) |
| `503` | Cluster cannot place the transaction actor |
| `500` | Internal server error |

---

## Database

### Migrations

Migrations run automatically on startup via golang-migrate over the embedded SQL in `internal/db/migrations/`, tracked in `schema_migrations`. They can also be run by hand:

```bash
make migrate-up
make migrate-down   # rolls back one step
```

### Partitioning

`transactions` and `ledgers` are `PARTITION BY RANGE (created_at)`. On startup the service creates monthly partitions for the current month and the next two, and re-checks once a day, so no manual partition management is needed. Point lookups always include a `created_at` bound so Postgres can prune partitions.

---

*README last updated: September 2026.*
