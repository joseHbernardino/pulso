//go:build integration

package containerupdate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Uses only randomly named containers, volumes, networks and local test images.
// Pull and release lookup are injected; all replacement/health/rollback operations use real Docker.
func TestDockerUpdateAndRollback(t *testing.T) {
	oldImage, newImage, nextImage, badImage := os.Getenv("PULSO_UPDATE_OLD_IMAGE"), os.Getenv("PULSO_UPDATE_NEW_IMAGE"), os.Getenv("PULSO_UPDATE_NEXT_IMAGE"), os.Getenv("PULSO_UPDATE_BAD_IMAGE")
	if oldImage == "" || newImage == "" || nextImage == "" || badImage == "" {
		t.Skip("set the four PULSO_UPDATE_*_IMAGE variables")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	d := NewEngine("/var/run/docker.sock")
	if err := d.negotiate(ctx); err != nil {
		t.Fatal(err)
	}
	prefix := fmt.Sprintf("pulso-update-test-%d", time.Now().UnixNano())
	network, data, apps := prefix+"-net", prefix+"-data", prefix+"-apps"
	appName, agentName := prefix+"-pulso-1", prefix+"-pulso-updater-1"
	run := func(args ...string) string {
		t.Helper()
		out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("docker %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	t.Cleanup(func() {
		// All test-owned containers carry this project label, including backups/workers.
		out, _ := exec.Command("docker", "ps", "-aq", "--filter", "label="+projectLabel+"="+prefix).Output()
		for _, id := range strings.Fields(string(out)) {
			_ = exec.Command("docker", "rm", "-f", id).Run()
		}
		_ = exec.Command("docker", "network", "rm", network).Run()
		_ = exec.Command("docker", "volume", "rm", data, apps).Run()
		_ = exec.Command("docker", "image", "rm", prefix+":latest", prefix+":latest-python311").Run()
	})
	run("network", "create", network)
	run("tag", oldImage, prefix+":latest")
	run("tag", oldImage, prefix+":latest-python311")
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(dir, 10001, 10001); err != nil {
		t.Fatal(err)
	}
	run("run", "-d", "--name", appName, "--restart", "unless-stopped", "--network", network, "--label", projectLabel+"="+prefix, "--label", serviceLabel+"=pulso",
		"--publish", "127.0.0.1::9615", "--health-interval", "1s", "--health-start-period", "1s", "--health-retries", "2",
		"--env", "PULSO_UPDATE_DIR="+DefaultDirectory, "--mount", "type=volume,source="+data+",target=/var/lib/pulso", "--mount", "type=volume,source="+apps+",target=/apps",
		"--mount", "type=bind,source="+dir+",target="+DefaultDirectory+",readonly", "--read-only", "--tmpfs", "/tmp:rw,nosuid,size=64m", "--cap-drop", "ALL", prefix+":latest-python311")
	agentID := run("run", "-d", "--name", agentName, "--restart", "unless-stopped", "--network", network, "--label", projectLabel+"="+prefix, "--label", serviceLabel+"=pulso-updater",
		"--user", "0:0", "--read-only", "--tmpfs", "/tmp:rw,size=32m", "--cap-drop", "ALL", "--cap-add", "CHOWN", "--cap-add", "DAC_OVERRIDE", "--cap-add", "FOWNER", "--security-opt", "no-new-privileges:true", "--env", "PULSO_UPDATE_DIR="+DefaultDirectory, "--env", "PULSO_UPDATE_IMAGE="+prefix,
		"--mount", "type=bind,source="+dir+",target="+DefaultDirectory, "--mount", "type=bind,source=/var/run/docker.sock,target=/var/run/docker.sock",
		"--health-cmd", "/usr/local/bin/pulso container-updater health", "--health-interval", "1s", "--health-start-period", "1s",
		"--entrypoint", "/usr/bin/tini", prefix+":latest", "--", "/bin/sleep", "3600")
	defer func() {
		if t.Failed() {
			for _, name := range []string{appName, agentName} {
				out, _ := exec.Command("docker", "logs", "--tail", "30", name).CombinedOutput()
				t.Logf("%s: %s", name, out)
			}
			if j, _ := loadJob(dir); j != nil && j.WorkerID != "" {
				out, _ := exec.Command("docker", "logs", "--tail", "50", j.WorkerID).CombinedOutput()
				t.Logf("worker: %s", out)
			}
		}
	}()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 3 * time.Second}
	port := run("port", appName, "9615/tcp")
	base := "http://" + port
	api := func(method, path string, body any) (map[string]any, int, error) {
		var input io.Reader
		if body != nil {
			payload, _ := json.Marshal(body)
			input = bytes.NewReader(payload)
		}
		req, _ := http.NewRequest(method, base+path, input)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return nil, 0, err
		}
		defer func() { _ = resp.Body.Close() }()
		var result map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&result)
		return result, resp.StatusCode, nil
	}
	eventually := func(label string, predicate func() bool) {
		t.Helper()
		deadline := time.Now().Add(70 * time.Second)
		for time.Now().Before(deadline) {
			if predicate() {
				return
			}
			time.Sleep(200 * time.Millisecond)
		}
		t.Fatalf("timeout: %s", label)
	}
	eventually("initial dashboard", func() bool { _, status, err := api("GET", "/api/auth/status", nil); return err == nil && status == 200 })
	token := run("exec", appName, "cat", "/var/lib/pulso/security/setup-token")
	if _, status, err := api("POST", "/api/auth/setup", map[string]string{"token": token, "username": "updateadmin", "password": "DisposableUpdatePassword123!"}); err != nil || status != 201 {
		t.Fatalf("setup: %d %v", status, err)
	}
	// Daemon autosave must restore the running application and keep the paused one stopped.
	for _, name := range []string{"active", "paused"} {
		app, status, err := api("POST", "/api/v1/processes", map[string]any{"name": name, "runtime": "process", "entrypoint": "sleep", "args": []string{"3600"}, "restart_policy": "never"})
		if err != nil || status != 201 {
			t.Fatalf("start: %d %v %v", status, err, app)
		}
		if name == "paused" {
			if _, status, err = api("POST", "/api/v1/processes/"+app["id"].(string)+"/stop", nil); err != nil || status != 200 {
				t.Fatalf("pause: %d %v", status, err)
			}
		}
	}
	makeAgent := func(selfID, tag string) *Agent {
		t.Helper()
		a, err := NewAgent(ctx, Config{Directory: dir, SelfID: selfID, Repository: prefix, LookupRelease: func(context.Context) (string, string, error) {
			return tag, "https://github.com/example/pulso/releases/tag/" + tag, nil
		}, Pull: func(ctx context.Context, ref string) (*imageInfo, error) {
			selected := newImage
			if strings.Contains(ref, "v0.0.3") {
				selected = nextImage
				if strings.HasSuffix(ref, "-python311") {
					selected = badImage
				}
			}
			return d.image(ctx, selected)
		}})
		if err != nil {
			t.Fatal(err)
		}
		// The host fixture provides the initial control endpoint; the replacement runs the real service.
		a.self.Config["Entrypoint"] = []string{"/usr/bin/tini", "--", "/usr/local/bin/pulso"}
		a.self.Config["Cmd"] = []string{"container-updater", "serve"}
		t.Cleanup(a.close)
		a.check(ctx)
		return a
	}
	a := makeAgent(agentID, "v0.0.2")
	if _, err := a.begin(ctx, "v0.0.2"); err != nil {
		t.Fatal(err)
	}
	var first *job
	eventually("successful update", func() bool { first, _ = loadJob(dir); return first != nil && !first.Active() })
	if first.State != "completed" {
		t.Fatalf("update failed: %+v", first.Status)
	}
	expected, _ := d.image(ctx, newImage)
	current, err := d.inspect(ctx, appName)
	if err != nil || current.Image != expected.ID {
		t.Fatalf("wrong application image: %v %+v", err, current)
	}
	nextAgent, err := d.inspect(ctx, agentName)
	if err != nil || nextAgent.Image != expected.ID {
		t.Fatalf("updater itself not replaced: %v", err)
	}
	result, status, err := api("GET", "/api/v1/system/version", nil)
	if err != nil || status != 200 || result["build"].(map[string]any)["version"] != "v0.0.2" {
		t.Fatalf("version/session/port lost: %d %v %v", status, err, result)
	}
	checkApps := func() {
		t.Helper()
		resp, err := client.Get(base + "/api/v1/processes")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		var procs []struct{ Name, State string }
		if err = json.NewDecoder(resp.Body).Decode(&procs); err != nil {
			t.Fatal(err)
		}
		states := map[string]string{}
		for _, p := range procs {
			states[p.Name] = p.State
		}
		if states["active"] != "running" || states["paused"] != "stopped" {
			t.Fatalf("desired application state lost: %v", states)
		}
	}
	checkApps()
	// The second candidate exits immediately. The old portal and account/session must return.
	rollbackAgent := makeAgent(nextAgent.ID, "v0.0.3")
	if _, err = rollbackAgent.begin(ctx, "v0.0.3"); err != nil {
		t.Fatal(err)
	}
	var second *job
	eventually("automatic rollback", func() bool {
		second, _ = loadJob(dir)
		return second != nil && second.ID != first.ID && !second.Active()
	})
	if second.State != "rolled_back" {
		t.Fatalf("rollback failed: %+v", second.Status)
	}
	eventually("dashboard after rollback", func() bool {
		r, status, err := api("GET", "/api/v1/system/version", nil)
		return err == nil && status == 200 && r["build"].(map[string]any)["version"] == "v0.0.2"
	})
	checkApps()
	if missingImage := os.Getenv("PULSO_UPDATE_MISSING_RUNTIME_IMAGE"); missingImage != "" {
		badImage = missingImage
		missingRuntimeAgent := makeAgent(nextAgent.ID, "v0.0.3")
		if _, err = missingRuntimeAgent.begin(ctx, "v0.0.3"); err != nil {
			t.Fatal(err)
		}
		var third *job
		eventually("rollback after application recovery failure", func() bool { third, _ = loadJob(dir); return third != nil && third.ID != second.ID && !third.Active() })
		if third.State != "rolled_back" {
			t.Fatalf("application rollback failed: %+v", third.Status)
		}
		eventually("dashboard after restoring recovery snapshot", func() bool {
			_, status, err := api("GET", "/api/v1/system/version", nil)
			return err == nil && status == 200
		})
		checkApps()
	}
	// Kill the detached worker mid-transaction. Its retry must restore the previous containers.
	badImage = nextImage
	interruptedAgent := makeAgent(nextAgent.ID, "v0.0.3")
	if _, err = interruptedAgent.begin(ctx, "v0.0.3"); err != nil {
		t.Fatal(err)
	}
	var interrupted *job
	eventually("worker replacing portal", func() bool {
		interrupted, _ = loadJob(dir)
		return interrupted != nil && interrupted.Active() && interrupted.Step == "updating_portal" && len(interrupted.Plans) > 0 && interrupted.Plans[0].CandidateID != ""
	})
	run("kill", "--signal", "KILL", interrupted.WorkerID)
	var recovered *job
	eventually("recovery after killed worker", func() bool {
		recovered, _ = loadJob(dir)
		return recovered != nil && recovered.ID == interrupted.ID && !recovered.Active()
	})
	if recovered.State != "rolled_back" {
		t.Fatalf("interrupted update recovery failed: %+v", recovered.Status)
	}
	eventually("dashboard after interrupted update", func() bool {
		_, status, err := api("GET", "/api/v1/system/version", nil)
		return err == nil && status == 200
	})
	checkApps()
	eventually("finished worker has restart disabled", func() bool {
		worker, err := d.inspect(ctx, recovered.WorkerID)
		return err == nil && stringMap(worker.HostConfig["RestartPolicy"])["Name"] == "no"
	})
	// Read-only mount and persistent data still refer to the same named volumes after both operations.
	state, err := LatestStatus(dir)
	if err != nil || state.State != "rolled_back" {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(dir, "job.json")); err != nil {
		t.Fatal(err)
	}
}
