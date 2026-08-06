package mockmt

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

func InitDatabase() error {
	var err error
	dbPath := getEnv("DATABASE_URL", "./webmail.db")
	dsn := dbPath + "?_journal_mode=WAL&_busy_timeout=5000&_txlock=immediate"
	db, err = sql.Open("sqlite3", dsn)
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

	return createRelayTables()
}

// createRelayTables creates the relay-mode schema. These tables are
// entirely separate from users/emails above -- capture-only mode never
// writes to them and relay mode never writes to users/emails (research
// R9), which is what makes FR-002 and SC-001 structurally true. Notably,
// queued_messages has no user_id column and no foreign key to users: there
// is no column into which a recipient identity could be recorded, so
// relaying can never provision a portal account for an external address
// (FR-018a).
func createRelayTables() error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS queued_messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			message_id TEXT UNIQUE NOT NULL,
			envelope_from TEXT NOT NULL,
			header_from TEXT,
			subject TEXT NOT NULL,
			raw_message BLOB,
			size_bytes INTEGER NOT NULL,
			state TEXT NOT NULL CHECK (state IN ('pending_review','sending','sent','failed','rejected')),
			failure_kind TEXT CHECK (failure_kind IS NULL OR failure_kind IN ('confirmed','indeterminate')),
			failure_reason TEXT,
			upstream_response TEXT,
			received_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			decided_at DATETIME,
			decided_by TEXT,
			purged_at DATETIME
		)
	`)
	if err != nil {
		return err
	}

	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_queued_messages_state ON queued_messages (state)`)
	if err != nil {
		return err
	}
	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_queued_messages_received_at ON queued_messages (received_at)`)
	if err != nil {
		return err
	}
	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_queued_messages_state_decided_at ON queued_messages (state, decided_at)`)
	if err != nil {
		return err
	}

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS queued_recipients (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			message_id INTEGER NOT NULL REFERENCES queued_messages(id),
			address TEXT NOT NULL,
			hidden BOOLEAN NOT NULL DEFAULT FALSE,
			delivered BOOLEAN NOT NULL DEFAULT FALSE,
			delivered_at DATETIME,
			upstream_response TEXT
		)
	`)
	if err != nil {
		return err
	}
	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_queued_recipients_message_id ON queued_recipients (message_id)`)
	if err != nil {
		return err
	}

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS delivery_attempts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			message_id INTEGER NOT NULL REFERENCES queued_messages(id),
			started_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			finished_at DATETIME,
			outcome TEXT CHECK (outcome IS NULL OR outcome IN ('sent','confirmed_failed','indeterminate')),
			upstream_response TEXT,
			error TEXT,
			initiated_by TEXT NOT NULL
		)
	`)
	if err != nil {
		return err
	}
	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_delivery_attempts_message_id ON delivery_attempts (message_id)`)
	if err != nil {
		return err
	}

	// message_id is deliberately a plain integer, not a foreign key with
	// ON DELETE CASCADE: no future deletion of a message should be able to
	// cascade away its audit trail (FR-031).
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS audit_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			message_id INTEGER NOT NULL,
			from_state TEXT,
			to_state TEXT NOT NULL,
			actor TEXT NOT NULL,
			occurred_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			detail TEXT
		)
	`)
	if err != nil {
		return err
	}
	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_audit_events_message_id ON audit_events (message_id)`)
	if err != nil {
		return err
	}
	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_audit_events_occurred_at ON audit_events (occurred_at)`)
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
	defer func() { _ = rows.Close() }()

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
