package swarmcd

import (
	"context"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/swarm"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestServiceCollector(t *testing.T) {
	cleanup := SetStackStatusForTest(map[string]*StackStatus{"media": {}})
	defer cleanup()

	replicas := uint64(2)
	service := func(name, stack string, running uint64) swarm.Service {
		svc := swarm.Service{ServiceStatus: &swarm.ServiceStatus{RunningTasks: running, DesiredTasks: replicas}}
		svc.Spec.Name = name
		svc.Spec.Labels = map[string]string{dockerStackNamespaceLabel: stack}
		svc.Spec.Mode.Replicated = &swarm.ReplicatedService{Replicas: &replicas}
		return svc
	}
	collector := serviceCollector{listServices: func(context.Context) ([]swarm.Service, error) {
		return []swarm.Service{
			service("media_plex", "media", 2),
			service("media_sonarr", "media", 1),
			service("other_web", "unmanaged", 2),
		}, nil
	}}

	expected := `
# HELP swarmcd_service_replicas_running Running tasks of a service in a managed stack.
# TYPE swarmcd_service_replicas_running gauge
swarmcd_service_replicas_running{service="media_plex",stack="media"} 2
swarmcd_service_replicas_running{service="media_sonarr",stack="media"} 1
`
	if err := testutil.CollectAndCompare(collector, strings.NewReader(expected), "swarmcd_service_replicas_running"); err != nil {
		t.Error(err)
	}
	expected = `
# HELP swarmcd_service_health Health of a service in a managed stack (healthy, progressing, degraded); 1 for the current state.
# TYPE swarmcd_service_health gauge
swarmcd_service_health{health="degraded",service="media_plex",stack="media"} 0
swarmcd_service_health{health="degraded",service="media_sonarr",stack="media"} 0
swarmcd_service_health{health="healthy",service="media_plex",stack="media"} 1
swarmcd_service_health{health="healthy",service="media_sonarr",stack="media"} 0
swarmcd_service_health{health="progressing",service="media_plex",stack="media"} 0
swarmcd_service_health{health="progressing",service="media_sonarr",stack="media"} 1
`
	if err := testutil.CollectAndCompare(collector, strings.NewReader(expected), "swarmcd_service_health"); err != nil {
		t.Error(err)
	}
}
