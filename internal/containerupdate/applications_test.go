package containerupdate

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplicationValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*ApplicationConfig)
	}{
		{"name", func(c *ApplicationConfig) { c.Name = "../escape" }},
		{"memory", func(c *ApplicationConfig) { c.MemoryMB = -1 }},
		{"cpu", func(c *ApplicationConfig) { c.CPUs = 999 }},
		{"port", func(c *ApplicationConfig) { c.Port = 70000 }},
		{"host-without-port", func(c *ApplicationConfig) { c.HostPort = 80 }},
		{"volume-root", func(c *ApplicationConfig) { c.DataPath = "/" }},
		{"environment", func(c *ApplicationConfig) { c.Env = map[string]string{"BAD=KEY": "value"} }},
		{"image", func(c *ApplicationConfig) { c.Image = "image\nnext" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := ApplicationConfig{Name: "test", Image: "example:tag"}
			tc.change(&cfg)
			if ValidateApplication(&cfg) == nil {
				t.Fatal("unsafe config accepted")
			}
		})
	}
	cfg := ApplicationConfig{Name: "test", Image: "example:tag"}
	if err := ValidateApplication(&cfg); err != nil || cfg.CPUs != 1 || cfg.MemoryMB != 512 {
		t.Fatalf("defaults: %+v %v", cfg, err)
	}
}

func TestApplicationCannotControlAnotherProjectsContainer(t *testing.T) {
	mutations := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			mutations++
			w.WriteHeader(500)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"Id": "foreign", "Config": map[string]any{"Labels": map[string]string{applicationOwner: "another-project", applicationLabel: "0123456789abcdef01234567"}}})
	}))
	defer server.Close()
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		r.URL.Host = strings.TrimPrefix(server.URL, "http://")
		return http.DefaultTransport.RoundTrip(r)
	})
	a := &Agent{cfg: Config{Directory: t.TempDir()}, project: "own-project", engine: &Engine{client: &http.Client{Transport: transport}}}
	if err := os.MkdirAll(a.appDir(), 0o750); err != nil {
		t.Fatal(err)
	}
	app := &ContainerApplication{ID: "0123456789abcdef01234567", ContainerID: "foreign"}
	if err := a.saveApplication(app); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"start", "stop", "restart", "delete"} {
		if err := a.applicationAction(context.Background(), app.ID, action); err == nil {
			t.Fatalf("foreign %s accepted", action)
		}
	}
	if mutations != 0 {
		t.Fatal("Docker received a mutation for an unrelated container")
	}
	if _, err := os.Stat(filepath.Join(a.appDir(), app.ID+".json")); err != nil {
		t.Fatal("foreign-control attempt erased metadata")
	}
}
