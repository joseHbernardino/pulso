package containerupdate

import (
	"context"
	"errors"
	"fmt"
	"github.com/rs/zerolog/log"
	"net/url"
	"os"
	"strings"
	"time"
)

func replacementConfig(old *container, image, jobID string) map[string]any {
	body := cloneMap(old.Config)
	body["Image"] = image
	labels := stringMap(body["Labels"])
	labels["io.pulso.update.job"] = jobID
	labels["io.pulso.update.tracking"] = trackingRef(old)
	labels["com.docker.compose.image"] = image
	body["Labels"] = labels
	if host, _ := body["Hostname"].(string); host == old.ID || (len(old.ID) >= 12 && host == old.ID[:12]) {
		body["Hostname"] = ""
	}
	hostConfig := cloneMap(old.HostConfig)
	// Freeze dynamically allocated host ports too, so the browser returns to the same URL.
	if bindings, ok := hostConfig["PortBindings"].(map[string]any); ok {
		for port, mappings := range old.NetworkSettings.Ports {
			if _, published := bindings[port]; published && len(mappings) > 0 {
				bindings[port] = mappings
			}
		}
	}
	// Explicit binds already preserve named volumes; make anonymous volumes explicit as well.
	occupied := map[string]bool{}
	binds, _ := hostConfig["Binds"].([]any)
	for _, bind := range binds {
		parts := strings.Split(fmt.Sprint(bind), ":")
		if len(parts) > 1 {
			occupied[parts[1]] = true
		}
	}
	if mounts, ok := hostConfig["Mounts"].([]any); ok {
		for _, mount := range mounts {
			if m, ok := mount.(map[string]any); ok {
				occupied[fmt.Sprint(m["Target"])] = true
			}
		}
	}
	for _, mount := range old.Mounts {
		if mount.Type == "volume" && mount.Name != "" && !occupied[mount.Destination] {
			mode := "ro"
			if mount.RW {
				mode = "rw"
			}
			binds = append(binds, mount.Name+":"+mount.Destination+":"+mode)
		}
	}
	if len(binds) > 0 {
		hostConfig["Binds"] = binds
	}
	body["HostConfig"] = hostConfig
	endpoints := map[string]any{}
	for name, network := range old.NetworkSettings.Networks {
		cleaned := map[string]any{}
		for _, key := range []string{"Aliases", "Links", "IPAMConfig", "DriverOpts", "GwPriority"} {
			if value, ok := network[key]; ok {
				cleaned[key] = value
			}
		}
		endpoints[name] = cleaned
	}
	if len(endpoints) > 0 {
		body["NetworkingConfig"] = map[string]any{"EndpointsConfig": endpoints}
	}
	return body
}
func (d *Engine) tag(ctx context.Context, image, ref string) error {
	at := strings.LastIndex(ref, ":")
	if at < 0 {
		return fmt.Errorf("imagem sem tag: %s", ref)
	}
	return d.request(ctx, "POST", "/images/"+image+"/tag?repo="+url.QueryEscape(ref[:at])+"&tag="+url.QueryEscape(ref[at+1:]), nil, nil)
}
func (d *Engine) restartPolicy(ctx context.Context, id string, policy any) error {
	return d.request(ctx, "POST", "/containers/"+id+"/update", map[string]any{"RestartPolicy": policy}, nil)
}
func (d *Engine) healthy(ctx context.Context, id string) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		c, err := d.inspect(ctx, id)
		if err != nil {
			return err
		}
		if !c.State.Running {
			return d.failure(ctx, id, fmt.Sprintf("novo contêiner encerrou com código %d", c.State.ExitCode))
		}
		if c.State.Health == nil {
			return fmt.Errorf("a imagem precisa de um health check para validar a atualização")
		}
		if c.State.Health.Status == "healthy" {
			return nil
		}
		if c.State.Health.Status == "unhealthy" {
			return d.failure(ctx, id, "novo contêiner falhou no health check")
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("tempo esgotado aguardando o health check: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

// Worker runs outside both services, so it survives replacing the portal and the agent.
func Worker(ctx context.Context, engine *Engine, dir, id string) error {
	if err := engine.negotiate(ctx); err != nil {
		return err
	}
	j, err := loadJob(dir)
	if err != nil {
		return err
	}
	if j == nil || j.ID != id {
		return fmt.Errorf("atualização não encontrada")
	}
	defer func() {
		latest, _ := loadJob(dir)
		if os.Getenv("PULSO_CONTAINER") == "1" && latest != nil && !latest.Active() {
			finish, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			selfID, _ := os.Hostname()
			if self, err := engine.inspect(finish, selfID); err == nil && stringMap(self.Config["Labels"])["io.pulso.update.worker"] == id {
				if err = engine.restartPolicy(finish, selfID, map[string]any{"Name": "no"}); err != nil {
					log.Error().Err(err).Msg("disable completed update worker restart")
				}
			}
		}
	}()
	if !j.Active() {
		return nil
	}
	if len(j.Plans) != 2 {
		return fmt.Errorf("plano de atualização inválido")
	}
	// A retry after interruption restores the previous containers instead of guessing which steps ran.
	if j.Step != "queued" {
		return rollbackJob(engine, dir, j, fmt.Errorf("a execução anterior foi interrompida"))
	}
	for index := range j.Plans {
		p := &j.Plans[index]
		if index == 0 {
			j.Step, j.Message = "updating_portal", "Recriando o Pulso e recuperando as aplicações."
		} else {
			j.Step, j.Message = "updating_agent", "Atualizando o serviço auxiliar."
		}
		if err = saveJob(dir, j); err != nil {
			return rollbackJob(engine, dir, j, err)
		}
		if err = applyPlan(ctx, engine, dir, j, p); err != nil {
			return rollbackJob(engine, dir, j, err)
		}
		if index == 0 {
			candidate, inspectErr := engine.inspect(ctx, p.CandidateID)
			if inspectErr != nil {
				return rollbackJob(engine, dir, j, inspectErr)
			}
			check, cancel := context.WithTimeout(ctx, time.Minute)
			probeErr := engine.waitApplications(check, candidate, j.Applications)
			cancel()
			if probeErr != nil {
				return rollbackJob(engine, dir, j, probeErr)
			}
		}
	}

	// Commit success before cleanup: an interruption during cleanup must not trigger rollback
	// after one of the backups has already been removed.
	if err = finishJob(dir, j, "completed", "Pulso atualizado. As aplicações foram recuperadas."); err != nil {
		return err
	}
	for _, p := range j.Plans {
		if err = engine.remove(ctx, p.Old.ID); err != nil {
			return finishJob(dir, j, "completed", "Pulso atualizado. Um contêiner anterior ficou parado para limpeza posterior: "+err.Error())
		}
	}
	return nil
}
func applyPlan(ctx context.Context, d *Engine, dir string, j *job, p *plan) error {
	if err := d.tag(ctx, p.NewImage, p.TrackingRef); err != nil {
		return err
	}
	if err := d.restartPolicy(ctx, p.Old.ID, map[string]any{"Name": "no"}); err != nil {
		return err
	}
	if err := d.stop(ctx, p.Old.ID); err != nil {
		return err
	}
	name := strings.TrimPrefix(p.Old.Name, "/")
	if err := d.rename(ctx, p.Old.ID, name+"-pulso-backup-"+j.ID[:12]); err != nil {
		return err
	}
	body := replacementConfig(p.Old, p.NewImage, j.ID)
	if published, inspectErr := d.image(ctx, p.NewImage); inspectErr == nil {
		labels := stringMap(body["Labels"])
		for _, key := range []string{"org.opencontainers.image.version", "org.opencontainers.image.revision", "org.opencontainers.image.created"} {
			if value, ok := published.Config.Labels[key]; ok {
				labels[key] = value
			} else {
				delete(labels, key)
			}
		}
		body["Labels"] = labels
	}

	// Create by the verified immutable ID. Another project can retag latest concurrently.
	// The tracking reference is retained in a label for subsequent update checks.
	id, err := d.create(ctx, name, body)
	if err != nil {
		return err
	}
	p.CandidateID = id
	if err = saveJob(dir, j); err != nil {
		return err
	}
	if err = d.start(ctx, id); err != nil {
		return err
	}
	ready, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	return d.healthy(ready, id)
}
func rollbackJob(d *Engine, dir string, j *job, cause error) error {
	j.Step, j.Message = "rolling_back", "A nova imagem falhou. Restaurando a instalação anterior."
	_ = saveJob(dir, j)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	var failures []error
	for index := len(j.Plans) - 1; index >= 0; index-- {
		p := &j.Plans[index]
		candidate := p.CandidateID
		if candidate == "" {
			if c, err := d.inspect(ctx, strings.TrimPrefix(p.Old.Name, "/")); err == nil && stringMap(c.Config["Labels"])["io.pulso.update.job"] == j.ID {
				candidate = c.ID
			}
		}
		if candidate != "" {
			if _, err := d.inspect(ctx, candidate); err == nil {
				if err = d.remove(ctx, candidate); err != nil {
					failures = append(failures, err)
				}
			}
		}
		old, err := d.inspect(ctx, p.Old.ID)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		// Leave the local tracking tag on the downloaded release. Other projects can share it.
		// Rollback starts the saved container by its original immutable image ID.
		if index == 0 && (!old.State.Running || old.Name != p.Old.Name || candidate != "") {
			if err = d.restoreSnapshot(ctx, old, j.RecoverySnapshot); err != nil {
				failures = append(failures, err)
			}
		}
		if old.Name != p.Old.Name {
			if err = d.rename(ctx, old.ID, strings.TrimPrefix(p.Old.Name, "/")); err != nil {
				failures = append(failures, err)
			}
		}
		if err = d.restartPolicy(ctx, old.ID, p.Old.HostConfig["RestartPolicy"]); err != nil {
			failures = append(failures, err)
		}
		if p.Old.State.Running && !old.State.Running {
			if err = d.start(ctx, old.ID); err != nil {
				failures = append(failures, err)
			}
		}
		if p.Old.State.Running {
			ready, cancel := context.WithTimeout(ctx, 2*time.Minute)
			err = d.healthy(ready, old.ID)
			cancel()
			if err != nil {
				failures = append(failures, err)
			} else if index == 0 {
				check, cancel := context.WithTimeout(ctx, time.Minute)
				err = d.waitApplications(check, old, j.Applications)
				cancel()
				if err != nil {
					failures = append(failures, err)
				}
			}
		}
	}
	message := "Atualização cancelada; a versão anterior foi restaurada. Motivo: " + cause.Error()
	state := "rolled_back"
	if len(failures) > 0 {
		state = "failed"
		message = "A atualização falhou e a recuperação precisa de atenção: " + cause.Error() + "; " + errors.Join(failures...).Error()
	}
	if err := finishJob(dir, j, state, message); err != nil {
		return err
	}
	return fmt.Errorf("%s", message)
}
