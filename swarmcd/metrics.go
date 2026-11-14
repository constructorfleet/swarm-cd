package swarmcd

import (
	"context"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/swarm"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const metricsNamespace = "swarmcd"

const (
	updateResultSuccess  = "success"
	updateResultError    = "error"
	updateResultDeferred = "deferred"
)

var (
	stackUpdatesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Name:      "stack_updates_total",
		Help:      "Stack update cycles by result (success, error, deferred).",
	}, []string{"stack", "result"})

	stackUpdateDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: metricsNamespace,
		Name:      "stack_update_duration_seconds",
		Help:      "Duration of stack update cycles, including git fetch and deploy.",
		Buckets:   []float64{0.5, 1, 2.5, 5, 10, 30, 60, 120, 300},
	}, []string{"stack"})

	stackLastSuccess = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Name:      "stack_last_success_timestamp_seconds",
		Help:      "Unix time of the last successful stack update cycle.",
	}, []string{"stack"})

	stackSyncError = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Name:      "stack_sync_error",
		Help:      "1 if the last update cycle of the stack failed, 0 otherwise.",
	}, []string{"stack"})

	stackDeferred = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Name:      "stack_deferred",
		Help:      "1 if the stack update window is closed, 0 otherwise.",
	}, []string{"stack"})

	serviceDeferred = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Name:      "service_deferred",
		Help:      "1 for each service held back because its update window is closed.",
	}, []string{"stack", "service"})

	stackInfo = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Name:      "stack_info",
		Help:      "Static information about a managed stack; always 1.",
	}, []string{"stack", "repo", "ref", "revision"})
)

var buildInfo = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Namespace: metricsNamespace,
	Name:      "build_info",
	Help:      "SwarmCD build information; always 1.",
}, []string{"version"})

var (
	serviceReplicasRunningDesc = prometheus.NewDesc(
		prometheus.BuildFQName(metricsNamespace, "service", "replicas_running"),
		"Running tasks of a service in a managed stack.",
		[]string{"stack", "service"}, nil)
	serviceReplicasDesiredDesc = prometheus.NewDesc(
		prometheus.BuildFQName(metricsNamespace, "service", "replicas_desired"),
		"Desired tasks of a service in a managed stack.",
		[]string{"stack", "service"}, nil)
	serviceHealthDesc = prometheus.NewDesc(
		prometheus.BuildFQName(metricsNamespace, "service", "health"),
		"Health of a service in a managed stack (healthy, progressing, degraded); 1 for the current state.",
		[]string{"stack", "service", "health"}, nil)
)

// serviceCollector reports live replica counts and health of the services in
// managed stacks, queried from the docker daemon on each scrape.
type serviceCollector struct {
	listServices func(ctx context.Context) ([]swarm.Service, error)
}

func (collector serviceCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- serviceReplicasRunningDesc
	ch <- serviceReplicasDesiredDesc
	ch <- serviceHealthDesc
}

func (collector serviceCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), dockerQueryTimeout)
	defer cancel()
	services, err := collector.listServices(ctx)
	if err != nil {
		logger.Warn("could not list services for metrics", "error", err)
		return
	}
	managed := GetStackStatus()
	for _, svc := range services {
		stack := svc.Spec.Labels[dockerStackNamespaceLabel]
		if _, ok := managed[stack]; !ok {
			continue
		}
		status := serviceToStatus(svc)
		ch <- prometheus.MustNewConstMetric(serviceReplicasRunningDesc, prometheus.GaugeValue, float64(status.RunningTasks), stack, status.Name)
		ch <- prometheus.MustNewConstMetric(serviceReplicasDesiredDesc, prometheus.GaugeValue, float64(status.DesiredTasks), stack, status.Name)
		for _, health := range []string{HealthHealthy, HealthProgressing, HealthDegraded} {
			value := 0.0
			if status.Health == health {
				value = 1
			}
			ch <- prometheus.MustNewConstMetric(serviceHealthDesc, prometheus.GaugeValue, value, stack, status.Name, health)
		}
	}
}

func listStackServices(ctx context.Context) ([]swarm.Service, error) {
	return dockerCli.Client().ServiceList(ctx, types.ServiceListOptions{
		Filters: filters.NewArgs(filters.Arg("label", dockerStackNamespaceLabel)),
		Status:  true,
	})
}

// initMetrics registers the metrics that depend on the docker client.
func initMetrics() {
	buildInfo.WithLabelValues(Version).Set(1)
	prometheus.MustRegister(serviceCollector{listServices: listStackServices})
}

// recordStackUpdate updates the metrics after an update cycle of a stack.
func recordStackUpdate(stack string, status StackStatus, result updateResult, err error, duration time.Duration) {
	stackUpdateDuration.WithLabelValues(stack).Observe(duration.Seconds())

	if err != nil {
		stackUpdatesTotal.WithLabelValues(stack, updateResultError).Inc()
		stackSyncError.WithLabelValues(stack).Set(1)
		return
	}
	stackSyncError.WithLabelValues(stack).Set(0)

	serviceDeferred.DeletePartialMatch(prometheus.Labels{"stack": stack})
	for service := range result.DeferredServices {
		serviceDeferred.WithLabelValues(stack, service).Set(1)
	}

	if result.DeferredUntil != nil {
		stackUpdatesTotal.WithLabelValues(stack, updateResultDeferred).Inc()
		stackDeferred.WithLabelValues(stack).Set(1)
		return
	}
	stackDeferred.WithLabelValues(stack).Set(0)
	stackUpdatesTotal.WithLabelValues(stack, updateResultSuccess).Inc()
	stackLastSuccess.WithLabelValues(stack).SetToCurrentTime()

	stackInfo.DeletePartialMatch(prometheus.Labels{"stack": stack})
	stackInfo.WithLabelValues(stack, status.RepoURL, status.RefType+":"+status.RefValue, status.Revision).Set(1)
}
