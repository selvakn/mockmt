#!/bin/bash

echo "🚀 Setting up Modern WebMail Application..."

# Check if Go is installed
if ! command -v go &> /dev/null; then
    echo "❌ Go is required but not installed."
    echo "   Please install Go from: https://golang.org/dl/"
    exit 1
fi

# Check if Node.js is installed
if ! command -v node &> /dev/null; then
    echo "❌ Node.js is required but not installed."
    echo "   Please install Node.js from: https://nodejs.org/"
    exit 1
fi

# Check if npm is installed
if ! command -v npm &> /dev/null; then
    echo "❌ npm is required but not installed."
    exit 1
fi

echo "✅ Prerequisites check passed"

# Install Go dependencies
echo "📦 Installing Go dependencies..."
go mod tidy

if [ $? -ne 0 ]; then
    echo "❌ Failed to install Go dependencies"
    exit 1
fi

echo "✅ Go dependencies installed"

# Install frontend dependencies
echo "📦 Installing frontend dependencies..."
cd frontend
npm install

if [ $? -ne 0 ]; then
    echo "❌ Failed to install frontend dependencies"
    exit 1
fi

cd ..
echo "✅ Frontend dependencies installed"

# Create .env file if it doesn't exist
if [ ! -f .env ]; then
    echo "📝 Creating .env file from template..."
    cp env.example .env
    echo "⚠️  Please edit .env file with your OAuth server credentials"
    echo "   Configure your OAuth server (Keycloak, Auth0, etc.) and update the .env file"
fi

echo ""
echo "🎉 Setup completed successfully!"
echo ""
echo "📋 Next steps:"
echo "1. Edit .env file with your Google OAuth credentials"
echo "2. Run 'go run .' to start the backend"
echo "3. Run 'cd frontend && npm run dev' to start the frontend"
echo "4. Access the application at http://localhost:3000"
echo ""
echo "📧 To test the SMTP server, run: go run cmd/test_email/main.go" 