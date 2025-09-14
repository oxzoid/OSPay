package db

import (
	"context"
	"database/sql"
	"time"

	_ "modernc.org/sqlite" // SQLite driver
)

type DB = sql.DB

func Open(dsn string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dsn) // e.g., "file:ospay.db?_pragma=busy_timeout=5000"
	if err != nil {
		return nil, err
	}
	// Harden SQLite for concurrent access: WAL, reasonable sync and busy timeout
	// Note: Still a single writer. For heavy write loads, consider Postgres.
	_, err = db.Exec(`
    PRAGMA journal_mode = WAL;
    PRAGMA synchronous = NORMAL;
    PRAGMA busy_timeout = 5000;
    PRAGMA foreign_keys = ON;
  `)
	if err != nil {
		db.Close()
		return nil, err
	}
	// Tune connection pool. SQLite will serialize writes; keep a small pool.
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(5 * time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return nil, err
	}
	return db, nil
}

func EnsureSchema(db *sql.DB) error {
	ddl := `
CREATE TABLE IF NOT EXISTS orders (
  id TEXT PRIMARY KEY,
  merchant_id TEXT NOT NULL,
  amount_minor TEXT NOT NULL,     -- String to handle arbitrarily large 18-decimal numbers
  asset TEXT NOT NULL,
  chain TEXT NOT NULL,
  status TEXT NOT NULL,
  deposit_address TEXT NOT NULL,
  customer_wallet_address TEXT,
  order_idempotency_key TEXT UNIQUE,
  refund_idempotency_key TEXT UNIQUE,
  tx_hash TEXT,
  confirmed_block INTEGER,
  paid_at TEXT,
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS merchants (
  id TEXT PRIMARY KEY,
  name TEXT,
  api_key TEXT NOT NULL UNIQUE,
  merchant_wallet_address TEXT,
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS ledger_entries (
  id TEXT PRIMARY KEY,
  order_id TEXT,
  merchant_id TEXT NOT NULL,
  asset TEXT NOT NULL,
  amount_minor TEXT NOT NULL,     -- String to handle arbitrarily large 18-decimal numbers
  bucket TEXT NOT NULL,      -- 'user' | 'clearing' | 'settlement'
  direction TEXT NOT NULL,   -- 'debit' | 'credit'
  event_type TEXT NOT NULL,  -- 'PAYMENT_CONFIRMED' | 'REFUND' | ...
  tx_hash TEXT,
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS settlement_batches (
  id TEXT PRIMARY KEY,
  merchant_id TEXT NOT NULL,
  asset TEXT NOT NULL,
  scheduled_for TEXT NOT NULL,
  status TEXT NOT NULL,            -- 'SCHEDULED' | 'EXECUTED' | 'CANCELLED'
  total_amount_minor TEXT NOT NULL,       -- String to handle arbitrarily large 18-decimal numbers
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  executed_at TEXT
);

CREATE TABLE IF NOT EXISTS outbox_events (
  id TEXT PRIMARY KEY,
  aggregate_type TEXT NOT NULL,    -- 'order' | 'batch'
  aggregate_id TEXT NOT NULL,
  event_name TEXT NOT NULL,
  payload_json TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  delivered_at TEXT,
  retry_count INTEGER NOT NULL DEFAULT 0
);
`
	_, err := db.Exec(ddl)
	if err != nil {
		return err
	}

	// Add indexes and constraints
	indexDDL := `
CREATE UNIQUE INDEX IF NOT EXISTS idx_orders_txhash_notnull
  ON orders(tx_hash) WHERE tx_hash IS NOT NULL;

DROP INDEX IF EXISTS idx_ledger_unique_event;
CREATE UNIQUE INDEX IF NOT EXISTS idx_ledger_unique_event
  ON ledger_entries(order_id, event_type, bucket);

CREATE INDEX IF NOT EXISTS idx_ledger_order ON ledger_entries(order_id);
`
	_, err = db.Exec(indexDDL)
	return err
}
