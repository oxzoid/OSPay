#!/bin/bash

# Start the crypto payment processor full stack

echo "🚀 Starting OSPay Full Stack..."

# Function to cleanup background processes
cleanup() {
    echo "🛑 Shutting down..."
    kill $(jobs -p) 2>/dev/null
    exit 0
}

# Set trap to cleanup on script exit
trap cleanup SIGINT SIGTERM

# Start backend
echo "📦 Starting Go backend on :8080..."
cd "$(dirname "$0")"
go run cmd/server/main.go &
BACKEND_PID=$!

# Wait a moment for backend to start
sleep 2

# Start frontend
echo "🌐 Starting React frontend on :5173..."
cd frontend
npm run dev &
FRONTEND_PID=$!

echo ""
echo "✅ Both services started!"
echo "🌐 Frontend: http://localhost:5173"
echo "📡 Backend:  http://localhost:8080"
echo "📚 Swagger:  http://localhost:8080/swagger/"
echo ""
echo "Press Ctrl+C to stop all services"

# Wait for both processes
wait $BACKEND_PID $FRONTEND_PID