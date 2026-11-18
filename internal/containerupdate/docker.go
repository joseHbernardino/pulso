package containerupdate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Engine talks only to a local Unix socket; it is never exposed through the web API.
var backupNamePattern = regexp.MustCompile(`-pulso-backup-[0-9a-f]{12}$`)
var errDockerNotFound = errors.New("recurso Docker não encontrado")

type Engine struct {
	client *http.Client
	prefix string
}

type container struct {
	ID              string `json:"Id"`
	Name            string
	Image           string
	RestartCount    int
	Config          map[string]any
	HostConfig      map[string]any
	NetworkSettings struct {
		Networks map[string]map[string]any
		Ports    map[string][]map[string]string
	}
	Mounts []struct {
		// Source is the host path of a bind mount; it is read for the
		// inventory view and is not sent back to the Engine.
		Type, Name, Source, Destination string
		RW                              bool
	}
	State struct {
		Running    bool
		Restarting bool
		ExitCode   int
		Health     *struct{ Status string }
	}
}

type imageInfo struct {
	ID     string `json:"Id"`
	Config struct{ Labels map[string]string }
}

func NewEngine(socket string) *Engine {
	return &Engine{client: &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", socket)
	}}}}
}

func (d *Engine) negotiate(ctx context.Context) error {
	var v struct {
		APIVersion string `json:"ApiVersion"`
	}
	if err := d.request(ctx, "GET", "/version", nil, &v); err != nil {
		return err
	}
	// Docker itself supplies the supported API version. Validate it before constructing paths.
	parts := strings.Split(v.APIVersion, ".")
	if len(parts) != 2 || parts[0] != "1" {
		return fmt.Errorf("unsupported Docker API version %q", v.APIVersion)
	}
	for _, r := range parts[1] {
		if r < '0' || r > '9' {
			return fmt.Errorf("invalid Docker API version")
		}
	}
	if parts[1] == "" {
		return fmt.Errorf("missing Docker API version")
	}
	d.prefix = "/v" + v.APIVersion
	return nil
}

func (d *Engine) call(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var input io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		input = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://docker"+d.prefix+path, input)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if method == "POST" && strings.HasPrefix(path, "/images/create?") {
		values, _ := url.ParseQuery(strings.SplitN(path, "?", 2)[1])
		auth, err := registryAuth(values.Get("fromImage"))
		if err != nil {
			return nil, err
		}
		if auth != "" {
			req.Header.Set("X-Registry-Auth", auth)
		}
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("docker: %w", err)
	}
	idempotent := resp.StatusCode == 304 && (strings.Contains(path, "/start") || strings.Contains(path, "/stop"))
	if !idempotent && (resp.StatusCode < 200 || resp.StatusCode >= 300) {
		defer func() { _ = resp.Body.Close() }()
		var problem struct{ Message string }
		_ = json.NewDecoder(io.LimitReader(resp.Body, 8192)).Decode(&problem)
		if resp.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("%w: docker %s %s: HTTP %d: %s", errDockerNotFound, method, strings.Split(path, "?")[0], resp.StatusCode, problem.Message)
		}
		return nil, fmt.Errorf("docker %s %s: HTTP %d: %s", method, strings.Split(path, "?")[0], resp.StatusCode, problem.Message)
	}
	return resp, nil
}

func (d *Engine) request(ctx context.Context, method, path string, body, result any) error {
	resp, err := d.call(ctx, method, path, body)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if result != nil {
		return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(result)
	}
	_, err = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	return err
}
func (d *Engine) inspect(ctx context.Context, id string) (*container, error) {
	var c container
	err := d.request(ctx, "GET", "/containers/"+url.PathEscape(id)+"/json", nil, &c)
	return &c, err
}
func (d *Engine) image(ctx context.Context, id string) (*imageInfo, error) {
	var i imageInfo
	err := d.request(ctx, "GET", "/images/"+url.PathEscape(id)+"/json", nil, &i)
	return &i, err
}
func (d *Engine) pull(ctx context.Context, ref string) (*imageInfo, error) {
	resp, err := d.call(ctx, "POST", "/images/create?fromImage="+url.QueryEscape(ref), nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	dec := json.NewDecoder(resp.Body)
	for {
		var event struct {
			Error string `json:"error"`
		}
		err = dec.Decode(&event)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("download da imagem: %w", err)
		}
		if event.Error != "" {
			return nil, fmt.Errorf("download da imagem: %s", event.Error)
		}
	}
	return d.image(ctx, ref)
}
func (d *Engine) find(ctx context.Context, project, service string) (*container, error) {
	filters, _ := json.Marshal(map[string][]string{"label": {"com.docker.compose.project=" + project, "com.docker.compose.service=" + service}})
	var found []struct {
		ID string `json:"Id"`
	}
	if err := d.request(ctx, "GET", "/containers/json?all=true&filters="+url.QueryEscape(string(filters)), nil, &found); err != nil {
		return nil, err
	}
	var selected *container
	for _, item := range found {
		c, err := d.inspect(ctx, item.ID)
		if err != nil {
			return nil, err
		}
		labels := stringMap(c.Config["Labels"])
		if labels["io.pulso.update.backup"] != "" || backupNamePattern.MatchString(c.Name) {
			continue
		}
		if selected != nil {
			return nil, fmt.Errorf("mais de um contêiner para o serviço %s", service)
		}
		selected = c
	}
	if selected == nil {
		return nil, fmt.Errorf("serviço %s não encontrado neste projeto Compose", service)
	}
	return selected, nil
}
func (d *Engine) create(ctx context.Context, name string, body any) (string, error) {
	var result struct {
		ID string `json:"Id"`
	}
	err := d.request(ctx, "POST", "/containers/create?name="+url.QueryEscape(name), body, &result)
	return result.ID, err
}
func (d *Engine) start(ctx context.Context, id string) error {
	return d.request(ctx, "POST", "/containers/"+url.PathEscape(id)+"/start", nil, nil)
}
func (d *Engine) stop(ctx context.Context, id string) error {
	return d.request(ctx, "POST", "/containers/"+url.PathEscape(id)+"/stop", nil, nil)
}
func (d *Engine) rename(ctx context.Context, id, name string) error {
	return d.request(ctx, "POST", "/containers/"+url.PathEscape(id)+"/rename?name="+url.QueryEscape(name), nil, nil)
}
func (d *Engine) remove(ctx context.Context, id string) error {
	return d.request(ctx, "DELETE", "/containers/"+url.PathEscape(id)+"?force=true", nil, nil)
}
func stringMap(value any) map[string]string {
	out := map[string]string{}
	switch raw := value.(type) {
	case map[string]any:
		for k, v := range raw {
			if s, ok := v.(string); ok {
				out[k] = s
			}
		}
	case map[string]string:
		for k, v := range raw {
			out[k] = v
		}
	}
	return out
}
func cloneMap(value map[string]any) map[string]any {
	data, _ := json.Marshal(value)
	var out map[string]any
	_ = json.Unmarshal(data, &out)
	return out
}
