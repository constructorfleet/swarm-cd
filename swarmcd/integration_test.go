//go:build integration

// Integration tests run SwarmCD's update cycle against a real Docker Swarm, a
// real registry and a real git repo. They need:
//   - a docker daemon in swarm mode (DOCKER_HOST is honoured) and the docker CLI
//   - a registry reachable by that daemon, SWARMCD_IT_REGISTRY (default localhost:5000)
//
// Run with: go test -tags integration -run Integration -v ./swarmcd/
package swarmcd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/docker/cli/cli/command/stack"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/swarm"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/m-adawi/swarm-cd/util"
)

func integrationRegistry() string {
	if registry := os.Getenv("SWARMCD_IT_REGISTRY"); registry != "" {
		return registry
	}
	return "localhost:5000"
}

// setupIntegration initializes the docker client and a test config.
func setupIntegration(t *testing.T) {
	t.Helper()
	if err := initDockerCli(); err != nil {
		t.Fatalf("could not initialize docker client: %s", err)
	}
	info, err := dockerCli.Client().Info(context.Background())
	if err != nil {
		t.Fatalf("docker daemon unreachable: %s", err)
	}
	if info.Swarm.LocalNodeState != swarm.LocalNodeStateActive {
		t.Fatalf("docker daemon is not in swarm mode (state %q)", info.Swarm.LocalNodeState)
	}
	originalConfig := config
	config = &util.Config{UpdateInterval: 60, Concurrency: 1}
	t.Cleanup(func() { config = originalConfig })
}

func runDocker(t *testing.T, stdin string, args ...string) string {
	t.Helper()
	cmd := exec.Command("docker", args...)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("docker %s: %s\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// pushImage builds a distinct image, pushes it under ref and returns its digest.
func pushImage(t *testing.T, ref string, marker string) string {
	t.Helper()
	runDocker(t, fmt.Sprintf("FROM alpine:3.20\nRUN echo %s > /marker\n", marker), "build", "-q", "-t", ref, "-")
	runDocker(t, "", "push", "-q", ref)
	repoDigest := runDocker(t, "", "image", "inspect", "--format", "{{index .RepoDigests 0}}", ref)
	_, digest, _ := strings.Cut(repoDigest, "@")
	return digest
}

// gitSource is a local git repo used as a stack source.
type gitSource struct {
	t    *testing.T
	dir  string
	repo *git.Repository
}

func newGitSource(t *testing.T) *gitSource {
	t.Helper()
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	return &gitSource{t: t, dir: dir, repo: repo}
}

func (source *gitSource) commit(composeContent string) {
	source.t.Helper()
	if err := os.WriteFile(filepath.Join(source.dir, "compose.yaml"), []byte(composeContent), 0644); err != nil {
		source.t.Fatal(err)
	}
	workTree, err := source.repo.Worktree()
	if err != nil {
		source.t.Fatal(err)
	}
	if _, err = workTree.Add("compose.yaml"); err != nil {
		source.t.Fatal(err)
	}
	_, err = workTree.Commit("update stack", &git.CommitOptions{
		Author: &object.Signature{Name: "swarmcd-it", Email: "it@example.com", When: time.Now()},
	})
	if err != nil {
		source.t.Fatal(err)
	}
}

// newIntegrationStack clones source and returns a stack that is removed from
// the swarm when the test ends. The stack name gets a unique suffix, since
// docker removes a stack's network asynchronously and reusing a name across
// runs races with that.
func newIntegrationStack(t *testing.T, prefix string, source *gitSource, stackConfig *util.StackConfig) *swarmStack {
	t.Helper()
	name := fmt.Sprintf("%s%d", prefix, time.Now().UnixNano()%1000000)
	repo, err := newStackRepo(name, filepath.Join(t.TempDir(), "clone"), source.dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	head, err := source.repo.Head()
	if err != nil {
		t.Fatal(err)
	}
	stackConfig.ComposeFile = "compose.yaml"
	swarmStack := newSwarmStackFromConfig(name, repo, head.Name().Short(), "", stackConfig, false)
	if swarmStack.configErr != nil {
		t.Fatal(swarmStack.configErr)
	}
	t.Cleanup(func() {
		cmd := stack.NewStackCommand(dockerCli)
		cmd.SetArgs([]string{"rm", name})
		cmd.SilenceErrors = true
		cmd.SilenceUsage = true
		_ = cmd.Execute()
	})
	return swarmStack
}

func mustUpdate(t *testing.T, swarmStack *swarmStack) updateResult {
	t.Helper()
	result, err := swarmStack.updateStack()
	if err != nil {
		t.Fatalf("update failed: %s", err)
	}
	return result
}

func inspectService(t *testing.T, name string) swarm.Service {
	t.Helper()
	service, _, err := dockerCli.Client().ServiceInspectWithRaw(context.Background(), name, types.ServiceInspectOptions{})
	if err != nil {
		t.Fatalf("could not inspect service %s: %s", name, err)
	}
	return service
}

func serviceImageDigest(t *testing.T, name string) string {
	t.Helper()
	image := inspectService(t, name).Spec.TaskTemplate.ContainerSpec.Image
	_, digest, found := strings.Cut(image, "@")
	if !found {
		t.Fatalf("service %s image %s is not pinned to a digest", name, image)
	}
	return digest
}

func serviceEnv(t *testing.T, name string) []string {
	t.Helper()
	return inspectService(t, name).Spec.TaskTemplate.ContainerSpec.Env
}

// A service labelled swarmcd.image.pull=always follows its tag to a new
// digest, while one labelled changed stays on the digest it was deployed with.
func TestIntegrationImagePullLabels(t *testing.T) {
	setupIntegration(t)
	image := integrationRegistry() + "/swarmcd-it/pull:latest"
	digestA := pushImage(t, image, fmt.Sprintf("a-%d", time.Now().UnixNano()))

	source := newGitSource(t)
	source.commit(fmt.Sprintf(`services:
  follows:
    image: %[1]s
    command: ["sleep", "3600"]
    deploy:
      labels:
        swarmcd.image.pull: always
  stays:
    image: %[1]s
    command: ["sleep", "3600"]
    deploy:
      labels:
        swarmcd.image.pull: changed
`, image))
	swarmStack := newIntegrationStack(t, "itpull", source, &util.StackConfig{AlwaysPullContainers: boolPtr(false)})

	mustUpdate(t, swarmStack)
	follows, stays := swarmStack.name+"_follows", swarmStack.name+"_stays"
	for _, service := range []string{follows, stays} {
		if got := serviceImageDigest(t, service); got != digestA {
			t.Fatalf("%s: initial digest %s, want %s", service, got, digestA)
		}
	}

	digestB := pushImage(t, image, fmt.Sprintf("b-%d", time.Now().UnixNano()))
	mustUpdate(t, swarmStack)
	if got := serviceImageDigest(t, follows); got != digestB {
		t.Errorf("follows: digest %s, want the new digest %s", got, digestB)
	}
	if got := serviceImageDigest(t, stays); got != digestA {
		t.Errorf("stays: digest %s, want the original digest %s", got, digestA)
	}
}

// A service whose update window is closed keeps running its previous spec,
// while the rest of the stack picks up the new commit.
func TestIntegrationServiceWindowHoldsBack(t *testing.T) {
	setupIntegration(t)
	image := integrationRegistry() + "/swarmcd-it/window:latest"
	pushImage(t, image, "window")

	compose := func(value string, heldLabels string) string {
		return fmt.Sprintf(`services:
  held:
    image: %[1]s
    command: ["sleep", "3600"]
    environment:
      VALUE: "%[2]s"
%[3]s  free:
    image: %[1]s
    command: ["sleep", "3600"]
    environment:
      VALUE: "%[2]s"
`, image, value, heldLabels)
	}

	source := newGitSource(t)
	source.commit(compose("1", ""))
	swarmStack := newIntegrationStack(t, "itwindow", source, &util.StackConfig{})
	mustUpdate(t, swarmStack)

	// a window at the opposite side of the clock is closed for this cycle
	closedWindow := fmt.Sprintf("* %d * * *", (time.Now().UTC().Hour()+12)%24)
	source.commit(compose("2", fmt.Sprintf(`    deploy:
      labels:
        swarmcd.update.window: "%s"
`, closedWindow)))
	result := mustUpdate(t, swarmStack)

	if _, ok := result.DeferredServices["held"]; !ok || len(result.DeferredServices) != 1 {
		t.Errorf("expected only held to be deferred, got %v", result.DeferredServices)
	}
	if env := serviceEnv(t, swarmStack.name+"_held"); len(env) != 1 || env[0] != "VALUE=1" {
		t.Errorf("held: env %v, want it untouched at VALUE=1", env)
	}
	if env := serviceEnv(t, swarmStack.name+"_free"); len(env) != 1 || env[0] != "VALUE=2" {
		t.Errorf("free: env %v, want the new VALUE=2", env)
	}

	// a removed service is recreated although its window is closed
	runDocker(t, "", "service", "rm", swarmStack.name+"_held")
	result = mustUpdate(t, swarmStack)
	if len(result.DeferredServices) != 0 {
		t.Errorf("expected the missing service to be deployed, got deferred %v", result.DeferredServices)
	}
	if env := serviceEnv(t, swarmStack.name+"_held"); len(env) != 1 || env[0] != "VALUE=2" {
		t.Errorf("held: env %v, want it recreated with VALUE=2", env)
	}
}

// A closed stack update window holds back live services but creates missing ones.
func TestIntegrationStackWindowClosed(t *testing.T) {
	setupIntegration(t)
	image := integrationRegistry() + "/swarmcd-it/stackwindow:latest"
	pushImage(t, image, "stackwindow")

	compose := func(value string) string {
		return fmt.Sprintf(`services:
  app:
    image: %s
    command: ["sleep", "3600"]
    environment:
      VALUE: "%s"
`, image, value)
	}
	source := newGitSource(t)
	source.commit(compose("1"))
	closedWindow := fmt.Sprintf("* %d * * *", (time.Now().UTC().Hour()+12)%24)
	swarmStack := newIntegrationStack(t, "itstackwindow", source, &util.StackConfig{UpdateWindow: closedWindow})
	app := swarmStack.name + "_app"

	result := mustUpdate(t, swarmStack)
	if result.DeferredUntil == nil || !result.Deployed {
		t.Fatalf("expected the new stack to be deployed while deferred, got %+v", result)
	}
	if env := serviceEnv(t, app); len(env) != 1 || env[0] != "VALUE=1" {
		t.Fatalf("app: env %v, want VALUE=1", env)
	}

	source.commit(compose("2"))
	result = mustUpdate(t, swarmStack)
	if result.DeferredUntil == nil || result.Deployed || result.Revision != "" {
		t.Errorf("expected the update to be deferred, got %+v", result)
	}
	if env := serviceEnv(t, app); len(env) != 1 || env[0] != "VALUE=1" {
		t.Errorf("app: env %v, want it untouched at VALUE=1", env)
	}

	runDocker(t, "", "service", "rm", app)
	result = mustUpdate(t, swarmStack)
	if !result.Deployed {
		t.Fatalf("expected the removed service to be recreated, got %+v", result)
	}
	if env := serviceEnv(t, app); len(env) != 1 || env[0] != "VALUE=2" {
		t.Errorf("app: env %v, want it recreated with VALUE=2", env)
	}
}
