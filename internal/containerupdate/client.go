package containerupdate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

type Client struct{ http *http.Client }

func NewClient(dir string) *Client {
	return &Client{http: &http.Client{Timeout: 25 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "unix", filepath.Join(dir, "updater.sock"))
	}}}}
}
func (c *Client) call(ctx context.Context, method, path string, body, result any) error {
	var input io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		input = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://updater"+path, input)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("serviço de atualização indisponível; confira o Compose de atualizações: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		var problem struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 16384)).Decode(&problem)
		if problem.Error == "" {
			return fmt.Errorf("serviço auxiliar sem suporte a este recurso (HTTP %d); atualize com bash scripts/pulso-docker.sh start", resp.StatusCode)
		}
		return fmt.Errorf("%s", problem.Error)
	}
	if result != nil {
		return json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(result)
	}
	return nil
}
func (c *Client) Health(ctx context.Context) error { return c.call(ctx, "GET", "/health", nil, nil) }
func (c *Client) Status(ctx context.Context, check bool) (*UpdateInfo, error) {
	var result UpdateInfo
	method, path := "GET", "/status"
	if check {
		method, path = "POST", "/check"
	}
	err := c.call(ctx, method, path, nil, &result)
	return &result, err
}
func (c *Client) Begin(ctx context.Context, version string) (*Status, error) {
	var result Status
	err := c.call(ctx, "POST", "/update", map[string]string{"version": version}, &result)
	return &result, err
}
func (c *Client) Applications(ctx context.Context) ([]ContainerApplication, error) {
	var result []ContainerApplication
	err := c.call(ctx, "GET", "/applications", nil, &result)
	return result, err
}
func (c *Client) Application(ctx context.Context, id string) (*ContainerApplication, error) {
	if !validAppID(id) {
		return nil, fmt.Errorf("identificador inválido")
	}
	var app ContainerApplication
	err := c.call(ctx, "GET", "/applications/"+id, nil, &app)
	return &app, err
}
func (c *Client) DeployApplication(ctx context.Context, id string, cfg ApplicationConfig) (*ContainerApplication, error) {
	if id != "" && !validAppID(id) {
		return nil, fmt.Errorf("identificador inválido")
	}
	method, path := "POST", "/applications"
	if id != "" {
		method, path = "PUT", "/applications/"+id
	}
	var result ContainerApplication
	err := c.call(ctx, method, path, cfg, &result)
	return &result, err
}
func (c *Client) ApplicationAction(ctx context.Context, id, action string) error {
	if !validAppID(id) || (action != "start" && action != "stop" && action != "restart" && action != "delete") {
		return fmt.Errorf("ação ou identificador inválido")
	}
	return c.call(ctx, "POST", "/applications/"+id+"/"+action, nil, nil)
}

// ApplicationStats reports live usage for one application.
func (c *Client) ApplicationStats(ctx context.Context, id string) (*ApplicationStats, error) {
	if !validAppID(id) {
		return nil, fmt.Errorf("identificador inválido")
	}
	var result ApplicationStats
	if err := c.call(ctx, "GET", "/applications/"+id+"/stats", nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// AllApplicationStats reports live usage for every registered application in
// one request, for the dashboard table.
func (c *Client) AllApplicationStats(ctx context.Context) ([]ApplicationStats, error) {
	var result struct {
		Stats []ApplicationStats `json:"stats"`
	}
	if err := c.call(ctx, "GET", "/stats", nil, &result); err != nil {
		return nil, err
	}
	return result.Stats, nil
}

func (c *Client) ApplicationLogs(ctx context.Context, id string) (string, error) {
	if !validAppID(id) {
		return "", fmt.Errorf("identificador inválido")
	}
	var result struct {
		Logs string `json:"logs"`
	}
	err := c.call(ctx, "GET", "/applications/"+id+"/logs", nil, &result)
	return result.Logs, err
}

// ---------------------------------------------------- inventário do Docker

// DockerContainers lists every container on the Engine, classified.
func (c *Client) DockerContainers(ctx context.Context) ([]ContainerSummary, error) {
	var result struct {
		Containers []ContainerSummary `json:"containers"`
	}
	err := c.call(ctx, "GET", "/docker/containers", nil, &result)
	return result.Containers, err
}

// DockerContainer inspects one container. The result carries the container's
// environment, so the caller must redact it for non-administrators.
func (c *Client) DockerContainer(ctx context.Context, id string) (*ContainerDetails, error) {
	if !validDockerRef(id) {
		return nil, fmt.Errorf("identificador inválido")
	}
	var result ContainerDetails
	if err := c.call(ctx, "GET", "/docker/containers/"+url.PathEscape(id), nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// DockerContainerLogs reads the tail of any container's logs.
func (c *Client) DockerContainerLogs(ctx context.Context, id string, tail int) (string, error) {
	if !validDockerRef(id) {
		return "", fmt.Errorf("identificador inválido")
	}
	var result struct {
		Logs string `json:"logs"`
	}
	path := fmt.Sprintf("/docker/containers/%s/logs?tail=%d", url.PathEscape(id), tail)
	err := c.call(ctx, "GET", path, nil, &result)
	return result.Logs, err
}

// DockerContainerAction starts, stops, restarts or removes a container.
func (c *Client) DockerContainerAction(ctx context.Context, id, action string) error {
	if !validDockerRef(id) {
		return fmt.Errorf("identificador inválido")
	}
	switch action {
	case "start", "stop", "restart", "remove":
	default:
		return fmt.Errorf("ação inválida")
	}
	return c.call(ctx, "POST", "/docker/containers/"+url.PathEscape(id)+"/"+action, nil, nil)
}

// DockerImages lists the images.
func (c *Client) DockerImages(ctx context.Context) ([]ImageSummary, error) {
	var result struct {
		Images []ImageSummary `json:"images"`
	}
	err := c.call(ctx, "GET", "/docker/images", nil, &result)
	return result.Images, err
}

// DockerRemoveImage removes one image unless it is protected.
func (c *Client) DockerRemoveImage(ctx context.Context, id string) error {
	if !validDockerRef(id) {
		return fmt.Errorf("identificador inválido")
	}
	return c.call(ctx, "DELETE", "/docker/images/"+url.PathEscape(id), nil, nil)
}

// DockerVolumes lists the volumes.
func (c *Client) DockerVolumes(ctx context.Context) ([]VolumeSummary, error) {
	var result struct {
		Volumes []VolumeSummary `json:"volumes"`
	}
	err := c.call(ctx, "GET", "/docker/volumes", nil, &result)
	return result.Volumes, err
}

// DockerRemoveVolume removes one volume unless it is protected.
func (c *Client) DockerRemoveVolume(ctx context.Context, name string) error {
	if !validDockerRef(name) {
		return fmt.Errorf("nome inválido")
	}
	return c.call(ctx, "DELETE", "/docker/volumes/"+url.PathEscape(name), nil, nil)
}

// DockerNetworks lists the networks.
func (c *Client) DockerNetworks(ctx context.Context) ([]NetworkSummary, error) {
	var result struct {
		Networks []NetworkSummary `json:"networks"`
	}
	err := c.call(ctx, "GET", "/docker/networks", nil, &result)
	return result.Networks, err
}

// DockerRemoveNetwork removes one network unless it is protected.
func (c *Client) DockerRemoveNetwork(ctx context.Context, id string) error {
	if !validDockerRef(id) {
		return fmt.Errorf("identificador inválido")
	}
	return c.call(ctx, "DELETE", "/docker/networks/"+url.PathEscape(id), nil, nil)
}

// DockerDisk reports what the Engine holds and what cleanup could free.
func (c *Client) DockerDisk(ctx context.Context) (*DiskUsage, error) {
	var result DiskUsage
	if err := c.call(ctx, "GET", "/docker/disk", nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// DockerPrune removes unused resources, or reports what it would remove.
func (c *Client) DockerPrune(ctx context.Context, resources []string, dryRun bool) (*PruneResult, error) {
	body := map[string]any{"resources": resources, "dry_run": dryRun}
	var result PruneResult
	if err := c.call(ctx, "POST", "/docker/prune", body, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// validDockerRef accepts the shapes Docker uses for an identifier, a name or
// an image reference, and rejects anything that could escape the path.
func validDockerRef(value string) bool {
	if value == "" || len(value) > 300 {
		return false
	}
	if strings.ContainsAny(value, " \t\r\n\x00") {
		return false
	}
	// Uma referência nunca contém segmentos de caminho relativos.
	if value == "." || value == ".." || strings.Contains(value, "..") {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.' || r == '-' || r == '_' || r == ':' || r == '/' || r == '@' || r == '+':
		default:
			return false
		}
	}
	return true
}
