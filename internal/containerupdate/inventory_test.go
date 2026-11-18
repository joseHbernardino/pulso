package containerupdate

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// inventoryAgent builds an agent whose Engine is a fake responding from the
// supplied routing function.
func inventoryAgent(t *testing.T, routes map[string]any) (*Agent, *[]string) {
	t.Helper()
	var mutations []string
	agent := statsAgent(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			mutations = append(mutations, r.Method+" "+r.URL.Path)
		}
		for pattern, payload := range routes {
			if strings.Contains(r.URL.Path, pattern) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(payload)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})
	// Os fixtures usam nomes como pulso_data e pulso-applications, então o
	// projeto do agente tem de ser o mesmo.
	agent.project = "pulso"
	return agent, &mutations
}

func TestClassifyOwnership(t *testing.T) {
	agent := &Agent{project: "pulso"}
	for _, tc := range []struct {
		name          string
		labels        map[string]string
		wantOwnership string
		wantAppID     string
	}{
		{"sem rótulos é externo", nil, OwnershipForeign, ""},
		{
			"rótulo de infraestrutura",
			map[string]string{infrastructureLabel: "1"},
			OwnershipInfrastructure, "",
		},
		{
			// Instalações por Compose não têm o rótulo novo.
			"painel por projeto e serviço Compose",
			map[string]string{projectLabel: "pulso", serviceLabel: "pulso"},
			OwnershipInfrastructure, "",
		},
		{
			"agente por projeto e serviço Compose",
			map[string]string{projectLabel: "pulso", serviceLabel: "pulso-updater"},
			OwnershipInfrastructure, "",
		},
		{
			"aplicação do Pulso",
			map[string]string{applicationOwner: "pulso", applicationLabel: "0123456789abcdef01234567"},
			OwnershipApplication, "0123456789abcdef01234567",
		},
		{
			// Outra instalação do Pulso no mesmo host é externa a esta.
			"aplicação de outro projeto Pulso",
			map[string]string{applicationOwner: "outro", applicationLabel: "aaaa"},
			OwnershipForeign, "",
		},
		{
			"serviço Compose qualquer do mesmo nome de projeto",
			map[string]string{projectLabel: "pulso", serviceLabel: "banco"},
			OwnershipForeign, "",
		},
		{
			"projeto Compose alheio",
			map[string]string{projectLabel: "outra-stack", serviceLabel: "pulso"},
			OwnershipForeign, "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ownership, appID := agent.classify(tc.labels)
			if ownership != tc.wantOwnership {
				t.Errorf("propriedade = %q, esperado %q", ownership, tc.wantOwnership)
			}
			if appID != tc.wantAppID {
				t.Errorf("id de aplicação = %q, esperado %q", appID, tc.wantAppID)
			}
		})
	}
}

// A infraestrutura precisa ser intocável: parar o agente deixaria o painel
// sem como gerenciar nada.
func TestInfrastructureRefusesEveryAction(t *testing.T) {
	for _, action := range []string{"start", "stop", "restart", "remove"} {
		if err := containerActionAllowed(OwnershipInfrastructure, action); err == nil {
			t.Errorf("ação %q permitida na infraestrutura", action)
		}
	}
}

// Uma aplicação do Pulso pode ser parada e iniciada daqui, mas remover
// deixaria o cadastro órfão.
func TestApplicationRefusesOnlyRemoval(t *testing.T) {
	for _, action := range []string{"start", "stop", "restart"} {
		if err := containerActionAllowed(OwnershipApplication, action); err != nil {
			t.Errorf("ação %q recusada em aplicação: %v", action, err)
		}
	}
	if err := containerActionAllowed(OwnershipApplication, "remove"); err == nil {
		t.Error("remoção de aplicação permitida pelo inventário")
	}
}

func TestForeignAllowsEveryAction(t *testing.T) {
	for _, action := range []string{"start", "stop", "restart", "remove"} {
		if err := containerActionAllowed(OwnershipForeign, action); err != nil {
			t.Errorf("ação %q recusada em contêiner externo: %v", action, err)
		}
	}
}

func TestInventoryContainerActionRefusesInfrastructureWithoutTouchingEngine(t *testing.T) {
	agent, mutations := inventoryAgent(t, map[string]any{
		"/json": map[string]any{
			"Id":     "panel",
			"Config": map[string]any{"Labels": map[string]string{infrastructureLabel: "1"}},
			"State":  map[string]any{"Running": true},
		},
	})
	for _, action := range []string{"stop", "remove", "restart"} {
		if err := agent.inventoryContainerAction(context.Background(), "panel", action); err == nil {
			t.Errorf("ação %q aceita na infraestrutura", action)
		}
	}
	if len(*mutations) != 0 {
		t.Fatalf("o Engine recebeu mutações para a infraestrutura: %v", *mutations)
	}
}

func TestInventoryContainerActionRejectsUnknownAction(t *testing.T) {
	agent, _ := inventoryAgent(t, map[string]any{
		"/json": map[string]any{"Id": "x", "Config": map[string]any{"Labels": map[string]string{}}},
	})
	if err := agent.inventoryContainerAction(context.Background(), "x", "destruir"); err == nil {
		t.Fatal("ação inventada foi aceita")
	}
}

func TestVolumeProtection(t *testing.T) {
	agent := &Agent{project: "pulso"}
	for _, tc := range []struct {
		name      string
		volume    rawVolume
		protected bool
	}{
		{"volume de dados do Pulso", rawVolume{Name: "pulso_data"}, true},
		{"volume do socket do agente", rawVolume{Name: "pulso_updates"}, true},
		{
			"volume de projeto do Pulso",
			rawVolume{Name: "qualquer", Labels: map[string]string{applicationOwner: "pulso"}},
			true,
		},
		{
			"volume em uso",
			rawVolume{Name: "usado", UsageData: &struct {
				Size     int64 `json:"Size"`
				RefCount int64 `json:"RefCount"`
			}{Size: 10, RefCount: 2}},
			true,
		},
		{
			"volume solto",
			rawVolume{Name: "orfao", UsageData: &struct {
				Size     int64 `json:"Size"`
				RefCount int64 `json:"RefCount"`
			}{Size: 10, RefCount: 0}},
			false,
		},
		{"volume de outra instalação do Pulso", rawVolume{Name: "outro_data"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reason := agent.volumeProtection(tc.volume)
			if tc.protected && reason == "" {
				t.Error("volume que deveria ser protegido ficou liberado")
			}
			if !tc.protected && reason != "" {
				t.Errorf("volume liberado foi protegido: %s", reason)
			}
		})
	}
}

func TestNetworkProtection(t *testing.T) {
	agent := &Agent{project: "pulso"}
	for _, tc := range []struct {
		name      string
		network   rawNetwork
		protected bool
	}{
		{"bridge predefinida", rawNetwork{Name: "bridge"}, true},
		{"host predefinida", rawNetwork{Name: "host"}, true},
		{"none predefinida", rawNetwork{Name: "none"}, true},
		{"rede dos projetos", rawNetwork{Name: "pulso-applications"}, true},
		{
			"rede com contêineres",
			rawNetwork{Name: "ocupada", Containers: map[string]any{"a": nil}},
			true,
		},
		{
			"rede do projeto Compose do Pulso",
			rawNetwork{Name: "x", Labels: map[string]string{projectLabel: "pulso"}},
			true,
		},
		{"rede vazia alheia", rawNetwork{Name: "livre"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reason := agent.networkProtection(tc.network)
			if tc.protected && reason == "" {
				t.Error("rede que deveria ser protegida ficou liberada")
			}
			if !tc.protected && reason != "" {
				t.Errorf("rede liberada foi protegida: %s", reason)
			}
		})
	}
}

func TestInfrastructureImagesAreCollected(t *testing.T) {
	agent := &Agent{project: "pulso"}
	containers := []rawContainer{
		{ID: "a", Image: "pulso:latest", ImageID: "sha256:aaa", Labels: map[string]string{infrastructureLabel: "1"}},
		{ID: "b", Image: "nginx:alpine", ImageID: "sha256:bbb", Labels: map[string]string{}},
	}
	protected := agent.infrastructureImages(containers)
	if !protected["sha256:aaa"] || !protected["pulso:latest"] {
		t.Errorf("imagem da infraestrutura não protegida: %v", protected)
	}
	if protected["sha256:bbb"] {
		t.Error("imagem de contêiner externo foi protegida")
	}
}

func TestUsableTagsDropsDanglingPlaceholder(t *testing.T) {
	got := usableTags([]string{"<none>:<none>", "nginx:alpine", "", "<none>"})
	if len(got) != 1 || got[0] != "nginx:alpine" {
		t.Errorf("tags = %v", got)
	}
}

func TestTrimNamesRemovesLeadingSlash(t *testing.T) {
	got := trimNames([]string{"/api", "/db"})
	if got[0] != "api" || got[1] != "db" {
		t.Errorf("nomes = %v", got)
	}
}

func TestValidDockerRef(t *testing.T) {
	for _, valid := range []string{
		"a1b2c3", "nginx:alpine", "ghcr.io/owner/image:1.0",
		"image@sha256:0123456789abcdef", "pulso_data", "my-net.1",
	} {
		if !validDockerRef(valid) {
			t.Errorf("referência válida recusada: %q", valid)
		}
	}
	for _, invalid := range []string{
		"", "..", "../etc/passwd", "a/../b", "com espaço",
		"linha\nnova", "nulo\x00", "a;rm -rf /", strings.Repeat("a", 301),
	} {
		if validDockerRef(invalid) {
			t.Errorf("referência inválida aceita: %q", invalid)
		}
	}
}

// ------------------------------------------------------------------ prune

// A simulação precisa ser verdadeira: nenhuma remoção pode acontecer.
func TestPruneDryRunTouchesNothing(t *testing.T) {
	agent, mutations := inventoryAgent(t, map[string]any{
		"/system/df": map[string]any{
			"LayersSize": 1000,
			"Images": []map[string]any{
				{"Id": "sha256:orfa", "RepoTags": []string{}, "Size": 500, "Containers": 0},
				{"Id": "sha256:usada", "RepoTags": []string{"nginx:alpine"}, "Size": 300, "Containers": 1},
			},
			"Containers": []map[string]any{
				{"Id": "parado", "Names": []string{"/parado"}, "State": "exited", "Labels": map[string]string{}},
				{"Id": "rodando", "Names": []string{"/rodando"}, "State": "running", "Labels": map[string]string{}},
			},
			"Volumes": []map[string]any{
				{"Name": "orfao", "UsageData": map[string]any{"Size": 200, "RefCount": 0}},
				{"Name": "pulso_data", "UsageData": map[string]any{"Size": 50, "RefCount": 0}},
			},
			"BuildCache": []map[string]any{
				{"ID": "c1", "Size": 100, "InUse": false, "Shared": false},
				{"ID": "c2", "Size": 400, "InUse": true, "Shared": false},
			},
		},
		"/networks": []map[string]any{{"Id": "rede-livre", "Name": "livre"}},
	})

	result, err := agent.Prune(context.Background(), PruneResources(), true)
	if err != nil {
		t.Fatal(err)
	}
	if !result.DryRun {
		t.Error("o resultado não se identificou como simulação")
	}
	if len(*mutations) != 0 {
		t.Fatalf("a simulação alterou o Engine: %v", *mutations)
	}
	if len(result.Removed) != 0 {
		t.Errorf("a simulação reportou remoções: %v", result.Removed)
	}

	kinds := map[string]PruneCandidate{}
	for _, candidate := range result.Candidates {
		kinds[candidate.Resource] = candidate
	}
	// Imagem órfã, contêiner parado, volume solto, rede vazia e cache livre.
	for _, expected := range []string{ResourceImages, ResourceContainers, ResourceVolumes, ResourceNetworks, ResourceBuildCache} {
		if _, ok := kinds[expected]; !ok {
			t.Errorf("candidato ausente para %s: %+v", expected, result.Candidates)
		}
	}
	if kinds[ResourceImages].ID != "sha256:orfa" {
		t.Errorf("a imagem com tag entrou como candidata: %+v", kinds[ResourceImages])
	}
	if kinds[ResourceContainers].ID != "parado" {
		t.Errorf("contêiner em execução entrou como candidato: %+v", kinds[ResourceContainers])
	}
	if kinds[ResourceVolumes].ID != "orfao" {
		t.Errorf("volume protegido entrou como candidato: %+v", kinds[ResourceVolumes])
	}
	// Cache compartilhado e em uso não conta.
	if kinds[ResourceBuildCache].Size != 100 {
		t.Errorf("cache em uso foi contado: %+v", kinds[ResourceBuildCache])
	}
	// 500 (imagem) + 200 (volume) + 100 (cache) = 800
	if result.Reclaimed != 800 {
		t.Errorf("espaço recuperável = %d, esperado 800", result.Reclaimed)
	}

	// O volume do Pulso tem de aparecer como deliberadamente preservado.
	foundSkipped := false
	for _, skipped := range result.Skipped {
		if skipped.ID == "pulso_data" {
			foundSkipped = true
		}
	}
	if !foundSkipped {
		t.Errorf("o volume de dados do Pulso não foi listado como preservado: %+v", result.Skipped)
	}
}

func TestPruneRejectsUnknownResource(t *testing.T) {
	agent, _ := inventoryAgent(t, nil)
	if _, err := agent.Prune(context.Background(), []string{"tudo"}, true); err == nil {
		t.Fatal("tipo de recurso inventado foi aceito")
	}
	if _, err := agent.Prune(context.Background(), nil, true); err == nil {
		t.Fatal("limpeza sem recursos foi aceita")
	}
}

// Numa execução real, a limpeza nunca pode remover um recurso protegido.
func TestPruneNeverRemovesProtectedResources(t *testing.T) {
	var deleted []string
	agent := statsAgent(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deleted = append(deleted, r.URL.Path)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/system/df"):
			_, _ = w.Write([]byte(`{
              "Images": [{"Id":"sha256:painel","RepoTags":[],"Size":100,"Containers":0}],
              "Containers": [{"Id":"painel","Names":["/painel"],"State":"exited","Labels":{"io.pulso.infrastructure":"1"},"ImageID":"sha256:painel"}],
              "Volumes": [{"Name":"pulso_data","UsageData":{"Size":10,"RefCount":0}}],
              "BuildCache": []
            }`))
		case strings.Contains(r.URL.Path, "/networks"):
			_, _ = w.Write([]byte(`[{"Id":"n1","Name":"pulso-applications"}]`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	})

	agent.project = "pulso"
	result, err := agent.Prune(context.Background(), PruneResources(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 0 {
		t.Fatalf("recursos protegidos foram removidos: %v", deleted)
	}
	if len(result.Removed) != 0 {
		t.Errorf("remoções reportadas: %+v", result.Removed)
	}
	if len(result.Skipped) == 0 {
		t.Error("nada foi reportado como preservado")
	}
}

// Uma falha de remoção fica contida no próprio item.
func TestPruneReportsIndividualFailures(t *testing.T) {
	agent := statsAgent(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(409)
			_, _ = w.Write([]byte(`{"message":"em uso"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/system/df"):
			_, _ = w.Write([]byte(`{
              "Images": [{"Id":"sha256:orfa","RepoTags":[],"Size":100,"Containers":0}],
              "Containers": [], "Volumes": [], "BuildCache": []
            }`))
		case strings.Contains(r.URL.Path, "/networks"):
			_, _ = w.Write([]byte(`[]`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	})

	agent.project = "pulso"
	result, err := agent.Prune(context.Background(), []string{ResourceImages}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Failed) != 1 {
		t.Fatalf("falhas = %d, esperado 1: %+v", len(result.Failed), result)
	}
	if result.Failed[0].ID != "sha256:orfa" {
		t.Errorf("falha no item errado: %+v", result.Failed[0])
	}
	if result.Reclaimed != 0 {
		t.Errorf("espaço recuperado = %d após falha, esperado 0", result.Reclaimed)
	}
}

func TestDiskUsageSummarises(t *testing.T) {
	agent, _ := inventoryAgent(t, map[string]any{
		"/system/df": map[string]any{
			"LayersSize": 9000,
			"Images":     []map[string]any{{"Id": "sha256:a", "RepoTags": []string{}, "Size": 400, "Containers": 0}},
			"Containers": []map[string]any{{"Id": "c", "Names": []string{"/c"}, "State": "running", "Labels": map[string]string{}}},
			"Volumes":    []map[string]any{{"Name": "v", "UsageData": map[string]any{"Size": 600, "RefCount": 0}}},
			"BuildCache": []map[string]any{{"ID": "b", "Size": 300, "InUse": false, "Shared": false}},
		},
		"/networks": []map[string]any{},
	})
	usage, err := agent.DiskUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if usage.LayersSize != 9000 || usage.ImagesSize != 400 || usage.VolumesSize != 600 || usage.BuildCacheSize != 300 {
		t.Errorf("totais inesperados: %+v", usage)
	}
	// Recuperável = imagem órfã 400 + volume solto 600 + cache 300.
	if usage.Reclaimable.Total != 1300 {
		t.Errorf("recuperável = %d, esperado 1300", usage.Reclaimable.Total)
	}
	if usage.ContainersCount != 1 {
		t.Errorf("contêineres = %d", usage.ContainersCount)
	}
}

// A proteção por rótulo precisa valer mesmo quando o nome não ajuda: uma
// instalação renomeada, ou um volume com nome próprio, continua protegida.
func TestLabelProtectsRegardlessOfName(t *testing.T) {
	agent := &Agent{project: "projeto-renomeado"}

	volume := rawVolume{
		Name:   "nome-que-nao-casa",
		Labels: map[string]string{infrastructureLabel: "1"},
	}
	if reason := agent.volumeProtection(volume); reason == "" {
		t.Error("volume rotulado como infraestrutura ficou liberado")
	}

	network := rawNetwork{
		Name:   "rede-com-outro-nome",
		Labels: map[string]string{infrastructureLabel: "1"},
	}
	if reason := agent.networkProtection(network); reason == "" {
		t.Error("rede rotulada como infraestrutura ficou liberada")
	}

	// Sem o rótulo e sem casar o nome, segue liberado.
	if reason := agent.volumeProtection(rawVolume{Name: "nome-que-nao-casa"}); reason != "" {
		t.Errorf("volume alheio foi protegido: %s", reason)
	}
}

// ------------------------------------------- construtores das listagens

func TestContainerInventoryMapsAndFlags(t *testing.T) {
	agent, _ := inventoryAgent(t, map[string]any{
		"/containers/json": []map[string]any{
			{
				"Id": "painel", "Names": []string{"/pulso-panel"}, "Image": "pulso:latest",
				"ImageID": "sha256:p", "State": "running", "Status": "Up 2 minutes",
				"Created": 300, "Labels": map[string]string{infrastructureLabel: "1"},
			},
			{
				"Id": "projeto", "Names": []string{"/pulso-app-x"}, "Image": "nginx:alpine",
				"State": "running", "Created": 200,
				"Labels": map[string]string{applicationOwner: "pulso", applicationLabel: "0123456789abcdef01234567"},
				"Ports":  []map[string]any{{"IP": "0.0.0.0", "PrivatePort": 80, "PublicPort": 8080, "Type": "tcp"}},
			},
			{
				"Id": "alheio", "Names": []string{"/outra-stack-db"}, "Image": "postgres:17",
				"State": "exited", "Created": 100,
				"Labels": map[string]string{projectLabel: "outra-stack"},
			},
		},
	})

	containers, err := agent.containerInventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(containers) != 3 {
		t.Fatalf("contêineres = %d", len(containers))
	}
	// Ordenado por criação, mais recente primeiro.
	if containers[0].ID != "painel" || containers[2].ID != "alheio" {
		t.Errorf("ordenação inesperada: %s, %s, %s", containers[0].ID, containers[1].ID, containers[2].ID)
	}

	byID := map[string]ContainerSummary{}
	for _, container := range containers {
		byID[container.ID] = container
	}
	if byID["painel"].Ownership != OwnershipInfrastructure || !byID["painel"].Protected {
		t.Errorf("painel: %+v", byID["painel"])
	}
	// Uma aplicação é marcada como protegida porque a remoção é recusada.
	if byID["projeto"].Ownership != OwnershipApplication || !byID["projeto"].Protected {
		t.Errorf("projeto: %+v", byID["projeto"])
	}
	if byID["projeto"].ApplicationID != "0123456789abcdef01234567" {
		t.Errorf("id de aplicação = %q", byID["projeto"].ApplicationID)
	}
	if len(byID["projeto"].Ports) != 1 || byID["projeto"].Ports[0].PublicPort != 8080 {
		t.Errorf("portas = %+v", byID["projeto"].Ports)
	}
	if byID["alheio"].Ownership != OwnershipForeign || byID["alheio"].Protected {
		t.Errorf("alheio: %+v", byID["alheio"])
	}
	if byID["alheio"].ComposeProject != "outra-stack" {
		t.Errorf("projeto Compose = %q", byID["alheio"].ComposeProject)
	}
	// Nomes sem a barra inicial do Docker.
	if byID["painel"].Names[0] != "pulso-panel" {
		t.Errorf("nome = %q", byID["painel"].Names[0])
	}
}

func TestImageInventoryFlagsDanglingAndProtected(t *testing.T) {
	agent := statsAgent(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/images/json"):
			_, _ = w.Write([]byte(`[
              {"Id":"sha256:painel","RepoTags":["pulso:latest"],"Size":400,"Containers":1},
              {"Id":"sha256:orfa","RepoTags":["<none>:<none>"],"Size":300,"Containers":0},
              {"Id":"sha256:usada","RepoTags":["nginx:alpine"],"Size":200,"Containers":2},
              {"Id":"sha256:livre","RepoTags":["redis:7"],"Size":100,"Containers":0}
            ]`))
		case strings.Contains(r.URL.Path, "/containers/json"):
			_, _ = w.Write([]byte(`[{"Id":"p","ImageID":"sha256:painel","Image":"pulso:latest","Labels":{"io.pulso.infrastructure":"1"}}]`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	})
	agent.project = "pulso"

	images, err := agent.imageInventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]ImageSummary{}
	for _, image := range images {
		byID[image.ID] = image
	}
	// Ordenado por tamanho, maior primeiro.
	if images[0].ID != "sha256:painel" {
		t.Errorf("ordenação por tamanho falhou: %s", images[0].ID)
	}
	if !byID["sha256:painel"].Protected || !strings.Contains(byID["sha256:painel"].ProtectReason, "painel") {
		t.Errorf("imagem do painel: %+v", byID["sha256:painel"])
	}
	if !byID["sha256:orfa"].Dangling || byID["sha256:orfa"].Protected {
		t.Errorf("imagem órfã: %+v", byID["sha256:orfa"])
	}
	if !byID["sha256:usada"].InUse || !byID["sha256:usada"].Protected {
		t.Errorf("imagem em uso: %+v", byID["sha256:usada"])
	}
	// Imagem com tag e sem contêiner não é protegida, mas também não é órfã.
	if byID["sha256:livre"].Protected || byID["sha256:livre"].Dangling {
		t.Errorf("imagem livre: %+v", byID["sha256:livre"])
	}
}

func TestVolumeAndNetworkInventoryFlags(t *testing.T) {
	agent, _ := inventoryAgent(t, map[string]any{
		"/volumes": map[string]any{"Volumes": []map[string]any{
			{"Name": "pulso_data", "UsageData": map[string]any{"Size": 10, "RefCount": 0}},
			{"Name": "solto", "UsageData": map[string]any{"Size": 20, "RefCount": 0}},
			{"Name": "usado", "UsageData": map[string]any{"Size": 30, "RefCount": 1}},
		}},
		"/networks": []map[string]any{
			{"Id": "n1", "Name": "bridge"},
			{"Id": "n2", "Name": "pulso-applications"},
			{"Id": "n3", "Name": "livre"},
		},
	})

	volumes, err := agent.volumeInventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]VolumeSummary{}
	for _, volume := range volumes {
		byName[volume.Name] = volume
	}
	if !byName["pulso_data"].Protected || !byName["usado"].Protected {
		t.Errorf("volumes protegidos: %+v", volumes)
	}
	if byName["solto"].Protected || byName["solto"].Size != 20 {
		t.Errorf("volume solto: %+v", byName["solto"])
	}
	if !byName["usado"].InUse || byName["usado"].RefCount != 1 {
		t.Errorf("volume em uso: %+v", byName["usado"])
	}

	networks, err := agent.networkInventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byNetwork := map[string]NetworkSummary{}
	for _, network := range networks {
		byNetwork[network.Name] = network
	}
	if !byNetwork["bridge"].Predefined || !byNetwork["bridge"].Protected {
		t.Errorf("bridge: %+v", byNetwork["bridge"])
	}
	if !byNetwork["pulso-applications"].Protected {
		t.Errorf("rede dos projetos: %+v", byNetwork["pulso-applications"])
	}
	if byNetwork["livre"].Protected {
		t.Errorf("rede livre: %+v", byNetwork["livre"])
	}
}

func TestInventoryContainerDetails(t *testing.T) {
	agent, _ := inventoryAgent(t, map[string]any{
		"/json": map[string]any{
			"Id":   "abc",
			"Name": "/minha-app",
			"Config": map[string]any{
				"Image":  "nginx:alpine",
				"Labels": map[string]string{projectLabel: "outra"},
				"Env":    []any{"PORT=8080", "SENHA=secreta"},
			},
			"HostConfig":   map[string]any{"RestartPolicy": map[string]any{"Name": "unless-stopped"}},
			"RestartCount": 3,
			"State":        map[string]any{"Running": true, "ExitCode": 0, "Health": map[string]any{"Status": "healthy"}},
			"Mounts": []map[string]any{
				{"Type": "bind", "Source": "/srv/dados", "Destination": "/data", "RW": true},
			},
			"NetworkSettings": map[string]any{"Networks": map[string]any{"zeta": map[string]any{}, "alfa": map[string]any{}}},
		},
	})

	details, err := agent.inventoryContainerDetails(context.Background(), "abc")
	if err != nil {
		t.Fatal(err)
	}
	if details.Names[0] != "minha-app" {
		t.Errorf("nome = %v", details.Names)
	}
	if details.Image != "nginx:alpine" || details.Ownership != OwnershipForeign {
		t.Errorf("imagem/propriedade: %+v", details)
	}
	if len(details.Env) != 2 {
		t.Errorf("ambiente = %v", details.Env)
	}
	if details.RestartPolicy != "unless-stopped" || details.RestartCount != 3 {
		t.Errorf("política de reinício: %+v", details)
	}
	if details.Health != "healthy" || !details.Running {
		t.Errorf("saúde/estado: %+v", details)
	}
	if len(details.Mounts) != 1 || details.Mounts[0].Source != "/srv/dados" {
		t.Errorf("montagens = %+v", details.Mounts)
	}
	// Redes em ordem estável.
	if len(details.Networks) != 2 || details.Networks[0] != "alfa" {
		t.Errorf("redes = %v", details.Networks)
	}
	if details.State != "running" {
		t.Errorf("estado = %q", details.State)
	}
}

func TestRemoveInventoryResourcesRefuseProtected(t *testing.T) {
	var deleted []string
	agent := statsAgent(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deleted = append(deleted, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/images/json"):
			_, _ = w.Write([]byte(`[{"Id":"sha256:usada","RepoTags":["nginx:alpine"],"Size":1,"Containers":1}]`))
		case strings.Contains(r.URL.Path, "/containers/json"):
			_, _ = w.Write([]byte(`[]`))
		case strings.Contains(r.URL.Path, "/volumes"):
			_, _ = w.Write([]byte(`{"Volumes":[{"Name":"pulso_data","UsageData":{"Size":1,"RefCount":0}}]}`))
		case strings.Contains(r.URL.Path, "/networks"):
			_, _ = w.Write([]byte(`[{"Id":"n1","Name":"pulso-applications"}]`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	})
	agent.project = "pulso"
	ctx := context.Background()

	if err := agent.removeInventoryImage(ctx, "sha256:usada"); err == nil {
		t.Error("imagem em uso foi removida")
	}
	if err := agent.removeInventoryVolume(ctx, "pulso_data"); err == nil {
		t.Error("volume do Pulso foi removido")
	}
	if err := agent.removeInventoryNetwork(ctx, "n1"); err == nil {
		t.Error("rede dos projetos foi removida")
	}
	if len(deleted) != 0 {
		t.Fatalf("o Engine recebeu remoções de recursos protegidos: %v", deleted)
	}

	// Recursos inexistentes dão erro claro, sem chamar o Engine.
	if err := agent.removeInventoryImage(ctx, "sha256:naoexiste"); err == nil {
		t.Error("imagem inexistente aceita")
	}
	if err := agent.removeInventoryVolume(ctx, "naoexiste"); err == nil {
		t.Error("volume inexistente aceito")
	}
	if err := agent.removeInventoryNetwork(ctx, "naoexiste"); err == nil {
		t.Error("rede inexistente aceita")
	}
}

func TestRemoveInventoryAllowsUnprotectedResources(t *testing.T) {
	var deleted []string
	agent := statsAgent(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deleted = append(deleted, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/images/json"):
			_, _ = w.Write([]byte(`[{"Id":"sha256:orfa","RepoTags":[],"Size":1,"Containers":0}]`))
		case strings.Contains(r.URL.Path, "/containers/json"):
			_, _ = w.Write([]byte(`[]`))
		case strings.Contains(r.URL.Path, "/volumes"):
			_, _ = w.Write([]byte(`{"Volumes":[{"Name":"solto","UsageData":{"Size":1,"RefCount":0}}]}`))
		case strings.Contains(r.URL.Path, "/networks"):
			_, _ = w.Write([]byte(`[{"Id":"n9","Name":"livre"}]`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	})
	agent.project = "pulso"
	ctx := context.Background()

	if err := agent.removeInventoryImage(ctx, "sha256:orfa"); err != nil {
		t.Errorf("imagem órfã recusada: %v", err)
	}
	if err := agent.removeInventoryVolume(ctx, "solto"); err != nil {
		t.Errorf("volume solto recusado: %v", err)
	}
	if err := agent.removeInventoryNetwork(ctx, "n9"); err != nil {
		t.Errorf("rede livre recusada: %v", err)
	}
	if len(deleted) != 3 {
		t.Errorf("remoções enviadas = %v, esperado 3", deleted)
	}
}

func TestInventoryContainerActionOnForeignContainer(t *testing.T) {
	var posted []string
	agent := statsAgent(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost || r.Method == http.MethodDelete {
			posted = append(posted, r.Method+" "+r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Id":     "alheio",
			"Config": map[string]any{"Labels": map[string]string{}},
			"State":  map[string]any{"Running": true},
		})
	})
	agent.project = "pulso"
	ctx := context.Background()

	for _, action := range []string{"start", "stop", "restart", "remove"} {
		if err := agent.inventoryContainerAction(ctx, "alheio", action); err != nil {
			t.Errorf("ação %q recusada em contêiner alheio: %v", action, err)
		}
	}
	if len(posted) != 4 {
		t.Errorf("ações enviadas ao Engine = %v", posted)
	}
}

func TestInventoryContainerLogsBoundsTail(t *testing.T) {
	var query string
	agent := statsAgent(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/logs") {
			query = r.URL.RawQuery
			_, _ = w.Write([]byte("linha de log"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"Id": "abc", "Config": map[string]any{}})
	})

	// Um pedido absurdo é limitado ao padrão, para não puxar o log inteiro.
	if _, err := agent.inventoryContainerLogs(context.Background(), "abc", 999999); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(query, "tail=200") {
		t.Errorf("tail não foi limitado: %q", query)
	}
	if _, err := agent.inventoryContainerLogs(context.Background(), "abc", 50); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(query, "tail=50") {
		t.Errorf("tail informado não foi respeitado: %q", query)
	}
}
