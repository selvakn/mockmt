package main

import (
	"io"
	"log"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

func startWebServer() error {
	// Initialize authentication
	initAuth()

	// Create Gin router
	r := gin.Default()

	// Public routes
	r.GET("/api", func(c *gin.Context) {
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

	if getEnv("SERVE_FRONTEND_DIST", "") == "true" {
		r.Static("/assets", "./frontend/dist/assets")

		r.NoRoute(func(c *gin.Context) {
			c.File("./frontend/dist/index.html")
		})
	} else {
		r.NoRoute(func(c *gin.Context) {
			proxyURL := "http://localhost:3002"
			proxy := func(c *gin.Context) {
				remote, err := http.NewRequest(c.Request.Method, proxyURL+c.Request.RequestURI, c.Request.Body)
				if err != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"error": "Proxy request creation failed"})
					return
				}
				remote.Header = c.Request.Header

				client := &http.Client{}
				resp, err := client.Do(remote)
				if err != nil {
					c.JSON(http.StatusBadGateway, gin.H{"error": "Proxy request failed"})
					return
				}
				defer resp.Body.Close()

				for k, v := range resp.Header {
					for _, vv := range v {
						c.Writer.Header().Add(k, vv)
					}
				}
				c.Writer.WriteHeader(resp.StatusCode)
				_, err = io.Copy(c.Writer, resp.Body)
				if err != nil {
					log.Printf("Error copying response body in proxy: %v", err)
				}
			}
			proxy(c)
		})
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
