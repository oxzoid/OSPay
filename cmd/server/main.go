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

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-API-Key, Authorization")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func main() {
	dsn := "file:ospay.db?_pragma=busy_timeout=5000"
	database, err := db.Open(dsn)
	if err != nil {
		log.Fatalf("DB open failed: %v", err)
	}
	defer database.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := database.PingContext(ctx); err != nil {
		log.Fatalf("DB ping failed: %v", err)
	}

	if err := db.EnsureSchema(database); err != nil {
		log.Fatalf("migrations failed: %v", err)
	}

	api.Init(database)

	api.StartSettlementScheduler(database, 5*time.Minute, 10*time.Minute)

	api.StartOrderTimeoutScheduler(database, 30*time.Minute, 5*time.Minute)

	api.StartVerificationWorkers(4)
	addr := ":8080"
	fmt.Println("Server running on", addr)

	mux := http.NewServeMux()

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

	handler := corsMiddleware(mux)

	log.Fatal(http.ListenAndServe(addr, handler))
}
