package swarmcd

import (
	"time"

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
