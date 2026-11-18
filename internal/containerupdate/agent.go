package containerupdate

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"pulso.local/pulso/internal/updater"
)

var stableTag = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

type Config struct {
	Directory     string
	Socket        string
	SelfID        string
	Repository    string // Explicit image repository; requests cannot select arbitrary images.
	LookupRelease func(context.Context) (string, string, error)
	Pull          func(context.Context, string) (*imageInfo, error)
}

type UpdateInfo struct {
	Available      bool       `json:"available"`
	Reason         string     `json:"reason,omitempty"`
	CurrentVersion string     `json:"current_version,omitempty"`
	LatestVersion  string     `json:"latest_version,omitempty"`
	HasUpdate      bool       `json:"has_update"`
	ReleaseURL     string     `json:"release_url,omitempty"`
	CheckedAt      *time.Time `json:"checked_at,omitempty"`
	CheckError     string     `json:"check_error,omitempty"`
	Job            *Status    `json:"job,omitempty"`
}

type Agent struct {
	cfg        Config
	engine     *Engine
	self       *container
	project    string
	mu         sync.Mutex
	latest     string
	releaseURL string
	checkedAt  time.Time
	checkError string
	cancel     context.CancelFunc
	ctx        context.Context
	appsMu     sync.Mutex
	appsBusy   bool
}

func NewAgent(ctx context.Context, cfg Config) (*Agent, error) {
	if cfg.Directory == "" {
		cfg.Directory = DefaultDirectory
	}
	if cfg.Socket == "" {
		cfg.Socket = "/var/run/docker.sock"
	}
	if cfg.SelfID == "" {
		cfg.SelfID, _ = os.Hostname()
	}
	if cfg.Repository == "" {
		return nil, fmt.Errorf("configure PULSO_UPDATE_IMAGE com o repositório da imagem publicada")
	}
	if strings.ContainsAny(cfg.Repository, " @\n\t") || strings.Contains(cfg.Repository, "://") {
		return nil, fmt.Errorf("repositório de imagem inválido")
	}
	engine := NewEngine(cfg.Socket)
	if err := engine.negotiate(ctx); err != nil {
		return nil, err
	}
	self, err := engine.inspect(ctx, cfg.SelfID)
	if err != nil {
		return nil, err
	}
	labels := stringMap(self.Config["Labels"])
	if labels[projectLabel] == "" || labels[serviceLabel] != "pulso-updater" {
		return nil, fmt.Errorf("execute como serviço pulso-updater no mesmo projeto Compose")
	}
	if cfg.Pull == nil {
		cfg.Pull = engine.pull
	}
	if cfg.LookupRelease == nil {
		cfg.LookupRelease = func(ctx context.Context) (string, string, error) {
			r, err := updater.CheckForUpdate(ctx)
			if err != nil {
				return "", "", err
			}
			return "v" + strings.TrimPrefix(r.LatestVersion, "v"), r.ReleaseURL, nil
		}
	}
	agentCtx, cancel := context.WithCancel(ctx)
	return &Agent{cfg: cfg, engine: engine, self: self, project: labels[projectLabel], ctx: agentCtx, cancel: cancel}, nil
}
func (a *Agent) close() {
	a.cancel()
	if tr, ok := a.engine.client.Transport.(*http.Transport); ok {
		tr.CloseIdleConnections()
	}
}

func trackingTag(ref, repo string) (string, error) {
	prefix := repo + ":"
	if !strings.HasPrefix(ref, prefix) {
		return "", fmt.Errorf("a imagem precisa usar o repositório %s", repo)
	}
	tag := strings.TrimPrefix(ref, prefix)
	if tag != "latest" && tag != "latest-python311" {
		return "", fmt.Errorf("a atualização pelo painel exige a tag latest ou latest-python311; imagens locais e tags fixadas usam atualização pelo Compose")
	}
	if tag == "latest-python311" {
		return "-python311", nil
	}
	return "", nil
}
func (a *Agent) target(ctx context.Context) (*container, error) {
	return a.engine.find(ctx, a.project, "pulso")
}
func (a *Agent) info(ctx context.Context) UpdateInfo {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := UpdateInfo{LatestVersion: a.latest, ReleaseURL: a.releaseURL, CheckError: a.checkError}
	if !a.checkedAt.IsZero() {
		at := a.checkedAt
		out.CheckedAt = &at
	}
	if status, err := LatestStatus(a.cfg.Directory); err == nil {
		out.Job = status
	}
	app, err := a.target(ctx)
	if err != nil {
		out.Reason = err.Error()
		return out
	}
	ref := trackingRef(app)
	if _, err = trackingTag(ref, a.cfg.Repository); err != nil {
		out.Reason = err.Error()
		return out
	}
	if _, err = trackingTag(trackingRef(a.self), a.cfg.Repository); err != nil {
		out.Reason = "Imagem do serviço auxiliar: " + err.Error()
		return out
	}
	current, err := a.engine.image(ctx, app.Image)
	if err != nil {
		out.Reason = err.Error()
		return out
	}
	out.CurrentVersion = current.Config.Labels["org.opencontainers.image.version"]
	out.Available = true
	installed := "v" + strings.TrimPrefix(out.CurrentVersion, "v")
	out.HasUpdate = newer(a.latest, installed)
	return out
}
func (a *Agent) check(ctx context.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if time.Since(a.checkedAt) < time.Minute {
		return
	}
	a.checkedAt = time.Now()
	tag, releaseURL, err := a.cfg.LookupRelease(ctx)
	if err != nil {
		a.checkError = err.Error()
		return
	}
	if !stableTag.MatchString(tag) {
		a.checkError = "A publicação mais recente não é uma versão estável válida."
		return
	}
	a.latest, a.releaseURL, a.checkError = tag, releaseURL, ""
}
func (a *Agent) begin(ctx context.Context, expected string) (*Status, error) {
	info := a.info(ctx)
	a.mu.Lock()
	defer a.mu.Unlock()
	if existing, err := loadJob(a.cfg.Directory); err != nil {
		return nil, err
	} else if existing != nil {
		if existing.Active() {
			return nil, fmt.Errorf("já existe uma atualização em andamento")
		}
		if existing.WorkerID != "" {
			_ = a.engine.remove(ctx, existing.WorkerID)
		}
	}
	if !info.Available {
		return nil, fmt.Errorf("%s", info.Reason)
	}
	if !info.HasUpdate || expected != a.latest {
		return nil, fmt.Errorf("verifique novamente a versão disponível antes de atualizar")
	}
	app, err := a.target(ctx)
	if err != nil {
		return nil, err
	}
	if !app.State.Running {
		return nil, fmt.Errorf("o Pulso precisa estar em execução")
	}
	idBytes := make([]byte, 16)
	if _, err = rand.Read(idBytes); err != nil {
		return nil, err
	}
	j := &job{Status: Status{ID: hex.EncodeToString(idBytes), State: "running", Step: "downloading", Message: "Baixando a nova imagem. As aplicações continuam em execução.", FromVersion: info.CurrentVersion, ToVersion: a.latest, StartedAt: time.Now()}}
	if err = saveJob(a.cfg.Directory, j); err != nil {
		return nil, err
	}
	status := j.Status
	go a.prepare(j, app)
	return &status, nil
}
func (a *Agent) prepare(j *job, app *container) {
	ctx, cancel := context.WithTimeout(a.ctx, 15*time.Minute)
	defer cancel()
	for _, old := range []*container{app, a.self} {
		ref := trackingRef(old)
		variant, err := trackingTag(ref, a.cfg.Repository)
		if err != nil {
			a.finish(j, "failed", err.Error())
			return
		}
		image, err := a.cfg.Pull(ctx, a.cfg.Repository+":"+j.ToVersion+variant)
		if err != nil {
			a.finish(j, "failed", "Não foi possível baixar a imagem; a instalação atual foi mantida. "+err.Error())
			return
		}
		if image.Config.Labels["org.opencontainers.image.version"] != j.ToVersion {
			a.finish(j, "failed", "A versão da imagem publicada não corresponde à versão solicitada.")
			return
		}
		j.Plans = append(j.Plans, plan{Old: old, NewImage: image.ID, TrackingRef: ref})
	}
	probe, cancel := context.WithTimeout(ctx, 15*time.Second)
	applications, err := a.engine.applications(probe, app)
	cancel()
	if err != nil {
		a.finish(j, "failed", "Não foi possível registrar o estado das aplicações; a instalação atual foi mantida. "+err.Error())
		return
	}
	j.Applications = applications
	snapshot, err := a.engine.recoverySnapshot(ctx, app)
	if err != nil {
		a.finish(j, "failed", "Não foi possível preservar o estado das aplicações; a instalação atual foi mantida. "+err.Error())
		return
	}
	j.RecoverySnapshot = snapshot
	if encoded, _ := json.Marshal(j); len(encoded) > 7<<20 {
		j.Plans = nil
		j.RecoverySnapshot = nil
		a.finish(j, "failed", "A configuração excede o limite de 7 MiB para atualização pelo painel.")
		return
	}
	j.Step, j.Message = "queued", "Imagens verificadas. Preparando a troca dos contêineres."
	if err := saveJob(a.cfg.Directory, j); err != nil {
		a.finish(j, "failed", err.Error())
		return
	}
	if err := a.launchWorker(ctx, j); err != nil {
		a.finish(j, "failed", "Não foi possível iniciar a atualização; a instalação atual foi mantida. "+err.Error())
	}
}
func (a *Agent) launchWorker(ctx context.Context, j *job) error {
	body := cloneMap(a.self.Config)
	body["Image"] = j.Plans[1].NewImage
	if j.WorkerFallback {
		body["Image"] = j.Plans[1].Old.Image
	}
	body["Hostname"] = ""
	body["Entrypoint"] = []string{"/usr/bin/tini", "--", "/usr/local/bin/pulso"}
	body["Cmd"] = []string{"container-updater", "worker", "--job", j.ID}
	body["Healthcheck"] = map[string]any{"Test": []string{"NONE"}}
	labels := stringMap(body["Labels"])
	delete(labels, serviceLabel)
	labels["io.pulso.update.worker"] = j.ID
	body["Labels"] = labels
	host := cloneMap(a.self.HostConfig)
	host["RestartPolicy"] = map[string]any{"Name": "unless-stopped"}
	host["AutoRemove"] = false
	body["HostConfig"] = host
	id, err := a.engine.create(ctx, "pulso-update-"+j.ID, body)
	if err != nil {
		return err
	}
	j.WorkerID = id
	if err = saveJob(a.cfg.Directory, j); err != nil {
		return err
	}
	return a.engine.start(ctx, id)
}
func (a *Agent) recoverJob(ctx context.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()
	j, err := loadJob(a.cfg.Directory)
	if err != nil || j == nil {
		return
	}
	if !j.Active() {
		if err = saveStatus(a.cfg.Directory, &j.Status); err != nil {
			log.Error().Err(err).Msg("repair update progress")
		}
		return
	}
	if j.WorkerID != "" {
		worker, inspectErr := a.engine.inspect(ctx, j.WorkerID)
		if inspectErr == nil && worker.State.Running {
			return
		}
		if j.WorkerRecoveries > 0 {
			a.finish(j, "failed", "O processo de recuperação também foi interrompido. Confira os contêineres e os logs do serviço auxiliar.")
			return
		}
		if inspectErr == nil {
			_ = a.engine.remove(ctx, worker.ID)
		}
		j.WorkerID = ""
		j.WorkerFallback = true
		j.WorkerRecoveries++
		j.Step = "interrupted"
	}
	if len(j.Plans) == 2 && j.Step != "downloading" {
		if err = saveJob(a.cfg.Directory, j); err == nil {
			err = a.launchWorker(ctx, j)
		}
		if err != nil {
			a.finish(j, "failed", err.Error())
		}
		return
	}
	a.finish(j, "failed", "O serviço foi interrompido durante o download. A instalação atual foi mantida; tente novamente.")
}

// Serve exposes a small update protocol on a shared Unix socket, without a TCP port.
func (a *Agent) Serve(ctx context.Context) error {
	defer a.close()
	if err := os.MkdirAll(a.cfg.Directory, 0o750); err != nil {
		return err
	}
	if os.Geteuid() == 0 {
		if err := os.Chown(a.cfg.Directory, 10001, 10001); err != nil {
			return err
		}
	}
	socket := filepath.Join(a.cfg.Directory, "updater.sock")
	_ = os.Remove(socket)
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	defer func() { _ = listener.Close() }()
	if err = os.Chmod(socket, 0o660); err != nil {
		return err
	}
	if os.Geteuid() == 0 {
		if err = os.Chown(socket, 10001, 10001); err != nil {
			return err
		}
	}
	mux := http.NewServeMux()
	if os.Getenv("PULSO_APPLICATIONS_ENABLED") == "1" {
		if err := a.registerApplications(mux); err != nil {
			return err
		}
	}
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) { respond(w, 200, a.info(r.Context())) })
	mux.HandleFunc("POST /check", func(w http.ResponseWriter, r *http.Request) {
		a.check(r.Context())
		respond(w, 200, a.info(r.Context()))
	})
	mux.HandleFunc("POST /update", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Version string `json:"version"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&request); err != nil {
			respond(w, 400, map[string]string{"error": "versão inválida"})
			return
		}
		status, err := a.begin(r.Context(), request.Version)
		if err != nil {
			respond(w, 409, map[string]string{"error": err.Error()})
			return
		}
		respond(w, 202, status)
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 2 * time.Minute, WriteTimeout: 30 * time.Second}
	a.recoverJob(ctx)
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				status, err := LatestStatus(a.cfg.Directory)
				if err == nil && status != nil && status.Active() {
					j, readErr := loadJob(a.cfg.Directory)
					if readErr == nil && j != nil && j.WorkerID != "" {
						a.recoverJob(ctx)
					}
				}
			}
		}
	}()
	go func() {
		<-ctx.Done()
		stop, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(stop)
	}()
	err = server.Serve(listener)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}
func respond(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func newer(latest, current string) bool {
	if !stableTag.MatchString(latest) {
		return false
	}
	base := strings.SplitN(strings.SplitN(current, "+", 2)[0], "-", 2)[0]
	if !stableTag.MatchString(base) {
		return true
	}
	l, c := strings.Split(latest[1:], "."), strings.Split(base[1:], ".")
	for i := range l {
		if len(l[i]) != len(c[i]) {
			return len(l[i]) > len(c[i])
		}
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return strings.Contains(current, "-")
}

func trackingRef(c *container) string {
	if ref := stringMap(c.Config["Labels"])["io.pulso.update.tracking"]; ref != "" {
		return ref
	}
	ref, _ := c.Config["Image"].(string)
	return ref
}

func (a *Agent) finish(j *job, state, message string) {
	if err := finishJob(a.cfg.Directory, j, state, message); err != nil {
		log.Error().Err(err).Str("job", j.ID).Msg("persist update result")
	}
	if state == "failed" {
		log.Error().Str("job", j.ID).Str("reason", message).Msg("container update failed")
	}
}
