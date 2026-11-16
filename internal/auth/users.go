package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	_ "modernc.org/sqlite"
)

const (
	RoleAdmin = "admin"
	RoleView  = "view"
)

var (
	ErrUserExists        = errors.New("this username already exists")
	ErrLastAdmin         = errors.New("keep at least one enabled administrator")
	ErrUserNotFound      = errors.New("user not found")
	ErrAlreadyConfigured = errors.New("the initial administrator has already been configured")
	ErrInvalidUser       = errors.New("invalid user data")
	usernamePattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{2,63}$`)
)

type User struct {
	ID        int64     `json:"id"`
	Username  string    `json:"username"`
	Role      string    `json:"role"`
	Disabled  bool      `json:"disabled"`
	CreatedAt time.Time `json:"created_at"`
}

type UserUpdate struct {
	Role     *string `json:"role,omitempty"`
	Disabled *bool   `json:"disabled,omitempty"`
	Password *string `json:"password,omitempty"`
}

// UserStore persists credentials and sessions in an owner-only SQLite database.
// The driver is pure Go, including when CGO_ENABLED=0.
type UserStore struct {
	db        *sql.DB
	mu        sync.Mutex
	setupFile string
	dummyHash string
}

func OpenUserStore(path string) (*UserStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return nil, err
	}
	file.Close()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	schema := `PRAGMA busy_timeout = 5000;
PRAGMA foreign_keys = ON;
PRAGMA journal_mode = WAL;
CREATE TABLE IF NOT EXISTS users (
 id INTEGER PRIMARY KEY, username TEXT NOT NULL UNIQUE COLLATE NOCASE,
 password_hash TEXT NOT NULL, role TEXT NOT NULL CHECK(role IN ('admin','view')),
 disabled INTEGER NOT NULL DEFAULT 0 CHECK(disabled IN (0,1)), created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS web_sessions (
 token_hash TEXT PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 created_at INTEGER NOT NULL, expires_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS web_sessions_expiry ON web_sessions(expires_at);
CREATE TABLE IF NOT EXISTS auth_settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);`
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize accounts database: %w", err)
	}
	dummy, err := HashPassword("invalid-account-timing-placeholder")
	if err != nil {
		db.Close()
		return nil, err
	}
	store := &UserStore{db: db, setupFile: filepath.Join(filepath.Dir(path), "setup-token"), dummyHash: dummy}
	if err := store.ensureSetupToken(); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func (s *UserStore) Close() error { return s.db.Close() }

func tokenDigest(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}

func (s *UserStore) Initialized() (bool, error) {
	var count int
	err := s.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&count)
	return count > 0, err
}

func (s *UserStore) ensureSetupToken() error {
	ready, err := s.Initialized()
	if err != nil {
		return err
	}
	if ready {
		return nil
	}
	existing, err := os.ReadFile(s.setupFile)
	var stored string
	dbErr := s.db.QueryRow("SELECT value FROM auth_settings WHERE key='setup_token_hash'").Scan(&stored)
	if err == nil && dbErr == nil && tokenDigest(strings.TrimSpace(string(existing))) == stored {
		return os.Chmod(s.setupFile, 0o600)
	}
	token, err := GenerateToken()
	if err != nil {
		return err
	}
	if err := os.WriteFile(s.setupFile, []byte(token+"\n"), 0o600); err != nil {
		return err
	}
	if err := os.Chmod(s.setupFile, 0o600); err != nil {
		return err
	}
	_, err = s.db.Exec("INSERT INTO auth_settings(key,value) VALUES('setup_token_hash',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", tokenDigest(token))
	return err
}

func ValidateUser(username, password, role string) error {
	if !usernamePattern.MatchString(username) {
		return fmt.Errorf("%w: username must have 3–64 letters, numbers, dots, underscores or hyphens", ErrInvalidUser)
	}
	if role != RoleAdmin && role != RoleView {
		return fmt.Errorf("%w: role must be admin or view", ErrInvalidUser)
	}
	return validatePassword(password)
}

func validatePassword(password string) error {
	if utf8.RuneCountInString(password) < 12 || len(password) > 72 {
		return fmt.Errorf("%w: password must have at least 12 characters and at most 72 bytes", ErrInvalidUser)
	}
	return nil
}

func (s *UserStore) Bootstrap(token, username, password string) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ready, err := s.Initialized()
	if err != nil {
		return User{}, err
	}
	if ready {
		return User{}, ErrAlreadyConfigured
	}
	var hash string
	if err := s.db.QueryRow("SELECT value FROM auth_settings WHERE key='setup_token_hash'").Scan(&hash); err != nil {
		return User{}, err
	}
	if subtle.ConstantTimeCompare([]byte(tokenDigest(token)), []byte(hash)) != 1 {
		return User{}, ErrUnauthorized
	}
	username = strings.TrimSpace(username)
	if err := ValidateUser(username, password, RoleAdmin); err != nil {
		return User{}, err
	}
	hashed, err := HashPassword(password)
	if err != nil {
		return User{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRow("SELECT COUNT(*) FROM users").Scan(&count); err != nil {
		return User{}, err
	}
	if count != 0 {
		return User{}, ErrAlreadyConfigured
	}
	now := time.Now().UTC()
	result, err := tx.Exec("INSERT INTO users(username,password_hash,role,created_at) VALUES(?,?,?,?)", username, hashed, RoleAdmin, now.UnixNano())
	if err != nil {
		return User{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return User{}, err
	}
	if _, err := tx.Exec("DELETE FROM auth_settings WHERE key='setup_token_hash'"); err != nil {
		return User{}, err
	}
	if err := tx.Commit(); err != nil {
		return User{}, err
	}
	_ = os.Remove(s.setupFile)
	return User{ID: id, Username: username, Role: RoleAdmin, CreatedAt: now}, nil
}

func (s *UserStore) Authenticate(username, password string) (User, error) {
	var user User
	var hash string
	var created int64
	err := s.db.QueryRow("SELECT id,username,password_hash,role,disabled,created_at FROM users WHERE username=?", strings.TrimSpace(username)).Scan(&user.ID, &user.Username, &hash, &user.Role, &user.Disabled, &created)
	if errors.Is(err, sql.ErrNoRows) {
		_ = VerifyPassword(s.dummyHash, password)
		return User{}, ErrUnauthorized
	}
	if err != nil {
		return User{}, err
	}
	if err := VerifyPassword(hash, password); err != nil || user.Disabled {
		return User{}, ErrUnauthorized
	}
	user.CreatedAt = time.Unix(0, created).UTC()
	return user, nil
}

func (s *UserStore) ListUsers() ([]User, error) {
	rows, err := s.db.Query("SELECT id,username,role,disabled,created_at FROM users ORDER BY username")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := []User{}
	for rows.Next() {
		var user User
		var created int64
		if err := rows.Scan(&user.ID, &user.Username, &user.Role, &user.Disabled, &created); err != nil {
			return nil, err
		}
		user.CreatedAt = time.Unix(0, created).UTC()
		users = append(users, user)
	}
	return users, rows.Err()
}

func (s *UserStore) CreateUser(username, password, role string) (User, error) {
	username = strings.TrimSpace(username)
	if err := ValidateUser(username, password, role); err != nil {
		return User{}, err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return User{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	result, err := s.db.Exec("INSERT INTO users(username,password_hash,role,created_at) VALUES(?,?,?,?)", username, hash, role, now.UnixNano())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint") {
			return User{}, ErrUserExists
		}
		return User{}, err
	}
	id, err := result.LastInsertId()
	return User{ID: id, Username: username, Role: role, CreatedAt: now}, err
}

func (s *UserStore) changeUser(id int64, change UserUpdate, remove bool) error {
	var hash string
	if change.Password != nil {
		if err := validatePassword(*change.Password); err != nil {
			return err
		}
		var err error
		hash, err = HashPassword(*change.Password)
		if err != nil {
			return err
		}
	}
	if change.Role != nil && *change.Role != RoleAdmin && *change.Role != RoleView {
		return fmt.Errorf("%w: role must be admin or view", ErrInvalidUser)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var oldRole string
	var oldDisabled bool
	if err := tx.QueryRow("SELECT role,disabled FROM users WHERE id=?", id).Scan(&oldRole, &oldDisabled); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrUserNotFound
		}
		return err
	}
	role, disabled := oldRole, oldDisabled
	if change.Role != nil {
		role = *change.Role
	}
	if change.Disabled != nil {
		disabled = *change.Disabled
	}
	if oldRole == RoleAdmin && !oldDisabled && (remove || disabled || role != RoleAdmin) {
		var admins int
		if err := tx.QueryRow("SELECT COUNT(*) FROM users WHERE role='admin' AND disabled=0").Scan(&admins); err != nil {
			return err
		}
		if admins <= 1 {
			return ErrLastAdmin
		}
	}
	if remove {
		_, err = tx.Exec("DELETE FROM users WHERE id=?", id)
	} else {
		_, err = tx.Exec("UPDATE users SET role=?,disabled=? WHERE id=?", role, disabled, id)
		if err == nil && hash != "" {
			_, err = tx.Exec("UPDATE users SET password_hash=? WHERE id=?", hash, id)
		}
	}
	if err != nil {
		return err
	}
	// A password reset, revocation or role change invalidates existing sessions.
	if remove || disabled || hash != "" || role != oldRole {
		if _, err := tx.Exec("DELETE FROM web_sessions WHERE user_id=?", id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *UserStore) UpdateUser(id int64, change UserUpdate) error {
	return s.changeUser(id, change, false)
}
func (s *UserStore) DeleteUser(id int64) error { return s.changeUser(id, UserUpdate{}, true) }
