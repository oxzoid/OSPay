# Crypto Payment Processor

A high-performance cryptocurrency payment processor built in Go with React frontend. Supports real-time payment verification on multiple blockchain networks with automatic settlement and comprehensive transaction management
#
NOTE: only payment verification is in frontend rn will implement dashboiard and other features asap. the apis are working just not implemented in frontend and swagger will be improved
#
ONLY FOR EDUCATIONAL AND LEARNING PURPOSES,TRYING TO GET FINTECH RECRUITERS TO NOTICE ME
#
![Version](https://img.shields.io/badge/version-1.0.0-blue.svg)
![Go](https://img.shields.io/badge/go-1.21+-blue.svg)
![License](https://img.shields.io/badge/license-Apache%202.0-green.svg)

##  Features

- **Chain Support**: BSC,others are placeholders for now
- **Real-Time Verification**: Automatic blockchain transaction verification
- **Payment Processing**: Complete order lifecycle (PENDING → PAID → SETTLED)
- **QR Code Payments**: Generate payment QR codes for mobile wallets
- **Auto Settlement**: Background settlement with configurable delays
- **Double-Entry Ledger**: Proper accounting with merchant and clearing buckets
- **Idempotent Operations**: Safe retry mechanisms and duplicate prevention

##  Quick Start

### Prerequisites

- Go 
- Node.js
- Git

### Installation

1. **Clone the repository**
   ```bash
   git clone https://github.com/yourusername/crypto-payment-processor.git
   cd crypto-payment-processor
   ```

2. **Start the development environment**
   ```bash
   go run ./cmd/server
   ```
   ```bash
   cd frontend
   npm run dev
   ```

3. **Access the application**
   - Frontend: http://localhost:5173
   - Backend API: http://localhost:8080
   - Swagger Docs: http://localhost:8080/swagger/

##  Architecture

```
├── cmd/server/          # HTTP server and application entry point
├── pkg/
│   ├── api/            # REST API handlers and middleware
│   ├── blockchain/     # Blockchain integration (BSC, ETH, TRON)
│   └── db/             # Database layer and migrations
├── frontend/           # React TypeScript frontend
└── docs/              # API documentation (Swagger)
```

##  API Documentation

### Authentication

All API endpoints require the `X-API-Key` header for merchant authentication.

### Core Endpoints

#### Create Order
```http
POST /orders
Content-Type: application/json
X-API-Key: your-merchant-api-key

{
  "id": "order_123",
  "merchant_id": "merchant_abc",
  "asset": "USDT",
  "chain": "BSC",
  "amount_minor": 1000000
}
```

#### Payment Detection
```http
POST /events/payment-detected
Content-Type: application/json

{
  "order_id": "order_123",
  "tx_hash": "0xabc123..."
}
```

#### Get Order Status
```http
GET /orders/get?id=order_123
X-API-Key: your-merchant-api-key
```

For complete API documentation, visit `/swagger/` when running the server.

## 🔧 Configuration

### Environment Variables

```bash
# Backend Configuration
BSC_RPC_URL=https://bsc-dataseed.binance.org/
DATABASE_URL=file:ospay.db?_pragma=busy_timeout=5000

# Frontend Configuration (optional)
VITE_API_BASE=http://localhost:8080
```

### Database

The application uses SQLite with optimized settings:
- WAL mode for better concurrency
- Foreign key constraints enabled
- Automatic schema migrations

##  Supported Networks

| Network | Asset | Contract Address | Status |
|---------|-------|------------------|--------|
| BSC | BSC-USD | `0x55d398326f99059fF775485246999027B3197955` | ✅ Verified |
| Ethereum | USDT | `0xdAC17F958D2ee523a2206206994597C13D831ec7` | 🔄 Testing/not supported rn|
| TRON | USDT | `TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t` | 🔄 Testing/not supported rn |

##  Security Features

- **API Key Authentication**: Secure merchant identification
- **Input Validation**: Comprehensive request validation
- **Idempotent Operations**: Safe retry mechanisms
- **Transaction Verification**: On-chain payment verification
- **Rate Limiting**: Protection against abuse
- **Secure Headers**: CORS and security headers configured

##  Monitoring & Observability

### Metrics Endpoint
```http
GET /debug/metrics
```

Returns operational metrics including:
- Total orders created
- Payments processed
- Refunds completed
- System health status

### Health Check
```http
GET /health
```

##  Payment Flow

1. **Order Creation**: Merchant creates order with amount and asset
2. **QR Generation**: System generates payment QR code
3. **Customer Payment**: Customer scans QR and sends payment
4. **Detection**: System detects payment via webhook or polling
5. **Verification**: Blockchain verification confirms payment
6. **Settlement**: Automatic settlement after confirmation period
7. **Reconciliation**: Double-entry ledger maintains balance


### Running Tests
```bash
go test ./...
```

### Building for Production
```bash
# Backend
go build -o server ./cmd/server

# Frontend
cd frontend
npm run build
```

### Database Migrations
```bash
# Migrations are automatically applied on startup
# Manual application:
go run ./cmd/server
```

##  Scaling Considerations for future

- **Database**: Consider PostgreSQL for high-throughput scenarios
- **Caching**: Redis integration for improved performance
- **Queue System**: External job queue for high-volume processing
- **Load Balancing**: Multiple server instances with shared database
- **Monitoring**: Prometheus/Grafana integration available

##  Troubleshooting

### Common Issues

**Build Errors**
```bash
go mod tidy
go mod download
```

## 📄 License

This project is licensed under  GNU AFFERO GENERAL PUBLIC LICENSE- see the [LICENSE](LICENSE.MD) file for details.

For questions and support:
- Create an issue on GitHub
- Check the [API documentation](/swagger/)
- Review the troubleshooting section above

---

**Built with  using Go and React**
