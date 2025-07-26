package main

import (
	"database/sql"
	"log"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

var db *sql.DB

type User struct {
	ID        int       `json:"id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	Picture   string    `json:"picture"`
	CreatedAt time.Time `json:"created_at"`
}

type Email struct {
	ID         int       `json:"id"`
	MessageID  string    `json:"message_id"`
	FromEmail  string    `json:"from_email"`
	ToEmail    string    `json:"to_email"`
	Subject    string    `json:"subject"`
	Body       string    `json:"body"`
	HTMLBody   string    `json:"html_body"`
	ReceivedAt time.Time `json:"received_at"`
	IsDeleted  bool      `json:"is_deleted"`
	UserID     int       `json:"user_id"`
}

func initDatabase() error {
	var err error
	dbPath := getEnv("DATABASE_URL", "./webmail.db")
	db, err = sql.Open("sqlite3", dbPath)
	if err != nil {
		return err
	}

	// Create tables
	if err := createTables(); err != nil {
		return err
	}

	log.Println("Database initialized successfully")
	return nil
}

func createTables() error {
	// Users table
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			email TEXT UNIQUE NOT NULL,
			name TEXT NOT NULL,
			picture TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return err
	}

	// Emails table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS emails (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			message_id TEXT UNIQUE NOT NULL,
			from_email TEXT NOT NULL,
			to_email TEXT NOT NULL,
			subject TEXT NOT NULL,
			body TEXT NOT NULL,
			html_body TEXT,
			received_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			is_deleted BOOLEAN DEFAULT FALSE,
			user_id INTEGER,
			FOREIGN KEY (user_id) REFERENCES users (id)
		)
	`)
	if err != nil {
		return err
	}

	// Create indexes
	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_emails_to_email ON emails (to_email)`)
	if err != nil {
		return err
	}

	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_emails_user_id ON emails (user_id)`)
	if err != nil {
		return err
	}

	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_emails_received_at ON emails (received_at)`)
	if err != nil {
		return err
	}

	return nil
}

func createOrGetUser(email, name, picture string) (*User, error) {
	// Try to get existing user
	var user User
	err := db.QueryRow("SELECT id, email, name, picture, created_at FROM users WHERE email = ?", email).
		Scan(&user.ID, &user.Email, &user.Name, &user.Picture, &user.CreatedAt)

	if err == nil {
		return &user, nil
	}

	// Create new user
	result, err := db.Exec("INSERT INTO users (email, name, picture) VALUES (?, ?, ?)", email, name, picture)
	if err != nil {
		return nil, err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return nil, err
	}

	return &User{
		ID:        int(id),
		Email:     email,
		Name:      name,
		Picture:   picture,
		CreatedAt: time.Now(),
	}, nil
}

func saveEmail(fromEmail, toEmail, subject, body, htmlBody string) error {
	// Get or create user for recipient
	user, err := createOrGetUser(toEmail, toEmail, "")
	if err != nil {
		return err
	}

	// Generate message ID
	messageID := generateMessageID()

	_, err = db.Exec(`
		INSERT INTO emails (message_id, from_email, to_email, subject, body, html_body, user_id)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, messageID, fromEmail, toEmail, subject, body, htmlBody, user.ID)

	return err
}

func getEmailsByUser(userID int) ([]Email, error) {
	rows, err := db.Query(`
		SELECT id, message_id, from_email, to_email, subject, body, html_body, received_at, is_deleted, user_id
		FROM emails 
		WHERE user_id = ? AND is_deleted = FALSE 
		ORDER BY received_at DESC
	`, userID)
	if err != nil {
		return []Email{}, err
	}
	defer rows.Close()

	emails := []Email{}
	for rows.Next() {
		var email Email
		err := rows.Scan(
			&email.ID, &email.MessageID, &email.FromEmail, &email.ToEmail,
			&email.Subject, &email.Body, &email.HTMLBody, &email.ReceivedAt,
			&email.IsDeleted, &email.UserID,
		)
		if err != nil {
			return nil, err
		}
		emails = append(emails, email)
	}

	return emails, nil
}

func getEmailByID(emailID, userID int) (*Email, error) {
	var email Email
	err := db.QueryRow(`
		SELECT id, message_id, from_email, to_email, subject, body, html_body, received_at, is_deleted, user_id
		FROM emails 
		WHERE id = ? AND user_id = ? AND is_deleted = FALSE
	`, emailID, userID).Scan(
		&email.ID, &email.MessageID, &email.FromEmail, &email.ToEmail,
		&email.Subject, &email.Body, &email.HTMLBody, &email.ReceivedAt,
		&email.IsDeleted, &email.UserID,
	)
	if err != nil {
		return nil, err
	}

	return &email, nil
}

func deleteEmail(emailID, userID int) error {
	_, err := db.Exec("UPDATE emails SET is_deleted = TRUE WHERE id = ? AND user_id = ?", emailID, userID)
	return err
}

func getUserByEmail(email string) (*User, error) {
	var user User
	err := db.QueryRow("SELECT id, email, name, picture, created_at FROM users WHERE email = ?", email).
		Scan(&user.ID, &user.Email, &user.Name, &user.Picture, &user.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func getEmailStats(userID int) (int, error) {
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM emails WHERE user_id = ? AND is_deleted = FALSE", userID).Scan(&count)
	return count, err
}
