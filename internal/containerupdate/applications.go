package containerupdate

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

const applicationOwner = "io.pulso.application.owner"
const applicationLabel = "io.pulso.application.id"

type ApplicationConfig struct {
	Name     string             `json:"name"`
	Image    string             `json:"image"`
	Command  []string           `json:"command,omitempty"`
	Env      map[string]string  `json:"env,omitempty"`
	Port     int                `json:"port,omitempty"`
	HostPort int                `json:"host_port,omitempty"`
	MemoryMB int64              `json:"memory_mb"`
	CPUs     float64            `json:"cpus"`
	DataPath string             `json:"data_path,omitempty"`
	Pull     bool               `json:"pull,omitempty"`
	Source   *ApplicationSource `json:"source,omitempty"`
}

type ApplicationSource struct {
	Kind    string `json:"kind"`
	Path    string `json:"path,omitempty"`
	Version string `json:"version,omitempty"`
	Archive string `json:"archive,omitempty"`
	Rebuild bool   `json:"rebuild,omitempty"`
}

type ContainerApplication struct {
	ID          string            `json:"id"`
	Config      ApplicationConfig `json:"config"`
	ContainerID string            `json:"container_id,omitempty"`
	CandidateID string            `json:"candidate_id,omitempty"`
	OldRunning  bool              `json:"old_running,omitempty"`
	Phase       string            `json:"phase"`
	Error       string            `json:"error,omitempty"`
	BuildLog    string            `json:"build_log,omitempty"`
	State       string            `json:"state"`
	ExitCode    int               `json:"exit_code"`
	Ports       []string          `json:"ports"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

// ValidateApplication checks the form fields and supplies default runtime limits.
func ValidateApplication(cfg *ApplicationConfig) error {
	if !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,79}$`).MatchString(cfg.Name) {
		return fmt.Errorf("nome inválido: use até 80 letras, números, pontos, hífens ou sublinhados")
	}
	if cfg.Source == nil && (cfg.Image == "" || len(cfg.Image) > 255 || strings.ContainsAny(cfg.Image, " \t\n\r\x00") || strings.HasPrefix(cfg.Image, "-")) {
		return fmt.Errorf("informe uma imagem Docker válida, como nginx:alpine")
	}
	if cfg.Source != nil && cfg.Source.Kind != "dockerfile" && cfg.Source.Kind != "node" && cfg.Source.Kind != "python" {
		return fmt.Errorf("tipo de projeto inválido")
	}
	if cfg.Source != nil && cfg.Source.Version == "" {
		switch cfg.Source.Kind {
		case "node":
			cfg.Source.Version = "24"
		case "python":
			cfg.Source.Version = "3.11"
		}
	}
	if cfg.Source != nil && cfg.Source.Kind == "dockerfile" {
		cfg.Source.Version = ""
	}
	if cfg.MemoryMB == 0 {
		cfg.MemoryMB = 512
	}
	if cfg.CPUs == 0 {
		cfg.CPUs = 1
	}
	if cfg.MemoryMB < 32 || cfg.MemoryMB > 1048576 || cfg.CPUs < 0.1 || cfg.CPUs > 128 {
		return fmt.Errorf("memória deve estar entre 32 e 1048576 MiB; CPU entre 0,1 e 128")
	}
	if cfg.Port < 0 || cfg.Port > 65535 || cfg.HostPort < 0 || cfg.HostPort > 65535 || (cfg.Port == 0 && cfg.HostPort != 0) {
		return fmt.Errorf("portas inválidas: use de 1 a 65535, ou deixe a porta do servidor vazia para seleção automática")
	}
	if cfg.DataPath != "" && (!filepath.IsAbs(cfg.DataPath) || filepath.Clean(cfg.DataPath) == "/" || strings.ContainsAny(cfg.DataPath, ":\x00\n")) {
		return fmt.Errorf("a pasta persistente deve ser absoluta e diferente de /")
	}
	if len(cfg.Env) > 256 || len(cfg.Command) > 128 {
		return fmt.Errorf("limite de variáveis ou argumentos excedido")
	}
	for name, value := range cfg.Env {
		if !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(name) || strings.ContainsRune(value, 0) {
			return fmt.Errorf("variável de ambiente inválida: %s", name)
		}
	}
	for _, arg := range cfg.Command {
		if strings.ContainsRune(arg, 0) {
			return fmt.Errorf("argumento inválido")
		}
	}
	return nil
}

func validAppID(id string) bool { return regexp.MustCompile(`^[0-9a-f]{24}$`).MatchString(id) }
func randomID() (string, error) {
	data := make([]byte, 12)
	_, err := rand.Read(data)
	return hex.EncodeToString(data), err
}
func logApplicationError(err error) { log.Error().Err(err).Msg("persist container application") }
func (a *Agent) appDir() string     { return filepath.Join(a.cfg.Directory, "applications") }
func (a *Agent) loadApplication(id string) (*ContainerApplication, error) {
	if !validAppID(id) {
		return nil, fmt.Errorf("identificador de aplicação inválido")
	}
	data, err := os.ReadFile(filepath.Join(a.appDir(), id+".json"))
	if err != nil {
		return nil, fmt.Errorf("aplicação não encontrada: %w", err)
	}
	var app ContainerApplication
	if err = json.Unmarshal(data, &app); err != nil {
		return nil, err
	}
	if app.ID != id {
		return nil, fmt.Errorf("registro de aplicação inconsistente")
	}
	return &app, nil
}
func (a *Agent) saveApplication(app *ContainerApplication) error {
	if !validAppID(app.ID) {
		return fmt.Errorf("identificador de aplicação inválido")
	}
	app.UpdatedAt = time.Now().UTC()
	data, err := json.Marshal(app)
	if err != nil {
		return err
	}
	return writeStateFile(a.appDir(), app.ID+".json", data)
}
func (a *Agent) ownedApplication(ctx context.Context, appID, containerID string) (*container, error) {
	c, err := a.engine.inspect(ctx, containerID)
	if err != nil {
		return nil, err
	}
	labels := stringMap(c.Config["Labels"])
	if labels[applicationOwner] != a.project || labels[applicationLabel] != appID {
		return nil, fmt.Errorf("o contêiner não pertence a esta aplicação do Pulso")
	}
	return c, nil
}
func (a *Agent) applicationList(ctx context.Context) ([]ContainerApplication, error) {
	a.appsMu.Lock()
	defer a.appsMu.Unlock()
	entries, err := os.ReadDir(a.appDir())
	if err != nil {
		return nil, err
	}
	result := []ContainerApplication{}
	for _, entry := range entries {
		id := strings.TrimSuffix(entry.Name(), ".json")
		if !validAppID(id) || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		app, err := a.loadApplication(id)
		if err != nil {
			return nil, err
		}
		app.State = "not_created"
		app.Config.Env = nil
		app.Config.Command = nil
		app.BuildLog = ""
		app.Ports = []string{}
		if app.ContainerID != "" {
			c, err := a.ownedApplication(ctx, app.ID, app.ContainerID)
			if err != nil {
				app.State = "unavailable"
			} else {
				app.State = "stopped"
				if c.State.Running {
					app.State = "running"
				}
				if c.State.Restarting {
					app.State = "restarting"
				}
				app.ExitCode = c.State.ExitCode
				if c.State.Running && !c.State.Restarting && c.State.Health != nil && c.State.Health.Status != "healthy" {
					app.State = c.State.Health.Status
				}
				for _, bindings := range c.NetworkSettings.Ports {
					for _, binding := range bindings {
						app.Ports = append(app.Ports, binding["HostPort"])
					}
				}
				sort.Strings(app.Ports)
			}
		}
		result = append(result, *app)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Config.Name < result[j].Config.Name })
	return result, nil
}

func (a *Agent) deployApplication(id string, cfg ApplicationConfig) (*ContainerApplication, error) {
	if err := ValidateApplication(&cfg); err != nil {
		return nil, err
	}
	a.appsMu.Lock()
	defer a.appsMu.Unlock()
	if a.appsBusy {
		return nil, fmt.Errorf("há uma implantação em andamento; aguarde sua conclusão")
	}
	var app *ContainerApplication
	var err error
	entries, err := os.ReadDir(a.appDir())
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		otherID := strings.TrimSuffix(entry.Name(), ".json")
		if !strings.HasSuffix(entry.Name(), ".json") || !validAppID(otherID) || otherID == id {
			continue
		}
		other, e := a.loadApplication(otherID)
		if e != nil {
			return nil, e
		}
		if strings.EqualFold(other.Config.Name, cfg.Name) {
			return nil, fmt.Errorf("já existe um projeto com esse nome")
		}
	}
	if id == "" {
		id, err = randomID()
		if err != nil {
			return nil, err
		}
		app = &ContainerApplication{ID: id, Config: cfg}
	} else {
		app, err = a.loadApplication(id)
		if err != nil {
			return nil, err
		}
	}
	app.OldRunning = false
	probeContext, cancelProbe := context.WithTimeout(a.ctx, 5*time.Second)
	defer cancelProbe()
	if app.ContainerID != "" {
		old, err := a.ownedApplication(probeContext, app.ID, app.ContainerID)
		if errors.Is(err, errDockerNotFound) {
			app.ContainerID = ""
		} else if err != nil {
			return nil, err
		} else {
			app.OldRunning = old.State.Running
		}
	}
	if cfg.Source != nil && !cfg.Source.Rebuild && cfg.Image != "" {
		if _, err := a.engine.image(probeContext, cfg.Image); errors.Is(err, errDockerNotFound) {
			return nil, fmt.Errorf("a imagem do projeto não está disponível; marque Reconstruir imagem e selecione a pasta do código")
		} else if err != nil {
			return nil, err
		}
	}
	if cfg.Source != nil && (cfg.Source.Rebuild || cfg.Image == "") {
		if err = a.storeApplicationSource(id, cfg.Source); err != nil {
			return nil, err
		}
		if app.ContainerID == "" {
			app.Config.Source = cfg.Source
		}
	}
	app.Phase, app.Error, app.BuildLog = "deploying", "", ""
	if err = a.saveApplication(app); err != nil {
		return nil, err
	}
	a.appsBusy = true
	accepted := *app
	go func() {
		ctx, cancel := context.WithTimeout(a.ctx, 20*time.Minute)
		defer cancel()
		err := a.runApplicationDeploy(ctx, app, cfg)
		a.appsMu.Lock()
		defer a.appsMu.Unlock()
		if err != nil {
			app.Phase, app.Error = "failed", err.Error()
		} else {
			app.Phase, app.Error = "ready", ""
		}
		a.appsBusy = false
		if saveErr := a.saveApplication(app); saveErr != nil {
			logApplicationError(saveErr)
		}
	}()
	return &accepted, nil
}

func (a *Agent) runApplicationDeploy(ctx context.Context, app *ContainerApplication, cfg ApplicationConfig) (resultErr error) {
	variables := make(map[string]string, len(cfg.Env))
	for key, value := range cfg.Env {
		variables[key] = value
	}
	cfg.Env = variables
	if cfg.Source != nil {
		source := *cfg.Source
		cfg.Source = &source
	}
	defer func() {
		if resultErr != nil && app.ContainerID == "" && cfg.Source != nil && strings.HasPrefix(cfg.Image, a.project+"-application:"+app.ID+"-") {
			a.appsMu.Lock()
			app.Config = cfg
			a.appsMu.Unlock()
		}
	}()
	imageRef := cfg.Image
	if cfg.Source != nil && (cfg.Source.Rebuild || cfg.Image == "") {
		var err error
		imageRef, err = a.buildApplication(ctx, app, cfg.Source.Kind)
		if err != nil {
			return err
		}
		cfg.Image = imageRef
		cfg.Source.Rebuild = false
	}
	if cfg.Source != nil {
		cfg.Pull = false
	}
	image, err := a.engine.image(ctx, imageRef)
	if err != nil && cfg.Source != nil {
		return fmt.Errorf("a imagem do projeto não está disponível; marque Reconstruir imagem e selecione a pasta do código")
	}
	if err != nil || cfg.Pull {
		image, err = a.engine.pull(ctx, imageRef)
	}
	if err != nil {
		return err
	}
	environment := []string{}
	runtimeEnvironment := make(map[string]string, len(cfg.Env)+1)
	for key, value := range cfg.Env {
		runtimeEnvironment[key] = value
	}
	if cfg.Source != nil && cfg.Port > 0 {
		if _, exists := runtimeEnvironment["PORT"]; !exists {
			runtimeEnvironment["PORT"] = fmt.Sprint(cfg.Port)
		}
	}
	for key, value := range runtimeEnvironment {
		environment = append(environment, key+"="+value)
	}
	sort.Strings(environment)
	labels := map[string]string{applicationOwner: a.project, applicationLabel: app.ID}
	// Images built by Compose can carry its project/service labels. Independent
	// applications must never be mistaken for the portal or its companion.
	for key := range image.Config.Labels {
		if strings.HasPrefix(key, "com.docker.compose.") {
			labels[key] = ""
		}
	}
	host := map[string]any{"RestartPolicy": map[string]string{"Name": "unless-stopped"}, "Memory": cfg.MemoryMB << 20, "MemorySwap": cfg.MemoryMB << 20, "NanoCpus": int64(cfg.CPUs * 1e9), "PidsLimit": 256, "Init": true, "SecurityOpt": []string{"no-new-privileges:true"}, "LogConfig": map[string]any{"Type": "json-file", "Config": map[string]string{"max-size": "10m", "max-file": "3"}}}
	body := map[string]any{"Image": image.ID, "Env": environment, "Labels": labels, "HostConfig": host, "StopTimeout": 20}
	network := a.project + "-applications"
	var currentNetwork struct{ Labels map[string]string }
	if err = a.engine.request(ctx, "GET", "/networks/"+url.PathEscape(network), nil, &currentNetwork); err != nil {
		if err = a.engine.request(ctx, "POST", "/networks/create", map[string]any{"Name": network, "Driver": "bridge", "Labels": map[string]string{applicationOwner: a.project}}, nil); err != nil {
			return err
		}
	} else if currentNetwork.Labels[applicationOwner] != a.project {
		return fmt.Errorf("já existe uma rede com esse nome que não pertence a esta instalação")
	}
	host["NetworkMode"] = network
	body["NetworkingConfig"] = map[string]any{"EndpointsConfig": map[string]any{network: map[string]any{"Aliases": []string{cfg.Name}}}}
	if len(cfg.Command) > 0 {
		body["Cmd"] = cfg.Command
	}
	if cfg.Port > 0 {
		port := fmt.Sprintf("%d/tcp", cfg.Port)
		body["ExposedPorts"] = map[string]any{port: map[string]any{}}
		host["PortBindings"] = map[string]any{port: []map[string]string{{"HostIp": "0.0.0.0", "HostPort": fmt.Sprint(cfg.HostPort)}}}
	}
	if cfg.DataPath != "" {
		volume := a.project + "-app-" + app.ID + "-data"
		var storedVolume struct{ Labels map[string]string }
		if err = a.engine.request(ctx, "POST", "/volumes/create", map[string]any{"Name": volume, "Labels": labels}, &storedVolume); err != nil {
			return err
		}
		if storedVolume.Labels[applicationOwner] != a.project || storedVolume.Labels[applicationLabel] != app.ID {
			return fmt.Errorf("o volume existente não pertence a este projeto")
		}
		host["Mounts"] = []map[string]any{{"Type": "volume", "Source": volume, "Target": filepath.Clean(cfg.DataPath)}}
	}
	newID, err := a.engine.create(ctx, a.project+"-app-"+app.ID+"-"+fmt.Sprint(time.Now().UnixNano()), body)
	if err != nil {
		return err
	}
	oldID := app.ContainerID
	a.appsMu.Lock()
	app.CandidateID = newID
	app.OldRunning = false
	if oldID != "" {
		if old, e := a.ownedApplication(ctx, app.ID, oldID); e == nil {
			app.OldRunning = old.State.Running
		} else {
			err = e
		}
	}
	if err == nil {
		err = a.saveApplication(app)
	}
	a.appsMu.Unlock()
	if err != nil {
		_ = a.engine.remove(ctx, newID)
		a.appsMu.Lock()
		app.CandidateID = ""
		a.appsMu.Unlock()
		return err
	}
	if oldID != "" && app.OldRunning {
		err = a.engine.stop(ctx, oldID)
	}
	if err == nil {
		err = a.engine.start(ctx, newID)
	}
	if err == nil {
		err = a.waitApplication(ctx, app.ID, newID)
	}
	if err == nil && cfg.Port > 0 && cfg.HostPort == 0 {
		if created, e := a.engine.inspect(ctx, newID); e == nil {
			bindings := created.NetworkSettings.Ports[fmt.Sprintf("%d/tcp", cfg.Port)]
			if len(bindings) > 0 {
				cfg.HostPort, _ = strconv.Atoi(bindings[0]["HostPort"])
			}
		}
	}
	if err != nil {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if output, e := a.readApplicationContainerLogs(cleanup, newID); e == nil {
			a.appsMu.Lock()
			app.BuildLog += output
			a.appsMu.Unlock()
		}
		removeErr := a.engine.remove(cleanup, newID)
		if oldID != "" && app.OldRunning {
			if rollbackErr := a.engine.start(cleanup, oldID); rollbackErr != nil {
				err = fmt.Errorf("%w; retorno à aplicação anterior: %v", err, rollbackErr)
			}
		}
		a.appsMu.Lock()
		if removeErr == nil {
			app.CandidateID = ""
		} else {
			err = fmt.Errorf("%w; remoção do candidato: %v", err, removeErr)
		}
		a.appsMu.Unlock()
		return err
	}
	a.appsMu.Lock()
	previousConfig := app.Config
	previousPhase := app.Phase
	cfg.Pull = false
	app.ContainerID, app.CandidateID, app.Config = newID, "", cfg
	app.Phase = "ready"
	err = a.saveApplication(app)
	if err != nil {
		app.ContainerID, app.CandidateID, app.Config = oldID, newID, previousConfig
		app.Phase = previousPhase
	}
	a.appsMu.Unlock()
	if err != nil {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		removeErr := a.engine.remove(cleanup, newID)
		if oldID != "" && app.OldRunning {
			if restoreErr := a.engine.start(cleanup, oldID); restoreErr != nil {
				err = fmt.Errorf("%w; recuperação: %v", err, restoreErr)
			}
		}
		if removeErr == nil {
			a.appsMu.Lock()
			app.CandidateID = ""
			a.appsMu.Unlock()
		}
		return fmt.Errorf("não foi possível registrar a atualização: %w", err)
	}
	if oldID != "" {
		if err = a.engine.remove(ctx, oldID); err != nil {
			return fmt.Errorf("aplicação atualizada; não foi possível remover o contêiner anterior: %w", err)
		}
	}
	if previousConfig.Source != nil && previousConfig.Image != cfg.Image && strings.HasPrefix(previousConfig.Image, a.project+"-application:"+app.ID+"-") {
		if err := a.engine.request(ctx, "DELETE", "/images/"+url.PathEscape(previousConfig.Image), nil, nil); err != nil {
			logApplicationError(err)
		}
	}
	return nil
}

func (a *Agent) waitApplication(ctx context.Context, appID, id string) error {
	deadline := time.NewTimer(90 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	running := 0
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("o contêiner não ficou saudável em 90 segundos; confira os logs e o healthcheck")
		case <-ticker.C:
			c, err := a.ownedApplication(ctx, appID, id)
			if err != nil {
				return err
			}
			if !c.State.Running {
				return fmt.Errorf("o contêiner encerrou com código %d; confira o comando e as dependências", c.State.ExitCode)
			}
			if c.State.Restarting || c.RestartCount > 0 {
				return fmt.Errorf("o contêiner entrou em um ciclo de reinícios; confira os logs")
			}
			if c.State.Health != nil {
				if c.State.Health.Status == "healthy" {
					return nil
				}
				if c.State.Health.Status == "unhealthy" {
					return fmt.Errorf("o healthcheck da aplicação falhou")
				}
				continue
			}
			running++
			if running >= 2 {
				return nil
			}
		}
	}
}

func (a *Agent) applicationAction(ctx context.Context, id, action string) error {
	a.appsMu.Lock()
	defer a.appsMu.Unlock()
	if a.appsBusy {
		return fmt.Errorf("aguarde a implantação em andamento")
	}
	app, err := a.loadApplication(id)
	if err != nil {
		return err
	}
	if app.ContainerID != "" {
		if _, err = a.ownedApplication(ctx, id, app.ContainerID); err != nil {
			if action == "delete" && errors.Is(err, errDockerNotFound) {
				app.ContainerID = ""
				err = nil
			} else {
				return err
			}
		}
		if app.ContainerID != "" {
			switch action {
			case "start":
				err = a.engine.start(ctx, app.ContainerID)
			case "stop":
				err = a.engine.stop(ctx, app.ContainerID)
			case "restart":
				err = a.engine.request(ctx, "POST", "/containers/"+url.PathEscape(app.ContainerID)+"/restart?t=20", nil, nil)
			case "delete":
				if existing, e := a.engine.inspect(ctx, app.ContainerID); e == nil && existing.State.Running {
					err = a.engine.stop(ctx, app.ContainerID)
				}
				if err == nil {
					err = a.engine.remove(ctx, app.ContainerID)
				}
			default:
				return fmt.Errorf("ação inválida")
			}
		}
		if err != nil {
			return err
		}
	} else if action != "delete" {
		return fmt.Errorf("a aplicação ainda não possui um contêiner; confira o erro da implantação")
	}
	if action == "delete" {
		if app.Config.Source != nil && strings.HasPrefix(app.Config.Image, a.project+"-application:"+app.ID+"-") {
			if err := a.engine.request(ctx, "DELETE", "/images/"+url.PathEscape(app.Config.Image), nil, nil); err != nil {
				logApplicationError(err)
			}
		}
		_ = os.Remove(filepath.Join(a.appDir(), id+".tar.gz"))
		return os.Remove(filepath.Join(a.appDir(), id+".json"))
	}
	return nil
}

func (a *Agent) applicationLogs(ctx context.Context, id string) (string, error) {
	a.appsMu.Lock()
	app, err := a.loadApplication(id)
	a.appsMu.Unlock()
	if err != nil {
		return "", err
	}
	if app.ContainerID == "" {
		return app.BuildLog, nil
	}
	if _, err = a.ownedApplication(ctx, id, app.ContainerID); err != nil {
		return "", err
	}
	text, err := a.readApplicationContainerLogs(ctx, app.ContainerID)
	return app.BuildLog + text, err
}
func (a *Agent) readApplicationContainerLogs(ctx context.Context, id string) (string, error) {
	response, err := a.engine.call(ctx, "GET", "/containers/"+url.PathEscape(id)+"/logs?stdout=true&stderr=true&tail=100&timestamps=true", nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(response.Body, 128<<10))
	if err != nil {
		return "", err
	}
	if len(data) > 0 && (len(data) < 8 || data[0] > 2 || data[1] != 0 || data[2] != 0 || data[3] != 0) {
		return string(data), nil
	}
	// Non-TTY Docker logs use an eight-byte stream header per frame.
	var text strings.Builder
	for len(data) >= 8 {
		size := int(data[4])<<24 | int(data[5])<<16 | int(data[6])<<8 | int(data[7])
		if size > len(data)-8 {
			text.Write(data[8:])
			break
		}
		text.Write(data[8 : 8+size])
		data = data[8+size:]
	}
	return text.String(), nil
}

func (a *Agent) registerApplications(mux *http.ServeMux) error {
	if err := os.MkdirAll(a.appDir(), 0o750); err != nil {
		return err
	}
	if os.Geteuid() == 0 {
		if err := os.Chown(a.appDir(), 10001, 10001); err != nil {
			return err
		}
	}
	// Recover interrupted deployments before accepting new jobs.
	entries, err := os.ReadDir(a.appDir())
	if err != nil {
		return err
	}
	for _, entry := range entries {
		id := strings.TrimSuffix(entry.Name(), ".json")
		if !strings.HasSuffix(entry.Name(), ".json") || !validAppID(id) {
			continue
		}
		app, err := a.loadApplication(id)
		if err != nil {
			return err
		}
		if app.Phase != "deploying" && app.CandidateID == "" {
			continue
		}
		if app.CandidateID != "" {
			if _, e := a.ownedApplication(a.ctx, id, app.CandidateID); e == nil {
				_ = a.engine.remove(a.ctx, app.CandidateID)
			}
		}
		app.CandidateID = ""
		app.Phase, app.Error = "failed", "A implantação foi interrompida. Confira a aplicação e tente novamente."
		if app.OldRunning && app.ContainerID != "" {
			if _, e := a.ownedApplication(a.ctx, id, app.ContainerID); e == nil {
				if e = a.engine.start(a.ctx, app.ContainerID); e != nil {
					app.Error += " Retorno à aplicação anterior: " + e.Error()
				}
			}
		}
		if err = a.saveApplication(app); err != nil {
			return err
		}
	}
	mux.HandleFunc("GET /applications", func(w http.ResponseWriter, r *http.Request) {
		list, err := a.applicationList(r.Context())
		if err != nil {
			respond(w, 500, map[string]string{"error": err.Error()})
			return
		}
		respond(w, 200, list)
	})
	mux.HandleFunc("GET /applications/{id}", func(w http.ResponseWriter, r *http.Request) {
		a.appsMu.Lock()
		app, err := a.loadApplication(r.PathValue("id"))
		a.appsMu.Unlock()
		if err != nil {
			respond(w, 404, map[string]string{"error": err.Error()})
			return
		}
		respond(w, 200, app)
	})
	deploy := func(w http.ResponseWriter, r *http.Request) {
		var cfg ApplicationConfig
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 96<<20)).Decode(&cfg); err != nil {
			respond(w, 400, map[string]string{"error": "configuração inválida ou contexto maior que 64 MiB"})
			return
		}
		app, err := a.deployApplication(r.PathValue("id"), cfg)
		if err != nil {
			respond(w, 409, map[string]string{"error": err.Error()})
			return
		}
		respond(w, 202, app)
	}
	mux.HandleFunc("POST /applications", deploy)
	mux.HandleFunc("PUT /applications/{id}", deploy)
	mux.HandleFunc("POST /applications/{id}/{action}", func(w http.ResponseWriter, r *http.Request) {
		err := a.applicationAction(r.Context(), r.PathValue("id"), r.PathValue("action"))
		if err != nil {
			respond(w, 409, map[string]string{"error": err.Error()})
			return
		}
		respond(w, 200, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /applications/{id}/logs", func(w http.ResponseWriter, r *http.Request) {
		text, err := a.applicationLogs(r.Context(), r.PathValue("id"))
		if err != nil {
			respond(w, 409, map[string]string{"error": err.Error()})
			return
		}
		respond(w, 200, map[string]string{"logs": text})
	})
	return nil
}
