package auth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"sync"
	"time"
)

const (
	// SessionCookieName is the cookie name for web sessions.
	SessionCookieName = "pulso_session"

	// DefaultSessionTTL is the default session lifetime.
	DefaultSessionTTL = 24 * time.Hour

	// sessionTokenBytes is the number of random bytes in a session token.
	sessionTokenBytes = 32

	// cleanupInterval is how often expired sessions are purged.
	cleanupInterval = 5 * time.Minute
)

// Session represents an authenticated web session.
type Session struct {
	Token     string
	Username  string
	UserID    int64
	Role      string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// SessionStore manages web authentication sessions.
type SessionStore struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	ttl      time.Duration
	stopOnce sync.Once
	stopCh   chan struct{}
	users    *UserStore
}

// NewSessionStore creates a new session store with the given TTL.
func NewSessionStore(ttl time.Duration) *SessionStore {
	return newSessionStore(ttl, nil)
}

func newSessionStore(ttl time.Duration, users *UserStore) *SessionStore {
	if ttl <= 0 {
		ttl = DefaultSessionTTL
	}
	s := &SessionStore{
		sessions: make(map[string]*Session),
		ttl:      ttl,
		stopCh:   make(chan struct{}),
		users:    users,
	}
	go s.cleanupLoop()
	return s
}

func NewPersistentSessionStore(users *UserStore, ttl time.Duration) *SessionStore {
	return newSessionStore(ttl, users)
}

// Keep credential verification and session creation atomic with password,
// role and account revocation, including simultaneous login requests.
func (s *SessionStore) Login(username, password string) (*Session, error) {
	if s.users == nil {
		return nil, fmt.Errorf("persistent account store required")
	}
	s.users.mu.Lock()
	defer s.users.mu.Unlock()
	user, err := s.users.Authenticate(username, password)
	if err != nil {
		return nil, err
	}
	return s.CreateSession(user.Username)
}

// CreateSession generates a new session for the given username.
func (s *SessionStore) CreateSession(username string) (*Session, error) {
	token, err := generateSessionToken()
	if err != nil {
		return nil, err
	}

	now := time.Now()
	sess := &Session{
		Token:     token,
		Username:  username,
		Role:      RoleAdmin,
		CreatedAt: now,
		ExpiresAt: now.Add(s.ttl),
	}
	if s.users != nil {
		var disabled bool
		err := s.users.db.QueryRow("SELECT id,role,disabled FROM users WHERE username=?", username).Scan(&sess.UserID, &sess.Role, &disabled)
		if err != nil || disabled {
			return nil, fmt.Errorf("session user unavailable")
		}
		_, err = s.users.db.Exec("INSERT INTO web_sessions(token_hash,user_id,created_at,expires_at) VALUES(?,?,?,?)", tokenDigest(token), sess.UserID, now.UnixNano(), sess.ExpiresAt.UnixNano())
		if err != nil {
			return nil, err
		}
		return sess, nil
	}

	s.mu.Lock()
	s.sessions[token] = sess
	s.mu.Unlock()

	return sess, nil
}

// ValidateSession checks if a session token is valid and not expired.
func (s *SessionStore) ValidateSession(token string) (*Session, bool) {
	if token == "" {
		return nil, false
	}
	if s.users != nil {
		var sess Session
		var created, expires int64
		err := s.users.db.QueryRow(`SELECT u.id,u.username,u.role,s.created_at,s.expires_at FROM web_sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=? AND u.disabled=0 AND s.expires_at>?`, tokenDigest(token), time.Now().UnixNano()).Scan(&sess.UserID, &sess.Username, &sess.Role, &created, &expires)
		if err != nil {
			return nil, false
		}
		sess.Token = token
		sess.CreatedAt = time.Unix(0, created)
		sess.ExpiresAt = time.Unix(0, expires)
		return &sess, true
	}

	s.mu.RLock()
	sess, ok := s.sessions[token]
	s.mu.RUnlock()

	if !ok {
		return nil, false
	}

	if time.Now().After(sess.ExpiresAt) {
		s.mu.Lock()
		delete(s.sessions, token)
		s.mu.Unlock()
		return nil, false
	}

	return sess, true
}

// DeleteSession removes a session by token.
func (s *SessionStore) DeleteSession(token string) {
	if s.users != nil {
		_, _ = s.users.db.Exec("DELETE FROM web_sessions WHERE token_hash=?", tokenDigest(token))
		return
	}
	s.mu.Lock()
	delete(s.sessions, token)
	s.mu.Unlock()
}

// Stop shuts down the cleanup goroutine.
func (s *SessionStore) Stop() {
	s.stopOnce.Do(func() {
		close(s.stopCh)
	})
}

// cleanupLoop periodically removes expired sessions.
func (s *SessionStore) cleanupLoop() {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.purgeExpired()
		}
	}
}

func (s *SessionStore) purgeExpired() {
	if s.users != nil {
		_, _ = s.users.db.Exec("DELETE FROM web_sessions WHERE expires_at<=?", time.Now().UnixNano())
		return
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	for token, sess := range s.sessions {
		if now.After(sess.ExpiresAt) {
			delete(s.sessions, token)
		}
	}
}

// SessionFromRequest extracts the session token from a request cookie.
func SessionFromRequest(r *http.Request) string {
	cookie, err := r.Cookie(SessionCookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}

// SessionCookie creates an http.Cookie for the given session token.
func SessionCookie(token string, maxAge time.Duration) *http.Cookie {
	return &http.Cookie{
		Name:     SessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(maxAge.Seconds()),
	}
}

// ClearSessionCookie returns a cookie that clears the session.
func ClearSessionCookie() *http.Cookie {
	return &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	}
}

// generateSessionToken creates a cryptographically random session token.
func generateSessionToken() (string, error) {
	b := make([]byte, sessionTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
