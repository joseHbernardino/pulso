package containerupdate

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// Ownership classifies a Docker resource relative to this Pulso install.
// It drives both what the dashboard shows and what it is allowed to change.
const (
	// OwnershipInfrastructure is the panel or its agent. Nothing may change
	// these through the inventory: stopping the agent would brick the panel.
	OwnershipInfrastructure = "infrastructure"
	// OwnershipApplication is a container Pulso deployed as a project. It may
	// be started and stopped here, but not removed: removing it behind the
	// registry's back would orphan the project record.
	OwnershipApplication = "application"
	// OwnershipForeign is everything else on the Engine. Full control.
	OwnershipForeign = "foreign"
)

// infrastructureLabel marks the panel and agent containers. Installs made
// with Compose predate this label, so ownership also falls back to the
// Compose project and service names.
const infrastructureLabel = "io.pulso.infrastructure"

// PortBinding is one published port.
type PortBinding struct {
	IP          string `json:"ip,omitempty"`
	PrivatePort int    `json:"private_port"`
	PublicPort  int    `json:"public_port,omitempty"`
	Type        string `json:"type,omitempty"`
}

// ContainerSummary is one container as the inventory lists it. The Docker
// list endpoint does not return the environment, so a summary never carries
// a secret.
type ContainerSummary struct {
	ID             string            `json:"id"`
	Names          []string          `json:"names"`
	Image          string            `json:"image"`
	ImageID        string            `json:"image_id,omitempty"`
	Command        string            `json:"command,omitempty"`
	Created        int64             `json:"created"`
	State          string            `json:"state"`
	Status         string            `json:"status,omitempty"`
	Ports          []PortBinding     `json:"ports,omitempty"`
	Labels         map[string]string `json:"labels,omitempty"`
	Ownership      string            `json:"ownership"`
	ApplicationID  string            `json:"application_id,omitempty"`
	ComposeProject string            `json:"compose_project,omitempty"`
	Protected      bool              `json:"protected"`
	ProtectReason  string            `json:"protect_reason,omitempty"`
}

// ImageSummary is one image.
type ImageSummary struct {
	ID            string            `json:"id"`
	RepoTags      []string          `json:"repo_tags,omitempty"`
	RepoDigests   []string          `json:"repo_digests,omitempty"`
	Created       int64             `json:"created"`
	Size          int64             `json:"size"`
	Containers    int64             `json:"containers"`
	Labels        map[string]string `json:"labels,omitempty"`
	InUse         bool              `json:"in_use"`
	Dangling      bool              `json:"dangling"`
	Protected     bool              `json:"protected"`
	ProtectReason string            `json:"protect_reason,omitempty"`
}

// VolumeSummary is one volume.
type VolumeSummary struct {
	Name          string            `json:"name"`
	Driver        string            `json:"driver,omitempty"`
	Mountpoint    string            `json:"mountpoint,omitempty"`
	CreatedAt     string            `json:"created_at,omitempty"`
	Labels        map[string]string `json:"labels,omitempty"`
	Size          int64             `json:"size"`
	RefCount      int64             `json:"ref_count"`
	InUse         bool              `json:"in_use"`
	Protected     bool              `json:"protected"`
	ProtectReason string            `json:"protect_reason,omitempty"`
}

// NetworkSummary is one network.
type NetworkSummary struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	Driver        string            `json:"driver,omitempty"`
	Scope         string            `json:"scope,omitempty"`
	Internal      bool              `json:"internal"`
	Labels        map[string]string `json:"labels,omitempty"`
	Containers    int               `json:"containers"`
	Predefined    bool              `json:"predefined"`
	Protected     bool              `json:"protected"`
	ProtectReason string            `json:"protect_reason,omitempty"`
}

// predefinedNetworks cannot be removed by Docker at all.
var predefinedNetworks = map[string]bool{"bridge": true, "host": true, "none": true}

// ---------------------------------------------------------- Engine listing

// rawContainer mirrors the fields read from GET /containers/json.
type rawContainer struct {
	ID      string            `json:"Id"`
	Names   []string          `json:"Names"`
	Image   string            `json:"Image"`
	ImageID string            `json:"ImageID"`
	Command string            `json:"Command"`
	Created int64             `json:"Created"`
	State   string            `json:"State"`
	Status  string            `json:"Status"`
	Labels  map[string]string `json:"Labels"`
	Ports   []struct {
		IP          string `json:"IP"`
		PrivatePort int    `json:"PrivatePort"`
		PublicPort  int    `json:"PublicPort"`
		Type        string `json:"Type"`
	} `json:"Ports"`
}

func (d *Engine) listContainers(ctx context.Context, all bool) ([]rawContainer, error) {
	path := "/containers/json"
	if all {
		path += "?all=true"
	}
	var result []rawContainer
	if err := d.request(ctx, "GET", path, nil, &result); err != nil {
		return nil, err
	}
	return result, nil
}

type rawImage struct {
	ID          string            `json:"Id"`
	RepoTags    []string          `json:"RepoTags"`
	RepoDigests []string          `json:"RepoDigests"`
	Created     int64             `json:"Created"`
	Size        int64             `json:"Size"`
	Containers  int64             `json:"Containers"`
	Labels      map[string]string `json:"Labels"`
}

func (d *Engine) listImages(ctx context.Context) ([]rawImage, error) {
	var result []rawImage
	if err := d.request(ctx, "GET", "/images/json?all=false", nil, &result); err != nil {
		return nil, err
	}
	return result, nil
}

type rawVolume struct {
	Name       string            `json:"Name"`
	Driver     string            `json:"Driver"`
	Mountpoint string            `json:"Mountpoint"`
	CreatedAt  string            `json:"CreatedAt"`
	Labels     map[string]string `json:"Labels"`
	UsageData  *struct {
		Size     int64 `json:"Size"`
		RefCount int64 `json:"RefCount"`
	} `json:"UsageData"`
}

func (d *Engine) listVolumes(ctx context.Context) ([]rawVolume, error) {
	var payload struct {
		Volumes []rawVolume `json:"Volumes"`
	}
	if err := d.request(ctx, "GET", "/volumes", nil, &payload); err != nil {
		return nil, err
	}
	return payload.Volumes, nil
}

type rawNetwork struct {
	ID         string            `json:"Id"`
	Name       string            `json:"Name"`
	Driver     string            `json:"Driver"`
	Scope      string            `json:"Scope"`
	Internal   bool              `json:"Internal"`
	Labels     map[string]string `json:"Labels"`
	Containers map[string]any    `json:"Containers"`
}

func (d *Engine) listNetworks(ctx context.Context) ([]rawNetwork, error) {
	var result []rawNetwork
	if err := d.request(ctx, "GET", "/networks", nil, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (d *Engine) removeImage(ctx context.Context, id string) error {
	return d.request(ctx, "DELETE", "/images/"+url.PathEscape(id), nil, nil)
}

func (d *Engine) removeVolume(ctx context.Context, name string) error {
	return d.request(ctx, "DELETE", "/volumes/"+url.PathEscape(name), nil, nil)
}

func (d *Engine) removeNetwork(ctx context.Context, id string) error {
	return d.request(ctx, "DELETE", "/networks/"+url.PathEscape(id), nil, nil)
}

func (d *Engine) restartContainer(ctx context.Context, id string) error {
	return d.request(ctx, "POST", "/containers/"+url.PathEscape(id)+"/restart", nil, nil)
}

// ------------------------------------------------------------- ownership

// classify decides what a container is relative to this install.
func (a *Agent) classify(labels map[string]string) (ownership, applicationID string) {
	if labels == nil {
		return OwnershipForeign, ""
	}
	if labels[infrastructureLabel] != "" {
		return OwnershipInfrastructure, ""
	}
	// Instalações por Compose não têm o rótulo de infraestrutura; o par
	// projeto + serviço identifica o painel e o agente.
	if labels[projectLabel] == a.project {
		switch labels[serviceLabel] {
		case "pulso", "pulso-updater":
			return OwnershipInfrastructure, ""
		}
	}
	if labels[applicationOwner] == a.project {
		return OwnershipApplication, labels[applicationLabel]
	}
	return OwnershipForeign, ""
}

// containerAction reports whether an action is permitted on a container with
// the given ownership, and why not when it is refused.
func containerActionAllowed(ownership, action string) error {
	switch ownership {
	case OwnershipInfrastructure:
		return fmt.Errorf("este contêiner é o painel ou o agente do Pulso; alterá-lo daqui interromperia o próprio painel")
	case OwnershipApplication:
		if action == "remove" {
			return fmt.Errorf("este contêiner pertence a um projeto do Pulso; remova-o pela página Projetos para não deixar o cadastro órfão")
		}
	}
	return nil
}

// infrastructureImages returns the image IDs in use by the panel and agent,
// so they are never offered for removal.
func (a *Agent) infrastructureImages(containers []rawContainer) map[string]bool {
	protected := map[string]bool{}
	for _, container := range containers {
		ownership, _ := a.classify(container.Labels)
		if ownership == OwnershipInfrastructure {
			if container.ImageID != "" {
				protected[container.ImageID] = true
			}
			if container.Image != "" {
				protected[container.Image] = true
			}
		}
	}
	return protected
}

// volumeProtection explains why a volume must not be removed, or returns an
// empty string when removal is allowed.
func (a *Agent) volumeProtection(volume rawVolume) string {
	// O rótulo é a proteção primária: vale mesmo que a instalação seja
	// renomeada ou o volume tenha outro nome. A comparação por nome fica
	// como rede de segurança para instalações anteriores ao rótulo.
	if volume.Labels[infrastructureLabel] != "" {
		return "guarda os dados desta instalação do Pulso"
	}
	// Os volumes de dados e do socket do agente carregam as contas, os
	// cadastros e a auditoria desta instalação.
	for _, suffix := range []string{"_data", "_updates"} {
		if volume.Name == a.project+suffix {
			return "guarda os dados desta instalação do Pulso"
		}
	}
	if volume.Labels[applicationOwner] == a.project {
		return "guarda os dados de um projeto do Pulso; remova o projeto pela página Projetos"
	}
	if volume.Labels[projectLabel] == a.project {
		return "pertence à instalação do Pulso"
	}
	if volume.UsageData != nil && volume.UsageData.RefCount > 0 {
		return "está em uso por um contêiner"
	}
	return ""
}

// networkProtection explains why a network must not be removed.
func (a *Agent) networkProtection(network rawNetwork) string {
	if predefinedNetworks[network.Name] {
		return "rede predefinida do Docker"
	}
	if network.Labels[infrastructureLabel] != "" {
		return "pertence à instalação do Pulso"
	}
	if network.Name == a.project+"-applications" {
		return "é a rede dos projetos do Pulso"
	}
	if network.Labels[applicationOwner] == a.project || network.Labels[projectLabel] == a.project {
		return "pertence à instalação do Pulso"
	}
	if len(network.Containers) > 0 {
		return "possui contêineres conectados"
	}
	return ""
}

// ------------------------------------------------------------- inventory

// Inventory is the whole Docker inventory in one response, so the dashboard
// renders its overview from a single request.
type Inventory struct {
	Containers []ContainerSummary `json:"containers"`
	Images     []ImageSummary     `json:"images"`
	Volumes    []VolumeSummary    `json:"volumes"`
	Networks   []NetworkSummary   `json:"networks"`
}

// containerInventory lists every container on the Engine, including the ones
// Pulso did not create, each classified and marked as protected or not.
func (a *Agent) containerInventory(ctx context.Context) ([]ContainerSummary, error) {
	raw, err := a.listContainersRaw(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]ContainerSummary, 0, len(raw))
	for _, container := range raw {
		ownership, applicationID := a.classify(container.Labels)
		summary := ContainerSummary{
			ID:             container.ID,
			Names:          trimNames(container.Names),
			Image:          container.Image,
			ImageID:        container.ImageID,
			Command:        container.Command,
			Created:        container.Created,
			State:          container.State,
			Status:         container.Status,
			Labels:         container.Labels,
			Ownership:      ownership,
			ApplicationID:  applicationID,
			ComposeProject: container.Labels[projectLabel],
		}
		for _, port := range container.Ports {
			summary.Ports = append(summary.Ports, PortBinding{
				IP: port.IP, PrivatePort: port.PrivatePort,
				PublicPort: port.PublicPort, Type: port.Type,
			})
		}
		if err := containerActionAllowed(ownership, "remove"); err != nil {
			summary.Protected = true
			summary.ProtectReason = err.Error()
		}
		result = append(result, summary)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Created != result[j].Created {
			return result[i].Created > result[j].Created
		}
		return result[i].ID < result[j].ID
	})
	return result, nil
}

func (a *Agent) listContainersRaw(ctx context.Context) ([]rawContainer, error) {
	return a.engine.listContainers(ctx, true)
}

func (a *Agent) imageInventory(ctx context.Context) ([]ImageSummary, error) {
	images, err := a.engine.listImages(ctx)
	if err != nil {
		return nil, err
	}
	containers, err := a.listContainersRaw(ctx)
	if err != nil {
		return nil, err
	}
	protectedImages := a.infrastructureImages(containers)

	result := make([]ImageSummary, 0, len(images))
	for _, image := range images {
		summary := ImageSummary{
			ID:          image.ID,
			RepoTags:    usableTags(image.RepoTags),
			RepoDigests: image.RepoDigests,
			Created:     image.Created,
			Size:        image.Size,
			Containers:  image.Containers,
			Labels:      image.Labels,
			InUse:       image.Containers > 0,
		}
		summary.Dangling = len(summary.RepoTags) == 0
		if protectedImages[image.ID] {
			summary.Protected = true
			summary.ProtectReason = "é a imagem do painel ou do agente do Pulso"
		} else if summary.InUse {
			summary.Protected = true
			summary.ProtectReason = "está em uso por um contêiner"
		}
		result = append(result, summary)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Size != result[j].Size {
			return result[i].Size > result[j].Size
		}
		return result[i].ID < result[j].ID
	})
	return result, nil
}

func (a *Agent) volumeInventory(ctx context.Context) ([]VolumeSummary, error) {
	volumes, err := a.engine.listVolumes(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]VolumeSummary, 0, len(volumes))
	for _, volume := range volumes {
		summary := VolumeSummary{
			Name:       volume.Name,
			Driver:     volume.Driver,
			Mountpoint: volume.Mountpoint,
			CreatedAt:  volume.CreatedAt,
			Labels:     volume.Labels,
		}
		if volume.UsageData != nil {
			summary.Size = volume.UsageData.Size
			summary.RefCount = volume.UsageData.RefCount
			summary.InUse = volume.UsageData.RefCount > 0
		}
		if reason := a.volumeProtection(volume); reason != "" {
			summary.Protected = true
			summary.ProtectReason = reason
		}
		result = append(result, summary)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

func (a *Agent) networkInventory(ctx context.Context) ([]NetworkSummary, error) {
	networks, err := a.engine.listNetworks(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]NetworkSummary, 0, len(networks))
	for _, network := range networks {
		summary := NetworkSummary{
			ID:         network.ID,
			Name:       network.Name,
			Driver:     network.Driver,
			Scope:      network.Scope,
			Internal:   network.Internal,
			Labels:     network.Labels,
			Containers: len(network.Containers),
			Predefined: predefinedNetworks[network.Name],
		}
		if reason := a.networkProtection(network); reason != "" {
			summary.Protected = true
			summary.ProtectReason = reason
		}
		result = append(result, summary)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

// --------------------------------------------------------------- actions

// inventoryContainerAction applies an action to any container on the Engine,
// after checking that the inventory is allowed to touch it.
func (a *Agent) inventoryContainerAction(ctx context.Context, id, action string) error {
	current, err := a.engine.inspect(ctx, id)
	if err != nil {
		return err
	}
	ownership, _ := a.classify(stringMap(current.Config["Labels"]))
	if err := containerActionAllowed(ownership, action); err != nil {
		return err
	}
	switch action {
	case "start":
		return a.engine.start(ctx, current.ID)
	case "stop":
		return a.engine.stop(ctx, current.ID)
	case "restart":
		return a.engine.restartContainer(ctx, current.ID)
	case "remove":
		return a.engine.remove(ctx, current.ID)
	}
	return fmt.Errorf("ação inválida: %s", action)
}

// removeInventoryImage removes one image unless it is protected.
func (a *Agent) removeInventoryImage(ctx context.Context, id string) error {
	images, err := a.imageInventory(ctx)
	if err != nil {
		return err
	}
	for _, image := range images {
		if image.ID == id || matchesTag(image.RepoTags, id) {
			if image.Protected {
				return fmt.Errorf("a imagem não pode ser removida: %s", image.ProtectReason)
			}
			return a.engine.removeImage(ctx, image.ID)
		}
	}
	return fmt.Errorf("imagem não encontrada")
}

func (a *Agent) removeInventoryVolume(ctx context.Context, name string) error {
	volumes, err := a.engine.listVolumes(ctx)
	if err != nil {
		return err
	}
	for _, volume := range volumes {
		if volume.Name != name {
			continue
		}
		if reason := a.volumeProtection(volume); reason != "" {
			return fmt.Errorf("o volume não pode ser removido: %s", reason)
		}
		return a.engine.removeVolume(ctx, volume.Name)
	}
	return fmt.Errorf("volume não encontrado")
}

func (a *Agent) removeInventoryNetwork(ctx context.Context, id string) error {
	networks, err := a.engine.listNetworks(ctx)
	if err != nil {
		return err
	}
	for _, network := range networks {
		if network.ID != id && network.Name != id {
			continue
		}
		if reason := a.networkProtection(network); reason != "" {
			return fmt.Errorf("a rede não pode ser removida: %s", reason)
		}
		return a.engine.removeNetwork(ctx, network.ID)
	}
	return fmt.Errorf("rede não encontrada")
}

// ContainerMount is one mount of an inspected container.
type ContainerMount struct {
	Type        string `json:"type,omitempty"`
	Name        string `json:"name,omitempty"`
	Source      string `json:"source,omitempty"`
	Destination string `json:"destination,omitempty"`
	ReadWrite   bool   `json:"read_write"`
}

// ContainerDetails is one inspected container.
//
// Env carries the container's environment, which routinely holds credentials.
// The panel redacts it for anyone who is not an administrator; nothing here
// may be exposed to a read-only role.
type ContainerDetails struct {
	ContainerSummary
	Env           []string         `json:"env,omitempty"`
	Mounts        []ContainerMount `json:"mounts,omitempty"`
	Networks      []string         `json:"networks,omitempty"`
	RestartPolicy string           `json:"restart_policy,omitempty"`
	RestartCount  int              `json:"restart_count"`
	Health        string           `json:"health,omitempty"`
	Running       bool             `json:"running"`
	ExitCode      int              `json:"exit_code"`
}

// inventoryContainerDetails inspects any container on the Engine.
func (a *Agent) inventoryContainerDetails(ctx context.Context, id string) (*ContainerDetails, error) {
	current, err := a.engine.inspect(ctx, id)
	if err != nil {
		return nil, err
	}
	labels := stringMap(current.Config["Labels"])
	ownership, applicationID := a.classify(labels)

	details := &ContainerDetails{
		ContainerSummary: ContainerSummary{
			ID:             current.ID,
			Names:          trimNames([]string{current.Name}),
			Image:          stringValue(current.Config["Image"]),
			Labels:         labels,
			Ownership:      ownership,
			ApplicationID:  applicationID,
			ComposeProject: labels[projectLabel],
			State:          stateName(current.State.Running, current.State.Restarting),
		},
		RestartCount: current.RestartCount,
		Running:      current.State.Running,
		ExitCode:     current.State.ExitCode,
	}
	if current.State.Health != nil {
		details.Health = current.State.Health.Status
	}
	if err := containerActionAllowed(ownership, "remove"); err != nil {
		details.Protected = true
		details.ProtectReason = err.Error()
	}
	for _, value := range anySlice(current.Config["Env"]) {
		if text, ok := value.(string); ok {
			details.Env = append(details.Env, text)
		}
	}
	for _, mount := range current.Mounts {
		details.Mounts = append(details.Mounts, ContainerMount{
			Type: mount.Type, Name: mount.Name, Source: mount.Source,
			Destination: mount.Destination, ReadWrite: mount.RW,
		})
	}
	for name := range current.NetworkSettings.Networks {
		details.Networks = append(details.Networks, name)
	}
	sort.Strings(details.Networks)
	if policy, ok := current.HostConfig["RestartPolicy"].(map[string]any); ok {
		details.RestartPolicy = stringValue(policy["Name"])
	}
	return details, nil
}

func stateName(running, restarting bool) string {
	switch {
	case restarting:
		return "restarting"
	case running:
		return "running"
	}
	return "exited"
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func anySlice(value any) []any {
	items, _ := value.([]any)
	return items
}

// ----------------------------------------------------------------- helpers

// trimNames strips the leading slash Docker puts on container names.
func trimNames(names []string) []string {
	result := make([]string, 0, len(names))
	for _, name := range names {
		result = append(result, strings.TrimPrefix(name, "/"))
	}
	return result
}

// usableTags drops the "<none>:<none>" placeholder Docker reports for
// dangling images.
func usableTags(tags []string) []string {
	result := make([]string, 0, len(tags))
	for _, tag := range tags {
		if tag == "" || tag == "<none>:<none>" || tag == "<none>" {
			continue
		}
		result = append(result, tag)
	}
	return result
}

func matchesTag(tags []string, candidate string) bool {
	for _, tag := range tags {
		if tag == candidate {
			return true
		}
	}
	return false
}

// containerLogs reads the tail of any container's logs.
func (a *Agent) inventoryContainerLogs(ctx context.Context, id string, tail int) (string, error) {
	if tail <= 0 || tail > 2000 {
		tail = 200
	}
	if _, err := a.engine.inspect(ctx, id); err != nil {
		return "", err
	}
	return a.readContainerLogTail(ctx, id, tail)
}
