package main

import (
	"log"
	"net/http"
	"strconv"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func startWebServer() error {
	// Initialize authentication
	initAuth()

	// Create Gin router
	r := gin.Default()

	// Configure CORS
	config := cors.DefaultConfig()
	config.AllowOrigins = []string{getEnv("FRONTEND_URL", "http://localhost:3000")}
	config.AllowCredentials = true
	config.AllowHeaders = []string{"Origin", "Content-Length", "Content-Type", "Authorization"}
	r.Use(cors.New(config))

	// Public routes
	r.GET("/", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": "WebMail API"})
	})

	r.GET("/auth/oauth", handleOAuthLogin)
	r.GET("/auth/callback", handleOAuthCallback)

	// Protected routes
	api := r.Group("/api")
	api.Use(authMiddleware())
	{
		api.GET("/user", handleGetUser)
		api.GET("/emails", handleGetEmails)
		api.GET("/emails/:id", handleGetEmail)
		api.DELETE("/emails/:id", handleDeleteEmail)
		api.GET("/stats", handleGetStats)
	}

	port := getEnv("PORT", "8080")
	log.Printf("Starting web server on port %s", port)
	return r.Run(":" + port)
}

func handleGetUser(c *gin.Context) {
	userEmail := c.GetString("user_email")
	user, err := getUserByEmail(userEmail)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}

	c.JSON(http.StatusOK, user)
}

func handleGetEmails(c *gin.Context) {
	userID := c.GetInt("user_id")
	emails, err := getEmailsByUser(userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get emails"})
		return
	}
	log.Printf("Emails: %+v", emails)

	c.JSON(http.StatusOK, emails)
}

func handleGetEmail(c *gin.Context) {
	userID := c.GetInt("user_id")
	emailID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid email ID"})
		return
	}

	email, err := getEmailByID(emailID, userID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Email not found"})
		return
	}

	c.JSON(http.StatusOK, email)
}

func handleDeleteEmail(c *gin.Context) {
	userID := c.GetInt("user_id")
	emailID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid email ID"})
		return
	}

	err = deleteEmail(emailID, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete email"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Email deleted successfully"})
}

func handleGetStats(c *gin.Context) {
	userID := c.GetInt("user_id")
	count, err := getEmailStats(userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get stats"})
		return
	}

	userEmail := c.GetString("user_email")
	c.JSON(http.StatusOK, gin.H{
		"total_emails": count,
		"user_email":   userEmail,
	})
}
