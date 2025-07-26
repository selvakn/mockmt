package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/oauth2"
)

var (
	oauthConfig *oauth2.Config
	jwtSecret   []byte
)

type OAuthUserInfo struct {
	ID                string `json:"sub"`
	Email             string `json:"email"`
	EmailVerified     bool   `json:"email_verified"`
	Name              string `json:"name"`
	Picture           string `json:"picture"`
	PreferredUsername string `json:"preferred_username"`
}

type Claims struct {
	Email  string `json:"email"`
	UserID int    `json:"user_id"`
	jwt.RegisteredClaims
}

func initAuth() {
	clientID := getEnv("OAUTH_CLIENT_ID", "")
	clientSecret := getEnv("OAUTH_CLIENT_SECRET", "")
	authURL := getEnv("OAUTH_AUTH_URL", "")
	tokenURL := getEnv("OAUTH_TOKEN_URL", "")
	redirectURI := getEnv("OAUTH_REDIRECT_URI", "http://localhost:8080/auth/callback")
	scopes := getEnv("OAUTH_SCOPES", "openid email profile")

	jwtSecretStr := getEnv("JWT_SECRET_KEY", "your-secret-key-change-this")
	jwtSecret = []byte(jwtSecretStr)

	// Parse scopes
	scopeList := strings.Split(scopes, " ")

	oauthConfig = &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURI,
		Scopes:       scopeList,
		Endpoint: oauth2.Endpoint{
			AuthURL:  authURL,
			TokenURL: tokenURL,
		},
	}
}

func handleOAuthLogin(c *gin.Context) {
	url := oauthConfig.AuthCodeURL("state")
	c.Redirect(http.StatusTemporaryRedirect, url)
}

func handleOAuthCallback(c *gin.Context) {
	code := c.Query("code")
	if code == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Authorization code not provided"})
		return
	}
	log.Printf("Received authorization code: %s", code)

	// Exchange code for token
	token, err := oauthConfig.Exchange(context.Background(), code)
	if err != nil {
		log.Printf("Error exchanging code for token: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to exchange code for token"})
		return
	}

	// Get user info from OAuth server
	userInfo, err := getUserInfoFromOAuth(token.AccessToken)
	if err != nil {
		log.Printf("Error getting user info: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get user info"})
		return
	}

	// Create or get user from database
	user, err := createOrGetUser(userInfo.Email, userInfo.Name, userInfo.Picture)
	if err != nil {
		log.Printf("Error creating/getting user: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create user"})
		return
	}

	// Generate JWT token
	jwtToken, err := generateJWT(user.Email, user.ID)
	if err != nil {
		log.Printf("Error generating JWT: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate token"})
		return
	}

	// Redirect to frontend with token
	frontendURL := getEnv("FRONTEND_URL", "http://localhost:3000")
	c.Redirect(http.StatusTemporaryRedirect, fmt.Sprintf("%s/postlogin?token=%s", frontendURL, jwtToken))
}

func getUserInfoFromOAuth(accessToken string) (*OAuthUserInfo, error) {
	userinfoURL := getEnv("OAUTH_USERINFO_URL", "")
	if userinfoURL == "" {
		return nil, fmt.Errorf("OAUTH_USERINFO_URL not configured")
	}
	log.Printf("Getting user info from: %s", userinfoURL)
	log.Printf("Using access token (first 8 chars): Bearer %s...", accessToken[:8])

	req, err := http.NewRequest("GET", userinfoURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to get user info: %s. Response: %s", resp.Status, string(body))
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read user info response body: %w", err)
	}
	// Re-create reader for subsequent decodes
	rdr1 := io.NopCloser(bytes.NewBuffer(bodyBytes))
	rdr2 := io.NopCloser(bytes.NewBuffer(bodyBytes))

	var userInfo OAuthUserInfo
	if err := json.NewDecoder(rdr1).Decode(&userInfo); err != nil {
		return nil, err
	}

	// Handle different OAuth providers that might use different field names
	if userInfo.Email == "" {
		// Try alternative field names
		var altUserInfo map[string]interface{}
		if err := json.NewDecoder(rdr2).Decode(&altUserInfo); err == nil {
			if email, ok := altUserInfo["email"].(string); ok {
				userInfo.Email = email
			}
			if name, ok := altUserInfo["name"].(string); ok {
				userInfo.Name = name
			}
			if picture, ok := altUserInfo["picture"].(string); ok {
				userInfo.Picture = picture
			}
		}
	}

	// Use preferred_username as fallback for name
	if userInfo.Name == "" && userInfo.PreferredUsername != "" {
		userInfo.Name = userInfo.PreferredUsername
	}

	// Use email as fallback for name if still empty
	if userInfo.Name == "" {
		userInfo.Name = userInfo.Email
	}

	return &userInfo, nil
}

func generateJWT(email string, userID int) (string, error) {
	claims := Claims{
		Email:  email,
		UserID: userID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			NotBefore: jwt.NewNumericDate(time.Now()),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(jwtSecret)
}

func validateJWT(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		return jwtSecret, nil
	})

	if err != nil {
		return nil, err
	}

	if claims, ok := token.Claims.(*Claims); ok && token.Valid {
		return claims, nil
	}

	return nil, fmt.Errorf("invalid token")
}

func authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Authorization header required"})
			c.Abort()
			return
		}

		// Extract token from "Bearer <token>"
		tokenString := authHeader
		if len(authHeader) > 7 && authHeader[:7] == "Bearer " {
			tokenString = authHeader[7:]
		}

		claims, err := validateJWT(tokenString)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid token"})
			c.Abort()
			return
		}

		// Add user info to context
		c.Set("user_email", claims.Email)
		c.Set("user_id", claims.UserID)
		c.Next()
	}
}
