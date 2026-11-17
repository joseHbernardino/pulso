package containerupdate

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

const DefaultDirectory = "/var/run/pulso-update"
const projectLabel = "com.docker.compose.project"
const serviceLabel = "com.docker.compose.service"

type Status struct {
	ID          string     `json:"id"`
	State       string     `json:"state"`
	Step        string     `json:"step"`
	Message     string     `json:"message"`
	FromVersion string     `json:"from_version"`
	ToVersion   string     `json:"to_version"`
	StartedAt   time.Time  `json:"started_at"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
}

func (s Status) Active() bool { return s.State == "running" }

type plan struct {
	Old         *container
	NewImage    string
	TrackingRef string
	CandidateID string
}
type job struct {
	Status
	WorkerID         string
	WorkerFallback   bool
	WorkerRecoveries int
	Plans            []plan
	Applications     []Application
	RecoverySnapshot []byte
}

func loadJob(dir string) (*job, error) {
	file, err := os.Open(filepath.Join(dir, "job.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, 8<<20))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var j job
	if err = json.Unmarshal(data, &j); err != nil {
		return nil, fmt.Errorf("estado da atualização: %w", err)
	}
	return &j, nil
}
func writeStateFile(dir, name string, data []byte) error {
	f, err := os.CreateTemp(dir, ".update-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if err = f.Chmod(0o600); err == nil && os.Geteuid() == 0 {
		err = f.Chown(10001, 10001)
	}
	if err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), filepath.Join(dir, name))
}
func saveStatus(dir string, status *Status) error {
	data, err := json.Marshal(status)
	if err != nil {
		return err
	}
	return writeStateFile(dir, "status.json", data)
}
func saveJob(dir string, j *job) error {
	data, err := json.Marshal(j)
	if err != nil {
		return err
	}
	if len(data) > 8<<20 {
		return fmt.Errorf("estado de atualização excede 8 MiB")
	}
	if err = writeStateFile(dir, "job.json", data); err != nil {
		return err
	}
	if err = saveStatus(dir, &j.Status); err != nil {
		return err
	}
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = directory.Close() }()
	return directory.Sync()
}
func finishJob(dir string, j *job, state, message string) error {
	now := time.Now()
	if runes := []rune(message); len(runes) > 8192 {
		message = string(runes[:4096]) + "\n…\n" + string(runes[len(runes)-4096:])
	}
	j.State, j.Message, j.FinishedAt = state, message, &now
	return saveJob(dir, j)
}

// LatestStatus exposes progress without exposing saved container configuration or environment.
func LatestStatus(dir string) (*Status, error) {
	f, err := os.Open(filepath.Join(dir, "status.json"))
	if os.IsNotExist(err) {
		j, err := loadJob(dir)
		if err != nil || j == nil {
			return nil, err
		}
		status := j.Status
		return &status, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var status Status
	if err = json.NewDecoder(io.LimitReader(f, 64<<10)).Decode(&status); err != nil {
		return nil, err
	}
	return &status, nil
}
