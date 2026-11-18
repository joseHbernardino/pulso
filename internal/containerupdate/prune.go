package containerupdate

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// Resource kinds that cleanup can act on.
const (
	ResourceContainers = "containers"
	ResourceImages     = "images"
	ResourceVolumes    = "volumes"
	ResourceNetworks   = "networks"
	ResourceBuildCache = "build-cache"
)

// PruneResources lists the kinds cleanup understands.
func PruneResources() []string {
	return []string{ResourceContainers, ResourceImages, ResourceVolumes, ResourceNetworks, ResourceBuildCache}
}

// DiskUsage summarises what the Engine is holding.
type DiskUsage struct {
	LayersSize      int64 `json:"layers_size"`
	ImagesSize      int64 `json:"images_size"`
	ImagesCount     int   `json:"images_count"`
	ContainersCount int   `json:"containers_count"`
	VolumesSize     int64 `json:"volumes_size"`
	VolumesCount    int   `json:"volumes_count"`
	BuildCacheSize  int64 `json:"build_cache_size"`
	BuildCacheCount int   `json:"build_cache_count"`

	// Reclaimable is what cleanup would free, honouring every protection.
	Reclaimable DiskReclaimable `json:"reclaimable"`
}

// DiskReclaimable is the freeable space per resource kind.
type DiskReclaimable struct {
	Images     int64 `json:"images"`
	Volumes    int64 `json:"volumes"`
	BuildCache int64 `json:"build_cache"`
	Containers int64 `json:"containers"`
	Total      int64 `json:"total"`
}

// PruneCandidate is one resource cleanup would remove.
type PruneCandidate struct {
	Resource    string `json:"resource"`
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	Size        int64  `json:"size"`
	Description string `json:"description,omitempty"`
}

// PruneResult reports a cleanup run, or a simulation of one.
type PruneResult struct {
	DryRun     bool             `json:"dry_run"`
	Candidates []PruneCandidate `json:"candidates"`
	Removed    []PruneCandidate `json:"removed,omitempty"`
	Failed     []PruneFailure   `json:"failed,omitempty"`
	Reclaimed  int64            `json:"reclaimed"`
	Skipped    []PruneSkipped   `json:"skipped,omitempty"`
}

// PruneFailure is one resource the Engine refused to remove.
type PruneFailure struct {
	Resource string `json:"resource"`
	ID       string `json:"id"`
	Name     string `json:"name,omitempty"`
	Error    string `json:"error"`
}

// PruneSkipped is one protected resource cleanup deliberately left alone.
type PruneSkipped struct {
	Resource string `json:"resource"`
	ID       string `json:"id"`
	Name     string `json:"name,omitempty"`
	Reason   string `json:"reason"`
}

type rawBuildCacheEntry struct {
	ID          string `json:"ID"`
	Type        string `json:"Type"`
	Description string `json:"Description"`
	InUse       bool   `json:"InUse"`
	Shared      bool   `json:"Shared"`
	Size        int64  `json:"Size"`
}

type rawDiskUsage struct {
	LayersSize int64                `json:"LayersSize"`
	Images     []rawImage           `json:"Images"`
	Containers []rawContainer       `json:"Containers"`
	Volumes    []rawVolume          `json:"Volumes"`
	BuildCache []rawBuildCacheEntry `json:"BuildCache"`
}

func (d *Engine) diskUsage(ctx context.Context) (*rawDiskUsage, error) {
	var usage rawDiskUsage
	if err := d.request(ctx, "GET", "/system/df", nil, &usage); err != nil {
		return nil, err
	}
	return &usage, nil
}

// pruneBuildCache asks BuildKit to drop its cache. Build cache has no
// ownership: it is derived data and always safe to discard.
func (d *Engine) pruneBuildCache(ctx context.Context) (int64, error) {
	var result struct {
		SpaceReclaimed int64 `json:"SpaceReclaimed"`
	}
	if err := d.request(ctx, "POST", "/build/prune", map[string]any{}, &result); err != nil {
		return 0, err
	}
	return result.SpaceReclaimed, nil
}

// DiskUsage reports what the Engine holds and what cleanup could free.
func (a *Agent) DiskUsage(ctx context.Context) (*DiskUsage, error) {
	usage, err := a.engine.diskUsage(ctx)
	if err != nil {
		return nil, err
	}
	summary := &DiskUsage{
		LayersSize:      usage.LayersSize,
		ImagesCount:     len(usage.Images),
		ContainersCount: len(usage.Containers),
		VolumesCount:    len(usage.Volumes),
		BuildCacheCount: len(usage.BuildCache),
	}
	for _, image := range usage.Images {
		summary.ImagesSize += image.Size
	}
	for _, volume := range usage.Volumes {
		if volume.UsageData != nil {
			summary.VolumesSize += volume.UsageData.Size
		}
	}
	for _, entry := range usage.BuildCache {
		if !entry.Shared {
			summary.BuildCacheSize += entry.Size
		}
	}

	// O espaço recuperável vem dos mesmos candidatos que a limpeza usaria,
	// então o número exibido é o número que a limpeza realmente libera.
	candidates, _ := a.pruneCandidates(ctx, usage, PruneResources())
	for _, candidate := range candidates {
		switch candidate.Resource {
		case ResourceImages:
			summary.Reclaimable.Images += candidate.Size
		case ResourceVolumes:
			summary.Reclaimable.Volumes += candidate.Size
		case ResourceBuildCache:
			summary.Reclaimable.BuildCache += candidate.Size
		case ResourceContainers:
			summary.Reclaimable.Containers += candidate.Size
		}
	}
	summary.Reclaimable.Total = summary.Reclaimable.Images + summary.Reclaimable.Volumes +
		summary.Reclaimable.BuildCache + summary.Reclaimable.Containers
	return summary, nil
}

// pruneCandidates computes exactly what would be removed.
//
// Docker's own prune endpoints are all-or-nothing and cannot honour these
// protections, so candidates are computed here and removed one by one. That
// is what makes a truthful dry run possible.
func (a *Agent) pruneCandidates(ctx context.Context, usage *rawDiskUsage, resources []string) ([]PruneCandidate, []PruneSkipped) {
	wanted := map[string]bool{}
	for _, resource := range resources {
		wanted[resource] = true
	}

	candidates := []PruneCandidate{}
	skipped := []PruneSkipped{}

	protectedImages := a.infrastructureImages(usage.Containers)

	if wanted[ResourceContainers] {
		for _, container := range usage.Containers {
			ownership, _ := a.classify(container.Labels)
			if container.State == "running" || container.State == "restarting" || container.State == "paused" {
				continue
			}
			name := firstName(container.Names)
			if err := containerActionAllowed(ownership, "remove"); err != nil {
				skipped = append(skipped, PruneSkipped{Resource: ResourceContainers, ID: container.ID, Name: name, Reason: err.Error()})
				continue
			}
			candidates = append(candidates, PruneCandidate{
				Resource: ResourceContainers, ID: container.ID, Name: name,
				Description: container.Status,
			})
		}
	}

	if wanted[ResourceImages] {
		for _, image := range usage.Images {
			tags := usableTags(image.RepoTags)
			name := strings.Join(tags, ", ")
			if protectedImages[image.ID] {
				skipped = append(skipped, PruneSkipped{Resource: ResourceImages, ID: image.ID, Name: name, Reason: "é a imagem do painel ou do agente do Pulso"})
				continue
			}
			if image.Containers > 0 {
				continue
			}
			// Apenas imagens sem tag (órfãs) são candidatas. Remover imagens
			// com tag apagaria o que foi baixado de propósito.
			if len(tags) > 0 {
				continue
			}
			candidates = append(candidates, PruneCandidate{
				Resource: ResourceImages, ID: image.ID, Name: name,
				Size: image.Size, Description: "imagem sem tag",
			})
		}
	}

	if wanted[ResourceVolumes] {
		for _, volume := range usage.Volumes {
			if reason := a.volumeProtection(volume); reason != "" {
				// Volume em uso não é anomalia; não polui a lista de ignorados.
				if volume.UsageData == nil || volume.UsageData.RefCount == 0 {
					skipped = append(skipped, PruneSkipped{Resource: ResourceVolumes, ID: volume.Name, Name: volume.Name, Reason: reason})
				}
				continue
			}
			size := int64(0)
			if volume.UsageData != nil {
				size = volume.UsageData.Size
			}
			candidates = append(candidates, PruneCandidate{
				Resource: ResourceVolumes, ID: volume.Name, Name: volume.Name,
				Size: size, Description: "volume sem uso",
			})
		}
	}

	if wanted[ResourceNetworks] {
		networks, err := a.engine.listNetworks(ctx)
		if err == nil {
			for _, network := range networks {
				if reason := a.networkProtection(network); reason != "" {
					if len(network.Containers) == 0 && !predefinedNetworks[network.Name] {
						skipped = append(skipped, PruneSkipped{Resource: ResourceNetworks, ID: network.ID, Name: network.Name, Reason: reason})
					}
					continue
				}
				candidates = append(candidates, PruneCandidate{
					Resource: ResourceNetworks, ID: network.ID, Name: network.Name,
					Description: "rede sem contêineres",
				})
			}
		}
	}

	if wanted[ResourceBuildCache] {
		var total int64
		var count int
		for _, entry := range usage.BuildCache {
			if entry.InUse || entry.Shared {
				continue
			}
			total += entry.Size
			count++
		}
		if count > 0 {
			candidates = append(candidates, PruneCandidate{
				Resource: ResourceBuildCache, ID: "build-cache", Name: "cache de compilação",
				Size: total, Description: fmt.Sprintf("%d registros sem uso", count),
			})
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Size != candidates[j].Size {
			return candidates[i].Size > candidates[j].Size
		}
		return candidates[i].ID < candidates[j].ID
	})
	return candidates, skipped
}

// Prune removes the unused resources of the requested kinds. With dryRun the
// candidates are only reported, and nothing is touched.
func (a *Agent) Prune(ctx context.Context, resources []string, dryRun bool) (*PruneResult, error) {
	if len(resources) == 0 {
		return nil, fmt.Errorf("informe ao menos um tipo de recurso para limpar")
	}
	for _, resource := range resources {
		known := false
		for _, candidate := range PruneResources() {
			if resource == candidate {
				known = true
				break
			}
		}
		if !known {
			return nil, fmt.Errorf("tipo de recurso desconhecido: %q", resource)
		}
	}

	usage, err := a.engine.diskUsage(ctx)
	if err != nil {
		return nil, err
	}
	candidates, skipped := a.pruneCandidates(ctx, usage, resources)

	result := &PruneResult{DryRun: dryRun, Candidates: candidates, Skipped: skipped}
	if dryRun {
		for _, candidate := range candidates {
			result.Reclaimed += candidate.Size
		}
		return result, nil
	}

	for _, candidate := range candidates {
		var removeErr error
		switch candidate.Resource {
		case ResourceContainers:
			removeErr = a.engine.remove(ctx, candidate.ID)
		case ResourceImages:
			removeErr = a.engine.removeImage(ctx, candidate.ID)
		case ResourceVolumes:
			removeErr = a.engine.removeVolume(ctx, candidate.ID)
		case ResourceNetworks:
			removeErr = a.engine.removeNetwork(ctx, candidate.ID)
		case ResourceBuildCache:
			reclaimed, err := a.engine.pruneBuildCache(ctx)
			removeErr = err
			if err == nil {
				entry := candidate
				entry.Size = reclaimed
				result.Removed = append(result.Removed, entry)
				result.Reclaimed += reclaimed
				continue
			}
		}
		if removeErr != nil {
			result.Failed = append(result.Failed, PruneFailure{
				Resource: candidate.Resource, ID: candidate.ID,
				Name: candidate.Name, Error: removeErr.Error(),
			})
			continue
		}
		result.Removed = append(result.Removed, candidate)
		result.Reclaimed += candidate.Size
	}
	return result, nil
}

func firstName(names []string) string {
	trimmed := trimNames(names)
	if len(trimmed) == 0 {
		return ""
	}
	return trimmed[0]
}
