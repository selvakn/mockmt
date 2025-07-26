package main

import (
	"crypto/rand"
	"fmt"
	"os"
	"strings"
	"time"
)

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func generateMessageID() string {
	// Generate a random message ID
	b := make([]byte, 16)
	rand.Read(b)
	return fmt.Sprintf("%x@localhost", b)
}

func formatDate(date time.Time) string {
	now := time.Now()
	diff := now.Sub(date)

	if diff < 24*time.Hour {
		return date.Format("15:04")
	} else if diff < 7*24*time.Hour {
		return date.Format("Mon")
	} else {
		return date.Format("Jan 2")
	}
}

func truncateText(text string, maxLength int) string {
	if len(text) <= maxLength {
		return text
	}
	return strings.TrimSpace(text[:maxLength]) + "..."
} 