package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pulso.local/pulso/internal/auth"
	"pulso.local/pulso/internal/notify"
	"pulso.local/pulso/internal/supervisor"
)

func notifyServer(t *testing.T) (*Server, *notify.Store, map[string]string) {
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

	notifyStore, err := notify.OpenStore(filepath.Join(dir, "notifications.json"))
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := notify.NewDispatcher(notifyStore)
	t.Cleanup(dispatcher.Close)

	sup := supervisor.New(supervisor.Options{LogDir: dir})
	t.Cleanup(func() { _ = sup.Shutdown() })

	server := NewServerWithUsers(sup, "127.0.0.1:0", store)
	t.Cleanup(server.sessions.Stop)
	server.SetNotifications(notifyStore, dispatcher)

	tokens := map[string]string{}
	for _, name := range []string{"admin", "leitor"} {
		session, err := server.sessions.CreateSession(name)
		if err != nil {
			t.Fatal(err)
		}
		tokens[name] = session.Token
	}
	return server, notifyStore, tokens
}

func notifyRequest(t *testing.T, server *Server, method, path, body, sessionToken string) *httptest.ResponseRecorder {
	t.Helper()
	handler, err := server.handler()
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if sessionToken != "" {
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sessionToken})
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}

// Credenciais de canal nunca podem sair pela API.
func TestNotificationAPINeverReturnsSecrets(t *testing.T) {
	server, store, tokens := notifyServer(t)
	const secret = "xoxb-token-que-nao-pode-sair"

	if _, err := store.Put(notify.Channel{
		Kind: notify.KindSlack, Name: "Equipe", Enabled: true,
		Settings: map[string]string{"webhook_url": "https://hooks.slack.com/services/" + secret},
	}); err != nil {
		t.Fatal(err)
	}

	w := notifyRequest(t, server, "GET", "/api/v1/notifications/channels", "", tokens["admin"])
	if w.Code != 200 {
		t.Fatalf("resposta %d: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), secret) {
		t.Fatalf("a credencial vazou na listagem: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), notify.Redacted) {
		t.Errorf("o marcador de segredo guardado não apareceu: %s", w.Body.String())
	}
}

func TestNotificationAPIRequiresAdministrator(t *testing.T) {
	server, _, tokens := notifyServer(t)

	for _, tc := range []struct{ method, path, body string }{
		{"GET", "/api/v1/notifications/channels", ""},
		{"POST", "/api/v1/notifications/channels", `{"kind":"webhook","name":"x","settings":{"url":"http://a/b"}}`},
		{"DELETE", "/api/v1/notifications/channels/abc", ""},
		{"POST", "/api/v1/notifications/channels/abc/test", ""},
	} {
		if code := notifyRequest(t, server, tc.method, tc.path, tc.body, tokens["leitor"]).Code; code != 403 {
			t.Errorf("%s %s com papel de visualização respondeu %d, esperado 403", tc.method, tc.path, code)
		}
		if code := notifyRequest(t, server, tc.method, tc.path, tc.body, "").Code; code != 401 {
			t.Errorf("%s %s sem sessão respondeu %d, esperado 401", tc.method, tc.path, code)
		}
	}
}

func TestNotificationAPICreateUpdateDelete(t *testing.T) {
	server, store, tokens := notifyServer(t)

	w := notifyRequest(t, server, "POST", "/api/v1/notifications/channels",
		`{"kind":"ntfy","name":"Plantao","enabled":true,"events":["deploy.failed"],"settings":{"url":"http://ntfy.interno","topic":"pulso","token":"segredo"}}`,
		tokens["admin"])
	if w.Code != 200 {
		t.Fatalf("criação respondeu %d: %s", w.Code, w.Body.String())
	}
	var created notify.Channel
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.ID == "" {
		t.Fatal("identificador ausente")
	}
	if created.Settings["token"] != notify.Redacted {
		t.Errorf("o segredo não foi redigido na resposta de criação: %q", created.Settings["token"])
	}

	// Salvar de volta o canal redigido não pode apagar o token guardado.
	w = notifyRequest(t, server, "PUT", "/api/v1/notifications/channels/"+created.ID,
		`{"kind":"ntfy","name":"Plantao renomeado","enabled":true,"events":["deploy.failed"],"settings":{"url":"http://ntfy.interno","topic":"pulso","token":"`+notify.Redacted+`"}}`,
		tokens["admin"])
	if w.Code != 200 {
		t.Fatalf("atualização respondeu %d: %s", w.Code, w.Body.String())
	}
	stored, err := store.Get(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Settings["token"] != "segredo" {
		t.Errorf("o token guardado foi perdido na edição: %q", stored.Settings["token"])
	}
	if stored.Name != "Plantao renomeado" {
		t.Errorf("nome não atualizado: %q", stored.Name)
	}

	w = notifyRequest(t, server, "DELETE", "/api/v1/notifications/channels/"+created.ID, "", tokens["admin"])
	if w.Code != 200 {
		t.Fatalf("remoção respondeu %d: %s", w.Code, w.Body.String())
	}
	if _, err := store.Get(created.ID); err == nil {
		t.Error("canal removido ainda presente")
	}
}

func TestNotificationAPIRejectsInvalidChannel(t *testing.T) {
	server, _, tokens := notifyServer(t)

	for _, body := range []string{
		`{"kind":"slack","name":"x","settings":{"webhook_url":"http://sem-https/x"}}`,
		`{"kind":"inexistente","name":"x"}`,
		`{"kind":"webhook","name":"","settings":{"url":"http://a/b"}}`,
		`{"kind":"webhook","name":"x","events":["nao.existe"],"settings":{"url":"http://a/b"}}`,
	} {
		w := notifyRequest(t, server, "POST", "/api/v1/notifications/channels", body, tokens["admin"])
		if w.Code == 200 {
			t.Errorf("configuração inválida aceita: %s", body)
		}
	}
}

// O teste de canal precisa reportar o erro real de entrega, para a
// configuração ser corrigível pelo painel.
func TestNotificationTestReportsDeliveryFailure(t *testing.T) {
	server, store, tokens := notifyServer(t)

	// Endereço reservado para documentação: a conexão não completa.
	created, err := store.Put(notify.Channel{
		Kind: notify.KindWebhook, Name: "Inalcancavel", Enabled: true,
		Settings: map[string]string{"url": "http://192.0.2.1:9/hook"},
	})
	if err != nil {
		t.Fatal(err)
	}

	w := notifyRequest(t, server, "POST", "/api/v1/notifications/channels/"+created.ID+"/test", "", tokens["admin"])
	if w.Code != http.StatusBadGateway {
		t.Fatalf("teste de canal inalcançável respondeu %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "error") {
		t.Errorf("o erro de entrega não foi reportado: %s", w.Body.String())
	}
}

func TestNotificationTestDeliversToReachableChannel(t *testing.T) {
	server, store, tokens := notifyServer(t)

	received := make(chan notify.Event, 1)
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var event notify.Event
		_ = json.NewDecoder(r.Body).Decode(&event)
		received <- event
		w.WriteHeader(200)
	}))
	defer destination.Close()

	created, err := store.Put(notify.Channel{
		Kind: notify.KindWebhook, Name: "Interno", Enabled: true,
		// Assina apenas um evento: o teste deve chegar mesmo assim.
		Events:   []string{notify.EventSystemUpdate},
		Settings: map[string]string{"url": destination.URL},
	})
	if err != nil {
		t.Fatal(err)
	}

	w := notifyRequest(t, server, "POST", "/api/v1/notifications/channels/"+created.ID+"/test", "", tokens["admin"])
	if w.Code != 200 {
		t.Fatalf("teste respondeu %d: %s", w.Code, w.Body.String())
	}
	select {
	case event := <-received:
		if event.Kind != notify.EventTest {
			t.Errorf("evento recebido = %q", event.Kind)
		}
	default:
		t.Fatal("o destino não recebeu o evento de teste")
	}
}

// Sem notificações configuradas o painel segue funcionando.
func TestNotificationAPIWithoutStore(t *testing.T) {
	server, _, tokens := notifyServer(t)
	server.SetNotifications(nil, nil)

	w := notifyRequest(t, server, "GET", "/api/v1/notifications/channels", "", tokens["admin"])
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"enabled":false`) {
		t.Errorf("listagem sem armazenamento respondeu %d: %s", w.Code, w.Body.String())
	}
}
