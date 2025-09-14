@echo off
setlocal EnableDelayedExpansion

echo 🚀 Starting OSPay Full Stack...

REM Start backend
echo 📦 Starting Go backend on :8080...
start "OSPay Backend" cmd /c "go run cmd/server/main.go"

REM Wait a moment for backend to start
timeout /t 2 >nul

REM Start frontend  
echo 🌐 Starting React frontend on :5173...
start "OSPay Frontend" cmd /c "cd frontend && npm run dev"

echo.
echo ✅ Both services starting in separate windows!
echo 🌐 Frontend: http://localhost:5173
echo 📡 Backend:  http://localhost:8080
echo 📚 Swagger:  http://localhost:8080/swagger/
echo.
echo Press any key to exit...
pause >nul