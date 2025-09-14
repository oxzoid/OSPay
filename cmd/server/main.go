// @securityDefinitions.apikey ApiKeyAuth
// @in header
// @name X-API-Key
package main

// @title OSPay API
// @version 1.0
// @description Crypto payment processor API.
// @host localhost:8080
// @BasePath /

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/oxzoid/OSPay/pkg/api"
	"github.com/oxzoid/OSPay/pkg/db"
	httpSwagger "github.com/swaggo/http-swagger"

	_ "github.com/oxzoid/OSPay/docs"
)

// CORS middleware to allow frontend communication
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Set CORS headers
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-API-Key, Authorization")

		// Handle preflight OPTIONS request
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func main() {
	// --- 1) pick DSN with a safe default (SQLite: no install needed) ---
	dsn := "file:ospay.db?_pragma=busy_timeout=5000"
	database, err := db.Open(dsn)
	if err != nil {
		log.Fatalf("DB open failed: %v", err)
	}
	defer database.Close()

	// ping to be sure
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := database.PingContext(ctx); err != nil {
		log.Fatalf("DB ping failed: %v", err)
	}

	// 🔑 THIS is what makes api package see the db
	// after Open() and Ping()
	if err := db.EnsureSchema(database); err != nil {
		log.Fatalf("migrations failed: %v", err)
	}

	api.Init(database)

	// Start the settlement scheduler: T+24h, runs every hour
	api.StartSettlementScheduler(database, 5*time.Minute, 10*time.Minute)

	// Start order timeout scheduler: 30-minute timeout, check every 5 minutes
	api.StartOrderTimeoutScheduler(database, 30*time.Minute, 5*time.Minute)

	// Optionally start background verification workers (currently placeholder)
	api.StartVerificationWorkers(4)

	// --- 3) give DB to API package (so handlers can use it) ---
	// api.Init(database) // Removed: not needed, no such function

	addr := ":8080"
	fmt.Println("Server running on", addr)

	// Create a new ServeMux and apply CORS middleware
	mux := http.NewServeMux()

	// Add all routes to the mux
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	})

	mux.HandleFunc("/dbhealth", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := database.PingContext(ctx); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"ok":false}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	})

	mux.Handle("/swagger/", httpSwagger.WrapHandler)

	mux.HandleFunc("/orders", api.APIKeyAuthMiddleware(api.CreateOrderHandler))
	mux.HandleFunc("/orders/get", api.APIKeyAuthMiddleware(api.GetOrderHandler))
	mux.HandleFunc("/orders/refund", api.APIKeyAuthMiddleware(api.RefundHandler))
	mux.HandleFunc("/events/payment-detected", api.APIKeyAuthMiddleware(api.PaymentDetectedHandler))
	mux.HandleFunc("/debug/metrics", api.DebugMetricsHandler)
	mux.HandleFunc("/merchants", api.CreateMerchantHandler)

	// Apply CORS middleware to the entire mux
	handler := corsMiddleware(mux)

	log.Fatal(http.ListenAndServe(addr, handler))
}
