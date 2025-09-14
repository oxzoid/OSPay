package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"
)

var refundsProcessedTotal int64

type refundResp struct {
	OrderID string `json:"order_id"`
	Status  string `json:"status"`
	Message string `json:"message"`
}
type refundReq struct {
	OrderID              string `json:"order_id"`
	AmountMinor          *int64 `json:"amount_minor,omitempty"`
	RefundTxHash         string `json:"refundtxhash,omitempty"`
	RefundIdempotencyKey string `json:"refund_idempotency_key"`
}

const (
	refundEvent = "REFUND"
)

// RefundHandler godoc
// @Summary      Refund an order
// @Description  Refunds a paid order by ID
// @Tags         orders
// @Accept       json
// @Produce      json
// @Param        id  query  string  true  "Order ID"
// @Param        refund  body  refundReq  true  "Refund info"
// @Success      200  {object}  refundResp
// @Failure      400  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Security     ApiKeyAuth
// @Router       /orders/refund [post]
func RefundHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErrorJSON(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	if db == nil {
		writeErrorJSON(w, http.StatusInternalServerError, "db_not_initialized", "db not initialized")
		return
	}

	orderID := r.URL.Query().Get("id")
	if orderID == "" {
		writeErrorJSON(w, http.StatusBadRequest, "missing_query_param", "missing query param")
		return
	}
	var req refundReq
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	if req.RefundIdempotencyKey == "" {
		writeErrorJSON(w, http.StatusBadRequest, "missing_idempotency_key", "refund_idempotency_key is required")
		return
	}

	// Check for existing refund with this idempotency key
	const sel = `SELECT id, status FROM orders WHERE refund_idempotency_key = ? AND id = ?`
	var existingID, existingStatus string
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	err := db.QueryRowContext(ctx, sel, req.RefundIdempotencyKey, orderID).Scan(&existingID, &existingStatus)
	if err == nil {
		// Refund already exists, return it
		writeJSON(w, http.StatusOK, refundResp{
			OrderID: existingID,
			Status:  existingStatus,
			Message: "no-op (already refunded)",
		})
		return
	} else if err != sql.ErrNoRows {
		writeErrorJSON(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}

	tx, err := db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	defer func() { _ = tx.Rollback() }()

	var (
		merchantID string
		orderAmt   int64
		asset      string
		status     string
	)
	err = tx.QueryRowContext(ctx, `
		SELECT merchant_id, amount_minor, asset, status
		FROM orders
		WHERE id = ?
	`, orderID).Scan(&merchantID, &orderAmt, &asset, &status)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "order not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	switch status {
	case "REFUNDED":
		// _ = tx.Commit()
		writeJSON(w, http.StatusOK, refundResp{
			OrderID: orderID, Status: "REFUNDED", Message: "no-op (already refunded)",
		})
		return
	case "SETTLED":
		writeErrorJSON(w, http.StatusConflict, "cannot_refund_settled", "cannot refund a SETTLED order")
		return
	case "PENDING", "CONFIRMING":
		writeErrorJSON(w, http.StatusConflict, "order_not_paid", "order not paid yet; cannot refund")
		return
		// case "PAID": allowed
	}
	amt := orderAmt
	if req.AmountMinor != nil && *req.AmountMinor > 0 {
		amt = *req.AmountMinor
	}
	if amt <= 0 {
		writeErrorJSON(w, http.StatusBadRequest, "invalid_refund_amount", "refund amount must be > 0")
		return
	}
	if amt > orderAmt {
		writeErrorJSON(w, http.StatusBadRequest, "refund_exceeds_order", "refund amount cannot exceed order amount")
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)

	const insLedger = `
		INSERT INTO ledger_entries
		  (id, order_id, merchant_id, asset, amount_minor, bucket, direction, event_type, tx_hash, created_at)
		VALUES
		  (?,  ?,        ?,           ?,     ?,            ?,      ?,         ?,          ?,      ?)
	`
	lidA := "led_" + now + "_refund_a_" + orderID
	lidB := "led_" + now + "_refund_b_" + orderID

	if _, err := tx.ExecContext(ctx, insLedger,
		lidA, orderID, merchantID, asset, amt, bucketMerchant, dirDebit, refundEvent, req.RefundTxHash, now,
	); err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	if _, err := tx.ExecContext(ctx, insLedger,
		lidB, orderID, merchantID, asset, amt, bucketClearing, dirCredit, refundEvent, req.RefundTxHash, now,
	); err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	if _, err := tx.ExecContext(ctx, `
		   UPDATE orders
		   SET status = ?, refund_idempotency_key = ?
		   WHERE id = ?
	   `, "REFUNDED", req.RefundIdempotencyKey, orderID); err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}

	// 4) Commit atomically
	if err := tx.Commit(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	log.Printf("event=refund_processed order_id=%s merchant_id=%s asset=%s amount_minor=%d status=REFUNDED", orderID, merchantID, asset, amt)
	refundsProcessedTotal++
	writeJSON(w, http.StatusOK, refundResp{
		OrderID: orderID,
		Status:  "REFUNDED",
		Message: "refund recorded with double-entry ledger",
	})

}
