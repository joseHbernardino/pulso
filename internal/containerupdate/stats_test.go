package containerupdate

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// closeTo compara percentuais sem exigir igualdade binária exata.
func closeTo(got, want float64) bool {
	return math.Abs(got-want) < 1e-9
}

func TestComputeCPUPercent(t *testing.T) {
	for _, tc := range []struct {
		name                          string
		total, preTotal, sys, preSys  int64
		cpus                          int64
		expectedPercent, expectedCPUs float64
	}{
		{
			// 1s de CPU do contêiner em 10s de CPU do host, com 4 CPUs:
			// 0,1 * 4 * 100 = 40%.
			name:  "quarenta por cento em quatro cpus",
			total: 2_000_000_000, preTotal: 1_000_000_000,
			sys: 20_000_000_000, preSys: 10_000_000_000,
			cpus: 4, expectedPercent: 40, expectedCPUs: 4,
		},
		{
			name:  "contêiner ocioso",
			total: 1_000, preTotal: 1_000,
			sys: 20_000_000_000, preSys: 10_000_000_000,
			cpus: 8, expectedPercent: 0, expectedCPUs: 8,
		},
		{
			// A primeira leitura após o start pode não ter amostra anterior.
			name:  "sem amostra anterior",
			total: 5_000_000, preTotal: 0,
			sys: 0, preSys: 0,
			cpus: 2, expectedPercent: 0, expectedCPUs: 2,
		},
		{
			// Janela curta pode render razão acima do teto; é limitada.
			name:  "acima do teto é limitado",
			total: 100_000_000_000, preTotal: 0,
			sys: 10_000_000_000, preSys: 0,
			cpus: 2, expectedPercent: 200, expectedCPUs: 2,
		},
		{
			// online_cpus ausente em Engines antigos: assume uma CPU.
			name:  "sem online_cpus assume uma",
			total: 2_000_000_000, preTotal: 1_000_000_000,
			sys: 20_000_000_000, preSys: 10_000_000_000,
			cpus: 0, expectedPercent: 10, expectedCPUs: 1,
		},
		{
			// Contadores reiniciados (recriação do contêiner) não geram
			// percentual negativo.
			name:  "contador reiniciado",
			total: 1_000, preTotal: 9_000_000,
			sys: 20_000_000_000, preSys: 10_000_000_000,
			cpus: 4, expectedPercent: 0, expectedCPUs: 4,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var s dockerStats
			s.CPUStats.CPUUsage.TotalUsage = tc.total
			s.CPUStats.SystemUsage = tc.sys
			s.CPUStats.OnlineCPUs = tc.cpus
			s.PreCPUStats.CPUUsage.TotalUsage = tc.preTotal
			s.PreCPUStats.SystemUsage = tc.preSys

			got := s.compute()
			if !closeTo(got.CPUPercent, tc.expectedPercent) {
				t.Errorf("cpu_percent = %v, esperado %v", got.CPUPercent, tc.expectedPercent)
			}
			if got.CPUs != tc.expectedCPUs {
				t.Errorf("cpus = %v, esperado %v", got.CPUs, tc.expectedCPUs)
			}
			if got.CPUPercent < 0 {
				t.Errorf("cpu_percent negativo: %v", got.CPUPercent)
			}
		})
	}
}

func TestComputeMemoryDiscountsPageCache(t *testing.T) {
	for _, tc := range []struct {
		name          string
		stats         map[string]int64
		usage, limit  int64
		expectedBytes int64
		expectedPct   float64
	}{
		{
			// cgroups v2 usa inactive_file.
			name:  "cgroups v2",
			stats: map[string]int64{"inactive_file": 200},
			usage: 1200, limit: 2000,
			expectedBytes: 1000, expectedPct: 50,
		},
		{
			// cgroups v1 com hierarquia usa total_inactive_file.
			name:  "cgroups v1 hierárquico",
			stats: map[string]int64{"total_inactive_file": 500},
			usage: 1500, limit: 2000,
			expectedBytes: 1000, expectedPct: 50,
		},
		{
			name:  "cgroups v1 simples",
			stats: map[string]int64{"cache": 300},
			usage: 1300, limit: 2600,
			expectedBytes: 1000, expectedPct: 38.46153846153846,
		},
		{
			name:  "sem contador de cache",
			stats: map[string]int64{},
			usage: 1000, limit: 2000,
			expectedBytes: 1000, expectedPct: 50,
		},
		{
			// Cache maior que o uso total seria inconsistente: ignorado.
			name:  "cache incoerente é ignorado",
			stats: map[string]int64{"inactive_file": 5000},
			usage: 1000, limit: 2000,
			expectedBytes: 1000, expectedPct: 50,
		},
		{
			// Sem limite (contêiner irrestrito) não há percentual.
			name:  "sem limite de memória",
			stats: map[string]int64{"inactive_file": 100},
			usage: 1100, limit: 0,
			expectedBytes: 1000, expectedPct: 0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var s dockerStats
			s.MemoryStats.Usage = tc.usage
			s.MemoryStats.Limit = tc.limit
			s.MemoryStats.Stats = tc.stats

			got := s.compute()
			if got.MemoryBytes != tc.expectedBytes {
				t.Errorf("memory_bytes = %d, esperado %d", got.MemoryBytes, tc.expectedBytes)
			}
			if !closeTo(got.MemoryPercent, tc.expectedPct) {
				t.Errorf("memory_percent = %v, esperado %v", got.MemoryPercent, tc.expectedPct)
			}
		})
	}
}

func TestComputeAggregatesNetworkBlockAndPids(t *testing.T) {
	// Formato real do Engine, conferido contra GET /containers/{id}/stats.
	payload := `{
      "cpu_stats": {"cpu_usage": {"total_usage": 2000000000}, "system_cpu_usage": 20000000000, "online_cpus": 2},
      "precpu_stats": {"cpu_usage": {"total_usage": 1000000000}, "system_cpu_usage": 10000000000},
      "memory_stats": {"usage": 19542016, "limit": 268435456, "stats": {"inactive_file": 204800}},
      "networks": {"eth0": {"rx_bytes": 736, "tx_bytes": 126}, "eth1": {"rx_bytes": 64, "tx_bytes": 10}},
      "blkio_stats": {"io_service_bytes_recursive": [
        {"op": "read", "value": 4096}, {"op": "write", "value": 8192},
        {"op": "Read", "value": 1024}, {"op": "sync", "value": 999}
      ]},
      "pids_stats": {"current": 25, "limit": 19121}
    }`
	var s dockerStats
	if err := json.Unmarshal([]byte(payload), &s); err != nil {
		t.Fatal(err)
	}
	got := s.compute()

	if got.CPUPercent != 20 {
		t.Errorf("cpu_percent = %v, esperado 20", got.CPUPercent)
	}
	if got.MemoryBytes != 19542016-204800 {
		t.Errorf("memory_bytes = %d", got.MemoryBytes)
	}
	if got.NetworkRx != 800 || got.NetworkTx != 136 {
		t.Errorf("rede rx/tx = %d/%d, esperado 800/136", got.NetworkRx, got.NetworkTx)
	}
	// "Read" com maiúscula conta junto; "sync" é ignorado.
	if got.BlockRead != 5120 || got.BlockWrite != 8192 {
		t.Errorf("bloco leitura/escrita = %d/%d, esperado 5120/8192", got.BlockRead, got.BlockWrite)
	}
	if got.Processes != 25 {
		t.Errorf("processos = %d, esperado 25", got.Processes)
	}
}

// statsAgent monta um agente apontado para um Engine falso.
func statsAgent(t *testing.T, handler http.HandlerFunc) *Agent {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		r.URL.Host = strings.TrimPrefix(server.URL, "http://")
		return http.DefaultTransport.RoundTrip(r)
	})
	a := &Agent{
		cfg:     Config{Directory: t.TempDir()},
		project: "own-project",
		engine:  &Engine{client: &http.Client{Transport: transport}},
	}
	if err := os.MkdirAll(a.appDir(), 0o750); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestApplicationStatsRefusesAnotherProjectsContainer(t *testing.T) {
	statsRequested := false
	a := statsAgent(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/stats") {
			statsRequested = true
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Id":     "foreign",
			"Config": map[string]any{"Labels": map[string]string{applicationOwner: "another-project", applicationLabel: "0123456789abcdef01234567"}},
		})
	})
	app := &ContainerApplication{ID: "0123456789abcdef01234567", ContainerID: "foreign"}
	if err := a.saveApplication(app); err != nil {
		t.Fatal(err)
	}
	if _, err := a.applicationStats(context.Background(), app.ID); err == nil {
		t.Fatal("métricas de contêiner de outro projeto foram aceitas")
	}
	if statsRequested {
		t.Fatal("o Engine recebeu um pedido de métricas para contêiner não pertencente")
	}
}

func TestApplicationStatsWithoutContainerReportsZeros(t *testing.T) {
	a := statsAgent(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Error("o Engine não deveria ser consultado")
		w.WriteHeader(500)
	})
	app := &ContainerApplication{ID: "0123456789abcdef01234567", Config: ApplicationConfig{Name: "sem-container"}}
	if err := a.saveApplication(app); err != nil {
		t.Fatal(err)
	}
	stats, err := a.applicationStats(context.Background(), app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Running || stats.CPUPercent != 0 || stats.MemoryBytes != 0 {
		t.Fatalf("esperado zerado e parado, obtido %+v", stats)
	}
	if stats.Name != "sem-container" {
		t.Errorf("nome = %q", stats.Name)
	}
}

func TestApplicationStatsStoppedContainerReportsZeros(t *testing.T) {
	a := statsAgent(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/stats") {
			t.Error("métricas pedidas para contêiner parado")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Id":     "stopped",
			"Config": map[string]any{"Labels": map[string]string{applicationOwner: "own-project", applicationLabel: "0123456789abcdef01234567"}},
			"State":  map[string]any{"Running": false},
		})
	})
	app := &ContainerApplication{ID: "0123456789abcdef01234567", ContainerID: "stopped"}
	if err := a.saveApplication(app); err != nil {
		t.Fatal(err)
	}
	stats, err := a.applicationStats(context.Background(), app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Running {
		t.Fatal("contêiner parado reportado como em execução")
	}
}

func TestApplicationStatsRunningContainer(t *testing.T) {
	a := statsAgent(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/stats") {
			if r.URL.Query().Get("stream") != "false" {
				t.Errorf("stream deveria ser false, veio %q", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{
              "cpu_stats": {"cpu_usage": {"total_usage": 3000000000}, "system_cpu_usage": 30000000000, "online_cpus": 2},
              "precpu_stats": {"cpu_usage": {"total_usage": 1000000000}, "system_cpu_usage": 10000000000},
              "memory_stats": {"usage": 2048, "limit": 4096, "stats": {"inactive_file": 1024}},
              "pids_stats": {"current": 3}
            }`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Id":     "running",
			"Config": map[string]any{"Labels": map[string]string{applicationOwner: "own-project", applicationLabel: "0123456789abcdef01234567"}},
			"State":  map[string]any{"Running": true},
		})
	})
	app := &ContainerApplication{ID: "0123456789abcdef01234567", ContainerID: "running", Config: ApplicationConfig{Name: "api"}}
	if err := a.saveApplication(app); err != nil {
		t.Fatal(err)
	}
	stats, err := a.applicationStats(context.Background(), app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !stats.Running {
		t.Fatal("esperado em execução")
	}
	if stats.CPUPercent != 20 {
		t.Errorf("cpu_percent = %v, esperado 20", stats.CPUPercent)
	}
	if stats.MemoryBytes != 1024 || stats.MemoryPercent != 25 {
		t.Errorf("memória = %d bytes (%v%%)", stats.MemoryBytes, stats.MemoryPercent)
	}
	if stats.Name != "api" || stats.ApplicationID != app.ID {
		t.Errorf("identificação = %q/%q", stats.Name, stats.ApplicationID)
	}
}

func TestAllApplicationStatsIsolatesFailures(t *testing.T) {
	a := statsAgent(t, func(w http.ResponseWriter, r *http.Request) {
		// O contêiner "broken" pertence a outro projeto: o erro fica contido
		// na entrada dele e não derruba a listagem.
		if strings.Contains(r.URL.Path, "/broken") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Id":     "broken",
				"Config": map[string]any{"Labels": map[string]string{applicationOwner: "another-project"}},
			})
			return
		}
		if strings.Contains(r.URL.Path, "/stats") {
			_, _ = w.Write([]byte(`{"memory_stats": {"usage": 100, "limit": 200}, "pids_stats": {"current": 1}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Id":     "healthy",
			"Config": map[string]any{"Labels": map[string]string{applicationOwner: "own-project", applicationLabel: "aaaaaaaaaaaaaaaaaaaaaaaa"}},
			"State":  map[string]any{"Running": true},
		})
	})
	ok := &ContainerApplication{ID: "aaaaaaaaaaaaaaaaaaaaaaaa", ContainerID: "healthy", Config: ApplicationConfig{Name: "boa"}}
	bad := &ContainerApplication{ID: "bbbbbbbbbbbbbbbbbbbbbbbb", ContainerID: "broken", Config: ApplicationConfig{Name: "ruim"}}
	for _, app := range []*ContainerApplication{ok, bad} {
		if err := a.saveApplication(app); err != nil {
			t.Fatal(err)
		}
	}

	all, err := a.allApplicationStats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("esperado 2 entradas, obtido %d", len(all))
	}
	// Ordenado por nome: "boa" antes de "ruim".
	if all[0].ApplicationID != ok.ID {
		t.Errorf("ordenação inesperada: %+v", all)
	}
	if all[0].Error != "" || !all[0].Running {
		t.Errorf("a aplicação saudável não foi coletada: %+v", all[0])
	}
	if all[1].Error == "" {
		t.Error("a falha da aplicação problemática não foi reportada na entrada dela")
	}
	if all[1].Name != "ruim" {
		t.Errorf("a entrada com falha perdeu o nome: %q", all[1].Name)
	}
}

func TestApplicationIDsLockedIgnoresUnrelatedFiles(t *testing.T) {
	a := statsAgent(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) })
	valid := "0123456789abcdef01234567"
	if err := a.saveApplication(&ContainerApplication{ID: valid}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"notes.txt", "0123456789abcdef01234567.tar.gz", "short.json", "../escape.json"} {
		path := a.appDir() + "/" + strings.ReplaceAll(name, "/", "_")
		if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ids, err := a.applicationIDsLocked()
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != valid {
		t.Fatalf("ids = %v, esperado apenas %s", ids, valid)
	}
}
