package containerupdate

import (
	"context"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
)

// ApplicationStats reports the live resource usage of one application
// container, in the same terms the Docker CLI presents.
type ApplicationStats struct {
	ApplicationID string  `json:"application_id"`
	Name          string  `json:"name,omitempty"`
	Running       bool    `json:"running"`
	CPUPercent    float64 `json:"cpu_percent"`
	CPUs          float64 `json:"cpus"`
	MemoryBytes   int64   `json:"memory_bytes"`
	MemoryLimit   int64   `json:"memory_limit"`
	MemoryPercent float64 `json:"memory_percent"`
	NetworkRx     int64   `json:"network_rx_bytes"`
	NetworkTx     int64   `json:"network_tx_bytes"`
	BlockRead     int64   `json:"block_read_bytes"`
	BlockWrite    int64   `json:"block_write_bytes"`
	Processes     int64   `json:"processes"`
	Error         string  `json:"error,omitempty"`
}

// cpuSample holds the two cumulative counters needed for a CPU delta.
type cpuSample struct {
	CPUUsage struct {
		TotalUsage int64 `json:"total_usage"`
	} `json:"cpu_usage"`
	SystemUsage int64 `json:"system_cpu_usage"`
	OnlineCPUs  int64 `json:"online_cpus"`
}

// dockerStats mirrors only the fields this package reads from
// GET /containers/{id}/stats.
type dockerStats struct {
	CPUStats    cpuSample `json:"cpu_stats"`
	PreCPUStats cpuSample `json:"precpu_stats"`
	MemoryStats struct {
		Usage int64            `json:"usage"`
		Limit int64            `json:"limit"`
		Stats map[string]int64 `json:"stats"`
	} `json:"memory_stats"`
	Networks map[string]struct {
		RxBytes int64 `json:"rx_bytes"`
		TxBytes int64 `json:"tx_bytes"`
	} `json:"networks"`
	BlkioStats struct {
		IOServiceBytes []struct {
			Op    string `json:"op"`
			Value int64  `json:"value"`
		} `json:"io_service_bytes_recursive"`
	} `json:"blkio_stats"`
	PidsStats struct {
		Current int64 `json:"current"`
	} `json:"pids_stats"`
}

// compute converts the cumulative Docker counters into the rates and totals
// shown in the dashboard.
func (s *dockerStats) compute() ApplicationStats {
	var out ApplicationStats

	cpus := s.CPUStats.OnlineCPUs
	if cpus <= 0 {
		cpus = 1
	}
	out.CPUs = float64(cpus)

	// Both counters are cumulative nanosecond totals. The ratio between the
	// container delta and the host delta, scaled by the number of CPUs, is the
	// same percentage `docker stats` prints.
	cpuDelta := s.CPUStats.CPUUsage.TotalUsage - s.PreCPUStats.CPUUsage.TotalUsage
	systemDelta := s.CPUStats.SystemUsage - s.PreCPUStats.SystemUsage
	if cpuDelta > 0 && systemDelta > 0 {
		out.CPUPercent = float64(cpuDelta) / float64(systemDelta) * float64(cpus) * 100
		// Brief sampling windows can overshoot the ceiling.
		if ceiling := float64(cpus) * 100; out.CPUPercent > ceiling {
			out.CPUPercent = ceiling
		}
	}

	// Reclaimable page cache is not memory the application holds, so it is
	// subtracted. The counter is named inactive_file on cgroups v2,
	// total_inactive_file on v1 with hierarchy, and cache on plain v1.
	used := s.MemoryStats.Usage
	for _, key := range []string{"inactive_file", "total_inactive_file", "cache"} {
		if value, ok := s.MemoryStats.Stats[key]; ok {
			if value > 0 && value < used {
				used -= value
			}
			break
		}
	}
	out.MemoryBytes = used
	out.MemoryLimit = s.MemoryStats.Limit
	if s.MemoryStats.Limit > 0 {
		out.MemoryPercent = float64(used) / float64(s.MemoryStats.Limit) * 100
	}

	for _, network := range s.Networks {
		out.NetworkRx += network.RxBytes
		out.NetworkTx += network.TxBytes
	}
	for _, entry := range s.BlkioStats.IOServiceBytes {
		switch strings.ToLower(entry.Op) {
		case "read":
			out.BlockRead += entry.Value
		case "write":
			out.BlockWrite += entry.Value
		}
	}
	out.Processes = s.PidsStats.Current
	return out
}

// containerStats reads a single usage sample. With stream=false the Engine
// still fills precpu_stats from its previous reading, which is what makes the
// CPU percentage computable from one request.
func (d *Engine) containerStats(ctx context.Context, id string) (*dockerStats, error) {
	var sample dockerStats
	path := "/containers/" + url.PathEscape(id) + "/stats?stream=false"
	if err := d.request(ctx, "GET", path, nil, &sample); err != nil {
		return nil, err
	}
	return &sample, nil
}

// applicationStats reports usage for one registered application. An
// application without a container, or with a stopped one, reports zeros
// instead of an error: the dashboard lists it either way.
func (a *Agent) applicationStats(ctx context.Context, id string) (*ApplicationStats, error) {
	a.appsMu.Lock()
	app, err := a.loadApplication(id)
	a.appsMu.Unlock()
	if err != nil {
		return nil, err
	}
	result := &ApplicationStats{ApplicationID: id, Name: app.Config.Name}
	if app.ContainerID == "" {
		return result, nil
	}
	current, err := a.ownedApplication(ctx, id, app.ContainerID)
	if err != nil {
		return nil, err
	}
	if !current.State.Running {
		return result, nil
	}
	sample, err := a.engine.containerStats(ctx, app.ContainerID)
	if err != nil {
		return nil, err
	}
	computed := sample.compute()
	computed.ApplicationID = id
	computed.Name = app.Config.Name
	computed.Running = true
	return &computed, nil
}

// applicationIDsLocked lists the registered application IDs. The caller must
// hold appsMu, matching the contract of loadApplication and saveApplication.
func (a *Agent) applicationIDsLocked() ([]string, error) {
	entries, err := os.ReadDir(a.appDir())
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		id := strings.TrimSuffix(name, ".json")
		if !validAppID(id) {
			continue
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// maxStatsWorkers bounds the concurrent Engine requests made while collecting
// usage for every application, so a large install does not flood the socket.
const maxStatsWorkers = 6

// allApplicationStats collects usage for every registered application. A
// failure on one application is reported in its own entry rather than failing
// the whole listing.
func (a *Agent) allApplicationStats(ctx context.Context) ([]ApplicationStats, error) {
	a.appsMu.Lock()
	ids, err := a.applicationIDsLocked()
	a.appsMu.Unlock()
	if err != nil {
		return nil, err
	}

	results := make([]ApplicationStats, len(ids))
	semaphore := make(chan struct{}, maxStatsWorkers)
	var wait sync.WaitGroup
	for index, id := range ids {
		wait.Add(1)
		go func(index int, id string) {
			defer wait.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			stats, err := a.applicationStats(ctx, id)
			if err == nil {
				results[index] = *stats
				return
			}
			// A entrada com falha mantém o nome, para o painel não exibir uma
			// linha em branco e para a ordenação continuar fazendo sentido.
			entry := ApplicationStats{ApplicationID: id, Error: err.Error()}
			a.appsMu.Lock()
			if app, loadErr := a.loadApplication(id); loadErr == nil {
				entry.Name = app.Config.Name
			}
			a.appsMu.Unlock()
			results[index] = entry
		}(index, id)
	}
	wait.Wait()

	sort.Slice(results, func(i, j int) bool {
		if results[i].Name != results[j].Name {
			return results[i].Name < results[j].Name
		}
		return results[i].ApplicationID < results[j].ApplicationID
	})
	return results, nil
}
