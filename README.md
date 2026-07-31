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

- **Sub-5ms transaction processing** under concurrent load
- **Lock-free balance updates** via the actor model — no `SELECT FOR UPDATE`, no deadlocks
- **Automatic reversal** when any transfer in a transaction fails
- **Immutable ledger** — every balance change is recorded with before/after balances
- **Tamper detection** via SHA-256 checksum on every wallet row
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
  "order_id": "ORD-2026-001",
  "transfers": [
    { "wallet_id": "wallet-A", "action": "debit",  "amount": 10890, "purpose": "initial debit",  "is_initial": true  },
    { "wallet_id": "wallet-A", "action": "debit",  "amount": 100,   "purpose": "fee debit",       "is_fee": true      },
    { "wallet_id": "wallet-B", "action": "credit", "amount": 10890, "purpose": "initial credit", "is_initial": true  },
    { "wallet_id": "wallet-C", "action": "credit", "amount": 100,   "purpose": "fee credit",      "is_fee": true      }
  ]
}
```

### Step-by-step lifecycle

```
1. HTTP handler receives POST /transactions/initiate
        │
        ▼
2. TransactionService validates all wallet IDs concurrently
        │
        ▼
3. Transaction row inserted into DB with status = pending
        │
        ▼
4. SpawnTransactionActor → cluster routes to correct node
        │
        ▼
5. StartTransactionMsg sent to TransactionActor
        │
        ▼
6. HTTP handler returns 200 with the transaction in `pending` state ◄── client unblocked here
        │
        ▼  (async from this point)
7. TransactionActor updates transaction status → processing
        │
        ▼
8. TransactionActor fans out all transfers in parallel goroutines
   ├── ctx.Request(WalletActor-A, DebitWalletMsg{amount: 10890})
   ├── ctx.Request(WalletActor-A, DebitWalletMsg{amount: 100})
   ├── ctx.Request(WalletActor-B, CreditWalletMsg{amount: 10890})
   └── ctx.Request(WalletActor-C, CreditWalletMsg{amount: 100})
        │
        ▼ (replies arrive asynchronously)
9. Each WalletActor processes its message:
   - Validates wallet status and balance
   - Applies balance change + writes ledger row in one DB transaction
   - Sends WalletResultMsg back to TransactionActor
        │
        ▼
10. TransactionActor collects replies via auditTransaction()
    - Matches reply to transfer by idempotency key
    - Marks transfer as done/success/failed
        │
        ├─── ALL SUCCESS ──────────────────────────────────────────┐
        │                                                           ▼
        │                                         UPDATE transaction → success
        │                                         SET is_completed = true
        │                                         SET date_completed = NOW()
        │
        └─── ANY FAILURE ──────────────────────────────────────────┐
                                                                    ▼
                                             fireReversals() — send opposite operation
                                             to every wallet that already applied its transfer
                                                                    │
                                                                    ▼
                                             Collect reversal replies
                                                                    │
                                                                    ▼
                                             UPDATE transaction → failed
                                             SET narration = error message
```

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

Every transfer is assigned a UUID idempotency key before being sent to the wallet actor. If a message is delivered twice (e.g. after a network retry), the wallet actor checks the key and skips the duplicate without changing the balance or writing a duplicate ledger row.

### Optimistic concurrency

Every wallet row has a `version` counter. The `UPDATE` statement uses `WHERE id = ? AND version = ?` — if the version has changed since the actor read the wallet, the update affects 0 rows and the operation fails cleanly rather than silently overwriting a concurrent change.

### Checksum integrity

After every balance change, a SHA-256 checksum of `wallet_id + balance + version` is written to the `checksum` column. Any out-of-band database modification (direct SQL, migration bug) will cause a checksum mismatch detectable on next read.

---

## Project Structure

```
wallet/
├── cmd/
│   ├── main.go             # Entry point — init order: logger → db → migrations → server
│   └── servers/
│       ├── server.go       # Boots cluster, then HTTP + gRPC servers
│       ├── http.go         # Chi HTTP server setup
│       └── grpc.go         # gRPC server setup — registers Profile/Wallet/Transaction services
├── config/
│   └── config.go           # Environment variable loading
├── proto/
│   ├── profile/            # ProfileService .proto + generated code
│   ├── wallet/             # WalletService .proto + generated code
│   └── transaction/        # TransactionService .proto + generated code
├── dpk/
│   ├── logger/             # Structured file + stdout logging
│   ├── cache/              # Redis client wrapper
│   └── utils/              # RRN generation and shared utilities
├── internal/
│   ├── actors/
│   │   ├── wallet.go       # WalletActor — balance ops + ledger writes
│   │   ├── transaction.go  # TransactionActor — orchestration + reversal
│   │   ├── dead_latter.go  # Dead letter handler
│   │   └── types.go        # All actor message types
│   ├── cluster/
│   │   ├── cluster.go      # Proto.Actor cluster init (automanaged / k8s)
│   │   └── kinds.go        # Kind registration — Wallet, Transaction
│   ├── db/
│   │   ├── connection.go   # GORM PostgreSQL connection
│   │   ├── migrator.go     # Embedded SQL migration runner
│   │   └── migrations/     # Ordered .up.sql / .down.sql files
│   ├── handlers/
│   │   ├── http/           # Chi HTTP handlers — profile, wallet, transaction
│   │   └── grpc/           # gRPC service handlers — profile, wallet, transaction
│   ├── routers/            # HTTP route registration
│   ├── models/             # GORM models — Profile, Wallet, Transaction, Ledger
│   ├── services/           # Business logic layer, shared by HTTP and gRPC handlers
│   └── types/              # DTOs and actor message types
└── .env
```

---

## Data Models

### Profile
Represents any entity that can own a wallet — individual customer or business merchant.

### Wallet
Belongs to a profile. Tracks three balance fields:
- `available_balance` — funds the owner can spend right now
- `processing_balance` — funds held pending settlement
- `actual_balance` — true ledger balance including processing funds

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

### Running in cluster mode

```bash
CLUSTER_MODE=cluster make run
```

### Deploying to Kubernetes

Manifests live under [`internal/k8`](k8) and are assembled with `kustomize`: namespace,
RBAC (for the Proto.Actor k8s cluster provider), config/secrets, a self-contained Postgres
StatefulSet, and the `wallet` Deployment/Service/Ingress/PDB.

**Local cluster** (e.g. Docker Desktop's Kubernetes, which shares the host Docker daemon —
no image push needed):

```bash
make docker-build  # builds the local wallet:latest image the Deployment already points at
make k8s-apply      # kubectl apply -k internal/k8

kubectl get pods -n wallet -w
kubectl port-forward -n wallet svc/wallet 3003:3003 3004:3004
```

**Real cluster**: push the image to a registry, update `image:`/`imagePullPolicy` in
`internal/k8/05-deployment.yaml` accordingly, and replace the placeholder secret:

```bash
docker tag wallet ghcr.io/<you>/wallet:latest
docker push ghcr.io/<you>/wallet:latest

kubectl create secret generic wallet-secrets --namespace wallet \
  --from-literal=DB_PASSWORD=... --from-literal=POSTGRES_PASSWORD=... \
  --dry-run=client -o yaml | kubectl apply -f -

make k8s-apply     # kubectl apply -k internal/k8
make k8s-render    # preview the rendered manifests without applying
make k8s-delete    # tear the deployment down
```

---

## Configuration

| Variable | Default | Description |
|---|---|---|
| `PORT` | `3003` | HTTP server port |
| `GRPC_PORT` | `3004` | gRPC server port |
| `CLUSTER_MODE` | `single` | `single` for local, `cluster` for k8s |
| `CLUSTER_NAME` | `wallet-cluster` | Proto.Actor cluster name |
| `CLUSTER_PORT` | `8090` | Inter-node communication port |
| `ADVERTISED_HOST` | `127.0.0.1` | This node's IP — critical in k8s |
| `K8S_NAMESPACE` | `default` | Kubernetes namespace for k8s provider |
| `DB_HOST` | `127.0.0.1` | PostgreSQL host |
| `DB_PORT` | `5432` | PostgreSQL port |
| `DB_NAME` | `wallet_database` | Database name |
| `DB_USER` | `postgres` | Database user |
| `DB_PASSWORD` | `` (empty) | Database password |

---

## API

Every resource is available over both HTTP and gRPC, backed by the same service layer. Only `Create`/`Register`/`Initiate` operations exist today — there are no read/list/get endpoints yet.

### HTTP

Mounted under `/v1`.

| Method | Endpoint | Description |
|---|---|---|
| `POST` | `/v1/profile/register` | Register a profile |
| `POST` | `/v1/wallet/create` | Create a wallet |
| `POST` | `/v1/transaction/initiate` | Initiate a transaction |

### gRPC

Served on `GRPC_PORT`, with server reflection enabled.

| Service | RPC | Description |
|---|---|---|
| `profile.ProfileService` | `Register` | Register a profile |
| `wallet.WalletService` | `Create` | Create a wallet |
| `transaction.TransactionService` | `Initiate` | Initiate a transaction |

Proto definitions live under `proto/{profile,wallet,transaction}/`.

### Response codes (HTTP)

| Code | Meaning |
|---|---|
| `200` | Success — for transactions, the transaction was accepted and is processing asynchronously |
| `400` | Bad request — malformed JSON or business validation failure |
| `422` | Unprocessable — request validation failed |
| `500` | Internal server error |

---

## Database

### Migrations

Migrations run automatically on startup via the embedded migrator. Files in `internal/db/migrations/` are executed in filename order — prefix with `000001_`, `000002_` etc. to control order.

To roll back, run the corresponding `.down.sql` file manually.

### Partitioning

`transactions` and `ledgers` are declared `PARTITION BY RANGE (created_at)` in their migrations, but no partition-management logic exists yet — the migrator only creates the partitioned parent tables. Actual partitions must be created manually (or via an external scheduler such as `pg_cron`) before any rows can be inserted; a range-partitioned table with no matching partition rejects inserts.

```sql
-- Example: create a monthly partition ahead of time
CREATE TABLE transactions_2026_08 PARTITION OF transactions
    FOR VALUES FROM ('2026-08-01') TO ('2026-09-01');

CREATE TABLE ledgers_2026_08 PARTITION OF ledgers
    FOR VALUES FROM ('2026-08-01') TO ('2026-09-01');
```

---

*README last updated: July 2026. More features incoming — this document will be updated with each release.*
