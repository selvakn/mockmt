package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"
)

func main() {
	// Load environment variables
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, using system environment variables")
	}

	// Initialize database
	if err := initDatabase(); err != nil {
		log.Fatal("Failed to initialize database:", err)
	}

	// Start SMTP server
	go func() {
		if err := startSMTPServer(); err != nil {
			log.Fatal("Failed to start SMTP server:", err)
		}
	}()

	// Start web server
	go func() {
		if err := startWebServer(); err != nil {
			log.Fatal("Failed to start web server:", err)
		}
	}()

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Println("Shutting down gracefully...")
} 