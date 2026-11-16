package auth

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func bootstrapTestStore(t *testing.T) (*UserStore, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "security", "auth.sqlite")
	store, err := OpenUserStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	token, err := os.ReadFile(store.setupFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Bootstrap(strings.TrimSpace(string(token)), "admin", "AdminPassword123!"); err != nil {
		t.Fatal(err)
	}
	return store, path
}

func TestUserStoreBootstrapProtection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.sqlite")
	store, err := OpenUserStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	}()
	token, err := os.ReadFile(store.setupFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Bootstrap("wrong", "admin", "AdminPassword123!"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("wrong token: %v", err)
	}
	if _, err := store.Bootstrap(strings.TrimSpace(string(token)), "admin", "short"); err == nil {
		t.Fatal("short password accepted")
	}
	if _, err := store.Bootstrap(strings.TrimSpace(string(token)), "admin", "AdminPassword123!"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Bootstrap(strings.TrimSpace(string(token)), "attacker", "AttackerPassword123!"); !errors.Is(err, ErrAlreadyConfigured) {
		t.Fatalf("setup reused: %v", err)
	}
	if _, err := os.Stat(store.setupFile); !os.IsNotExist(err) {
		t.Fatal("setup token remains after bootstrap")
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0600 {
		t.Fatalf("database permissions: %v", err)
	}
	var hash string
	if err := store.db.QueryRow("SELECT password_hash FROM users WHERE username='admin'").Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if hash == "AdminPassword123!" || !strings.HasPrefix(hash, "$2") {
		t.Fatal("password not hashed")
	}
}

func TestUserSessionsPersistAndRevoke(t *testing.T) {
	store, path := bootstrapTestStore(t)
	viewer, err := store.CreateUser("viewer", "ViewerPassword123!", RoleView)
	if err != nil {
		t.Fatal(err)
	}
	sessions := NewPersistentSessionStore(store, time.Hour)
	defer sessions.Stop()
	session, err := sessions.CreateSession(viewer.Username)
	if err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := store.db.QueryRow("SELECT token_hash FROM web_sessions WHERE user_id=?", viewer.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == session.Token {
		t.Fatal("raw session token persisted")
	}
	sessions.Stop()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenUserStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := reopened.Close(); err != nil {
			t.Error(err)
		}
	}()
	restored := NewPersistentSessionStore(reopened, time.Hour)
	defer restored.Stop()
	if current, ok := restored.ValidateSession(session.Token); !ok || current.Role != RoleView {
		t.Fatal("session did not survive reopen")
	}
	role := RoleAdmin
	if err := reopened.UpdateUser(viewer.ID, UserUpdate{Role: &role}); err != nil {
		t.Fatal(err)
	}
	if _, ok := restored.ValidateSession(session.Token); ok {
		t.Fatal("role change did not revoke session")
	}
	newSession, err := restored.CreateSession(viewer.Username)
	if err != nil {
		t.Fatal(err)
	}
	disabled := true
	if err := reopened.UpdateUser(viewer.ID, UserUpdate{Disabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	if _, ok := restored.ValidateSession(newSession.Token); ok {
		t.Fatal("disabled user retained access")
	}
	if _, err := reopened.Authenticate(viewer.Username, "ViewerPassword123!"); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("disabled user logged in")
	}
}

func TestLastAdminCannotBeRemovedOrDemoted(t *testing.T) {
	store, _ := bootstrapTestStore(t)
	users, err := store.ListUsers()
	if err != nil {
		t.Fatal(err)
	}
	admin := users[0]
	role := RoleView
	disabled := true
	for _, change := range []UserUpdate{{Role: &role}, {Disabled: &disabled}} {
		if err := store.UpdateUser(admin.ID, change); !errors.Is(err, ErrLastAdmin) {
			t.Fatalf("last admin changed: %v", err)
		}
	}
	if err := store.DeleteUser(admin.ID); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("last admin deleted: %v", err)
	}
	second, err := store.CreateUser("second", "SecondPassword123!", RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, id := range []int64{admin.ID, second.ID} {
		wg.Add(1)
		go func(id int64) { defer wg.Done(); results <- store.DeleteUser(id) }(id)
	}
	wg.Wait()
	close(results)
	successful := 0
	for err := range results {
		if err == nil {
			successful++
		} else if !errors.Is(err, ErrLastAdmin) {
			t.Fatal(err)
		}
	}
	if successful != 1 {
		t.Fatalf("concurrent admin deletion: %d succeeded", successful)
	}
}

func TestPasswordResetRevokesSessions(t *testing.T) {
	store, _ := bootstrapTestStore(t)
	user, err := store.CreateUser("viewer", "ViewerPassword123!", RoleView)
	if err != nil {
		t.Fatal(err)
	}
	sessions := NewPersistentSessionStore(store, time.Hour)
	defer sessions.Stop()
	session, err := sessions.CreateSession(user.Username)
	if err != nil {
		t.Fatal(err)
	}
	password := "ReplacementPassword123!"
	if err := store.UpdateUser(user.ID, UserUpdate{Password: &password}); err != nil {
		t.Fatal(err)
	}
	if _, ok := sessions.ValidateSession(session.Token); ok {
		t.Fatal("password reset did not revoke session")
	}
	if _, err := store.Authenticate(user.Username, "ViewerPassword123!"); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("old password still works")
	}
	if _, err := store.Authenticate(user.Username, password); err != nil {
		t.Fatal(err)
	}
}
