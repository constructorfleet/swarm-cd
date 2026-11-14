package swarmcd

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/distribution/reference"
	"github.com/docker/cli/cli/command"
)

// Labels set in a service's deploy.labels to override stack-level policies.
const (
	labelImagePull    = "swarmcd.image.pull"
	labelUpdateWindow = "swarmcd.update.window"
)

const (
	imagePullAlways  = "always"
	imagePullChanged = "changed"
)

// getServices returns the services of a parsed compose file, validating types.
func getServices(composeMap map[string]any) (map[string]map[string]any, error) {
	result := make(map[string]map[string]any)
	services, ok := composeMap["services"].(map[string]any)
	if !ok {
		return result, nil
	}
	for serviceName, service := range services {
		serviceMap, ok := service.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("invalid compose file: service %s must be a map", serviceName)
		}
		result[serviceName] = serviceMap
	}
	return result, nil
}

// getDeployLabel reads a label from a service's deploy.labels, which compose
// allows to be either a map or a list of "key=value" strings.
func getDeployLabel(service map[string]any, key string) string {
	deploy, ok := service["deploy"].(map[string]any)
	if !ok {
		return ""
	}
	switch labels := deploy["labels"].(type) {
	case map[string]any:
		if value, ok := labels[key]; ok {
			return strings.TrimSpace(fmt.Sprint(value))
		}
	case []any:
		for _, label := range labels {
			labelString, ok := label.(string)
			if !ok {
				continue
			}
			if name, value, found := strings.Cut(labelString, "="); found && strings.TrimSpace(name) == key {
				return strings.TrimSpace(value)
			}
		}
	}
	return ""
}

// holdBackServices removes services whose update window is closed from the
// compose file. Since stacks are deployed without --prune, services missing
// from the compose file are left running untouched. It returns, for each held
// back service, the time its window opens next.
func holdBackServices(composeMap map[string]any, lastCheck time.Time, now time.Time) (map[string]time.Time, error) {
	services, err := getServices(composeMap)
	if err != nil {
		return nil, err
	}
	deferred := map[string]time.Time{}
	for serviceName, service := range services {
		window, err := parseUpdateWindow(getDeployLabel(service, labelUpdateWindow))
		if err != nil {
			return nil, fmt.Errorf("service %s: %w", serviceName, err)
		}
		if !window.isOpen(lastCheck, now) {
			deferred[serviceName] = window.nextOpen(now)
			delete(composeMap["services"].(map[string]any), serviceName)
		}
	}
	return deferred, nil
}

// digestResolver returns the registry digest of an image reference.
type digestResolver func(image string) (string, error)

// applyImagePullPolicy returns the --resolve-image mode for the stack deploy.
//
// When no service sets the swarmcd.image.pull label, the stack-level mode is
// used as is. Otherwise the stack is deployed with "changed", and services
// whose effective mode is "always" get their image pinned to the current
// registry digest, so they are updated whenever the digest moves. Like
// `docker stack deploy`, an image whose digest cannot be resolved is deployed
// unpinned with a warning rather than failing the stack.
func applyImagePullPolicy(composeMap map[string]any, stackMode string, resolveDigest digestResolver) (string, error) {
	services, err := getServices(composeMap)
	if err != nil {
		return "", err
	}
	modes := map[string]string{}
	overridden := false
	for serviceName, service := range services {
		mode := getDeployLabel(service, labelImagePull)
		switch mode {
		case "":
			mode = stackMode
		case imagePullAlways, imagePullChanged:
			overridden = true
		default:
			return "", fmt.Errorf("service %s: invalid %s label %q, must be %q or %q", serviceName, labelImagePull, mode, imagePullAlways, imagePullChanged)
		}
		modes[serviceName] = mode
	}
	if !overridden {
		return stackMode, nil
	}

	// sort for deterministic registry queries and error messages
	serviceNames := make([]string, 0, len(modes))
	for serviceName := range modes {
		serviceNames = append(serviceNames, serviceName)
	}
	sort.Strings(serviceNames)
	for _, serviceName := range serviceNames {
		if modes[serviceName] != imagePullAlways {
			continue
		}
		image, ok := services[serviceName]["image"].(string)
		if !ok || image == "" {
			continue
		}
		pinned, err := pinImageDigest(image, resolveDigest)
		if err != nil {
			logger.Warn("could not pin image digest, deploying unpinned", "service", serviceName, "error", err)
			continue
		}
		services[serviceName]["image"] = pinned
	}
	return imagePullChanged, nil
}

// pinImageDigest returns image with its tag kept and the registry digest
// appended. Images already pinned to a digest are returned unchanged.
func pinImageDigest(image string, resolveDigest digestResolver) (string, error) {
	named, err := reference.ParseNormalizedNamed(image)
	if err != nil {
		return "", fmt.Errorf("invalid image reference %s: %w", image, err)
	}
	if _, ok := named.(reference.Digested); ok {
		return image, nil
	}
	digest, err := resolveDigest(image)
	if err != nil {
		return "", fmt.Errorf("could not resolve digest of image %s: %w", image, err)
	}
	return image + "@" + digest, nil
}

// registryDigest queries the registry, through the docker daemon and with the
// configured registry credentials, for the digest of an image.
func registryDigest(image string) (string, error) {
	encodedAuth, err := command.RetrieveAuthTokenFromImage(dockerCli.ConfigFile(), image)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	inspect, err := dockerCli.Client().DistributionInspect(ctx, image, encodedAuth)
	if err != nil {
		return "", err
	}
	return inspect.Descriptor.Digest.String(), nil
}
