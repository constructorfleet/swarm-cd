package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/m-adawi/swarm-cd/swarmcd"
	"github.com/m-adawi/swarm-cd/util"
)

// setupTestStacks populates the swarmcd package-level state with test data
// and returns a cleanup function to restore the original state.
func setupTestStacks(t *testing.T, data map[string]*swarmcd.StackStatus) func() {
	t.Helper()
	return swarmcd.SetStackStatusForTest(data)
}

// decodeBody is a test helper that decodes a JSON response body into a map.
func decodeBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to decode response body: %v", err)
	}
	return body
}

func TestGetStacks_ReturnsAllFields(t *testing.T) {
	gin.SetMode(gin.TestMode)

	now := time.Now().Truncate(time.Second)
	earlier := now.Add(-1 * time.Hour)

	cleanup := setupTestStacks(t, map[string]*swarmcd.StackStatus{
		"my-stack": {
			Error:          "some error",
			Revision:       "abc12345",
			RepoURL:        "https://github.com/example/repo",
			RefType:        "branch",
			RefValue:       "main",
			ComposeFile:    "docker-compose.yaml",
			LastChangeAt:   &earlier,
			LastDeployedAt: &now,
		},
	})
	defer cleanup()

	r := gin.New()
	r.GET("/stacks", getStacks)

	req := httptest.NewRequest(http.MethodGet, "/stacks", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var stacks []stackResponse
	if err := json.Unmarshal(w.Body.Bytes(), &stacks); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(stacks) != 1 {
		t.Fatalf("expected 1 stack, got %d", len(stacks))
	}

	s := stacks[0]

	if s.Name != "my-stack" {
		t.Errorf("expected name %q, got %q", "my-stack", s.Name)
	}
	if s.RepoURL != "https://github.com/example/repo" {
		t.Errorf("expected repo_url %q, got %q", "https://github.com/example/repo", s.RepoURL)
	}
	if s.RefType != "branch" {
		t.Errorf("expected ref_type %q, got %q", "branch", s.RefType)
	}
	if s.RefValue != "main" {
		t.Errorf("expected ref_value %q, got %q", "main", s.RefValue)
	}
	if s.Revision != "abc12345" {
		t.Errorf("expected revision %q, got %q", "abc12345", s.Revision)
	}
	if s.ComposeFile != "docker-compose.yaml" {
		t.Errorf("expected compose_file %q, got %q", "docker-compose.yaml", s.ComposeFile)
	}
	if s.Error != "some error" {
		t.Errorf("expected error %q, got %q", "some error", s.Error)
	}
	if s.LastChangeAt == nil {
		t.Fatal("expected last_change_at to be non-nil")
	}
	if !s.LastChangeAt.Equal(earlier) {
		t.Errorf("expected last_change_at %v, got %v", earlier, *s.LastChangeAt)
	}
	if s.LastDeployedAt == nil {
		t.Fatal("expected last_deployed_at to be non-nil")
	}
	if !s.LastDeployedAt.Equal(now) {
		t.Errorf("expected last_deployed_at %v, got %v", now, *s.LastDeployedAt)
	}

	// Verify JSON field names are snake_case by checking raw JSON
	var raw []map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("failed to decode raw response: %v", err)
	}
	expectedKeys := []string{"name", "repo_url", "ref_type", "ref_value", "revision", "compose_file", "error", "last_change_at", "last_deployed_at"}
	for _, key := range expectedKeys {
		if _, ok := raw[0][key]; !ok {
			t.Errorf("expected JSON key %q not found in response", key)
		}
	}
}

func TestGetStacks_EmptyIsArray(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cleanup := setupTestStacks(t, map[string]*swarmcd.StackStatus{})
	defer cleanup()

	r := gin.New()
	r.GET("/stacks", getStacks)

	req := httptest.NewRequest(http.MethodGet, "/stacks", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	if got := w.Body.String(); got != "[]" {
		t.Errorf("expected empty JSON array, got %s", got)
	}
}

func TestGetStacks_SortOrder(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cleanup := setupTestStacks(t, map[string]*swarmcd.StackStatus{
		"charlie": {
			RepoURL:  "https://github.com/example/repo",
			RefType:  "branch",
			RefValue: "main",
		},
		"alpha": {
			RepoURL:  "https://github.com/example/repo",
			RefType:  "tag",
			RefValue: "v1.0.0",
		},
		"bravo": {
			RepoURL:  "https://github.com/example/repo",
			RefType:  "branch",
			RefValue: "develop",
		},
	})
	defer cleanup()

	r := gin.New()
	r.GET("/stacks", getStacks)

	req := httptest.NewRequest(http.MethodGet, "/stacks", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var stacks []stackResponse
	if err := json.Unmarshal(w.Body.Bytes(), &stacks); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(stacks) != 3 {
		t.Fatalf("expected 3 stacks, got %d", len(stacks))
	}

	expectedOrder := []string{"alpha", "bravo", "charlie"}
	for i, expected := range expectedOrder {
		if stacks[i].Name != expected {
			t.Errorf("position %d: expected %q, got %q", i, expected, stacks[i].Name)
		}
	}
}

// ---------- GET /stacks/:name tests ----------

func TestGetStackByName_Found(t *testing.T) {
	gin.SetMode(gin.TestMode)

	now := time.Now().Truncate(time.Second)
	earlier := now.Add(-1 * time.Hour)

	cleanup := setupTestStacks(t, map[string]*swarmcd.StackStatus{
		"my-stack": {
			Error:          "",
			Revision:       "abc12345",
			RepoURL:        "https://github.com/example/repo",
			RefType:        "branch",
			RefValue:       "main",
			ComposeFile:    "docker-compose.yaml",
			LastChangeAt:   &earlier,
			LastDeployedAt: &now,
		},
		"other-stack": {
			Revision: "def67890",
			RepoURL:  "https://github.com/example/other",
			RefType:  "tag",
			RefValue: "v1.0.0",
		},
	})
	defer cleanup()

	r := gin.New()
	r.GET("/stacks/:name", getStack)

	req := httptest.NewRequest(http.MethodGet, "/stacks/my-stack", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var s stackResponse
	if err := json.Unmarshal(w.Body.Bytes(), &s); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if s.Name != "my-stack" {
		t.Errorf("expected name %q, got %q", "my-stack", s.Name)
	}
	if s.RepoURL != "https://github.com/example/repo" {
		t.Errorf("expected repo_url %q, got %q", "https://github.com/example/repo", s.RepoURL)
	}
	if s.RefType != "branch" {
		t.Errorf("expected ref_type %q, got %q", "branch", s.RefType)
	}
	if s.RefValue != "main" {
		t.Errorf("expected ref_value %q, got %q", "main", s.RefValue)
	}
}

func TestGetStackByName_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cleanup := setupTestStacks(t, map[string]*swarmcd.StackStatus{
		"existing-stack": {
			Revision: "abc12345",
			RepoURL:  "https://github.com/example/repo",
			RefType:  "branch",
			RefValue: "main",
		},
	})
	defer cleanup()

	r := gin.New()
	r.GET("/stacks/:name", getStack)

	req := httptest.NewRequest(http.MethodGet, "/stacks/foo", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", w.Code)
	}

	body := decodeBody(t, w)
	expected := "stack 'foo' not found"
	if body["error"] != expected {
		t.Errorf("expected error %q, got %q", expected, body["error"])
	}
}

// ---------- GET /health tests ----------

func TestGetHealth_ReturnsBootTime(t *testing.T) {
	gin.SetMode(gin.TestMode)

	bootedAt := time.Now().Add(-5 * time.Minute).Truncate(time.Second)
	cleanupRuntime := swarmcd.SetRuntimeInfoForTest(swarmcd.RuntimeInfo{
		BootedAt: bootedAt,
		Version:  "dev",
	})
	defer cleanupRuntime()

	cleanupStacks := setupTestStacks(t, map[string]*swarmcd.StackStatus{})
	defer cleanupStacks()

	oldInterval := util.Configs.UpdateInterval
	util.Configs.UpdateInterval = 120
	defer func() { util.Configs.UpdateInterval = oldInterval }()

	r := gin.New()
	r.GET("/health", getHealth)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	body := decodeBody(t, w)

	if body["status"] != "healthy" {
		t.Errorf("expected status %q, got %q", "healthy", body["status"])
	}

	// Verify booted_at is present and parseable
	bootedAtStr, ok := body["booted_at"].(string)
	if !ok {
		t.Fatalf("expected booted_at to be a string, got %T", body["booted_at"])
	}
	parsedBoot, err := time.Parse(time.RFC3339Nano, bootedAtStr)
	if err != nil {
		t.Fatalf("failed to parse booted_at %q: %v", bootedAtStr, err)
	}
	if !parsedBoot.Equal(bootedAt) {
		t.Errorf("expected booted_at %v, got %v", bootedAt, parsedBoot)
	}

	// Verify uptime is positive
	uptimeRaw, ok := body["uptime_seconds"].(float64)
	if !ok {
		t.Fatalf("expected uptime_seconds to be a number, got %T", body["uptime_seconds"])
	}
	if uptimeRaw <= 0 {
		t.Errorf("expected positive uptime_seconds, got %v", uptimeRaw)
	}

	if body["version"] != "dev" {
		t.Errorf("expected version %q, got %q", "dev", body["version"])
	}

	if body["update_interval_seconds"] != float64(120) {
		t.Errorf("expected update_interval_seconds=120, got %v", body["update_interval_seconds"])
	}
}

func TestGetHealth_ReturnsStackCount(t *testing.T) {
	gin.SetMode(gin.TestMode)

	bootedAt := time.Now().Add(-1 * time.Minute)
	cleanupRuntime := swarmcd.SetRuntimeInfoForTest(swarmcd.RuntimeInfo{
		BootedAt: bootedAt,
		Version:  "1.2.3",
	})
	defer cleanupRuntime()

	cleanupStacks := setupTestStacks(t, map[string]*swarmcd.StackStatus{
		"stack-a": {Revision: "aaa", RepoURL: "https://github.com/a"},
		"stack-b": {Revision: "bbb", RepoURL: "https://github.com/b"},
		"stack-c": {Revision: "ccc", RepoURL: "https://github.com/c"},
	})
	defer cleanupStacks()

	oldInterval := util.Configs.UpdateInterval
	util.Configs.UpdateInterval = 60
	defer func() { util.Configs.UpdateInterval = oldInterval }()

	r := gin.New()
	r.GET("/health", getHealth)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	body := decodeBody(t, w)

	if body["stacks_managed"] != float64(3) {
		t.Errorf("expected stacks_managed=3, got %v", body["stacks_managed"])
	}

	if body["version"] != "1.2.3" {
		t.Errorf("expected version %q, got %q", "1.2.3", body["version"])
	}
}

// ---------- GET /health config_warnings tests ----------

func TestGetHealth_ReturnsConfigWarnings(t *testing.T) {
	gin.SetMode(gin.TestMode)

	bootedAt := time.Now().Add(-1 * time.Minute)
	cleanupRuntime := swarmcd.SetRuntimeInfoForTest(swarmcd.RuntimeInfo{
		BootedAt: bootedAt,
		Version:  "dev",
	})
	defer cleanupRuntime()

	cleanupStacks := setupTestStacks(t, map[string]*swarmcd.StackStatus{})
	defer cleanupStacks()

	oldInterval := util.Configs.UpdateInterval
	util.Configs.UpdateInterval = 120
	defer func() { util.Configs.UpdateInterval = oldInterval }()

	// Simulate inline stacks shadowing an existing stacks.yaml
	oldConflict := util.Conflict
	util.Conflict = util.ConfigConflict{
		StacksInline:     true,
		StacksFileExists: true,
	}
	defer func() { util.Conflict = oldConflict }()

	r := gin.New()
	r.GET("/health", getHealth)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	body := decodeBody(t, w)

	rawWarnings, ok := body["config_warnings"]
	if !ok {
		t.Fatal("expected config_warnings key in health response")
	}
	warnings, ok := rawWarnings.([]any)
	if !ok {
		t.Fatalf("expected config_warnings to be an array, got %T", rawWarnings)
	}
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}
	if warnings[0] != "stacks.yaml is ignored because stacks are defined in config.yaml" {
		t.Errorf("unexpected warning: %v", warnings[0])
	}
}

func TestGetHealth_NoConfigWarnings(t *testing.T) {
	gin.SetMode(gin.TestMode)

	bootedAt := time.Now().Add(-1 * time.Minute)
	cleanupRuntime := swarmcd.SetRuntimeInfoForTest(swarmcd.RuntimeInfo{
		BootedAt: bootedAt,
		Version:  "dev",
	})
	defer cleanupRuntime()

	cleanupStacks := setupTestStacks(t, map[string]*swarmcd.StackStatus{})
	defer cleanupStacks()

	oldInterval := util.Configs.UpdateInterval
	util.Configs.UpdateInterval = 120
	defer func() { util.Configs.UpdateInterval = oldInterval }()

	// Inline stacks without a split file are not a conflict
	oldConflict := util.Conflict
	util.Conflict = util.ConfigConflict{StacksInline: true, ReposInline: true}
	defer func() { util.Conflict = oldConflict }()

	r := gin.New()
	r.GET("/health", getHealth)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	body := decodeBody(t, w)

	if _, ok := body["config_warnings"]; ok {
		t.Error("expected no config_warnings key when there are no conflicts")
	}
}

// ---------- GET /stacks/:name/services tests ----------

func servicesRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/stacks/:name/services", getStackServices)
	return router
}

// withStubServices swaps the getStackServicesFn seam for the duration of a test.
func withStubServices(t *testing.T, stub func(name string) ([]swarmcd.ServiceStatus, error)) {
	t.Helper()
	original := getStackServicesFn
	getStackServicesFn = stub
	t.Cleanup(func() { getStackServicesFn = original })
}

func TestGetStackServices_NotFound(t *testing.T) {
	withStubServices(t, func(string) ([]swarmcd.ServiceStatus, error) {
		return nil, swarmcd.ErrStackNotFound
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/stacks/unknown/services", nil)
	servicesRouter().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	if body := decodeBody(t, rec); body["error"] != "stack 'unknown' not found" {
		t.Errorf("unexpected error body: %v", body)
	}
}

func TestGetStackServices_OK(t *testing.T) {
	withStubServices(t, func(name string) ([]swarmcd.ServiceStatus, error) {
		return []swarmcd.ServiceStatus{
			{
				ID: "1", Name: "web_nginx", Image: "nginx:1.27", Mode: "replicated",
				RunningTasks: 3, DesiredTasks: 3, Health: swarmcd.HealthHealthy,
				Tasks:   []swarmcd.TaskStatus{{ID: "t1", Slot: 1, NodeID: "n1", State: "running", DesiredState: "running", ContainerID: "c1"}},
				Secrets: []string{"db_password"},
				Configs: []string{},
			},
		}, nil
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/stacks/web/services", nil)
	servicesRouter().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var got []swarmcd.ServiceStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(services) = %d, want 1", len(got))
	}
	if got[0].Name != "web_nginx" || got[0].Health != swarmcd.HealthHealthy {
		t.Errorf("unexpected service payload: %+v", got[0])
	}

	// The services API uses the same snake_case convention as /stacks.
	var raw []map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("failed to decode raw response: %v", err)
	}
	for _, key := range []string{"id", "name", "image", "mode", "running_tasks", "desired_tasks", "failed_tasks", "health", "tasks", "secrets", "configs"} {
		if _, ok := raw[0][key]; !ok {
			t.Errorf("expected JSON key %q not found in service", key)
		}
	}
	var tasks []map[string]json.RawMessage
	if err := json.Unmarshal(raw[0]["tasks"], &tasks); err != nil {
		t.Fatalf("failed to decode tasks: %v", err)
	}
	for _, key := range []string{"id", "slot", "node_id", "state", "desired_state", "error", "container_id"} {
		if _, ok := tasks[0][key]; !ok {
			t.Errorf("expected JSON key %q not found in task", key)
		}
	}
}

func TestGetStackServices_InternalError(t *testing.T) {
	withStubServices(t, func(string) ([]swarmcd.ServiceStatus, error) {
		return nil, errors.New("docker daemon unreachable")
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/stacks/web/services", nil)
	servicesRouter().ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestGetStackByName_Deferred(t *testing.T) {
	gin.SetMode(gin.TestMode)

	until := time.Date(2026, 11, 15, 2, 0, 0, 0, time.UTC)
	cleanup := setupTestStacks(t, map[string]*swarmcd.StackStatus{
		"media": {
			DeferredUntil:    &until,
			DeferredServices: map[string]time.Time{"plex": until},
		},
	})
	defer cleanup()

	r := gin.New()
	r.GET("/stacks/:name", getStack)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/stacks/media", nil))

	body := decodeBody(t, w)
	if body["deferred_until"] != "2026-11-15T02:00:00Z" {
		t.Errorf("deferred_until = %v", body["deferred_until"])
	}
	services, ok := body["deferred_services"].(map[string]any)
	if !ok || services["plex"] != "2026-11-15T02:00:00Z" {
		t.Errorf("deferred_services = %v", body["deferred_services"])
	}
}
