package audit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func openTestLog(t *testing.T, retention time.Duration) *Log {
	t.Helper()
	log, err := Open(filepath.Join(t.TempDir(), "audit.sqlite"), retention)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = log.Close() })
	return log
}

func TestOpenCreatesOwnerOnlyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "audit.sqlite")
	log, err := Open(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = log.Close() }()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("permissão do arquivo = %o, esperado 600", mode)
	}
	if log.Retention() != DefaultRetention {
		t.Errorf("retenção = %v, esperado o padrão", log.Retention())
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.sqlite")
	first, err := Open(path, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Record(Entry{Actor: "admin", Action: "test.action"}); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	// Reabrir um arquivo existente preserva as entradas: o esquema usa
	// CREATE TABLE IF NOT EXISTS, como o banco de contas.
	second, err := Open(path, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()
	total, err := second.Count()
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("entradas após reabrir = %d, esperado 1", total)
	}
}

func TestRecordRequiresAction(t *testing.T) {
	log := openTestLog(t, 0)
	if err := log.Record(Entry{Actor: "admin"}); err == nil {
		t.Fatal("entrada sem ação foi aceita")
	}
	if err := log.Record(Entry{Actor: "admin", Action: "   "}); err == nil {
		t.Fatal("ação em branco foi aceita")
	}
}

// Um Log nulo descarta a entrada, para que os chamadores não precisem
// verificar se a auditoria está ativa.
func TestNilLogIsSafe(t *testing.T) {
	var log *Log
	if err := log.Record(Entry{Action: "test.action"}); err != nil {
		t.Errorf("Record em Log nulo: %v", err)
	}
	entries, err := log.List(Filter{})
	if err != nil || len(entries) != 0 {
		t.Errorf("List em Log nulo: %v %v", entries, err)
	}
	if removed, err := log.Prune(time.Now()); err != nil || removed != 0 {
		t.Errorf("Prune em Log nulo: %d %v", removed, err)
	}
	if total, err := log.Count(); err != nil || total != 0 {
		t.Errorf("Count em Log nulo: %d %v", total, err)
	}
	if err := log.Close(); err != nil {
		t.Errorf("Close em Log nulo: %v", err)
	}
	if log.Retention() != 0 {
		t.Error("Retention em Log nulo deveria ser zero")
	}
}

func TestListOrdersNewestFirstAndFilters(t *testing.T) {
	log := openTestLog(t, 0)
	base := time.Now().Add(-10 * time.Hour)
	entries := []Entry{
		{Actor: "admin", Action: "application.deploy", Target: "api", CreatedAt: base},
		{Actor: "leitor", Action: "auth.login", CreatedAt: base.Add(time.Hour)},
		{Actor: "admin", Action: "application.stop", Target: "api", CreatedAt: base.Add(2 * time.Hour)},
		{Actor: "admin", Action: "user.create", Target: "novo", CreatedAt: base.Add(3 * time.Hour)},
	}
	for _, entry := range entries {
		if err := log.Record(entry); err != nil {
			t.Fatal(err)
		}
	}

	all, err := log.List(Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 4 {
		t.Fatalf("total = %d, esperado 4", len(all))
	}
	if all[0].Action != "user.create" || all[3].Action != "application.deploy" {
		t.Errorf("ordenação errada: %s ... %s", all[0].Action, all[3].Action)
	}

	byActor, err := log.List(Filter{Actor: "leitor"})
	if err != nil {
		t.Fatal(err)
	}
	if len(byActor) != 1 || byActor[0].Actor != "leitor" {
		t.Errorf("filtro por ator: %+v", byActor)
	}

	// O filtro de ação casa por prefixo, para selecionar uma família inteira.
	family, err := log.List(Filter{Action: "application."})
	if err != nil {
		t.Fatal(err)
	}
	if len(family) != 2 {
		t.Errorf("filtro por família de ação = %d entradas, esperado 2", len(family))
	}

	recent, err := log.List(Filter{Since: base.Add(90 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 2 {
		t.Errorf("filtro por período = %d entradas, esperado 2", len(recent))
	}

	limited, err := log.List(Filter{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 2 {
		t.Errorf("limite = %d entradas, esperado 2", len(limited))
	}
}

func TestListRoundTripsAllFields(t *testing.T) {
	log := openTestLog(t, 0)
	moment := time.Now().Add(-time.Minute).Truncate(time.Nanosecond)
	want := Entry{
		Actor:     "admin",
		Role:      "admin",
		Action:    "application.deploy",
		Target:    "api",
		Outcome:   OutcomeDenied,
		Status:    403,
		Detail:    "imagem nginx:alpine",
		IP:        "10.0.0.5",
		UserAgent: "curl/8.0",
		CreatedAt: moment,
	}
	if err := log.Record(want); err != nil {
		t.Fatal(err)
	}
	got, err := log.List(Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("entradas = %d", len(got))
	}
	entry := got[0]
	if entry.Actor != want.Actor || entry.Role != want.Role || entry.Action != want.Action ||
		entry.Target != want.Target || entry.Outcome != want.Outcome || entry.Status != want.Status ||
		entry.Detail != want.Detail || entry.IP != want.IP || entry.UserAgent != want.UserAgent {
		t.Errorf("campos não preservados: %+v", entry)
	}
	if !entry.CreatedAt.Equal(moment) {
		t.Errorf("horário = %v, esperado %v", entry.CreatedAt, moment)
	}
	if entry.ID == 0 {
		t.Error("identificador não atribuído")
	}
}

func TestRecordDefaultsOutcomeAndTime(t *testing.T) {
	log := openTestLog(t, 0)
	before := time.Now()
	if err := log.Record(Entry{Actor: "admin", Action: "test.action"}); err != nil {
		t.Fatal(err)
	}
	entries, err := log.List(Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if entries[0].Outcome != OutcomeOK {
		t.Errorf("resultado padrão = %q, esperado %q", entries[0].Outcome, OutcomeOK)
	}
	if entries[0].CreatedAt.Before(before.Add(-time.Second)) {
		t.Errorf("horário não preenchido: %v", entries[0].CreatedAt)
	}
}

func TestPruneRemovesOnlyEntriesPastRetention(t *testing.T) {
	log := openTestLog(t, 24*time.Hour)
	now := time.Now()
	if err := log.Record(Entry{Actor: "a", Action: "old.action", CreatedAt: now.Add(-48 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := log.Record(Entry{Actor: "b", Action: "recent.action", CreatedAt: now.Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	removed, err := log.Prune(now)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removidas = %d, esperado 1", removed)
	}
	entries, err := log.List(Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Action != "recent.action" {
		t.Errorf("a entrada errada foi removida: %+v", entries)
	}
}

func TestNegativeRetentionKeepsEverything(t *testing.T) {
	log := openTestLog(t, -1)
	if err := log.Record(Entry{Actor: "a", Action: "ancient.action", CreatedAt: time.Now().Add(-10000 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	removed, err := log.Prune(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if removed != 0 {
		t.Fatalf("removidas = %d com retenção negativa, esperado 0", removed)
	}
}

// EnvKeys é a barreira que impede valores de ambiente de chegarem ao
// registro. Deve listar apenas nomes, em ordem estável.
func TestEnvKeysNeverExposesValues(t *testing.T) {
	env := map[string]string{
		"DATABASE_URL": "postgres://usuario:senhaSuperSecreta@db:5432/app",
		"API_KEY":      "sk-live-0123456789",
		"PORT":         "8080",
	}
	rendered := EnvKeys(env)

	if rendered != "API_KEY, DATABASE_URL, PORT" {
		t.Errorf("chaves = %q", rendered)
	}
	for _, secret := range []string{"senhaSuperSecreta", "sk-live-0123456789", "postgres://", "8080"} {
		if strings.Contains(rendered, secret) {
			t.Errorf("valor %q vazou nas chaves: %q", secret, rendered)
		}
	}
	if EnvKeys(nil) != "" || EnvKeys(map[string]string{}) != "" {
		t.Error("ambiente vazio deveria render string vazia")
	}
}

func TestTruncateBoundsStoredText(t *testing.T) {
	log := openTestLog(t, 0)
	huge := strings.Repeat("A", maxDetail*3)
	if err := log.Record(Entry{Actor: "admin", Action: "test.action", Detail: huge}); err != nil {
		t.Fatal(err)
	}
	entries, err := log.List(Filter{})
	if err != nil {
		t.Fatal(err)
	}
	// Um detalhe longo não pode crescer o arquivo sem limite.
	if len(entries[0].Detail) > maxDetail+4 {
		t.Errorf("detalhe armazenado com %d bytes, acima do limite", len(entries[0].Detail))
	}
}

func TestRecordStripsNullBytes(t *testing.T) {
	log := openTestLog(t, 0)
	if err := log.Record(Entry{Actor: "ad\x00min", Action: "test.action", Detail: "a\x00b"}); err != nil {
		t.Fatal(err)
	}
	entries, err := log.List(Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsRune(entries[0].Actor, 0) || strings.ContainsRune(entries[0].Detail, 0) {
		t.Error("byte nulo persistido")
	}
}
