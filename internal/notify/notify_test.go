package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	store, err := OpenStore(filepath.Join(t.TempDir(), "notifications.json"))
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestStoreCreatesOwnerOnlyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "notifications.json")
	if _, err := OpenStore(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("permissão = %o, esperado 600", mode)
	}
}

func TestStorePutGetDelete(t *testing.T) {
	store := testStore(t)

	created, err := store.Put(Channel{
		Kind: KindNtfy, Name: "Equipe", Enabled: true,
		Events:   []string{EventDeployFailed, EventDeploySucceeded},
		Settings: map[string]string{"url": "http://ntfy.interno", "topic": "pulso", "token": "segredo-ntfy"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" {
		t.Fatal("identificador não atribuído")
	}
	if len(created.Events) != 2 || created.Events[0] != EventDeployFailed {
		t.Errorf("eventos não normalizados: %v", created.Events)
	}

	fetched, err := store.Get(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fetched.Settings["token"] != "segredo-ntfy" {
		t.Error("o segredo não foi persistido para uso interno")
	}

	if err := store.Delete(created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(created.ID); err == nil {
		t.Fatal("canal removido ainda encontrado")
	}
	if err := store.Delete(created.ID); err == nil {
		t.Fatal("remoção de canal ausente foi aceita")
	}
}

func TestStoreRejectsUpdateOfMissingChannel(t *testing.T) {
	store := testStore(t)
	_, err := store.Put(Channel{
		ID: "naoexiste", Kind: KindSlack, Name: "X",
		Settings: map[string]string{"webhook_url": "https://hooks.slack.com/services/a/b/c"},
	})
	if err == nil {
		t.Fatal("atualização de canal inexistente foi aceita")
	}
}

// Segredos nunca podem sair para o navegador.
func TestPublicRedactsSecretsOnly(t *testing.T) {
	channel := Channel{
		Kind: KindEmail, Name: "Avisos",
		Settings: map[string]string{
			"host": "smtp.interno", "port": "587", "from": "pulso@interno",
			"to": "equipe@interno", "username": "pulso", "password": "SenhaSMTPSecreta",
		},
	}
	public := channel.Public()

	if public.Settings["password"] != Redacted {
		t.Errorf("senha não redigida: %q", public.Settings["password"])
	}
	for _, visible := range []string{"host", "port", "from", "to", "username"} {
		if public.Settings[visible] != channel.Settings[visible] {
			t.Errorf("campo não secreto %q foi alterado: %q", visible, public.Settings[visible])
		}
	}
	// A redação não pode alterar o original.
	if channel.Settings["password"] != "SenhaSMTPSecreta" {
		t.Error("Public alterou a configuração de origem")
	}
	serialized, err := json.Marshal(public)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(serialized), "SenhaSMTPSecreta") {
		t.Fatalf("segredo presente no JSON público: %s", serialized)
	}
}

// Um campo vazio continua vazio: a interface precisa distinguir "não
// configurado" de "configurado e oculto".
func TestPublicKeepsEmptySecretEmpty(t *testing.T) {
	public := Channel{Kind: KindNtfy, Settings: map[string]string{"token": ""}}.Public()
	if public.Settings["token"] != "" {
		t.Errorf("segredo ausente foi marcado como guardado: %q", public.Settings["token"])
	}
}

// Editar um canal sem redigitar o segredo preserva o valor guardado.
func TestPutPreservesStoredSecretOnEdit(t *testing.T) {
	store := testStore(t)
	created, err := store.Put(Channel{
		Kind: KindGotify, Name: "Gotify", Enabled: true,
		Settings: map[string]string{"url": "http://gotify.interno", "token": "token-original"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// A interface devolve o marcador no lugar do segredo; salvar assim não
	// pode apagar o token.
	edited := created.Public()
	edited.Name = "Gotify renomeado"
	if _, err := store.Put(edited); err != nil {
		t.Fatal(err)
	}

	stored, err := store.Get(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Settings["token"] != "token-original" {
		t.Errorf("token = %q, esperado o original preservado", stored.Settings["token"])
	}
	if stored.Name != "Gotify renomeado" {
		t.Errorf("nome não atualizado: %q", stored.Name)
	}
}

func TestValidation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		channel Channel
		valid   bool
	}{
		{"slack válido", Channel{Kind: KindSlack, Name: "S", Settings: map[string]string{"webhook_url": "https://hooks.slack.com/services/a/b/c"}}, true},
		{"slack sem https", Channel{Kind: KindSlack, Name: "S", Settings: map[string]string{"webhook_url": "http://hooks.slack.com/x"}}, false},
		{"slack sem url", Channel{Kind: KindSlack, Name: "S"}, false},
		{"tipo desconhecido", Channel{Kind: KindSlack + "x", Name: "S"}, false},
		{"sem nome", Channel{Kind: KindSlack, Settings: map[string]string{"webhook_url": "https://x.y/z"}}, false},
		{"nome com quebra de linha", Channel{Kind: KindSlack, Name: "a\nb", Settings: map[string]string{"webhook_url": "https://x.y/z"}}, false},
		{"webhook http interno aceito", Channel{Kind: KindWebhook, Name: "W", Settings: map[string]string{"url": "http://interno:9000/hook"}}, true},
		{"webhook url inválida", Channel{Kind: KindWebhook, Name: "W", Settings: map[string]string{"url": "ftp://x/y"}}, false},
		{"telegram completo", Channel{Kind: KindTelegram, Name: "T", Settings: map[string]string{"bot_token": "123:abc", "chat_id": "-100"}}, true},
		{"telegram sem chat", Channel{Kind: KindTelegram, Name: "T", Settings: map[string]string{"bot_token": "123:abc"}}, false},
		{"ntfy completo", Channel{Kind: KindNtfy, Name: "N", Settings: map[string]string{"url": "http://ntfy.interno", "topic": "t"}}, true},
		{"ntfy sem tópico", Channel{Kind: KindNtfy, Name: "N", Settings: map[string]string{"url": "http://ntfy.interno"}}, false},
		{"gotify sem token", Channel{Kind: KindGotify, Name: "G", Settings: map[string]string{"url": "http://g.interno"}}, false},
		{"email completo", Channel{Kind: KindEmail, Name: "E", Settings: map[string]string{"host": "smtp.interno", "from": "a@b.c", "to": "d@e.f"}}, true},
		{"email sem destinatário", Channel{Kind: KindEmail, Name: "E", Settings: map[string]string{"host": "smtp.interno", "from": "a@b.c"}}, false},
		{"email destinatário inválido", Channel{Kind: KindEmail, Name: "E", Settings: map[string]string{"host": "smtp.interno", "from": "a@b.c", "to": "semarroba"}}, false},
		{"email porta inválida", Channel{Kind: KindEmail, Name: "E", Settings: map[string]string{"host": "h", "from": "a@b.c", "to": "d@e.f", "port": "99999"}}, false},
		{"evento desconhecido", Channel{Kind: KindWebhook, Name: "W", Events: []string{"nao.existe"}, Settings: map[string]string{"url": "http://x/y"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			channel := tc.channel
			err := channel.Validate()
			if tc.valid && err != nil {
				t.Fatalf("configuração válida recusada: %v", err)
			}
			if !tc.valid && err == nil {
				t.Fatal("configuração inválida aceita")
			}
		})
	}
}

// Injeção de cabeçalho por e-mail: quebras de linha no título não podem
// criar cabeçalhos novos.
func TestEmailHeaderInjectionIsBlocked(t *testing.T) {
	event := Event{
		Title: "ok\r\nBcc: invasor@externo\r\nX-Injetado: 1",
		At:    time.Now(),
	}.normalize()
	message := string(buildEmail("pulso@interno", []string{"equipe@interno"}, event))

	headerSection := message
	if index := strings.Index(message, "\r\n\r\n"); index >= 0 {
		headerSection = message[:index]
	}
	// A injeção só ocorre se uma LINHA nova de cabeçalho for criada. O texto
	// pode continuar dentro do valor do assunto, o que é inofensivo.
	allowed := map[string]bool{"from": true, "to": true, "subject": true, "date": true, "mime-version": true, "content-type": true}
	for _, line := range strings.Split(headerSection, "\r\n") {
		if line == "" || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue
		}
		name, _, found := strings.Cut(line, ":")
		if !found {
			t.Fatalf("linha de cabeçalho malformada: %q", line)
		}
		if !allowed[strings.ToLower(strings.TrimSpace(name))] {
			t.Fatalf("cabeçalho injetado criado: %q", line)
		}
	}
	if strings.Count(headerSection, "Subject:") != 1 {
		t.Fatalf("mais de um assunto no cabeçalho:\n%s", headerSection)
	}
}

func TestEmailValidationRejectsHeaderBreakInFrom(t *testing.T) {
	channel := Channel{Kind: KindEmail, Name: "E", Settings: map[string]string{
		"host": "smtp.interno", "from": "a@b.c\r\nBcc: x@y.z", "to": "d@e.f",
	}}
	if err := channel.Validate(); err == nil {
		t.Fatal("remetente com quebra de linha foi aceito")
	}
}

func TestSubscriptionFiltering(t *testing.T) {
	all := Channel{Kind: KindWebhook, Name: "todos"}
	if !all.Subscribes(EventDeployFailed) || !all.Subscribes(EventSystemUpdate) {
		t.Error("lista vazia deveria assinar todos os eventos")
	}
	specific := Channel{Kind: KindWebhook, Name: "um", Events: []string{EventDeployFailed}}
	if !specific.Subscribes(EventDeployFailed) {
		t.Error("evento assinado foi recusado")
	}
	if specific.Subscribes(EventDeploySucceeded) {
		t.Error("evento não assinado foi aceito")
	}
	// O teste manual do painel precisa chegar independentemente das assinaturas.
	if !specific.Subscribes(EventTest) {
		t.Error("evento de teste deveria sempre passar")
	}
}

// recordingSender captura os envios para testar o despachante.
type recordingSender struct {
	mu     sync.Mutex
	events []Event
	fail   int32
}

func (r *recordingSender) Send(_ context.Context, event Event) error {
	if atomic.LoadInt32(&r.fail) > 0 {
		atomic.AddInt32(&r.fail, -1)
		return io.ErrUnexpectedEOF
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
	return nil
}

func (r *recordingSender) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.events)
}

func TestDispatcherDeliversOnlyToSubscribedEnabledChannels(t *testing.T) {
	store := testStore(t)
	for _, channel := range []Channel{
		{Kind: KindWebhook, Name: "assina", Enabled: true, Events: []string{EventDeployFailed}, Settings: map[string]string{"url": "http://a/b"}},
		{Kind: KindWebhook, Name: "nao-assina", Enabled: true, Events: []string{EventSystemUpdate}, Settings: map[string]string{"url": "http://a/b"}},
		{Kind: KindWebhook, Name: "desativado", Enabled: false, Settings: map[string]string{"url": "http://a/b"}},
	} {
		if _, err := store.Put(channel); err != nil {
			t.Fatal(err)
		}
	}

	recorder := &recordingSender{}
	dispatcher := NewDispatcher(store)
	dispatcher.newSender = func(Channel) (Sender, error) { return recorder, nil }

	dispatcher.Dispatch(Event{Kind: EventDeployFailed, Title: "falhou"})
	dispatcher.Close()

	if recorder.count() != 1 {
		t.Fatalf("envios = %d, esperado 1 (só o canal assinante e ativo)", recorder.count())
	}
}

func TestDispatcherRetriesThenSucceeds(t *testing.T) {
	store := testStore(t)
	if _, err := store.Put(Channel{Kind: KindWebhook, Name: "w", Enabled: true, Settings: map[string]string{"url": "http://a/b"}}); err != nil {
		t.Fatal(err)
	}
	recorder := &recordingSender{fail: 2}
	dispatcher := NewDispatcher(store)
	dispatcher.newSender = func(Channel) (Sender, error) { return recorder, nil }

	channel, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	// Chamada direta para não esperar o backoff do worker duas vezes.
	if err := dispatcher.send(channel[0], Event{Kind: EventTest}.normalize()); err != nil {
		t.Fatalf("o envio deveria ter sucedido na terceira tentativa: %v", err)
	}
	dispatcher.Close()
	if recorder.count() != 1 {
		t.Errorf("entregas registradas = %d, esperado 1", recorder.count())
	}
}

// Dispatch nunca bloqueia: uma notificação não pode travar um deploy.
func TestDispatchNeverBlocksAndCountsDrops(t *testing.T) {
	store := testStore(t)
	if _, err := store.Put(Channel{Kind: KindWebhook, Name: "w", Enabled: true, Settings: map[string]string{"url": "http://a/b"}}); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	blocking := senderFunc(func(ctx context.Context, _ Event) error {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil
	})
	dispatcher := NewDispatcher(store)
	dispatcher.newSender = func(Channel) (Sender, error) { return blocking, nil }
	t.Cleanup(func() {
		close(release)
		dispatcher.Close()
	})

	done := make(chan struct{})
	go func() {
		for i := 0; i < queueCapacity*3; i++ {
			dispatcher.Dispatch(Event{Kind: EventTest, Title: "x"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Dispatch bloqueou com a fila cheia")
	}
	if dispatcher.Dropped() == 0 {
		t.Error("eventos descartados não foram contados")
	}
}

func TestDispatchAfterCloseIsSafe(t *testing.T) {
	store := testStore(t)
	dispatcher := NewDispatcher(store)
	dispatcher.Close()
	// Não deve causar panic nem escrever em canal fechado.
	dispatcher.Dispatch(Event{Kind: EventTest})
	dispatcher.Close()
}

func TestNilDispatcherIsSafe(t *testing.T) {
	var dispatcher *Dispatcher
	dispatcher.Dispatch(Event{Kind: EventTest})
	dispatcher.Close()
	if dispatcher.Dropped() != 0 {
		t.Error("Dropped em despachante nulo")
	}
	if err := dispatcher.TestChannel("x"); err == nil {
		t.Error("TestChannel em despachante nulo deveria falhar")
	}
}

type senderFunc func(context.Context, Event) error

func (f senderFunc) Send(ctx context.Context, event Event) error { return f(ctx, event) }

// ------------------------------------------------- canais sobre HTTP real

func TestSlackSenderPayload(t *testing.T) {
	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&received)
		w.WriteHeader(200)
	}))
	defer server.Close()

	sender := &slackSender{channel: Channel{Kind: KindSlack, Settings: map[string]string{"webhook_url": server.URL}}}
	event := Event{Kind: EventDeployFailed, Severity: SeverityError, Title: "Falha", Message: "detalhe", Application: "api"}.normalize()
	if err := sender.Send(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if received["text"] != "Falha" {
		t.Errorf("texto = %v", received["text"])
	}
	attachments, ok := received["attachments"].([]any)
	if !ok || len(attachments) != 1 {
		t.Fatalf("anexos = %v", received["attachments"])
	}
	first := attachments[0].(map[string]any)
	if first["color"] != "#d93025" {
		t.Errorf("cor de erro = %v", first["color"])
	}
}

func TestNtfySenderUsesHeadersAndBearer(t *testing.T) {
	var gotAuth, gotTitle, gotPriority, body string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotTitle = r.Header.Get("Title")
		gotPriority = r.Header.Get("Priority")
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		w.WriteHeader(200)
	}))
	defer server.Close()

	sender := &ntfySender{channel: Channel{Kind: KindNtfy, Settings: map[string]string{
		"url": server.URL, "topic": "pulso", "token": "tok123",
	}}}
	event := Event{Kind: EventDeployFailed, Severity: SeverityError, Title: "Linha1\nLinha2", Message: "corpo"}.normalize()
	if err := sender.Send(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer tok123" {
		t.Errorf("authorization = %q", gotAuth)
	}
	// Quebras de linha no título quebrariam o cabeçalho HTTP.
	if strings.ContainsAny(gotTitle, "\r\n") {
		t.Errorf("título com quebra de linha no cabeçalho: %q", gotTitle)
	}
	if gotPriority != "high" {
		t.Errorf("prioridade = %q, esperado high para erro", gotPriority)
	}
	if !strings.Contains(body, "corpo") {
		t.Errorf("corpo = %q", body)
	}
}

func TestGotifySenderSendsTokenInHeaderNotURL(t *testing.T) {
	var gotKey, gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-Gotify-Key")
		gotQuery = r.URL.RawQuery
		w.WriteHeader(200)
	}))
	defer server.Close()

	sender := &gotifySender{channel: Channel{Kind: KindGotify, Settings: map[string]string{
		"url": server.URL, "token": "segredo",
	}}}
	if err := sender.Send(context.Background(), Event{Kind: EventTest, Title: "t"}.normalize()); err != nil {
		t.Fatal(err)
	}
	if gotKey != "segredo" {
		t.Errorf("cabeçalho do token = %q", gotKey)
	}
	// O token na URL apareceria em logs de proxies e servidores.
	if strings.Contains(gotQuery, "segredo") {
		t.Errorf("token exposto na query: %q", gotQuery)
	}
}

func TestWebhookSenderSendsEventAndAuthorization(t *testing.T) {
	var received Event
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&received)
		w.WriteHeader(204)
	}))
	defer server.Close()

	sender := &webhookSender{channel: Channel{Kind: KindWebhook, Settings: map[string]string{
		"url": server.URL, "authorization": "Bearer interno",
	}}}
	event := Event{Kind: EventDeploySucceeded, Title: "ok", Application: "api", Actor: "admin"}.normalize()
	if err := sender.Send(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer interno" {
		t.Errorf("authorization = %q", gotAuth)
	}
	if received.Kind != EventDeploySucceeded || received.Application != "api" || received.Actor != "admin" {
		t.Errorf("evento recebido = %+v", received)
	}
}

func TestSenderReportsDestinationError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte("algo deu errado no destino"))
	}))
	defer server.Close()

	sender := &webhookSender{channel: Channel{Kind: KindWebhook, Settings: map[string]string{"url": server.URL}}}
	err := sender.Send(context.Background(), Event{Kind: EventTest}.normalize())
	if err == nil {
		t.Fatal("erro do destino foi ignorado")
	}
	// A mensagem do destino ajuda a diagnosticar no painel.
	if !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "algo deu errado") {
		t.Errorf("erro pouco informativo: %v", err)
	}
}

func TestSenderRespectsContextDeadline(t *testing.T) {
	// O handler precisa terminar sozinho: httptest.Server.Close espera pelas
	// requisições pendentes, e o servidor só percebe o cliente desconectado
	// depois de consumir o corpo da requisição.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	}))
	defer server.Close()

	sender := &webhookSender{channel: Channel{Kind: KindWebhook, Settings: map[string]string{"url": server.URL}}}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := sender.Send(ctx, Event{Kind: EventTest}.normalize()); err == nil {
		t.Fatal("o prazo do contexto foi ignorado")
	}
}

func TestSenderForRejectsInvalidChannel(t *testing.T) {
	if _, err := senderFor(Channel{Kind: "inexistente", Name: "x"}); err == nil {
		t.Fatal("tipo inválido aceito")
	}
	if _, err := senderFor(Channel{Kind: KindSlack, Name: "x"}); err == nil {
		t.Fatal("slack sem URL aceito")
	}
}

func TestPlainTextIncludesContext(t *testing.T) {
	event := Event{
		Kind: EventDeployFailed, Title: "Falha na implantação",
		Message: "build falhou", Application: "api", Actor: "admin",
	}.normalize()
	text := event.plainText()
	for _, expected := range []string{"Falha na implantação", "api", "build falhou", "admin"} {
		if !strings.Contains(text, expected) {
			t.Errorf("texto sem %q: %s", expected, text)
		}
	}
}
