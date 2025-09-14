// PaymentDetectedHandler godoc
// @Summary      Detect payment event
// @Description  Notify the system of an on-chain payment for an order
// @Tags         events
// @Accept       json
// @Produce      json
// @Param        payment  body  paymentDetectedReq  true  "Payment info"
// @Success      200  {object}  paymentDetectedResp
// @Failure      400  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Security     ApiKeyAuth
// @Router       /events/payment-detected [post]
package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/oxzoid/OSPay/pkg/blockchain"
)

// var paymentsDetectedTotal int64
var paymentsDetectedTotal int64

// throttle concurrent on-chain verifications and dedupe tx hashes
var (
	verifySem  = make(chan struct{}, 50) // cap concurrent verifications
	recentTx   = make(map[string]time.Time)
	recentTxMu sync.RWMutex
)

// package-level db comes from api.Init(database) in main.go
// var db *sql.DB

// ----- request/response types -----
type paymentDetectedReq struct {
	OrderID     string  `json:"order_id"`
	TxHash      string  `json:"tx_hash"`
	AmountMinor *string `json:"amount_minor,omitempty"` // optional override; if nil, use order.amount_minor (string for large numbers)
}

type paymentDetectedResp struct {
	OrderID string `json:"order_id"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

// Optional background verification job (decouples API from RPC latency)
type verifyJob struct {
	OrderID    string
	TxHash     string
	MerchantID string
}

var (
	verifyJobs chan verifyJob
)

// StartVerificationWorkers starts n workers processing verification jobs. Call from main during startup if desired.
func StartVerificationWorkers(n int) {
	if n <= 0 {
		n = 1
	}
	if verifyJobs == nil {
		verifyJobs = make(chan verifyJob, 1000)
	}
	for i := 0; i < n; i++ {
		go func() {
			for job := range verifyJobs {
				// Process verification jobs asynchronously
				processVerificationJob(job)
			}
		}()
	}
}

// ----- constants for ledger -----
const (
	bucketMerchant = "merchant"
	bucketClearing = "clearing"

	dirDebit  = "debit"
	dirCredit = "credit"

	eventPaymentConfirmed = "PAYMENT_CONFIRMED"
)

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// PaymentDetectedHandler godoc
// @Summary      Detect payment event
// @Description  Notify the system of an on-chain payment for an order
// @Tags         events
// @Accept       json
// @Produce      json
// @Param        payment  body  paymentDetectedReq  true  "Payment info"
// @Success      200  {object}  paymentDetectedResp
// @Success      202  {object}  paymentDetectedResp
// @Failure      400  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Security     ApiKeyAuth
// @Router       /events/payment-detected [post]
func PaymentDetectedHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErrorJSON(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	if db == nil {
		writeErrorJSON(w, http.StatusInternalServerError, "db_not_initialized", "db not initialized")
		return
	}

	var req paymentDetectedReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "invalid_json", "invalid JSON")
		return
	}
	if req.OrderID == "" || req.TxHash == "" {
		writeErrorJSON(w, http.StatusBadRequest, "missing_fields", "order_id and tx_hash required")
		return
	}

	if verifyJobs != nil {
		// Load merchant_id for the job (needed by worker)
		var merchantID string
		if err := db.QueryRow(`SELECT merchant_id FROM orders WHERE id = ?`, req.OrderID).Scan(&merchantID); err != nil {
			writeErrorJSON(w, http.StatusNotFound, "order_not_found", "order not found")
			return
		}

		select {
		case verifyJobs <- verifyJob{OrderID: req.OrderID, TxHash: req.TxHash, MerchantID: merchantID}:
			writeJSON(w, http.StatusAccepted, paymentDetectedResp{OrderID: req.OrderID, Status: "PENDING", Message: "verification enqueued"})
			return
		default:
			// queue full, fall back to inline path
		}
	}

	// Inline path (fallback): do verification and DB updates synchronously
	// dedupe: if we've recently processed this tx_hash, short-circuit
	recentTxMu.RLock()
	t, ok := recentTx[strings.ToLower(req.TxHash)]
	recentTxMu.RUnlock()
	if ok && time.Since(t) < 2*time.Minute {
		writeJSON(w, http.StatusOK, paymentDetectedResp{OrderID: req.OrderID, Status: "PAID", Message: "recent duplicate tx hash"})
		return
	}

	reqCtx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	tx, err := db.BeginTx(reqCtx, &sql.TxOptions{})
	if err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	defer func() {
		_ = tx.Rollback() // safe if already committed
	}()

	var (
		merchantID  string
		amountMinor string
		asset       string
		status      string
	)
	err = tx.QueryRowContext(reqCtx, `
		   SELECT merchant_id, amount_minor, asset, status
		   FROM orders
		   WHERE id = ?
	   `, req.OrderID).Scan(&merchantID, &amountMinor, &asset, &status)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeErrorJSON(w, http.StatusNotFound, "order_not_found", "order not found")
			return
		}
		writeErrorJSON(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}

	// 1b) fetch merchant wallet address
	var merchantWalletAddress string
	err = tx.QueryRowContext(reqCtx, `SELECT merchant_wallet_address FROM merchants WHERE id = ?`, merchantID).Scan(&merchantWalletAddress)
	if err != nil || merchantWalletAddress == "" {
		writeErrorJSON(w, http.StatusBadRequest, "missing_wallet_address", "merchant wallet address not set")
		return
	}

	// 1c) on-chain verification for BSC-USD on BSC (throttled)
	if strings.ToUpper(asset) == "USDT" && strings.Contains(strings.ToLower(asset+"-bsc"), "bsc") {
		verifySem <- struct{}{}
		defer func() { <-verifySem }()
		// amount_minor is stored as string for 18 decimals (wei-style), parse to big.Int
		expectedAmount, ok := new(big.Int).SetString(amountMinor, 10)
		if !ok {
			writeErrorJSON(w, http.StatusBadRequest, "invalid_amount", "invalid amount_minor format")
			return
		}

		log.Printf("BSC verification: using amount %s (18-decimal) directly", amountMinor)

		ok, err := blockchain.VerifyBSCUSDTransfer(req.TxHash, merchantWalletAddress, expectedAmount)
		if err != nil || !ok {
			writeErrorJSON(w, http.StatusBadRequest, "onchain_verification_failed", "BSC-USD transfer not found or invalid")
			return
		}
		recentTxMu.Lock()
		recentTx[strings.ToLower(req.TxHash)] = time.Now()
		recentTxMu.Unlock()
	}

	// idempotency: if already PAID (or beyond), return OK without duplicating ledger
	if status == "PAID" || status == "SETTLED" || status == "REFUNDED" {
		_ = tx.Commit()
		writeJSON(w, http.StatusOK, paymentDetectedResp{
			OrderID: req.OrderID,
			Status:  status,
			Message: "no-op (already processed)",
		})
		return
	}

	// optional override amount
	if req.AmountMinor != nil && isValidAmountString(*req.AmountMinor) {
		amountMinor = *req.AmountMinor
	}

	now := time.Now().UTC().Format(time.RFC3339)

	// 2) update order -> PAID, set tx_hash, paid_at, but only if status is PENDING or CONFIRMING
	res, err := tx.ExecContext(reqCtx, `
		UPDATE orders
		SET status = ?, tx_hash = ?, paid_at = ?
		WHERE id = ? AND (status = 'PENDING' OR status = 'CONFIRMING')
	`, "PAID", req.TxHash, now, req.OrderID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	rowsAffected, _ := res.RowsAffected()
	if rowsAffected == 0 {
		// Another process already updated the order, treat as already processed
		_ = tx.Commit()
		writeJSON(w, http.StatusOK, paymentDetectedResp{
			OrderID: req.OrderID,
			Status:  status,
			Message: "no-op (already processed)",
		})
		return
	}

	// 3) insert two balanced ledger entries (double-entry)
	//    a) merchant CREDIT  +amount
	//    b) clearing DEBIT   -amount
	// (Use order_id + event_type to make these rows easy to query.)
	insertLedger := `
		INSERT INTO ledger_entries
		  (id, order_id, merchant_id, asset, amount_minor, bucket, direction, event_type, tx_hash, created_at)
		VALUES
		  (?,  ?,        ?,           ?,     ?,            ?,      ?,         ?,           ?,       ?)
	`
	// generate simple IDs (SQLite) — you can switch to UUIDs if you like
	lid1 := "led_" + now + "_a"
	lid2 := "led_" + now + "_b"

	if _, err := tx.ExecContext(reqCtx, insertLedger,
		lid1, req.OrderID, merchantID, asset, amountMinor, bucketMerchant, dirCredit, eventPaymentConfirmed, req.TxHash, now,
	); err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	if _, err := tx.ExecContext(reqCtx, insertLedger,
		lid2, req.OrderID, merchantID, asset, amountMinor, bucketClearing, dirDebit, eventPaymentConfirmed, req.TxHash, now,
	); err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}

	// 4) commit
	if err := tx.Commit(); err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}

	log.Printf("event=payment_detected order_id=%s merchant_id=%s asset=%s amount_minor=%d tx_hash=%s status=PAID", req.OrderID, merchantID, asset, amountMinor, req.TxHash)
	atomic.AddInt64(&paymentsDetectedTotal, 1)
	writeJSON(w, http.StatusOK, paymentDetectedResp{
		OrderID: req.OrderID,
		Status:  "PAID",
		Message: "payment recorded; double-entry ledger written",
	})
}

// DebugMetricsHandler godoc
// @Summary      Get debug metrics
// @Description  Returns in-memory metrics counters
// @Tags         debug
// @Produce      json
// @Success      200  {object}  map[string]int64
// @Router       /debug/metrics [get]
func DebugMetricsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]int64{
		"orders_created_total":    ordersCreatedTotal,
		"refunds_processed_total": refundsProcessedTotal,
		"payments_detected_total": paymentsDetectedTotal,
	})
}

// ReconciliationHandler godoc
// @Summary      Get reconciliation data
// @Description  Returns balance and settlement data for a merchant and asset
// @Tags         reconciliation
// @Produce      json
// @Param        merchant_id  query  string  true  "Merchant ID"
// @Param        asset  query  string  true  "Asset symbol"
// @Success      200  {object}  map[string]any
// @Failure      400  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Router       /reconciliation [get]
func ReconciliationHandler(w http.ResponseWriter, r *http.Request) {
	merchantID := r.URL.Query().Get("merchant_id")
	asset := r.URL.Query().Get("asset")
	if merchantID == "" || asset == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "merchant_id and asset are required"})
		return
	}
	if db == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db not initialized"})
		return
	}
	// Apply a short timeout for reconciliation queries
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	var merchantBalance, clearingBalance, unsettledPaid int64
	// Clearing balance
	if err := db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(CASE WHEN direction='credit' THEN amount_minor ELSE -amount_minor END),0)
		FROM ledger_entries
		WHERE merchant_id = ? AND asset = ? AND bucket = 'clearing'
	`, merchantID, asset).Scan(&clearingBalance); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// Merchant balance
	if err := db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(CASE WHEN direction='credit' THEN amount_minor ELSE -amount_minor END),0)
		FROM ledger_entries
		WHERE merchant_id = ? AND asset = ? AND bucket = 'merchant'
	`, merchantID, asset).Scan(&merchantBalance); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// Unsettled PAID orders count
	if err := db.QueryRowContext(ctx, `
		SELECT COALESCE(COUNT(1),0)
		FROM orders
		WHERE merchant_id = ? AND asset = ? AND status = 'PAID'
	`, merchantID, asset).Scan(&unsettledPaid); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"merchant_id":            merchantID,
		"asset":                  asset,
		"merchant_balance_minor": merchantBalance,
		"clearing_balance_minor": clearingBalance,
		"unsettled_paid_count":   unsettledPaid,
	})
}

// processVerificationJob verifies the tx on-chain and updates the DB/ledger similar to the inline path.
func processVerificationJob(job verifyJob) {
	log.Printf("processing verification job: order=%s tx=%s merchant=%s", job.OrderID, job.TxHash, job.MerchantID)

	// Defensive context timeout per job
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if db == nil {
		log.Printf("db is nil for job %s", job.OrderID)
		return
	}

	// Load order basics
	var (
		merchantID  string
		amountMinor string
		asset       string
		chain       string
		status      string
	)
	if err := db.QueryRowContext(ctx, `SELECT merchant_id, amount_minor, asset, chain, status FROM orders WHERE id = ?`, job.OrderID).Scan(&merchantID, &amountMinor, &asset, &chain, &status); err != nil {
		log.Printf("failed to load order %s: %v", job.OrderID, err)
		return
	}
	log.Printf("Processing verification for order %s: asset=%s, chain=%s, amount=%s", job.OrderID, asset, chain, amountMinor)

	// Already processed?
	if status == "PAID" || status == "SETTLED" || status == "REFUNDED" {
		log.Printf("order %s already processed with status %s", job.OrderID, status)
		return
	}
	// Merchant wallet
	var merchantWalletAddress string
	if err := db.QueryRowContext(ctx, `SELECT merchant_wallet_address FROM merchants WHERE id = ?`, merchantID).Scan(&merchantWalletAddress); err != nil || merchantWalletAddress == "" {
		return
	}
	// On-chain verify (only for BSC-USD on BSC chain)
	if strings.ToUpper(asset) == "USDT" && strings.ToUpper(chain) == "BSC" {
		log.Printf("Starting BSC-USD verification for order %s, tx %s", job.OrderID, job.TxHash)
		verifySem <- struct{}{}
		// amount_minor is stored as string for 18 decimals (wei-style), parse to big.Int
		expected, ok := new(big.Int).SetString(amountMinor, 10)
		if !ok {
			log.Printf("invalid amount format for order %s: %s", job.OrderID, amountMinor)
			<-verifySem
			return
		}

		log.Printf("BSC verification: using amount %s (18-decimal) directly", amountMinor)

		ok, err := blockchain.VerifyBSCUSDTransfer(job.TxHash, merchantWalletAddress, expected)
		<-verifySem
		if err != nil || !ok {
			log.Printf("verification failed for order=%s tx=%s err=%v ok=%v", job.OrderID, job.TxHash, err, ok)
			return
		}
		log.Printf("BSC verification passed for order %s", job.OrderID)
	} else if strings.ToUpper(asset) == "USDT" {
		log.Printf("Skipping blockchain verification for USDT on %s chain (order %s) - auto-approving for testing", chain, job.OrderID)
	} else {
		log.Printf("Skipping blockchain verification for %s asset (order %s) - auto-approving for testing", asset, job.OrderID)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return
	}
	defer func() { _ = tx.Rollback() }()
	// Guarded update
	res, err := tx.ExecContext(ctx, `UPDATE orders SET status=?, tx_hash=?, paid_at=? WHERE id=? AND (status='PENDING' OR status='CONFIRMING')`, "PAID", job.TxHash, now, job.OrderID)
	if err != nil {
		return
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		_ = tx.Commit()
		return
	}

	insertLedger := `INSERT INTO ledger_entries (id, order_id, merchant_id, asset, amount_minor, bucket, direction, event_type, tx_hash, created_at) VALUES (?,?,?,?,?,?,?,?,?,?)`
	lid1 := "led_" + now + "_a"
	lid2 := "led_" + now + "_b"
	if _, err := tx.ExecContext(ctx, insertLedger, lid1, job.OrderID, merchantID, asset, amountMinor, bucketMerchant, dirCredit, eventPaymentConfirmed, job.TxHash, now); err != nil {
		return
	}
	if _, err := tx.ExecContext(ctx, insertLedger, lid2, job.OrderID, merchantID, asset, amountMinor, bucketClearing, dirDebit, eventPaymentConfirmed, job.TxHash, now); err != nil {
		return
	}
	if err := tx.Commit(); err != nil {
		return
	}

	recentTxMu.Lock()
	recentTx[strings.ToLower(job.TxHash)] = time.Now()
	recentTxMu.Unlock()
	atomic.AddInt64(&paymentsDetectedTotal, 1)
}

// StartSettlementScheduler runs a background goroutine to settle PAID orders after a delay.
func StartSettlementScheduler(db *sql.DB, delay time.Duration, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			<-ticker.C
			now := time.Now().UTC()
			cutoff := now.Add(-delay).Format(time.RFC3339)
			rows, err := db.Query(`SELECT id FROM orders WHERE status='PAID' AND paid_at <= ?`, cutoff)
			if err != nil {
				continue
			}
			for rows.Next() {
				var orderID string
				if err := rows.Scan(&orderID); err == nil {
					_, err := db.Exec(`UPDATE orders SET status='SETTLED' WHERE id=?`, orderID)
					if err != nil {
						continue
					}
				}
			}
			rows.Close()
		}
	}()
}

// StartOrderTimeoutScheduler runs a background goroutine to mark PENDING orders as FAILED after timeout.
func StartOrderTimeoutScheduler(db *sql.DB, timeout time.Duration, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			<-ticker.C
			now := time.Now().UTC()
			cutoff := now.Add(-timeout).Format(time.RFC3339)

			// Find PENDING orders older than the timeout
			rows, err := db.Query(`SELECT id FROM orders WHERE status='PENDING' AND created_at <= ?`, cutoff)
			if err != nil {
				log.Printf("failed to query expired orders: %v", err)
				continue
			}

			var expiredCount int
			for rows.Next() {
				var orderID string
				if err := rows.Scan(&orderID); err == nil {
					// Mark as FAILED
					_, err := db.Exec(`UPDATE orders SET status='FAILED' WHERE id=? AND status='PENDING'`, orderID)
					if err != nil {
						log.Printf("failed to mark order %s as FAILED: %v", orderID, err)
						continue
					}
					expiredCount++
					log.Printf("marked order %s as FAILED due to 30-minute timeout", orderID)
				}
			}
			rows.Close()

			if expiredCount > 0 {
				log.Printf("marked %d orders as FAILED due to timeout", expiredCount)
			}
		}
	}()
}
