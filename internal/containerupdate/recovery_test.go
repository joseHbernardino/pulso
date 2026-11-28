package containerupdate

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

// fakeEngine answers the few Docker calls the recovery path makes, and records
// the mutations it received so a test can prove what the agent did.
type fakeEngine struct {
	mu       sync.Mutex
	running  map[string]bool
	owner    string
	appID    string
	removed  []string
	started  []string
	server   *httptest.Server
	notFound map[string]bool
}

func newFakeEngine(t *testing.T, owner, appID string, running map[string]bool) (*Engine, *fakeEngine) {
	t.Helper()
	fake := &fakeEngine{running: running, owner: owner, appID: appID, notFound: map[string]bool{}}
	fake.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/containers/")
		id = strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(id, "/json"), "/start"), "/stop")
		fake.mu.Lock()
		defer fake.mu.Unlock()
		switch {
		case r.Method == http.MethodDelete:
			fake.removed = append(fake.removed, id)
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/start"):
			fake.started = append(fake.started, id)
			fake.running[id] = true
			w.WriteHeader(http.StatusNoContent)
		case fake.notFound[id]:
			w.WriteHeader(http.StatusNotFound)
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Id":    id,
				"State": map[string]any{"Running": fake.running[id]},
				"Config": map[string]any{"Labels": map[string]string{
					applicationOwner: fake.owner,
					applicationLabel: fake.appID,
				}},
			})
		}
	}))
	t.Cleanup(fake.server.Close)

	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		r.URL.Host = strings.TrimPrefix(fake.server.URL, "http://")
		return http.DefaultTransport.RoundTrip(r)
	})
	return &Engine{client: &http.Client{Transport: transport}}, fake
}

func newRecoveryAgent(t *testing.T, project string, engine *Engine) *Agent {
	t.Helper()
	agent := &Agent{cfg: Config{Directory: t.TempDir()}, project: project, engine: engine, ctx: context.Background()}
	if err := os.MkdirAll(agent.appDir(), 0o750); err != nil {
		t.Fatal(err)
	}
	return agent
}

// An agent replaced in the seconds between the application container coming up
// and the deployment recording its success used to delete that container on its
// next start: the candidate had not been promoted yet, and recovery treated it
// as unfinished work. The application vanished, with nothing able to bring it
// back. A running candidate that is the only container the application has is
// the application, so it is adopted.
func TestRecoveryAdoptsARunningFirstDeployment(t *testing.T) {
	const appID = "0123456789abcdef01234567"
	engine, fake := newFakeEngine(t, "own-project", appID, map[string]bool{"candidate": true})
	agent := newRecoveryAgent(t, "own-project", engine)

	if err := agent.saveApplication(&ContainerApplication{
		ID:          appID,
		Config:      ApplicationConfig{Name: "ci-nginx", Image: "nginx:alpine", Pull: true},
		CandidateID: "candidate",
		Phase:       "deploying",
	}); err != nil {
		t.Fatal(err)
	}

	if err := agent.registerApplications(http.NewServeMux()); err != nil {
		t.Fatalf("registerApplications: %v", err)
	}

	fake.mu.Lock()
	removed := append([]string(nil), fake.removed...)
	fake.mu.Unlock()
	if len(removed) != 0 {
		t.Errorf("a running application container was removed: %v", removed)
	}

	app, err := agent.loadApplication(appID)
	if err != nil {
		t.Fatal(err)
	}
	if app.ContainerID != "candidate" {
		t.Errorf("ContainerID = %q, want the adopted candidate", app.ContainerID)
	}
	if app.CandidateID != "" {
		t.Errorf("CandidateID = %q, want it cleared", app.CandidateID)
	}
	if app.Phase != "ready" || app.Error != "" {
		t.Errorf("Phase = %q, Error = %q, want ready with no error", app.Phase, app.Error)
	}
	if app.Config.Pull {
		t.Error("Pull stayed set: a later deployment would be forced to pull again")
	}
}

// A candidate that never came up is unfinished work, and discarding it is what
// keeps a half-created container from lingering.
func TestRecoveryRemovesACandidateThatNeverStarted(t *testing.T) {
	const appID = "0123456789abcdef01234568"
	engine, fake := newFakeEngine(t, "own-project", appID, map[string]bool{"candidate": false})
	agent := newRecoveryAgent(t, "own-project", engine)

	if err := agent.saveApplication(&ContainerApplication{
		ID:          appID,
		Config:      ApplicationConfig{Name: "ci-nginx", Image: "nginx:alpine"},
		CandidateID: "candidate",
		Phase:       "deploying",
	}); err != nil {
		t.Fatal(err)
	}

	if err := agent.registerApplications(http.NewServeMux()); err != nil {
		t.Fatalf("registerApplications: %v", err)
	}

	fake.mu.Lock()
	removed := append([]string(nil), fake.removed...)
	fake.mu.Unlock()
	if len(removed) != 1 || removed[0] != "candidate" {
		t.Errorf("removed = %v, want the stopped candidate discarded", removed)
	}

	app, err := agent.loadApplication(appID)
	if err != nil {
		t.Fatal(err)
	}
	if app.Phase != "failed" || app.Error == "" {
		t.Errorf("Phase = %q, Error = %q, want the interruption reported", app.Phase, app.Error)
	}
	if app.CandidateID != "" {
		t.Errorf("CandidateID = %q, want it cleared", app.CandidateID)
	}
}

// An interrupted redeployment is a different case: the saved configuration
// still describes the previous container, so the running candidate cannot be
// adopted under it. The candidate goes, and the previous container comes back.
func TestRecoveryRestoresThePreviousContainerOnAnInterruptedRedeployment(t *testing.T) {
	const appID = "0123456789abcdef01234569"
	engine, fake := newFakeEngine(t, "own-project", appID, map[string]bool{"candidate": true, "previous": false})
	agent := newRecoveryAgent(t, "own-project", engine)

	if err := agent.saveApplication(&ContainerApplication{
		ID:          appID,
		Config:      ApplicationConfig{Name: "ci-nginx", Image: "nginx:alpine"},
		ContainerID: "previous",
		CandidateID: "candidate",
		OldRunning:  true,
		Phase:       "deploying",
	}); err != nil {
		t.Fatal(err)
	}

	if err := agent.registerApplications(http.NewServeMux()); err != nil {
		t.Fatalf("registerApplications: %v", err)
	}

	fake.mu.Lock()
	removed := append([]string(nil), fake.removed...)
	started := append([]string(nil), fake.started...)
	fake.mu.Unlock()
	if len(removed) != 1 || removed[0] != "candidate" {
		t.Errorf("removed = %v, want only the candidate", removed)
	}
	if len(started) != 1 || started[0] != "previous" {
		t.Errorf("started = %v, want the previous container restored", started)
	}

	app, err := agent.loadApplication(appID)
	if err != nil {
		t.Fatal(err)
	}
	if app.ContainerID != "previous" {
		t.Errorf("ContainerID = %q, want the previous container kept", app.ContainerID)
	}
	if app.Phase != "failed" {
		t.Errorf("Phase = %q, want failed", app.Phase)
	}
}

// Recovery must never touch a container that belongs to someone else, however
// the state file names it.
func TestRecoveryIgnoresAForeignCandidate(t *testing.T) {
	const appID = "0123456789abcdef0123456a"
	engine, fake := newFakeEngine(t, "another-project", appID, map[string]bool{"candidate": true})
	agent := newRecoveryAgent(t, "own-project", engine)

	if err := agent.saveApplication(&ContainerApplication{
		ID:          appID,
		Config:      ApplicationConfig{Name: "ci-nginx", Image: "nginx:alpine"},
		CandidateID: "candidate",
		Phase:       "deploying",
	}); err != nil {
		t.Fatal(err)
	}

	if err := agent.registerApplications(http.NewServeMux()); err != nil {
		t.Fatalf("registerApplications: %v", err)
	}

	fake.mu.Lock()
	removed := append([]string(nil), fake.removed...)
	fake.mu.Unlock()
	if len(removed) != 0 {
		t.Errorf("a container owned by another project was removed: %v", removed)
	}

	app, err := agent.loadApplication(appID)
	if err != nil {
		t.Fatal(err)
	}
	if app.ContainerID != "" {
		t.Errorf("ContainerID = %q: a foreign container must not be adopted", app.ContainerID)
	}
}
