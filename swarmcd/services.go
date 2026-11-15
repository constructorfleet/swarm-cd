package swarmcd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/swarm"
)

// Health states for a stack service, modelled after ArgoCD-style health.
const (
	HealthHealthy     = "healthy"
	HealthProgressing = "progressing"
	HealthDegraded    = "degraded"
)

// dockerStackNamespaceLabel is the label Docker Swarm sets on every object that
// belongs to a stack deployed via `docker stack deploy`.
const dockerStackNamespaceLabel = "com.docker.stack.namespace"

// dockerQueryTimeout bounds a single Docker API call made while serving an HTTP
// request, so a slow/unreachable daemon cannot hang the request indefinitely.
const dockerQueryTimeout = 10 * time.Second

// ErrStackNotFound is returned by GetStackServices when the requested stack is
// not managed by SwarmCD. The web layer maps it to HTTP 404.
var ErrStackNotFound = errors.New("stack not found")

// ServiceStatus is the live state of a single Swarm service that belongs to a
// stack. It is the JSON contract shared with the UI (see
// ui/src/hooks/useFetchStackServices.tsx).
type ServiceStatus struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	Image        string       `json:"image"`
	Mode         string       `json:"mode"` // "replicated" | "global"
	RunningTasks uint64       `json:"running_tasks"`
	DesiredTasks uint64       `json:"desired_tasks"`
	FailedTasks  uint64       `json:"failed_tasks"`
	Health       string       `json:"health"`  // "healthy" | "progressing" | "degraded"
	Tasks        []TaskStatus `json:"tasks"`   // live tasks (containers) for the service
	Secrets      []string     `json:"secrets"` // secret names referenced by the service
	Configs      []string     `json:"configs"` // config names referenced by the service
	// set while the service is held back because its update window is closed
	DeferredUntil *time.Time `json:"deferred_until"`
}

// TaskStatus is the live state of a single Swarm task (a container instance) of
// a service. It is part of the JSON contract shared with the UI.
type TaskStatus struct {
	ID           string `json:"id"`
	Slot         int    `json:"slot"`
	NodeID       string `json:"node_id"`
	State        string `json:"state"`         // current state, e.g. "running" | "failed"
	DesiredState string `json:"desired_state"` // desired state, e.g. "running" | "shutdown"
	Error        string `json:"error"`         // task error message, if any
	ContainerID  string `json:"container_id"`
}

// deriveHealth maps running/desired task counts to an ArgoCD-style health
// string. The mapping is identical for replicated and global services (mode is
// accepted for clarity and future divergence):
//   - desired == 0          -> progressing (just created / scaled to zero)
//   - running == 0          -> degraded    (desired > 0 but nothing is running)
//   - running >= desired    -> healthy
//   - 0 < running < desired -> progressing (converging towards desired)
//
// It is intentionally pure (no logging, no I/O) so it stays unit-testable
// without a Docker daemon.
func deriveHealth(running, desired uint64, mode string) string {
	if desired == 0 {
		return HealthProgressing
	}
	if running == 0 {
		return HealthDegraded
	}
	if running >= desired {
		return HealthHealthy
	}
	return HealthProgressing
}

// GetStackServices returns the live Swarm services that belong to the given
// stack, querying the Docker daemon for the services labelled with the stack's
// namespace. It returns ErrStackNotFound when the stack is not managed by
// SwarmCD.
func GetStackServices(stackName string) ([]ServiceStatus, error) {
	log := logger.With(slog.String("stack", stackName))
	log.Debug("listing stack services")

	stateMu.RLock()
	stackState, ok := stackStatus[stackName]
	var deferredServices map[string]time.Time
	if ok {
		deferredServices = make(map[string]time.Time, len(stackState.DeferredServices))
		for service, t := range stackState.DeferredServices {
			deferredServices[service] = t
		}
	}
	stateMu.RUnlock()
	if !ok {
		log.Debug("unknown stack requested")
		return nil, fmt.Errorf("%w: %s", ErrStackNotFound, stackName)
	}

	ctx, cancel := context.WithTimeout(context.Background(), dockerQueryTimeout)
	defer cancel()

	namespaceFilter := dockerStackNamespaceLabel + "=" + stackName
	log.Debug("querying docker for services", slog.String("filter", namespaceFilter))

	services, err := dockerCli.Client().ServiceList(ctx, types.ServiceListOptions{
		Filters: filters.NewArgs(filters.Arg("label", namespaceFilter)),
		Status:  true,
	})
	if err != nil {
		log.Error("failed to list docker services", slog.Any("error", err))
		return nil, fmt.Errorf("could not list services for stack %s: %w", stackName, err)
	}

	log.Debug("docker returned services", slog.Int("count", len(services)))

	tasksByService, taskErr := stackTasksByService(ctx, stackName)
	if taskErr != nil {
		// Degrade gracefully: services still render without per-task detail.
		log.Error("failed to list docker tasks; returning services without task detail", slog.Any("error", taskErr))
		tasksByService = map[string][]swarm.Task{}
	} else {
		log.Debug("docker returned tasks", slog.Int("services_with_tasks", len(tasksByService)))
	}

	result := make([]ServiceStatus, 0, len(services))
	for _, svc := range services {
		status := serviceToStatus(svc)

		tasks := tasksByService[svc.ID]
		status.Tasks = make([]TaskStatus, 0, len(tasks))
		var failed uint64
		for _, task := range tasks {
			status.Tasks = append(status.Tasks, taskToStatus(task))
			if isFailedTaskState(task.Status.State) {
				failed++
			}
		}
		status.FailedTasks = failed
		// swarm names stack services <stack>_<service>
		if nextOpen, ok := deferredServices[strings.TrimPrefix(svc.Spec.Name, stackName+"_")]; ok {
			status.DeferredUntil = &nextOpen
		}

		log.Debug("mapped service",
			slog.String("service", status.Name),
			slog.Uint64("running", status.RunningTasks),
			slog.Uint64("desired", status.DesiredTasks),
			slog.Uint64("failed", status.FailedTasks),
			slog.Int("tasks", len(status.Tasks)),
			slog.Int("secrets", len(status.Secrets)),
			slog.String("health", status.Health),
		)
		result = append(result, status)
	}
	// Docker returns services in no particular order; keep the graph stable.
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

// stackTasksByService lists the live (desired-state=running) tasks for a stack
// and groups them by service ID, so each service can be enriched with its
// container/task detail.
func stackTasksByService(ctx context.Context, stackName string) (map[string][]swarm.Task, error) {
	filter := filters.NewArgs(
		filters.Arg("label", dockerStackNamespaceLabel+"="+stackName),
		filters.Arg("desired-state", "running"),
	)
	tasks, err := dockerCli.Client().TaskList(ctx, types.TaskListOptions{Filters: filter})
	if err != nil {
		return nil, fmt.Errorf("could not list tasks for stack %s: %w", stackName, err)
	}
	byService := make(map[string][]swarm.Task, len(tasks))
	for _, task := range tasks {
		byService[task.ServiceID] = append(byService[task.ServiceID], task)
	}
	return byService, nil
}

// serviceToStatus maps a Docker swarm.Service to the SwarmCD ServiceStatus
// contract. It is pure (no I/O) so it can be unit-tested independently.
func serviceToStatus(svc swarm.Service) ServiceStatus {
	mode := "replicated"
	if svc.Spec.Mode.Global != nil {
		mode = "global"
	}

	var running, desired uint64
	if svc.ServiceStatus != nil {
		running = svc.ServiceStatus.RunningTasks
		desired = svc.ServiceStatus.DesiredTasks
	} else if svc.Spec.Mode.Replicated != nil && svc.Spec.Mode.Replicated.Replicas != nil {
		// Fallback when the daemon did not populate ServiceStatus (older API).
		desired = *svc.Spec.Mode.Replicated.Replicas
	}

	var image string
	if svc.Spec.TaskTemplate.ContainerSpec != nil {
		image = stripImageDigest(svc.Spec.TaskTemplate.ContainerSpec.Image)
	}

	secrets, configs := serviceSecretRefs(svc)

	return ServiceStatus{
		ID:           svc.ID,
		Name:         svc.Spec.Name,
		Image:        image,
		Mode:         mode,
		RunningTasks: running,
		DesiredTasks: desired,
		Health:       deriveHealth(running, desired, mode),
		Tasks:        []TaskStatus{},
		Secrets:      secrets,
		Configs:      configs,
	}
}

// taskToStatus maps a Docker swarm.Task to the SwarmCD TaskStatus contract. It
// is pure (no I/O) so it can be unit-tested independently.
func taskToStatus(task swarm.Task) TaskStatus {
	status := TaskStatus{
		ID:           task.ID,
		Slot:         task.Slot,
		NodeID:       task.NodeID,
		State:        string(task.Status.State),
		DesiredState: string(task.DesiredState),
		Error:        task.Status.Err,
	}
	if task.Status.ContainerStatus != nil {
		status.ContainerID = task.Status.ContainerStatus.ContainerID
	}
	return status
}

// serviceSecretRefs returns the secret and config names referenced by a
// service's container spec. The returned slices are never nil so the JSON
// contract always serialises arrays (not null). Pure (no I/O).
func serviceSecretRefs(svc swarm.Service) (secrets, configs []string) {
	secrets = []string{}
	configs = []string{}
	containerSpec := svc.Spec.TaskTemplate.ContainerSpec
	if containerSpec == nil {
		return secrets, configs
	}
	for _, secret := range containerSpec.Secrets {
		secrets = append(secrets, secret.SecretName)
	}
	for _, config := range containerSpec.Configs {
		configs = append(configs, config.ConfigName)
	}
	return secrets, configs
}

// isFailedTaskState reports whether a task state represents a failed container
// (used to count FailedTasks).
func isFailedTaskState(state swarm.TaskState) bool {
	switch state {
	case swarm.TaskStateFailed, swarm.TaskStateRejected, swarm.TaskStateOrphaned:
		return true
	default:
		return false
	}
}

// stripImageDigest removes the @sha256:... digest suffix from an image
// reference for cleaner display in the UI.
func stripImageDigest(image string) string {
	if idx := strings.Index(image, "@"); idx != -1 {
		return image[:idx]
	}
	return image
}
