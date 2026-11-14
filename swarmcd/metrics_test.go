package swarmcd

import (
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestRecordStackUpdate(t *testing.T) {
	stack := "metrics-test"
	status := StackStatus{RepoURL: "https://example.com/repo.git", Ref: "branch:main", Revision: "abcd1234"}

	recordStackUpdate(stack, status, updateResult{Revision: "abcd1234"}, nil, time.Second)
	if got := testutil.ToFloat64(stackUpdatesTotal.WithLabelValues(stack, updateResultSuccess)); got != 1 {
		t.Errorf("success count = %v, want 1", got)
	}
	if got := testutil.ToFloat64(stackSyncError.WithLabelValues(stack)); got != 0 {
		t.Errorf("sync error = %v, want 0", got)
	}
	if got := testutil.ToFloat64(stackInfo.WithLabelValues(stack, status.RepoURL, status.Ref, "abcd1234")); got != 1 {
		t.Errorf("stack info = %v, want 1", got)
	}

	// a new revision replaces the previous stack_info series
	status.Revision = "ef567890"
	recordStackUpdate(stack, status, updateResult{
		Revision:         "ef567890",
		DeferredServices: map[string]time.Time{"plex": time.Now()},
	}, nil, time.Second)
	if got := testutil.CollectAndCount(stackInfo); got != 1 {
		t.Errorf("stack_info series = %d, want 1", got)
	}
	if got := testutil.ToFloat64(serviceDeferred.WithLabelValues(stack, "plex")); got != 1 {
		t.Errorf("plex deferred = %v, want 1", got)
	}

	// errors flag the stack without touching the deferred services
	recordStackUpdate(stack, status, updateResult{}, errors.New("boom"), time.Second)
	if got := testutil.ToFloat64(stackSyncError.WithLabelValues(stack)); got != 1 {
		t.Errorf("sync error = %v, want 1", got)
	}

	// a closed stack window counts as deferred and clears held back services
	until := time.Now().Add(time.Hour)
	recordStackUpdate(stack, status, updateResult{DeferredUntil: &until}, nil, time.Second)
	if got := testutil.ToFloat64(stackDeferred.WithLabelValues(stack)); got != 1 {
		t.Errorf("stack deferred = %v, want 1", got)
	}
	if got := testutil.ToFloat64(stackUpdatesTotal.WithLabelValues(stack, updateResultDeferred)); got != 1 {
		t.Errorf("deferred count = %v, want 1", got)
	}
	if got := testutil.CollectAndCount(serviceDeferred); got != 0 {
		t.Errorf("service_deferred series = %d, want 0", got)
	}
}
