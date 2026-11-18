package web

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pulso.local/pulso/internal/auth"
	"pulso.local/pulso/internal/containerupdate"
	"pulso.local/pulso/internal/supervisor"
)

// statsServer monta um painel com um agente falso escutando no socket Unix
// que o cliente real usa, e devolve também os papéis criados.
func statsServer(t *testing.T, agent http.HandlerFunc) (*Server, map[string]string) {
	t.Helper()
	dir := t.TempDir()
	updates := shortTempDir(t)

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

	sup := supervisor.New(supervisor.Options{LogDir: dir})
	t.Cleanup(func() { _ = sup.Shutdown() })

	server := NewServerWithUsers(sup, "127.0.0.1:0", store)
	t.Cleanup(server.sessions.Stop)
	server.updateDirectory = updates
	server.updateClient = containerupdate.NewClient(updates)

	listener, err := net.Listen("unix", filepath.Join(updates, "updater.sock"))
	if err != nil {
		t.Fatal(err)
	}
	helper := &http.Server{Handler: agent}
	go func() { _ = helper.Serve(listener) }()
	t.Cleanup(func() { _ = helper.Shutdown(context.Background()) })

	tokens := map[string]string{}
	for _, name := range []string{"admin", "leitor"} {
		session, err := server.sessions.CreateSession(name)
		if err != nil {
			t.Fatal(err)
		}
		tokens[name] = session.Token
	}
	return server, tokens
}

func statsRequest(t *testing.T, server *Server, path, sessionToken string) *httptest.ResponseRecorder {
	t.Helper()
	handler, err := server.handler()
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", path, nil)
	if sessionToken != "" {
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sessionToken})
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}

func TestContainerStatsRequireAuthentication(t *testing.T) {
	server, _ := statsServer(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Error("o agente foi consultado sem sessão")
		w.WriteHeader(500)
	})
	// O middleware de sessão de /api/v1 responde 401 antes de a requisição
	// alcançar o handler; o 403 do containerReader cobre o caso de sessão
	// presente sem armazenamento de usuários.
	for _, path := range []string{
		"/api/v1/container-stats",
		"/api/v1/container-applications/0123456789abcdef01234567/stats",
	} {
		if code := statsRequest(t, server, path, "").Code; code != 401 {
			t.Errorf("%s sem sessão respondeu %d, esperado 401", path, code)
		}
	}
}

// Métricas não expõem variáveis nem configuração, então o papel de
// visualização as recebe. Escrever continua restrito ao administrador.
func TestContainerStatsAllowViewRole(t *testing.T) {
	server, tokens := statsServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/stats" {
			t.Errorf("caminho inesperado no agente: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"stats":[{"application_id":"0123456789abcdef01234567","name":"api","running":true,"cpu_percent":12.5,"memory_bytes":1024}]}`))
	})

	w := statsRequest(t, server, "/api/v1/container-stats", tokens["leitor"])
	if w.Code != 200 {
		t.Fatalf("papel de visualização recebeu %d: %s", w.Code, w.Body.String())
	}
	var body struct {
		Enabled bool                               `json:"enabled"`
		Stats   []containerupdate.ApplicationStats `json:"stats"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Enabled || len(body.Stats) != 1 {
		t.Fatalf("corpo inesperado: %s", w.Body.String())
	}
	if body.Stats[0].Name != "api" || body.Stats[0].CPUPercent != 12.5 {
		t.Errorf("métricas não repassadas: %+v", body.Stats[0])
	}
}

func TestContainerStatsPerApplication(t *testing.T) {
	const id = "0123456789abcdef01234567"
	server, tokens := statsServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/applications/"+id+"/stats" {
			t.Errorf("caminho inesperado no agente: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"application_id":"` + id + `","running":true,"memory_percent":40}`))
	})

	w := statsRequest(t, server, "/api/v1/container-applications/"+id+"/stats", tokens["admin"])
	if w.Code != 200 {
		t.Fatalf("resposta %d: %s", w.Code, w.Body.String())
	}
	var stats containerupdate.ApplicationStats
	if err := json.Unmarshal(w.Body.Bytes(), &stats); err != nil {
		t.Fatal(err)
	}
	if stats.ApplicationID != id || !stats.Running || stats.MemoryPercent != 40 {
		t.Errorf("métricas inesperadas: %+v", stats)
	}
}

// Um identificador fora do formato é recusado pelo cliente, antes de qualquer
// requisição ao agente.
func TestContainerStatsRejectInvalidIdentifier(t *testing.T) {
	server, tokens := statsServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("o agente foi consultado com identificador inválido: %s", r.URL.Path)
		w.WriteHeader(500)
	})
	w := statsRequest(t, server, "/api/v1/container-applications/..%2fetc%2fpasswd/stats", tokens["admin"])
	if w.Code == 200 {
		t.Fatalf("identificador inválido aceito: %s", w.Body.String())
	}
}

// Sem o serviço auxiliar ativo, a listagem responde desabilitada em vez de
// erro, para o painel poder renderizar a página.
func TestAllContainerStatsWithoutAgent(t *testing.T) {
	server, tokens := statsServer(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) })
	server.updateClient = nil

	w := statsRequest(t, server, "/api/v1/container-stats", tokens["admin"])
	if w.Code != 200 {
		t.Fatalf("resposta %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"enabled":false`) {
		t.Errorf("esperado enabled:false, obtido %s", w.Body.String())
	}
}
