package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"mockmt/internal/mockmt"

	"github.com/joho/godotenv"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, using system environment variables")
	}

	if err := mockmt.InitDatabase(); err != nil {
		log.Fatal("Failed to initialize database:", err)
	}

	go func() {
		if err := mockmt.StartSMTPServer(); err != nil {
			log.Fatal("Failed to start SMTP server:", err)
		}
	}()

	go func() {
		if err := mockmt.StartWebServer(); err != nil {
			log.Fatal("Failed to start web server:", err)
		}
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Println("Shutting down gracefully...")
}
