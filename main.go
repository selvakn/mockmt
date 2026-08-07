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

	if _, _, err := mockmt.LoadSMTPCredentials(); err != nil {
		log.Fatal("SMTP authentication is not configured:", err)
	}

	relayConfig, err := mockmt.LoadRelayConfig()
	if err != nil {
		log.Fatal(err)
	}
	mockmt.InitRelay(relayConfig)
	mockmt.LogRelayStartupSummary(relayConfig)

	if err := mockmt.InitDatabase(); err != nil {
		log.Fatal("Failed to initialize database:", err)
	}

	if relayConfig.Enabled {
		if settled, err := mockmt.SweepOrphanedSendingMessages(); err != nil {
			log.Fatal("Failed to sweep orphaned sending messages:", err)
		} else if settled > 0 {
			log.Printf("Startup sweep: settled %d message(s) left in \"sending\" by a previous run as Failed-indeterminate", settled)
		}
		mockmt.StartRetentionTicker(relayConfig.RetentionDays)
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
