package swarmcd

import (
	"errors"
	"testing"
	"time"
)

func mustParseCompose(t *testing.T, content string) map[string]any {
	t.Helper()
	stack := &swarmStack{name: "test"}
	composeMap, err := stack.parseStackString([]byte(content))
	if err != nil {
		t.Fatal(err)
	}
	return composeMap
}

func TestUpdateWindowIsOpen(t *testing.T) {
	at := func(hour, minute int) time.Time {
		return time.Date(2026, 11, 14, hour, minute, 0, 0, time.UTC)
	}
	tests := []struct {
		name      string
		expr      string
		lastCheck time.Time
		now       time.Time
		want      bool
	}{
		{"no window is always open", "", at(10, 0), at(10, 2), true},
		{"inside range", "* 2-4 * * *", at(3, 0), at(3, 2), true},
		{"outside range", "* 2-4 * * *", at(10, 0), at(10, 2), false},
		{"range opened since last check", "* 2-4 * * *", at(1, 59), at(2, 1), true},
		{"point in time fired since last check", "0 3 * * *", at(2, 59), at(3, 1), true},
		{"point in time already passed", "0 3 * * *", at(3, 1), at(3, 3), false},
		{"descriptor", "@daily", at(23, 59), at(0, 0).Add(24 * time.Hour), true},
		{"timezone prefix", "CRON_TZ=America/Denver * 2-4 * * *", at(9, 0), at(9, 2), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			window, err := parseUpdateWindow(tt.expr)
			if err != nil {
				t.Fatal(err)
			}
			if got := window.isOpen(tt.lastCheck, tt.now); got != tt.want {
				t.Errorf("isOpen() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseUpdateWindowInvalid(t *testing.T) {
	if _, err := parseUpdateWindow("not a cron"); err == nil {
		t.Error("expected an error for an invalid cron expression")
	}
}

func TestGetDeployLabel(t *testing.T) {
	composeMap := mustParseCompose(t, `services:
  as-map:
    deploy:
      labels:
        swarmcd.image.pull: always
  as-list:
    deploy:
      labels:
        - other=value
        - swarmcd.image.pull=changed
  no-labels:
    image: nginx
`)
	services, err := getServices(composeMap)
	if err != nil {
		t.Fatal(err)
	}
	for serviceName, want := range map[string]string{"as-map": "always", "as-list": "changed", "no-labels": ""} {
		if got := getDeployLabel(services[serviceName], labelImagePull); got != want {
			t.Errorf("%s: got %q, want %q", serviceName, got, want)
		}
	}
}

func TestHoldBackServices(t *testing.T) {
	composeMap := mustParseCompose(t, `services:
  plex:
    image: plex
    deploy:
      labels:
        swarmcd.update.window: "* 2-4 * * *"
  sonarr:
    image: sonarr
`)
	lastCheck := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	now := lastCheck.Add(2 * time.Minute)
	deferred, err := holdBackServices(composeMap, lastCheck, now)
	if err != nil {
		t.Fatal(err)
	}
	wantNext := time.Date(2026, 11, 15, 2, 0, 0, 0, time.UTC)
	if len(deferred) != 1 || !deferred["plex"].Equal(wantNext) {
		t.Errorf("unexpected deferred services: %v", deferred)
	}
	services, _ := getServices(composeMap)
	if _, ok := services["plex"]; ok {
		t.Error("plex should have been removed from the compose file")
	}
	if _, ok := services["sonarr"]; !ok {
		t.Error("sonarr should still be in the compose file")
	}
}

func TestHoldBackServicesInvalidWindow(t *testing.T) {
	composeMap := mustParseCompose(t, `services:
  plex:
    deploy:
      labels:
        swarmcd.update.window: "nope"
`)
	if _, err := holdBackServices(composeMap, time.Now(), time.Now()); err == nil {
		t.Error("expected an error for an invalid service update window")
	}
}

func fakeResolver(digests map[string]string) digestResolver {
	return func(image string) (string, error) {
		if digest, ok := digests[image]; ok {
			return digest, nil
		}
		return "", errors.New("not found")
	}
}

func TestApplyImagePullPolicyWithoutLabels(t *testing.T) {
	composeMap := mustParseCompose(t, `services:
  web:
    image: nginx:1.27
`)
	for _, stackMode := range []string{imagePullAlways, imagePullChanged} {
		mode, err := applyImagePullPolicy(composeMap, stackMode, fakeResolver(nil))
		if err != nil {
			t.Fatal(err)
		}
		if mode != stackMode {
			t.Errorf("got mode %q, want stack mode %q", mode, stackMode)
		}
	}
	services, _ := getServices(composeMap)
	if services["web"]["image"] != "nginx:1.27" {
		t.Errorf("image should be untouched, got %v", services["web"]["image"])
	}
}

func TestApplyImagePullPolicyWithLabels(t *testing.T) {
	composeMap := mustParseCompose(t, `services:
  pinned-by-label:
    image: nginx:1.27
    deploy:
      labels:
        swarmcd.image.pull: always
  inherits-stack:
    image: redis:7
  opted-out:
    image: postgres:16
    deploy:
      labels:
        - swarmcd.image.pull=changed
  already-digested:
    image: alpine:3@sha256:0000000000000000000000000000000000000000000000000000000000000000
    deploy:
      labels:
        swarmcd.image.pull: always
`)
	resolver := fakeResolver(map[string]string{
		"nginx:1.27": "sha256:aaaa",
		"redis:7":    "sha256:bbbb",
	})
	mode, err := applyImagePullPolicy(composeMap, imagePullAlways, resolver)
	if err != nil {
		t.Fatal(err)
	}
	if mode != imagePullChanged {
		t.Errorf("got mode %q, want %q", mode, imagePullChanged)
	}
	services, _ := getServices(composeMap)
	want := map[string]string{
		"pinned-by-label":  "nginx:1.27@sha256:aaaa",
		"inherits-stack":   "redis:7@sha256:bbbb",
		"opted-out":        "postgres:16",
		"already-digested": "alpine:3@sha256:0000000000000000000000000000000000000000000000000000000000000000",
	}
	for serviceName, image := range want {
		if services[serviceName]["image"] != image {
			t.Errorf("%s: got image %v, want %s", serviceName, services[serviceName]["image"], image)
		}
	}
}

func TestApplyImagePullPolicyInvalidLabel(t *testing.T) {
	composeMap := mustParseCompose(t, `services:
  web:
    image: nginx
    deploy:
      labels:
        swarmcd.image.pull: sometimes
`)
	if _, err := applyImagePullPolicy(composeMap, imagePullAlways, fakeResolver(nil)); err == nil {
		t.Error("expected an error for an invalid pull label")
	}
}

func TestApplyImagePullPolicyResolveError(t *testing.T) {
	composeMap := mustParseCompose(t, `services:
  web:
    image: nginx
    deploy:
      labels:
        swarmcd.image.pull: always
`)
	mode, err := applyImagePullPolicy(composeMap, imagePullChanged, fakeResolver(nil))
	if err != nil {
		t.Fatalf("registry errors should not fail the deploy: %s", err)
	}
	services, _ := getServices(composeMap)
	if mode != imagePullChanged || services["web"]["image"] != "nginx" {
		t.Errorf("expected unpinned image with mode changed, got %v with %s", services["web"]["image"], mode)
	}
}

// A closed stack update window skips the update before touching the repo
func TestUpdateStackWindowClosed(t *testing.T) {
	window, err := parseUpdateWindow("0 0 1 1 *")
	if err != nil {
		t.Fatal(err)
	}
	stack := &swarmStack{name: "test", updateWindow: window, lastWindowCheck: time.Now().Add(-time.Minute)}
	result, err := stack.updateStack()
	if err != nil {
		t.Fatal(err)
	}
	if result.DeferredUntil == nil || !result.DeferredUntil.Equal(window.nextOpen(stack.lastWindowCheck)) {
		t.Errorf("expected stack to be deferred until the next window, got %v", result.DeferredUntil)
	}
}
