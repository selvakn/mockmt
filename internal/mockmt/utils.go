package mockmt

import (
	"crypto/rand"
	"fmt"
	"os"
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
