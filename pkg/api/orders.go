package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

var ordersCreatedTotal int64

// db is set by api.Init(database *sql.DB) in main.go
var db *sql.DB

// Init is called from main.go after opening the DB connection.
func Init(database *sql.DB) { db = database }

// ---------- helpers (scoped to this file to avoid name clashes) ----------

// isValidAmountString checks if a string represents a valid positive integer
func isValidAmountString(s string) bool {
	if s == "" || s == "0" {
		return false
	}
	// Check if string contains only digits
	for _, char := range s {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

type orderCreateReq struct {
	MerchantID     string `json:"merchant_id"`
	AmountMinor    string `json:"amount_minor"` // String to handle large 18-decimal numbers
	Asset          string `json:"asset"`        // e.g., "USDC"
	Chain          string `json:"chain"`        // e.g., "polygon-amoy"
	IdempotencyKey string `json:"idempotency_key"`
}

type orderCreateResp struct {
	OrderID        string `json:"order_id"`
	DepositAddress string `json:"deposit_address"`
	Status         string `json:"status"`
}

type orderGetResp struct {
	ID             string  `json:"id"`
	MerchantID     string  `json:"merchant_id"`
	AmountMinor    string  `json:"amount_minor"` // String to handle large 18-decimal numbers
	Asset          string  `json:"asset"`
	Chain          string  `json:"chain"`
	Status         string  `json:"status"`
	DepositAddress string  `json:"deposit_address"`
	TxHash         *string `json:"tx_hash,omitempty"`
	ConfirmedBlock *int64  `json:"confirmed_block,omitempty"`
	PaidAt         *string `json:"paid_at,omitempty"`
	CreatedAt      string  `json:"created_at"`
}

func writeErrorJSON(w http.ResponseWriter, code int, errStr, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": errStr, "message": msg})
}
func writeJSONOrders(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func badReq(w http.ResponseWriter, msg string) {
	writeErrorJSON(w, http.StatusBadRequest, "bad_request", msg)
}

func serverErr(w http.ResponseWriter, err error) {
	writeErrorJSON(w, http.StatusInternalServerError, "internal_error", err.Error())
}

// a simple placeholder deposit address (looks like 0x + 40 hex chars)
func makeDepositAddress() string {
	raw := strings.ReplaceAll(uuid.New().String(), "-", "")
	hex := raw + raw // ensure long enough
	return "0x" + hex[:40]
}

// CreateOrderHandler godoc
// @Summary      Create a new order
// @Description  Creates a new payment order for a merchant
// @Tags         orders
// @Accept       json
// @Produce      json
// @Param        order  body  orderCreateReq  true  "Order info"
// @Success      200  {object}  orderCreateResp
// @Failure      400  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Security     ApiKeyAuth
// CreateOrderHandler godoc
// @Summary      Create a new order
// @Description  Creates a new payment order for a merchant
// @Tags         orders
// @Accept       json
// @Produce      json
// @Param        order  body  orderCreateReq  true  "Order info"
// @Success      200  {object}  orderCreateResp
// @Failure      400  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Security     ApiKeyAuth
// @Router       /orders [post]
func CreateOrderHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErrorJSON(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	if db == nil {
		writeErrorJSON(w, http.StatusInternalServerError, "db_not_initialized", "db not initialized")
		return
	}

	var req orderCreateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "invalid_json", "invalid JSON body")
		return
	}
	if req.MerchantID == "" || !isValidAmountString(req.AmountMinor) || req.Asset == "" || req.Chain == "" {
		writeErrorJSON(w, http.StatusBadRequest, "missing_fields", "merchant_id, amount_minor (>0), asset, chain are required")
		return
	}

	if req.IdempotencyKey == "" {
		writeErrorJSON(w, http.StatusBadRequest, "missing_idempotency_key", "idempotency_key is required")
		return
	}

	// Check for existing order with this idempotency key
	const sel = `SELECT id, deposit_address, status FROM orders WHERE order_idempotency_key = ? AND merchant_id = ?`
	var existingID, existingDeposit, existingStatus string
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	err := db.QueryRowContext(ctx, sel, req.IdempotencyKey, req.MerchantID).Scan(&existingID, &existingDeposit, &existingStatus)
	if err == nil {
		// Order already exists, return it
		writeJSONOrders(w, http.StatusOK, orderCreateResp{
			OrderID:        existingID,
			DepositAddress: existingDeposit,
			Status:         existingStatus,
		})
		return
	} else if err != sql.ErrNoRows {
		writeErrorJSON(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}

	id := uuid.New().String()

	var merchantWalletAddress string
	err = db.QueryRowContext(ctx, `SELECT merchant_wallet_address FROM merchants WHERE id = ?`, req.MerchantID).Scan(&merchantWalletAddress)
	if err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "merchant_not_found", "merchant not found")
		return
	}

	deposit := merchantWalletAddress
	status := "PENDING"
	now := time.Now().UTC().Format(time.RFC3339)

	const insert = `
		INSERT INTO orders
		  (id, merchant_id, amount_minor, asset, chain, status, deposit_address, created_at, order_idempotency_key)
		VALUES
		  (?,  ?,           ?,            ?,     ?,     ?,      ?,               ?,      ?)
	`
	_, err = db.ExecContext(ctx, insert, id, req.MerchantID, req.AmountMinor, req.Asset, req.Chain, status, deposit, now, req.IdempotencyKey)
	if err != nil {
		// If unique constraint error, fetch and return existing order
		if sqliteIsUniqueConstraintError(err) {
			err2 := db.QueryRowContext(ctx, sel, req.IdempotencyKey, req.MerchantID).Scan(&existingID, &existingDeposit, &existingStatus)
			if err2 == nil {
				writeJSONOrders(w, http.StatusOK, orderCreateResp{
					OrderID:        existingID,
					DepositAddress: existingDeposit,
					Status:         existingStatus,
				})
				return
			}
		}
		writeErrorJSON(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}

	log.Printf("event=order_created order_id=%s merchant_id=%s asset=%s amount_minor=%s status=%s", id, req.MerchantID, req.Asset, req.AmountMinor, status)
	ordersCreatedTotal++
	writeJSONOrders(w, http.StatusOK, orderCreateResp{
		OrderID:        id,
		DepositAddress: deposit,
		Status:         status,
	})
}

// sqliteIsUniqueConstraintError checks if an error is a SQLite unique constraint violation.
func sqliteIsUniqueConstraintError(err error) bool {
	if err == nil {
		return false
	}
	// SQLite (modernc.org/sqlite) returns error strings containing "UNIQUE constraint failed"
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// GetOrderHandler godoc
// @Summary      Get order by ID
// @Description  Returns order details for a given order ID
// @Tags         orders
// @Accept       json
// @Produce      json
// @Param        id  query  string  true  "Order ID"
// @Success      200  {object}  orderGetResp
// @Failure      400  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Security     ApiKeyAuth
// @Router       /orders/get [get]
func GetOrderHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONOrders(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if db == nil {
		serverErr(w, errors.New("db not initialized"))
		return
	}

	id := r.URL.Query().Get("id")
	if id == "" {
		badReq(w, "missing query param: id")
		return
	}

	const sel = `
		SELECT id, merchant_id, amount_minor, asset, chain, status, deposit_address,
		       tx_hash, confirmed_block, paid_at, created_at
		FROM orders
		WHERE id = ?
	`
	var (
		resp           orderGetResp
		txHash         sql.NullString
		confirmedBlock sql.NullInt64
		paidAt         sql.NullString
	)
	ctx2, cancel2 := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel2()
	err := db.QueryRowContext(ctx2, sel, id).Scan(
		&resp.ID, &resp.MerchantID, &resp.AmountMinor, &resp.Asset, &resp.Chain, &resp.Status, &resp.DepositAddress,
		&txHash, &confirmedBlock, &paidAt, &resp.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSONOrders(w, http.StatusNotFound, map[string]string{"error": "order not found"})
			return
		}
		serverErr(w, err)
		return
	}

	if txHash.Valid {
		resp.TxHash = &txHash.String
	}
	if confirmedBlock.Valid {
		val := confirmedBlock.Int64
		resp.ConfirmedBlock = &val
	}
	if paidAt.Valid {
		val := paidAt.String
		resp.PaidAt = &val
	}

	writeJSONOrders(w, http.StatusOK, resp)
}

func APIKeyAuthMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		apiKey := r.Header.Get("X-API-Key")
		if apiKey == "" {
			writeJSONOrders(w, http.StatusUnauthorized, map[string]string{"error": "missing X-API-Key header", "message": "API key required"})
			return
		}
		var merchantID string
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		err := db.QueryRowContext(ctx, "SELECT id FROM merchants WHERE api_key = ?", apiKey).Scan(&merchantID)
		if err != nil {
			writeJSONOrders(w, http.StatusUnauthorized, map[string]string{"error": "invalid API key", "message": "Unauthorized"})
			return
		}
		next(w, r)
	}
}
