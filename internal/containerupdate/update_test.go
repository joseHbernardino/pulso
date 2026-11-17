package containerupdate

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersionOrderingAndTracking(t *testing.T) {
	for _, tc := range []struct {
		latest, current string
		want            bool
	}{
		{"v1.2.0", "v1.1.9", true}, {"v1.10.0", "v1.9.9", true}, {"v1.0.0", "v2.0.0", false}, {"v1.0.0", "v1.0.0", false}, {"v1.0.0", "v1.0.0-rc1", true}, {"v1.0.0", "dev", true}, {"v1.0.0-rc1", "v0.1.0", false},
	} {
		if got := newer(tc.latest, tc.current); got != tc.want {
			t.Errorf("newer(%s,%s)=%v", tc.latest, tc.current, got)
		}
	}
	for _, tc := range []struct {
		ref string
		ok  bool
	}{
		{"ghcr.io/example/pulso:latest", true}, {"ghcr.io/example/pulso:latest-python311", true}, {"ghcr.io/example/pulso:v1.0.0", false}, {"pulso:local", false}, {"ghcr.io/other/pulso:latest", false},
	} {
		_, err := trackingTag(tc.ref, "ghcr.io/example/pulso")
		if (err == nil) != tc.ok {
			t.Errorf("trackingTag(%s)=%v", tc.ref, err)
		}
	}
}

func TestReplacementPreservesContainerConfiguration(t *testing.T) {
	old := &container{ID: strings.Repeat("a", 64), Config: map[string]any{"Image": "pulso:latest", "Hostname": strings.Repeat("a", 12), "Env": []any{"KEY=private"}, "Labels": map[string]any{projectLabel: "p", serviceLabel: "pulso"}}, HostConfig: map[string]any{"ReadonlyRootfs": true, "Binds": []any{"data:/var/lib/pulso:rw", "/srv/apps:/apps:ro,Z"}, "PortBindings": map[string]any{"9615/tcp": []any{map[string]any{"HostIp": "0.0.0.0", "HostPort": "5011"}}}, "RestartPolicy": map[string]any{"Name": "unless-stopped"}}}
	old.NetworkSettings.Networks = map[string]map[string]any{"p_default": {"Aliases": []any{"pulso"}, "IPAddress": "172.18.0.4", "EndpointID": "old", "IPAMConfig": map[string]any{"IPv4Address": "172.18.0.4"}}}
	body := replacementConfig(old, "sha256:new", "job")
	if body["Hostname"] != "" || body["Image"] != "sha256:new" {
		t.Fatalf("image/hostname: %+v", body)
	}
	data, _ := json.Marshal(body)
	for _, expected := range []string{"private", "5011", "unless-stopped", "ReadonlyRootfs", "/srv/apps:/apps:ro,Z", "p_default", "IPAMConfig"} {
		if !strings.Contains(string(data), expected) {
			t.Fatalf("lost %s: %s", expected, data)
		}
	}
	if strings.Contains(string(data), "EndpointID") {
		t.Fatal("copied runtime network identity")
	}
	if old.Config["Image"] != "pulso:latest" {
		t.Fatal("mutated previous configuration")
	}
}

func TestProgressDoesNotExposeSavedSecrets(t *testing.T) {
	dir := t.TempDir()
	j := &job{Status: Status{ID: "job", State: "running", Step: "queued"}, Plans: []plan{{Old: &container{Config: map[string]any{"Env": []any{"TOKEN=very-private"}}}}}}
	if err := saveJob(dir, j); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "job.json"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("private update state: %v", err)
	}
	status, err := LatestStatus(dir)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(UpdateInfo{Job: status})
	if strings.Contains(string(data), "very-private") || strings.Contains(string(data), "Plans") {
		t.Fatalf("secret in public progress: %s", data)
	}
	restored, err := loadJob(dir)
	if err != nil || len(restored.Plans) != 1 {
		t.Fatalf("recovery data missing: %v", err)
	}
}

func TestDockerPullDetectsErrorsInsideSuccessfulHTTPResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{\"status\":\"Downloading\"}\n{\"error\":\"denied: access forbidden\"}\n"))
	}))
	defer server.Close()
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		r.URL.Scheme = "http"
		r.URL.Host = strings.TrimPrefix(server.URL, "http://")
		return http.DefaultTransport.RoundTrip(r)
	})
	engine := &Engine{client: &http.Client{Transport: transport}}
	_, err := engine.pull(context.Background(), "ghcr.io/example/pulso:v1.0.0")
	if err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("pull error ignored: %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestApplicationRecoveryChecksRunningAndPaused(t *testing.T) {
	expected := []Application{{Name: "api", State: "running", Ready: true}, {Name: "worker", State: "stopped", KeepStopped: true}}
	for _, tc := range []struct {
		apps []Application
		ok   bool
	}{
		{[]Application{{Name: "api", State: "running", Ready: true}, {Name: "worker", State: "stopped"}}, true},
		{[]Application{{Name: "api", State: "crashed"}, {Name: "worker", State: "stopped"}}, false},
		{[]Application{{Name: "api", State: "running", Ready: true}, {Name: "worker", State: "running", Ready: true}}, false},
		{[]Application{{Name: "worker", State: "stopped"}}, false},
	} {
		if err := compareApplications(expected, tc.apps); (err == nil) != tc.ok {
			t.Errorf("recovery check %v: %v", tc.apps, err)
		}
	}
}

func TestPrivateRegistryCredentialIsNotStoredInJob(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	fakeAuth := base64.StdEncoding.EncodeToString([]byte("reader:disposable-token"))
	data, _ := json.Marshal(map[string]any{"auths": map[string]any{"ghcr.io": map[string]string{"auth": fakeAuth}}})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PULSO_REGISTRY_CONFIG", path)
	header, err := registryAuth("ghcr.io/example/pulso:v1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.URLEncoding.DecodeString(header)
	if err != nil {
		t.Fatal(err)
	}
	var auth map[string]string
	if err = json.Unmarshal(decoded, &auth); err != nil {
		t.Fatal(err)
	}
	if auth["username"] != "reader" || auth["password"] != "disposable-token" {
		t.Fatal("incorrect registry credential")
	}
	header, err = registryAuth("other.example/pulso:v1.0.0")
	if err != nil || header != "" {
		t.Fatal("credential sent to another registry")
	}
}

func TestLabelsSurviveRepeatedConfigurationConversions(t *testing.T) {
	original := map[string]any{projectLabel: "test", serviceLabel: "pulso-updater"}
	first := stringMap(original)
	second := stringMap(first)
	if second[projectLabel] != "test" || second[serviceLabel] != "pulso-updater" {
		t.Fatalf("labels lost: %v", second)
	}
}

func TestProgressReadsSmallStatusInsteadOfPrivateRecoveryPayload(t *testing.T) {
	dir := t.TempDir()
	j := &job{Status: Status{ID: "light-progress", State: "completed"}, RecoverySnapshot: make([]byte, 1<<20)}
	if err := saveJob(dir, j); err != nil {
		t.Fatal(err)
	}
	// Progress remains readable without decoding the private recovery file.
	if err := os.WriteFile(filepath.Join(dir, "job.json"), []byte("private recovery payload is intentionally not JSON in this test"), 0o600); err != nil {
		t.Fatal(err)
	}
	status, err := LatestStatus(dir)
	if err != nil || status == nil || status.ID != "light-progress" {
		t.Fatalf("progress depended on recovery payload: %+v %v", status, err)
	}
}
