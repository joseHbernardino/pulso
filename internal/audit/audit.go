// Package audit keeps an append-only record of who changed what through the
// dashboard and the API.
//
// It lives in its own SQLite file, separate from the accounts database, so a
// problem with one store never takes the other down, and so the record can be
// kept and rotated independently of the accounts.
//
// Nothing written here may contain a secret. Callers pass short, already-safe
// descriptions; EnvKeys exists so that a change to environment variables is
// recorded by key name only, never by value.
package audit

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// DefaultRetention is how long entries are kept when no other period is set.
const DefaultRetention = 90 * 24 * time.Hour

// maxDetail bounds a single detail string, so a loop cannot grow the file
// without limit.
const maxDetail = 2000

// Outcomes recorded for an action.
const (
	OutcomeOK     = "ok"
	OutcomeDenied = "denied"
	OutcomeError  = "error"
)

// Entry is one recorded action.
type Entry struct {
	ID        int64     `json:"id"`
	Actor     string    `json:"actor"`
	Role      string    `json:"role,omitempty"`
	Action    string    `json:"action"`
	Target    string    `json:"target,omitempty"`
	Outcome   string    `json:"outcome"`
	Status    int       `json:"status,omitempty"`
	Detail    string    `json:"detail,omitempty"`
	IP        string    `json:"ip,omitempty"`
	UserAgent string    `json:"user_agent,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Log is the audit store.
type Log struct {
	db        *sql.DB
	retention time.Duration
}

const schema = `PRAGMA busy_timeout = 5000;
PRAGMA journal_mode = WAL;
CREATE TABLE IF NOT EXISTS audit_entries (
 id INTEGER PRIMARY KEY,
 actor TEXT NOT NULL DEFAULT '',
 role TEXT NOT NULL DEFAULT '',
 action TEXT NOT NULL,
 target TEXT NOT NULL DEFAULT '',
 outcome TEXT NOT NULL DEFAULT 'ok',
 status INTEGER NOT NULL DEFAULT 0,
 detail TEXT NOT NULL DEFAULT '',
 ip TEXT NOT NULL DEFAULT '',
 user_agent TEXT NOT NULL DEFAULT '',
 created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS audit_entries_time ON audit_entries(created_at DESC);
CREATE INDEX IF NOT EXISTS audit_entries_actor ON audit_entries(actor, created_at DESC);`

// Open prepares the audit log at path, creating the file with owner-only
// permissions. A retention of zero uses DefaultRetention; a negative
// retention keeps entries forever.
func Open(path string, retention time.Duration) (*Log, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("close audit database file: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// One connection matches the accounts store and avoids SQLite write
	// contention; the audit log is low volume.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initialize audit database: %w", err)
	}
	if retention == 0 {
		retention = DefaultRetention
	}
	return &Log{db: db, retention: retention}, nil
}

// Close releases the database.
func (l *Log) Close() error {
	if l == nil || l.db == nil {
		return nil
	}
	return l.db.Close()
}

// Retention reports the configured period.
func (l *Log) Retention() time.Duration {
	if l == nil {
		return 0
	}
	return l.retention
}

// Record appends one entry. A nil Log discards the entry, so callers do not
// need to check whether auditing is enabled.
func (l *Log) Record(entry Entry) error {
	if l == nil || l.db == nil {
		return nil
	}
	if strings.TrimSpace(entry.Action) == "" {
		return errors.New("audit entry needs an action")
	}
	if entry.Outcome == "" {
		entry.Outcome = OutcomeOK
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now()
	}
	_, err := l.db.Exec(
		`INSERT INTO audit_entries(actor,role,action,target,outcome,status,detail,ip,user_agent,created_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?)`,
		truncate(entry.Actor, 200), truncate(entry.Role, 40), truncate(entry.Action, 120),
		truncate(entry.Target, 400), truncate(entry.Outcome, 20), entry.Status,
		truncate(entry.Detail, maxDetail), truncate(entry.IP, 60), truncate(entry.UserAgent, 300),
		entry.CreatedAt.UnixNano(),
	)
	return err
}

// Filter narrows a listing.
type Filter struct {
	Actor  string
	Action string
	Since  time.Time
	Limit  int
}

// List returns entries newest first.
func (l *Log) List(filter Filter) ([]Entry, error) {
	if l == nil || l.db == nil {
		return []Entry{}, nil
	}
	query := `SELECT id,actor,role,action,target,outcome,status,detail,ip,user_agent,created_at
	          FROM audit_entries WHERE 1=1`
	args := []any{}
	if actor := strings.TrimSpace(filter.Actor); actor != "" {
		query += " AND actor = ?"
		args = append(args, actor)
	}
	if action := strings.TrimSpace(filter.Action); action != "" {
		// Prefixo permite filtrar uma família inteira, como "application.".
		query += " AND action LIKE ?"
		args = append(args, action+"%")
	}
	if !filter.Since.IsZero() {
		query += " AND created_at >= ?"
		args = append(args, filter.Since.UnixNano())
	}
	limit := filter.Limit
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	query += " ORDER BY created_at DESC, id DESC LIMIT ?"
	args = append(args, limit)

	rows, err := l.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	entries := []Entry{}
	for rows.Next() {
		var entry Entry
		var created int64
		if err := rows.Scan(&entry.ID, &entry.Actor, &entry.Role, &entry.Action, &entry.Target,
			&entry.Outcome, &entry.Status, &entry.Detail, &entry.IP, &entry.UserAgent, &created); err != nil {
			return nil, err
		}
		entry.CreatedAt = time.Unix(0, created)
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

// Prune removes entries older than the retention period and reports how many
// were deleted. A negative retention keeps everything.
func (l *Log) Prune(now time.Time) (int64, error) {
	if l == nil || l.db == nil || l.retention < 0 {
		return 0, nil
	}
	cutoff := now.Add(-l.retention).UnixNano()
	result, err := l.db.Exec("DELETE FROM audit_entries WHERE created_at < ?", cutoff)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// Count reports the number of stored entries.
func (l *Log) Count() (int64, error) {
	if l == nil || l.db == nil {
		return 0, nil
	}
	var total int64
	err := l.db.QueryRow("SELECT COUNT(*) FROM audit_entries").Scan(&total)
	return total, err
}

// EnvKeys renders a set of environment variables as a sorted, comma-separated
// list of KEY NAMES ONLY. Values never reach the audit log.
func EnvKeys(env map[string]string) string {
	if len(env) == 0 {
		return ""
	}
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

func truncate(value string, limit int) string {
	value = strings.ReplaceAll(value, "\x00", "")
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "…"
}
