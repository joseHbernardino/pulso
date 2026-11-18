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

// Valida o inventário contra o Docker Engine real: o formato das listagens, a
// classificação de propriedade e a honestidade da simulação de limpeza.
func TestInventoryAgainstRealDocker(t *testing.T) {
	socket := os.Getenv("PULSO_DOCKER_SOCKET")
	if socket == "" {
		socket = "/var/run/docker.sock"
	}
	if _, err := os.Stat(socket); err != nil {
		t.Skipf("socket do Docker indisponível em %s", socket)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	engine := NewEngine(socket)
	if err := engine.negotiate(ctx); err != nil {
		t.Skipf("Engine inacessível: %v", err)
	}
	agent := &Agent{
		cfg:     Config{Directory: t.TempDir()},
		project: "pulso-inventario-teste",
		engine:  engine,
	}

	// Um contêiner marcado como infraestrutura deste projeto e um contêiner
	// comum, para conferir a classificação com dados reais.
	infra := "pulso-inv-infra-test"
	plain := "pulso-inv-plain-test"
	for _, name := range []string{infra, plain} {
		_ = exec.CommandContext(ctx, "docker", "rm", "-f", name).Run()
	}
	t.Cleanup(func() {
		for _, name := range []string{infra, plain} {
			_ = exec.Command("docker", "rm", "-f", name).Run()
		}
	})

	create := exec.CommandContext(ctx, "docker", "run", "-d", "--name", infra,
		"--label", "io.pulso.infrastructure=1", "nginx:alpine")
	if output, err := create.CombinedOutput(); err != nil {
		t.Skipf("não foi possível criar o contêiner de teste: %v: %s", err, strings.TrimSpace(string(output)))
	}
	create = exec.CommandContext(ctx, "docker", "run", "-d", "--name", plain, "nginx:alpine")
	if output, err := create.CombinedOutput(); err != nil {
		t.Fatalf("criar contêiner comum: %v: %s", err, strings.TrimSpace(string(output)))
	}

	containers, err := agent.containerInventory(ctx)
	if err != nil {
		t.Fatal(err)
	}

	var foundInfra, foundPlain bool
	for _, container := range containers {
		switch {
		case contains(container.Names, infra):
			foundInfra = true
			if container.Ownership != OwnershipInfrastructure {
				t.Errorf("contêiner rotulado classificado como %q", container.Ownership)
			}
			if !container.Protected || container.ProtectReason == "" {
				t.Errorf("infraestrutura não marcada como protegida: %+v", container)
			}
		case contains(container.Names, plain):
			foundPlain = true
			if container.Ownership != OwnershipForeign {
				t.Errorf("contêiner comum classificado como %q", container.Ownership)
			}
			if container.Protected {
				t.Errorf("contêiner comum marcado como protegido: %+v", container)
			}
		}
	}
	if !foundInfra || !foundPlain {
		t.Fatalf("os contêineres de teste não apareceram no inventário (infra=%v comum=%v)", foundInfra, foundPlain)
	}

	// A ação precisa ser recusada no contêiner de infraestrutura, e o
	// contêiner tem de continuar em execução depois disso.
	if err := agent.inventoryContainerAction(ctx, infra, "stop"); err == nil {
		t.Error("parada aceita no contêiner de infraestrutura")
	}
	state, err := exec.CommandContext(ctx, "docker", "inspect", "--format", "{{.State.Running}}", infra).Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(state)) != "true" {
		t.Error("o contêiner de infraestrutura foi parado apesar da recusa")
	}

	// Listagens de imagens, volumes e redes precisam decodificar.
	if _, err := agent.imageInventory(ctx); err != nil {
		t.Errorf("inventário de imagens: %v", err)
	}
	if _, err := agent.volumeInventory(ctx); err != nil {
		t.Errorf("inventário de volumes: %v", err)
	}
	networks, err := agent.networkInventory(ctx)
	if err != nil {
		t.Errorf("inventário de redes: %v", err)
	}
	for _, network := range networks {
		if predefinedNetworks[network.Name] && !network.Protected {
			t.Errorf("rede predefinida %q não protegida", network.Name)
		}
	}

	// Uso de disco e simulação de limpeza com dados reais.
	usage, err := agent.DiskUsage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if usage.LayersSize <= 0 {
		t.Errorf("tamanho das camadas = %d", usage.LayersSize)
	}
	if usage.Reclaimable.Total < 0 {
		t.Errorf("recuperável negativo: %d", usage.Reclaimable.Total)
	}

	before := runningContainerIDs(t)
	result, err := agent.Prune(ctx, PruneResources(), true)
	if err != nil {
		t.Fatal(err)
	}
	if !result.DryRun || len(result.Removed) != 0 {
		t.Errorf("a simulação reportou remoções: %+v", result.Removed)
	}
	// Nenhum candidato pode ser um contêiner em execução.
	for _, candidate := range result.Candidates {
		if candidate.Resource == ResourceContainers && before[candidate.ID] {
			t.Errorf("contêiner em execução entrou como candidato: %+v", candidate)
		}
	}
	// E a simulação não pode ter mexido em nada.
	after := runningContainerIDs(t)
	if len(after) != len(before) {
		t.Errorf("a simulação alterou os contêineres em execução: %d -> %d", len(before), len(after))
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func runningContainerIDs(t *testing.T) map[string]bool {
	t.Helper()
	output, err := exec.Command("docker", "ps", "-q", "--no-trunc").Output()
	if err != nil {
		t.Fatal(err)
	}
	result := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if line != "" {
			result[line] = true
		}
	}
	return result
}
