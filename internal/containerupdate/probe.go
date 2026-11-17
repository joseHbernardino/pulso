package containerupdate

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

type Application struct {
	Name          string `json:"name"`
	State         string `json:"state"`
	Ready         bool   `json:"ready"`
	RestartPolicy string `json:"restart_policy"`
	KeepStopped   bool   `json:"keep_stopped"`
}

func (d *Engine) applications(ctx context.Context, c *container) ([]Application, error) {
	command := []string{"/usr/local/bin/pulso", "--data-dir", "/var/lib/pulso"}
	// Preserve explicit config/data-dir flags used by the deployed daemon.
	for _, field := range []string{"Entrypoint", "Cmd"} {
		if args, ok := c.Config[field].([]any); ok {
			for i := 0; i < len(args); i++ {
				arg := fmt.Sprint(args[i])
				if (arg == "--config" || arg == "--data-dir") && i+1 < len(args) {
					command = append(command, arg, fmt.Sprint(args[i+1]))
					i++
				} else if strings.HasPrefix(arg, "--config=") || strings.HasPrefix(arg, "--data-dir=") {
					command = append(command, arg)
				}
			}
		}
	}
	command = append(command, "container-updater", "probe")
	var created struct {
		ID string `json:"Id"`
	}
	if err := d.request(ctx, "POST", "/containers/"+c.ID+"/exec", map[string]any{"AttachStdout": true, "AttachStderr": true, "Cmd": command}, &created); err != nil {
		return nil, err
	}
	resp, err := d.call(ctx, "POST", "/exec/"+created.ID+"/start", map[string]any{"Detach": false, "Tty": false})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var stdout []byte
	for {
		header := make([]byte, 8)
		_, err = io.ReadFull(resp.Body, header)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		size := binary.BigEndian.Uint32(header[4:])
		if size > 1<<20 || len(stdout)+int(size) > 1<<20 {
			return nil, fmt.Errorf("resposta de processos muito grande")
		}
		data := make([]byte, int(size))
		if _, err = io.ReadFull(resp.Body, data); err != nil {
			return nil, err
		}
		if header[0] == 1 {
			stdout = append(stdout, data...)
		}
	}
	var result struct {
		ExitCode int
		Running  bool
	}
	if err = d.request(ctx, "GET", "/exec/"+created.ID+"/json", nil, &result); err != nil {
		return nil, err
	}
	if result.ExitCode != 0 || result.Running {
		return nil, fmt.Errorf("não foi possível consultar os processos pelo socket do daemon; confira a configuração de autenticação IPC")
	}
	var applications []Application
	if err = json.Unmarshal(stdout, &applications); err != nil {
		return nil, fmt.Errorf("consultar processos: %w", err)
	}
	return applications, nil
}
func (d *Engine) waitApplications(ctx context.Context, c *container, expected []Application) error {
	if len(expected) == 0 {
		return nil
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	good := 0
	var last error
	for {
		observed, err := d.applications(ctx, c)
		if err == nil {
			err = compareApplications(expected, observed)
			if err != nil {
				names := map[string]bool{}
				for _, app := range observed {
					names[app.Name] = true
				}
				for _, app := range expected {
					if !names[app.Name] {
						return err
					}
				}
			}
		}
		if err == nil {
			good++
			if good >= 2 {
				return nil
			}
		} else {
			good = 0
			last = err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("as aplicações não recuperaram o estado anterior: %v", last)
		case <-ticker.C:
		}
	}
}
func compareApplications(expected, observed []Application) error {
	byName := map[string]Application{}
	for _, app := range observed {
		byName[app.Name] = app
	}
	for _, before := range expected {
		current, exists := byName[before.Name]
		if !exists {
			return fmt.Errorf("aplicação %s não foi restaurada", before.Name)
		}
		if (before.State == "running" || before.State == "starting") && (current.State != "running" || !current.Ready) {
			return fmt.Errorf("aplicação %s: estado %s", before.Name, current.State)
		}
		if before.KeepStopped && current.State != "stopped" {
			return fmt.Errorf("aplicação %s deveria permanecer parada", before.Name)
		}
	}
	return nil
}
