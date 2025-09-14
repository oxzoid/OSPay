# OSPay Crypto Payment Processor

High-throughput, minimal crypto payment processor written in Go. Supports USDT verification on BSC, idempotent order/refund flows, double-entry ledger, async verification workers, and simple ops/metrics for observability.


NOTE: The frontend only verfied transactions rn will implement refunds and merchnat dashboards soon.

## Features

- Create and query orders (PENDING → PAID → SETTLED/REFUNDED)
- Payment detection with on-chain USDT transfer verification (BSC)
- Idempotent updates and guarded state transitions
- Double-entry ledger (merchant and clearing buckets)
- Refunds with ledger entries and state enforcement
- Background verification workers and scheduler for settlements
- API key auth (per-merchant) and structured error responses
- SQLite with WAL + tuned pool; timeouts around DB and RPC
- Lightweight metrics at `/debug/metrics`

## Project layout

```
cmd/server/main.go       # HTTP server, routes, workers, scheduler
pkg/api/orders.go        # Create/Get order endpoints, API key middleware
pkg/api/events.go        # Payment detection, reconciliation, metrics, workers, scheduler
pkg/api/refunds.go       # Refund endpoint
pkg/blockchain/bsc.go    # BSC RPC client and USDT verification
pkg/db/db.go             # DB open, PRAGMAs, migrations
pkg/db/migrations.sql    # Schema (orders, merchants, ledger, refunds)
```

## Quick start

1) Build

```powershell
go build ./...
```

2) Run the server

```powershell
go run ./cmd/server
```

By default, the server will create `ospay.db` (SQLite) in the working directory and apply migrations. The server registers workers (default from `StartVerificationWorkers(4)`) and a periodic settlement scheduler.

## Configuration

Environment variables (optional):

- `BSC_RPC_URL`: HTTPS RPC endpoint for Binance Smart Chain. Required for on-chain verification. Example: `https://bsc-dataseed.binance.org`.

SQLite is tuned with WAL, synchronous=NORMAL, and busy timeouts. See `pkg/db/db.go`.

## API overview

All endpoints expect and return JSON. Some endpoints require the `X-API-Key` header for merchant authentication.

### Create order

- Method/Path: `POST /orders`
- Auth: `X-API-Key: <merchant-api-key>`
- Body:
	```json
	{
		"id": "ord_123",                   // your order id (idempotent)
		"merchant_id": "mrc_abc",         // merchant id
		"asset": "USDT",                  // asset symbol
		"amount_minor": 5000000,            // integer minor units (e.g., 6 decimals for USDT)
		"customer_wallet_address": "0x..." // optional; recorded for your reference
	}
	```
- Response: 201 Created with the order payload including a deposit/destination wallet (merchant wallet).

### Get order

- Method/Path: `GET /orders/get?id=ord_123`
- Auth: `X-API-Key: <merchant-api-key>`
- Response: 200 OK with order JSON.

### Payment detected (async)

- Method/Path: `POST /events/payment-detected`
- Body:
	```json
	{ "order_id": "ord_123", "tx_hash": "0xabc..." }
	```
- Behavior:
	- Enqueues a verification job and returns `202 Accepted` with `{ status: "PENDING" }` if queue capacity is available.
	- If the queue is full, falls back to inline verification before returning.
	- On success, the order transitions to `PAID`, and balanced ledger entries are written.
	- Duplicate `tx_hash` within a short window is deduped.

### Refund

- Method/Path: `POST /orders/refund`
- Auth: `X-API-Key: <merchant-api-key>`
- Body:
	```json
	{ "order_id": "ord_123", "refund_id": "rfd_001", "reason": "customer request" }
	```
- Behavior:
	- Idempotent on `refund_id`.
	- Writes ledger entries to reverse funds and marks order as `REFUNDED` (guarded by preconditions).

### Reconciliation

- Method/Path: `GET /reconciliation?merchant_id=mrc_abc&asset=USDT`
- Response:
	```json
	{
		"merchant_id": "mrc_abc",
		"asset": "USDT",
		"merchant_balance_minor": 5000000,
		"clearing_balance_minor": -5000000,
		"unsettled_paid_count": 1
	}
	```

### Metrics

- Method/Path: `GET /debug/metrics`
- Response:
	```json
	{
		"orders_created_total": 10,
		"refunds_processed_total": 2,
		"payments_detected_total": 7
	}
	```

## Data model (simplified)

- `merchants(id, api_key, merchant_wallet_address, ...)`
- `orders(id, merchant_id, asset, amount_minor, status, tx_hash, paid_at, customer_wallet_address, ...)`
- `ledger_entries(id, order_id, merchant_id, asset, amount_minor, bucket, direction, event_type, tx_hash, created_at)`
- `refunds(id, order_id, merchant_id, reason, created_at)`

Buckets: `merchant` (customer funds) and `clearing` (platform counter-bucket). Directions: `credit` or `debit`. State transitions are guarded in SQL updates to ensure idempotency under concurrency.

## How on-chain verification works

For USDT on BSC:

- A single shared `ethclient` is reused (singleton), and calls are throttled by a semaphore.
- Each verification uses a 10s RPC timeout, validating transfer to the merchant wallet with the expected amount.
- `PaymentDetected` enqueues a job to workers. The worker verifies on-chain and updates the DB transactionally:
	- UPDATE order to `PAID` (only if currently `PENDING` or `CONFIRMING`)
	- INSERT two ledger entries: merchant CREDIT and clearing DEBIT
	- Mark tx hash as recently seen (dedupe map) and bump metrics

You can add other chains/assets by extending `pkg/blockchain/*` and branching in the handler/worker.

## Operations

- Database: SQLite with WAL and pool tuning. See `pkg/db/db.go` for PRAGMAs and connection limits. Suitable for prototypes and small-to-medium traffic; consider Postgres for higher concurrency.
- Timeouts: All DB calls in hot paths use `context.WithTimeout` (2–5s). RPC calls use a 10s timeout.
- Concurrency: RPC and handler-level throttles; background worker pool size is configurable (`StartVerificationWorkers(n)`).
- Scheduler: Periodically transitions `PAID` orders to `SETTLED` after a delay window. See `StartSettlementScheduler`.
- Dedupe: In-memory map to avoid reprocessing the same tx hash briefly.
- Logging: Minimal structured logs via `log.Printf`.

## Scaling notes

- Increase worker count gradually (e.g., 4 → 16) and size the verify semaphore accordingly.
- Move to Postgres for better concurrent write performance; keep guarded updates and idempotency keys.
- Externalize the job queue (e.g., Redis, NATS, SQS) if the in-process channel isn’t sufficient.
- Add rate-limiting per merchant and circuit breaker for RPCs under duress.
- Consider partitioning ledger tables or indexing by `(merchant_id, asset, created_at)`.

## Security

- API key auth via header `X-API-Key` for merchant endpoints.
- Validate inputs strictly; current code rejects missing/invalid fields and enforces status transitions.
- Never trust client-provided amounts for settlement; prefer on-chain verified amounts.

## Local development tips

- Ensure `BSC_RPC_URL` is set when testing on-chain verification.
- If you see `busy timeout` on SQLite, lower concurrency or increase `busy_timeout` PRAGMA.
- For Windows PowerShell users, use single-line commands or chain with `;`.

## Troubleshooting

- Build fails due to missing modules: run `go mod tidy`.
- On-chain verification failing: check `BSC_RPC_URL`, the tx hash, destination wallet, and token decimals; confirm USDT contract and chain.
- Duplicate payment events: handler/worker paths are idempotent; verify your client isn’t retrying excessively.

## License

Apache-2.0 (or your preferred license). Update if needed.
