package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"pulso.local/pulso/internal/containerupdate"
)

func dockerRequest(t *testing.T, server *Server, method, path, body, sessionToken string) *httptest.ResponseRecorder {
	t.Helper()
	return auditedRequestOn(t, server, method, path, body, sessionToken)
}

func TestDockerInventoryReadAllowsViewRole(t *testing.T) {
	server, tokens := statsServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/docker/containers"):
			_, _ = w.Write([]byte(`{"containers":[{"id":"abc","names":["app"],"ownership":"foreign","state":"running"}]}`))
		case strings.HasSuffix(r.URL.Path, "/docker/images"):
			_, _ = w.Write([]byte(`{"images":[{"id":"sha256:a","size":100,"dangling":true}]}`))
		case strings.HasSuffix(r.URL.Path, "/docker/volumes"):
			_, _ = w.Write([]byte(`{"volumes":[{"name":"v","size":10}]}`))
		case strings.HasSuffix(r.URL.Path, "/docker/networks"):
			_, _ = w.Write([]byte(`{"networks":[{"id":"n","name":"livre"}]}`))
		case strings.HasSuffix(r.URL.Path, "/docker/disk"):
			_, _ = w.Write([]byte(`{"layers_size":1000,"reclaimable":{"total":500}}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	})

	for _, path := range []string{
		"/api/v1/docker/containers",
		"/api/v1/docker/images",
		"/api/v1/docker/volumes",
		"/api/v1/docker/networks",
		"/api/v1/docker/disk",
	} {
		w := dockerRequest(t, server, "GET", path, "", tokens["leitor"])
		if w.Code != 200 {
			t.Errorf("%s com papel de visualização respondeu %d: %s", path, w.Code, w.Body.String())
		}
	}
}

// O inspect devolve o ambiente do contêiner, que costuma guardar
// credenciais. Para quem não é administrador, os valores têm de sair.
func TestDockerContainerRedactsEnvironmentForViewRole(t *testing.T) {
	const secret = "SenhaDoBancoQueNaoPodeSair"
	agent := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
          "id":"abc","ownership":"foreign","state":"running",
          "env":["PORT=8080","DATABASE_PASSWORD=` + secret + `"],
          "mounts":[{"type":"bind","source":"/srv/segredos","destination":"/data","read_write":true}]
        }`))
	}
	server, tokens := statsServer(t, agent)

	// Papel de visualização: chaves preservadas, valores removidos.
	w := dockerRequest(t, server, "GET", "/api/v1/docker/containers/abc", "", tokens["leitor"])
	if w.Code != 200 {
		t.Fatalf("resposta %d: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), secret) {
		t.Fatalf("a credencial vazou para o papel de visualização: %s", w.Body.String())
	}
	var redacted containerupdate.ContainerDetails
	if err := json.Unmarshal(w.Body.Bytes(), &redacted); err != nil {
		t.Fatal(err)
	}
	foundKey := false
	for _, entry := range redacted.Env {
		if strings.HasPrefix(entry, "DATABASE_PASSWORD=") {
			foundKey = true
			if !strings.HasSuffix(entry, "***") {
				t.Errorf("valor não redigido: %q", entry)
			}
		}
	}
	if !foundKey {
		t.Errorf("a chave da variável foi perdida na redação: %v", redacted.Env)
	}
	// O caminho de origem do bind revela a disposição do host.
	for _, mount := range redacted.Mounts {
		if mount.Source != "" {
			t.Errorf("caminho do host exposto ao papel de visualização: %q", mount.Source)
		}
	}

	// Administrador vê o valor real.
	w = dockerRequest(t, server, "GET", "/api/v1/docker/containers/abc", "", tokens["admin"])
	if !strings.Contains(w.Body.String(), secret) {
		t.Errorf("o administrador não recebeu o ambiente completo: %s", w.Body.String())
	}
}

// Logs podem conter segredos impressos pelo próprio programa.
func TestDockerContainerLogsAreAdminOnly(t *testing.T) {
	server, tokens := statsServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"logs":"linha de log"}`))
	})

	if code := dockerRequest(t, server, "GET", "/api/v1/docker/containers/abc/logs", "", tokens["leitor"]).Code; code != 403 {
		t.Errorf("papel de visualização leu logs: %d", code)
	}
	if code := dockerRequest(t, server, "GET", "/api/v1/docker/containers/abc/logs", "", tokens["admin"]).Code; code != 200 {
		t.Errorf("administrador não leu logs: %d", code)
	}
}

func TestDockerMutationsAreAdminOnly(t *testing.T) {
	server, tokens := statsServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})

	for _, tc := range []struct{ method, path, body string }{
		{"POST", "/api/v1/docker/containers/abc/stop", ""},
		{"DELETE", "/api/v1/docker/images/sha256:a", ""},
		{"DELETE", "/api/v1/docker/volumes/vol", ""},
		{"DELETE", "/api/v1/docker/networks/net", ""},
		{"POST", "/api/v1/docker/prune", `{"dry_run":true}`},
	} {
		if code := dockerRequest(t, server, tc.method, tc.path, tc.body, tokens["leitor"]).Code; code != 403 {
			t.Errorf("%s %s com papel de visualização respondeu %d, esperado 403", tc.method, tc.path, code)
		}
		if code := dockerRequest(t, server, tc.method, tc.path, tc.body, "").Code; code != 401 {
			t.Errorf("%s %s sem sessão respondeu %d, esperado 401", tc.method, tc.path, code)
		}
	}
}

// A limpeza apaga dados: sem simulação nem confirmação explícita, recusa.
func TestDockerPruneRequiresDryRunOrConfirmation(t *testing.T) {
	var reachedAgent bool
	server, tokens := statsServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/prune") {
			reachedAgent = true
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"dry_run":false,"reclaimed":0}`))
	})

	w := dockerRequest(t, server, "POST", "/api/v1/docker/prune", `{"resources":["images"]}`, tokens["admin"])
	if w.Code != 400 {
		t.Fatalf("limpeza sem confirmação respondeu %d: %s", w.Code, w.Body.String())
	}
	if reachedAgent {
		t.Fatal("a limpeza sem confirmação chegou ao agente")
	}

	// Com simulação, passa.
	w = dockerRequest(t, server, "POST", "/api/v1/docker/prune", `{"resources":["images"],"dry_run":true}`, tokens["admin"])
	if w.Code != 200 {
		t.Errorf("simulação respondeu %d: %s", w.Code, w.Body.String())
	}
	// Com confirmação explícita, passa.
	w = dockerRequest(t, server, "POST", "/api/v1/docker/prune", `{"resources":["images"],"confirm":true}`, tokens["admin"])
	if w.Code != 200 {
		t.Errorf("limpeza confirmada respondeu %d: %s", w.Code, w.Body.String())
	}
}

// O cliente recusa referências que tentariam escapar do caminho, antes de
// qualquer requisição ao agente.
func TestDockerRejectsPathEscapingReferences(t *testing.T) {
	server, tokens := statsServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("o agente foi consultado com referência inválida: %s", r.URL.Path)
		w.WriteHeader(500)
	})

	for _, path := range []string{
		"/api/v1/docker/volumes/..%2f..%2fetc",
		"/api/v1/docker/images/..%2f..%2fpasswd",
	} {
		if code := dockerRequest(t, server, "DELETE", path, "", tokens["admin"]).Code; code == 200 {
			t.Errorf("%s foi aceito", path)
		}
	}
}

func TestDockerDiskWithoutAgent(t *testing.T) {
	server, tokens := statsServer(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) })
	server.updateClient = nil

	w := dockerRequest(t, server, "GET", "/api/v1/docker/disk", "", tokens["admin"])
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"enabled":false`) {
		t.Errorf("sem agente respondeu %d: %s", w.Code, w.Body.String())
	}
}

func TestRedactEnvironment(t *testing.T) {
	got := redactEnvironment([]string{"A=1", "B=segredo", "SEM_IGUAL", "=semNome"})
	if len(got) != 2 {
		t.Fatalf("entradas = %v", got)
	}
	for _, entry := range got {
		if !strings.HasSuffix(entry, "=***") {
			t.Errorf("entrada não redigida: %q", entry)
		}
	}
	if redactEnvironment(nil) != nil {
		t.Error("ambiente vazio deveria render nil")
	}
}
