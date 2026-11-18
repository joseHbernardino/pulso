//go:build integration

package containerupdate

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Confere a leitura de métricas contra o Docker Engine real: o caminho da
// requisição, o formato do corpo e os números calculados. Usa um contêiner
// descartável com limites conhecidos.
func TestContainerStatsAgainstRealDocker(t *testing.T) {
	socket := os.Getenv("PULSO_DOCKER_SOCKET")
	if socket == "" {
		socket = "/var/run/docker.sock"
	}
	if _, err := os.Stat(socket); err != nil {
		t.Skipf("socket do Docker indisponível em %s", socket)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	engine := NewEngine(socket)
	if err := engine.negotiate(ctx); err != nil {
		t.Skipf("Engine inacessível: %v", err)
	}

	name := "pulso-stats-integration-test"
	_ = exec.CommandContext(ctx, "docker", "rm", "-f", name).Run()
	// 256 MiB de limite para validar memory_limit e memory_percent.
	create := exec.CommandContext(ctx, "docker", "run", "-d", "--name", name,
		"--memory", "256m", "--cpus", "0.5", "nginx:alpine")
	if output, err := create.CombinedOutput(); err != nil {
		t.Skipf("não foi possível criar o contêiner de teste: %v: %s", err, strings.TrimSpace(string(output)))
	}
	t.Cleanup(func() {
		_ = exec.Command("docker", "rm", "-f", name).Run()
	})

	// Duas amostras garantem que o Engine tenha uma leitura anterior.
	time.Sleep(2 * time.Second)
	if _, err := engine.containerStats(ctx, name); err != nil {
		t.Fatalf("primeira amostra: %v", err)
	}
	time.Sleep(2 * time.Second)

	sample, err := engine.containerStats(ctx, name)
	if err != nil {
		t.Fatalf("segunda amostra: %v", err)
	}
	stats := sample.compute()

	const limit = 256 * 1024 * 1024
	if stats.MemoryLimit != limit {
		t.Errorf("memory_limit = %d, esperado %d", stats.MemoryLimit, limit)
	}
	if stats.MemoryBytes <= 0 {
		t.Errorf("memory_bytes = %d, esperado maior que zero", stats.MemoryBytes)
	}
	if stats.MemoryBytes > stats.MemoryLimit {
		t.Errorf("memory_bytes (%d) acima do limite (%d)", stats.MemoryBytes, stats.MemoryLimit)
	}
	if stats.MemoryPercent <= 0 || stats.MemoryPercent > 100 {
		t.Errorf("memory_percent = %v, fora da faixa", stats.MemoryPercent)
	}
	if stats.CPUs < 1 {
		t.Errorf("cpus = %v, esperado ao menos 1", stats.CPUs)
	}
	if stats.CPUPercent < 0 || stats.CPUPercent > stats.CPUs*100 {
		t.Errorf("cpu_percent = %v, fora da faixa para %v cpus", stats.CPUPercent, stats.CPUs)
	}
	if stats.Processes <= 0 {
		t.Errorf("processos = %d, esperado maior que zero (nginx tem vários)", stats.Processes)
	}
}
