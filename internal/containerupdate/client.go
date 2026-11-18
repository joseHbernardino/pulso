package containerupdate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
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
