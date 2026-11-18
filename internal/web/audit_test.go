package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pulso.local/pulso/internal/audit"
	"pulso.local/pulso/internal/auth"
	"pulso.local/pulso/internal/supervisor"
)

// auditedServer monta um painel com auditoria ativa e devolve o log para
// inspeção, além dos tokens de sessão por papel.
func auditedServer(t *testing.T) (*Server, *audit.Log, map[string]string) {
	t.Helper()
	dir := t.TempDir()

	store, err := auth.OpenUserStore(filepath.Join(dir, "auth.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	token, err := os.ReadFile(filepath.Join(dir, "setup-token"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Bootstrap(strings.TrimSpace(string(token)), "admin", "DisposableAdminPassword123!"); err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateUser("leitor", "DisposableViewerPassword123!", auth.RoleView); err != nil {
		t.Fatal(err)
	}

	auditLog, err := audit.Open(filepath.Join(dir, "audit.sqlite"), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = auditLog.Close() })

	sup := supervisor.New(supervisor.Options{LogDir: dir})
	t.Cleanup(func() { _ = sup.Shutdown() })

	server := NewServerWithUsers(sup, "127.0.0.1:0", store)
	t.Cleanup(server.sessions.Stop)
	server.SetAuditLog(auditLog)

	tokens := map[string]string{}
	for _, name := range []string{"admin", "leitor"} {
		session, err := server.sessions.CreateSession(name)
		if err != nil {
			t.Fatal(err)
		}
		tokens[name] = session.Token
	}
	return server, auditLog, tokens
}

// auditedRequestOn é o mesmo auxiliar, exposto com nome próprio para os
// testes de outros arquivos do pacote.
func auditedRequestOn(t *testing.T, server *Server, method, path, body, sessionToken string) *httptest.ResponseRecorder {
	t.Helper()
	return auditedRequest(t, server, method, path, body, sessionToken)
}

func auditedRequest(t *testing.T, server *Server, method, path, body, sessionToken string) *httptest.ResponseRecorder {
	t.Helper()
	handler, err := server.handler()
	if err != nil {
		t.Fatal(err)
	}
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "teste-auditoria/1.0")
	req.RemoteAddr = "192.0.2.10:54321"
	if sessionToken != "" {
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sessionToken})
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}

func TestAuditRecordsMutationWithActorAndContext(t *testing.T) {
	server, auditLog, tokens := auditedServer(t)

	auditedRequest(t, server, "POST", "/api/v1/users", `{"username":"novo","password":"DisposablePassword123!","role":"view"}`, tokens["admin"])

	entries, err := auditLog.List(audit.Filter{Action: "user.create"})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entradas = %d, esperado 1", len(entries))
	}
	entry := entries[0]
	if entry.Actor != "admin" || entry.Role != auth.RoleAdmin {
		t.Errorf("ator = %q/%q", entry.Actor, entry.Role)
	}
	if entry.IP != "192.0.2.10" {
		t.Errorf("ip = %q, esperado o endereço do par sem a porta", entry.IP)
	}
	if entry.UserAgent != "teste-auditoria/1.0" {
		t.Errorf("user agent = %q", entry.UserAgent)
	}
	if entry.Status == 0 {
		t.Error("status não registrado")
	}
}

// Leituras não são registradas: encheriam o log sem acrescentar prestação
// de contas.
func TestAuditIgnoresReads(t *testing.T) {
	server, auditLog, tokens := auditedServer(t)

	auditedRequest(t, server, "GET", "/api/v1/processes", "", tokens["admin"])
	auditedRequest(t, server, "GET", "/api/v1/users", "", tokens["admin"])

	total, err := auditLog.Count()
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 {
		t.Fatalf("leituras registradas: %d entradas", total)
	}
}

// O valor de auditoria está justamente em registrar o que foi recusado.
func TestAuditRecordsDeniedAttempt(t *testing.T) {
	server, auditLog, tokens := auditedServer(t)

	w := auditedRequest(t, server, "POST", "/api/v1/users", `{"username":"x","password":"DisposablePassword123!","role":"admin"}`, tokens["leitor"])
	if w.Code != 403 {
		t.Fatalf("o papel de visualização criou usuário: %d", w.Code)
	}

	entries, err := auditLog.List(audit.Filter{Actor: "leitor"})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entradas = %d, esperado 1", len(entries))
	}
	if entries[0].Outcome != audit.OutcomeDenied {
		t.Errorf("resultado = %q, esperado %q", entries[0].Outcome, audit.OutcomeDenied)
	}
	if entries[0].Status != 403 {
		t.Errorf("status = %d, esperado 403", entries[0].Status)
	}
}

// Uma senha enviada no corpo nunca pode aparecer no registro. O middleware
// não lê o corpo da requisição; este teste é a prova dessa garantia.
func TestAuditNeverStoresRequestBodySecrets(t *testing.T) {
	server, auditLog, tokens := auditedServer(t)
	const secret = "SenhaQueNaoPodeAparecerNoLog123!"

	auditedRequest(t, server, "POST", "/api/v1/users", `{"username":"novo","password":"`+secret+`","role":"view"}`, tokens["admin"])
	auditedRequest(t, server, "POST", "/api/auth/login", `{"username":"admin","password":"`+secret+`"}`, "")

	entries, err := auditLog.List(audit.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("nada foi registrado")
	}
	for _, entry := range entries {
		combined := entry.Actor + " " + entry.Target + " " + entry.Detail + " " + entry.Action
		if strings.Contains(combined, secret) {
			t.Fatalf("a senha vazou no registro: %+v", entry)
		}
	}
}

// Uma tentativa de login malsucedida registra a conta tentada, para que uma
// sequência de falhas fique visível.
func TestAuditRecordsFailedLoginWithAttemptedAccount(t *testing.T) {
	server, auditLog, tokens := auditedServer(t)
	_ = tokens

	w := auditedRequest(t, server, "POST", "/api/auth/login", `{"username":"admin","password":"SenhaErrada123!"}`, "")
	if w.Code == 200 {
		t.Fatal("senha errada foi aceita")
	}

	entries, err := auditLog.List(audit.Filter{Action: "auth.login"})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entradas = %d, esperado 1", len(entries))
	}
	if entries[0].Target != "admin" {
		t.Errorf("conta tentada = %q, esperado admin", entries[0].Target)
	}
	if entries[0].Outcome == audit.OutcomeOK {
		t.Errorf("login falho registrado como sucesso: %+v", entries[0])
	}
}

// Rotas novas, ainda sem nome amigável, continuam auditadas pelo padrão da
// rota — a auditoria não depende de alguém lembrar de registrar a ação.
func TestAuditFallsBackToRoutePattern(t *testing.T) {
	server, auditLog, tokens := auditedServer(t)

	auditedRequest(t, server, "POST", "/api/v1/filesystem/browse", `{"path":"/"}`, tokens["admin"])

	entries, err := auditLog.List(audit.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entradas = %d, esperado 1", len(entries))
	}
	if !strings.Contains(entries[0].Action, "/api/v1/filesystem/browse") {
		t.Errorf("ação = %q, esperado conter o padrão da rota", entries[0].Action)
	}
}

func TestAuditEndpointRequiresAdministrator(t *testing.T) {
	server, _, tokens := auditedServer(t)

	if code := auditedRequest(t, server, "GET", "/api/v1/audit", "", "").Code; code != 401 {
		t.Errorf("sem sessão respondeu %d, esperado 401", code)
	}
	if code := auditedRequest(t, server, "GET", "/api/v1/audit", "", tokens["leitor"]).Code; code != 403 {
		t.Errorf("papel de visualização respondeu %d, esperado 403", code)
	}
	w := auditedRequest(t, server, "GET", "/api/v1/audit", "", tokens["admin"])
	if w.Code != 200 {
		t.Errorf("administrador respondeu %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"enabled":true`) {
		t.Errorf("corpo inesperado: %s", w.Body.String())
	}
}

// Sem auditoria configurada o painel segue funcionando normalmente.
func TestServerWorksWithoutAuditLog(t *testing.T) {
	server, _, tokens := auditedServer(t)
	server.SetAuditLog(nil)

	w := auditedRequest(t, server, "POST", "/api/v1/users", `{"username":"outro","password":"DisposablePassword123!","role":"view"}`, tokens["admin"])
	if w.Code >= 500 {
		t.Fatalf("o painel falhou sem auditoria: %d %s", w.Code, w.Body.String())
	}
	w = auditedRequest(t, server, "GET", "/api/v1/audit", "", tokens["admin"])
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"enabled":false`) {
		t.Errorf("endpoint de auditoria desativado respondeu %d: %s", w.Code, w.Body.String())
	}
}

// Uma requisição sem sessão é recusada pela autenticação, antes da
// autorização. Por a auditoria envolver os dois, a tentativa fica registrada
// — é o que permite notar varredura de endpoints sem credencial.
func TestAuditRecordsUnauthenticatedMutation(t *testing.T) {
	server, auditLog, _ := auditedServer(t)

	w := auditedRequest(t, server, "DELETE", "/api/v1/users/1", "", "")
	if w.Code != 401 {
		t.Fatalf("requisição sem sessão respondeu %d, esperado 401", w.Code)
	}

	entries, err := auditLog.List(audit.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entradas = %d, esperado 1", len(entries))
	}
	if entries[0].Actor != "anônimo" {
		t.Errorf("ator = %q, esperado anônimo", entries[0].Actor)
	}
	if entries[0].Outcome != audit.OutcomeDenied || entries[0].Status != 401 {
		t.Errorf("resultado/status = %q/%d", entries[0].Outcome, entries[0].Status)
	}
	if entries[0].IP != "192.0.2.10" {
		t.Errorf("ip = %q", entries[0].IP)
	}
}
