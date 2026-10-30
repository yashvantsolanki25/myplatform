package web

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	mysql "github.com/go-sql-driver/mysql"
)

// Credential config structs are JSON-marshaled then AES-encrypted before
// being stored.

type AWSCredConfig struct {
	AccessKeyID     string   `json:"accessKeyId"`
	SecretAccessKey string   `json:"secretAccessKey"`
	SessionToken    string   `json:"sessionToken,omitempty"`
	Regions         []string `json:"regions"`
}

type GCPCredConfig struct {
	ServiceAccountKey string `json:"serviceAccountKey"`
	ProjectID         string `json:"projectId"`
}

type AzureCredConfig struct {
	ClientID       string `json:"clientId"`
	ClientSecret   string `json:"clientSecret"`
	TenantID       string `json:"tenantId"`
	SubscriptionID string `json:"subscriptionId"`
}

// --- storage models ---

type User struct {
	ID           int       `json:"id"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"password_hash"`
	Name         string    `json:"name"`
	CreatedAt    time.Time `json:"created_at"`
}

type UserCred struct {
	UserID          int    `json:"user_id"`
	Provider        string `json:"provider"`
	EncryptedConfig string `json:"encrypted_config"`
}

type dbData struct {
	Users       []User     `json:"users"`
	Credentials []UserCred `json:"credentials"`
	NextID      int        `json:"next_id"`
}

// Store uses MySQL when configured and retains the local file-backed mode for CLI use.
type Store struct {
	mu     sync.RWMutex
	path   string
	secret []byte
	data   dbData
	db     *sql.DB
}

var ErrEmailExists = errors.New("email already registered")
var ErrUserNotFound = errors.New("user not found")

func NewStore() (*Store, error) {
	dir := os.Getenv("MYPLATFORM_DATA_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("home dir: %w", err)
		}
		dir = filepath.Join(home, ".myplatform")
	}

	secret, err := loadSecret(dir)
	if err != nil {
		return nil, err
	}

	s := &Store{
		path:   filepath.Join(dir, "data.json"),
		secret: secret,
		data:   dbData{NextID: 1},
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

// NewMySQLStore opens the application database and applies the current schema.
func NewMySQLStore(ctx context.Context, dsn string) (*Store, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, fmt.Errorf("MySQL DSN is required")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("open MySQL database: %w", err)
	}
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect to MySQL database: %w", err)
	}
	if err := migrateMySQL(ctx, db); err != nil {
		db.Close()
		return nil, err
	}

	dir := os.Getenv("MYPLATFORM_DATA_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			db.Close()
			return nil, fmt.Errorf("home dir: %w", err)
		}
		dir = filepath.Join(home, ".myplatform")
	}
	secret, err := loadSecret(dir)
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db, secret: secret}, nil
}

func loadSecret(dir string) ([]byte, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	if configured := os.Getenv("MYPLATFORM_ENCRYPTION_KEY"); configured != "" {
		secret, err := hex.DecodeString(configured)
		if err != nil || len(secret) != 32 {
			return nil, fmt.Errorf("MYPLATFORM_ENCRYPTION_KEY must be 64 hexadecimal characters")
		}
		return secret, nil
	}

	keyPath := filepath.Join(dir, "secret.key")
	raw, err := os.ReadFile(keyPath)
	if err == nil {
		if len(raw) != 32 {
			return nil, fmt.Errorf("stored encryption key at %s must be 32 bytes", keyPath)
		}
		return raw, nil
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read encryption key: %w", err)
	}

	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("generate encryption key: %w", err)
	}
	f, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if os.IsExist(err) {
			raw, readErr := os.ReadFile(keyPath)
			if readErr != nil {
				return nil, fmt.Errorf("read encryption key: %w", readErr)
			}
			if len(raw) != 32 {
				return nil, fmt.Errorf("stored encryption key at %s must be 32 bytes", keyPath)
			}
			return raw, nil
		}
		return nil, fmt.Errorf("create encryption key: %w", err)
	}
	if _, err := f.Write(secret); err != nil {
		f.Close()
		return nil, fmt.Errorf("write encryption key: %w", err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("close encryption key: %w", err)
	}
	return secret, nil
}

func migrateMySQL(ctx context.Context, db *sql.DB) error {
	const users = `CREATE TABLE IF NOT EXISTS users (
		id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
		email VARCHAR(254) NOT NULL UNIQUE,
		name VARCHAR(80) NOT NULL,
		password_hash VARCHAR(255) NOT NULL,
		created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`
	const credentials = `CREATE TABLE IF NOT EXISTS credentials (
		user_id BIGINT NOT NULL,
		provider VARCHAR(16) NOT NULL,
		encrypted_config TEXT NOT NULL,
		updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
		PRIMARY KEY (user_id, provider),
		CONSTRAINT fk_credentials_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`
	for _, statement := range []string{users, credentials} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("migrate MySQL database: %w", err)
		}
	}
	return nil
}

func (s *Store) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

func (s *Store) Secret() []byte { return s.secret }

func (s *Store) Ping(ctx context.Context) error {
	if s.db != nil {
		return s.db.PingContext(ctx)
	}
	return nil
}

func (s *Store) load() error {
	raw, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, &s.data)
}

func (s *Store) save() error {
	raw, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, raw, 0o600)
}

// --- user methods ---

func (s *Store) CreateUser(email, name, password string) (*User, error) {
	email = strings.ToLower(strings.TrimSpace(email))

	if s.db != nil {
		hash, err := HashPassword(password)
		if err != nil {
			return nil, err
		}
		u := User{Email: email, Name: name, PasswordHash: hash, CreatedAt: time.Now()}
		result, err := s.db.Exec("INSERT INTO users (email, name, password_hash) VALUES (?, ?, ?)", u.Email, u.Name, u.PasswordHash)
		if err != nil {
			var mysqlErr *mysql.MySQLError
			if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
				return nil, ErrEmailExists
			}
			return nil, fmt.Errorf("create user: %w", err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			return nil, fmt.Errorf("read new user ID: %w", err)
		}
		u.ID = int(id)
		return &u, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range s.data.Users {
		if u.Email == email {
			return nil, ErrEmailExists
		}
	}

	hash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}

	u := User{
		ID:           s.data.NextID,
		Email:        email,
		Name:         name,
		PasswordHash: hash,
		CreatedAt:    time.Now(),
	}
	s.data.NextID++
	s.data.Users = append(s.data.Users, u)
	if err := s.save(); err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *Store) GetUserByEmail(email string) (*User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if s.db != nil {
		u := &User{}
		err := s.db.QueryRow("SELECT id, email, name, password_hash, created_at FROM users WHERE email = ?", email).
			Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.CreatedAt)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, ErrUserNotFound
			}
			return nil, err
		}
		return u, nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := range s.data.Users {
		if s.data.Users[i].Email == email {
			u := s.data.Users[i]
			return &u, nil
		}
	}
	return nil, ErrUserNotFound
}

func (s *Store) GetUserByID(id int) (*User, error) {
	if s.db != nil {
		u := &User{}
		err := s.db.QueryRow("SELECT id, email, name, password_hash, created_at FROM users WHERE id = ?", id).
			Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.CreatedAt)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, ErrUserNotFound
			}
			return nil, err
		}
		return u, nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := range s.data.Users {
		if s.data.Users[i].ID == id {
			u := s.data.Users[i]
			return &u, nil
		}
	}
	return nil, ErrUserNotFound
}

// --- credential methods ---

func (s *Store) SaveCredentials(userID int, provider string, config any) error {
	jsonBytes, err := json.Marshal(config)
	if err != nil {
		return err
	}
	enc, err := Encrypt(string(jsonBytes), s.secret)
	if err != nil {
		return err
	}
	if s.db != nil {
		_, err := s.db.Exec(
			"INSERT INTO credentials (user_id, provider, encrypted_config) VALUES (?, ?, ?) "+
				"ON DUPLICATE KEY UPDATE encrypted_config = ?",
			userID, provider, enc, enc,
		)
		if err != nil {
			return fmt.Errorf("save credentials: %w", err)
		}
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Credentials {
		if s.data.Credentials[i].UserID == userID && s.data.Credentials[i].Provider == provider {
			s.data.Credentials[i].EncryptedConfig = enc
			return s.save()
		}
	}
	s.data.Credentials = append(s.data.Credentials, UserCred{
		UserID: userID, Provider: provider, EncryptedConfig: enc,
	})
	return s.save()
}

func (s *Store) GetDecryptedCredentials(userID int, provider string, out any) error {
	if s.db != nil {
		var encrypted string
		if err := s.db.QueryRow(
			"SELECT encrypted_config FROM credentials WHERE user_id = ? AND provider = ?",
			userID, provider,
		).Scan(&encrypted); err != nil {
			return err
		}
		plaintext, err := Decrypt(encrypted, s.secret)
		if err != nil {
			return err
		}
		return json.Unmarshal([]byte(plaintext), out)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, c := range s.data.Credentials {
		if c.UserID == userID && c.Provider == provider {
			plaintext, err := Decrypt(c.EncryptedConfig, s.secret)
			if err != nil {
				return err
			}
			return json.Unmarshal([]byte(plaintext), out)
		}
	}
	return fmt.Errorf("no %s credentials configured", provider)
}

func (s *Store) DeleteCredentials(userID int, provider string) error {
	if s.db != nil {
		if _, err := s.db.Exec("DELETE FROM credentials WHERE user_id = ? AND provider = ?", userID, provider); err != nil {
			return fmt.Errorf("delete credentials: %w", err)
		}
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for i, c := range s.data.Credentials {
		if c.UserID == userID && c.Provider == provider {
			s.data.Credentials = append(s.data.Credentials[:i], s.data.Credentials[i+1:]...)
			return s.save()
		}
	}
	return nil
}

func (s *Store) HasCredentials(userID int, provider string) (bool, error) {
	if s.db != nil {
		var exists bool
		err := s.db.QueryRow(
			"SELECT EXISTS(SELECT 1 FROM credentials WHERE user_id = ? AND provider = ?)",
			userID, provider,
		).Scan(&exists)
		if err != nil {
			return false, fmt.Errorf("check credentials: %w", err)
		}
		return exists, nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, c := range s.data.Credentials {
		if c.UserID == userID && c.Provider == provider {
			return true, nil
		}
	}
	return false, nil
}
