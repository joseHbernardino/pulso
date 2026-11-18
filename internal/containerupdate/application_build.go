package containerupdate

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func (a *Agent) storeApplicationSource(id string, source *ApplicationSource) error {
	if source.Archive == "" {
		return fmt.Errorf("selecione novamente a pasta para enviar o código do projeto")
	}
	archive, err := base64.StdEncoding.DecodeString(source.Archive)
	if err != nil || len(archive) > 64<<20 {
		return fmt.Errorf("contexto do projeto inválido ou maior que 64 MiB")
	}
	if err = writeStateFile(a.appDir(), id+".tar.gz", archive); err != nil {
		return err
	}
	source.Archive = ""
	return nil
}

type applicationBuildOutput struct {
	agent *Agent
	app   *ContainerApplication
	last  time.Time
}

func (out *applicationBuildOutput) Write(data []byte) (int, error) {
	out.agent.appsMu.Lock()
	defer out.agent.appsMu.Unlock()
	text := out.app.BuildLog + string(data)
	if len(text) > 128<<10 {
		text = text[len(text)-(128<<10):]
	}
	out.app.BuildLog = text
	if time.Since(out.last) > time.Second {
		if err := out.agent.saveApplication(out.app); err != nil {
			logApplicationError(err)
		}
		out.last = time.Now()
	}
	return len(data), nil
}
func (a *Agent) buildApplication(ctx context.Context, app *ContainerApplication, kind string) (string, error) {
	file, err := os.Open(filepath.Join(a.appDir(), app.ID+".tar.gz"))
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	config, err := os.MkdirTemp("", "pulso-buildx-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(config) }()
	if path := os.Getenv("PULSO_REGISTRY_CONFIG"); path != "" {
		input, err := os.Open(path)
		if err != nil {
			return "", err
		}
		data, err := io.ReadAll(io.LimitReader(input, 1<<20))
		_ = input.Close()
		if err != nil {
			return "", err
		}
		if err = os.WriteFile(filepath.Join(config, "config.json"), data, 0o600); err != nil {
			return "", err
		}
	}
	image := a.project + "-application:" + app.ID + "-" + fmt.Sprint(time.Now().UnixNano())
	// Docker consumes the archive itself; no project path is extracted on the helper.
	dockerfile := "Dockerfile"
	if kind != "dockerfile" {
		dockerfile = ".pulso.Dockerfile"
	}
	cmd := exec.CommandContext(ctx, "docker", "--host", "unix://"+a.cfg.Socket, "--config", config, "buildx", "build", "--load", "--pull", "--progress", "plain", "--file", dockerfile, "--tag", image, "-")
	cmd.Stdin = file
	output := &applicationBuildOutput{agent: a, app: app}
	cmd.Stdout, cmd.Stderr = output, output
	if err = cmd.Run(); err != nil {
		return "", fmt.Errorf("compilação falhou; consulte o log da implantação: %w", err)
	}
	return strings.TrimSpace(image), nil
}
