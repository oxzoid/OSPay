package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// MerchantCreateReq is the request body for creating a merchant
// swagger:model
// @Description Request to create a new merchant
// @Param name body string true "Merchant name"
// @Param merchant_wallet_address body string true "Merchant wallet address"
type MerchantCreateReq struct {
	Name                  string `json:"name"`
	MerchantWalletAddress string `json:"merchant_wallet_address"`
}

// MerchantCreateResp is the response for merchant creation
// swagger:model
// @Description Response after creating a merchant
// @Param id body string true "Merchant ID"
// @Param api_key body string true "API Key"
// @Param merchant_wallet_address body string true "Merchant wallet address"
type MerchantCreateResp struct {
	ID                    string `json:"id"`
	APIKey                string `json:"api_key"`
	MerchantWalletAddress string `json:"merchant_wallet_address"`
}

// CreateMerchantHandler godoc
// @Summary      Create a new merchant
// @Description  Creates a new merchant and returns the merchant ID and API key
// @Tags         merchants
// @Accept       json
// @Produce      json
// @Param        merchant  body  MerchantCreateReq  true  "Merchant info"
// @Success      201  {object}  MerchantCreateResp
// @Failure      400  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Router       /merchants [post]
func CreateMerchantHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "method_not_allowed"})
		return
	}
	if db == nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "db_not_initialized"})
		return
	}
	var req MerchantCreateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_json"})
		return
	}
	if req.Name == "" || req.MerchantWalletAddress == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "missing_fields"})
		return
	}
	id := uuid.New().String()
	apiKey := uuid.New().String()
	now := time.Now().UTC().Format(time.RFC3339)
	const insert = `INSERT INTO merchants (id, name, api_key, merchant_wallet_address, created_at) VALUES (?, ?, ?, ?, ?)`
	_, err := db.Exec(insert, id, req.Name, apiKey, req.MerchantWalletAddress, now)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "db_error"})
		return
	}
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(MerchantCreateResp{
		ID:                    id,
		APIKey:                apiKey,
		MerchantWalletAddress: req.MerchantWalletAddress,
	})
}
