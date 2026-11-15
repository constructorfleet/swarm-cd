package swarmcd

import (
	"bytes"
	"crypto/md5"
	"fmt"
	"log/slog"
	"os"
	"path"
	"text/template"
	"time"

	"github.com/docker/cli/cli/command/stack"
	"github.com/goccy/go-yaml"
	"github.com/m-adawi/swarm-cd/util"
)

type swarmStack struct {
	name                 string
	repo                 *stackRepo
	branch               string
	tag                  string
	composePath          string
	sopsFiles            []string
	valuesFile           string
	discoverSecrets      bool
	alwaysPullContainers *bool
	updateWindow         *updateWindow
	// invalid stack configuration; the stack is reported as failed and never
	// deployed, without affecting other stacks
	configErr error
	// time of the previous update cycle, used to evaluate update windows;
	// only accessed by the worker holding the repo lock
	lastWindowCheck time.Time
}

// updateResult describes the outcome of a successful update cycle.
type updateResult struct {
	Revision string
	// set when the stack update window is closed and nothing was deployed
	DeferredUntil *time.Time
	// services held back because their update window is closed, with the
	// time each window opens next
	DeferredServices map[string]time.Time
}

func newSwarmStack(name string, repo *stackRepo, branch string, tag string, composePath string, sopsFiles []string, valuesFile string, discoverSecrets bool, alwaysPullContainers *bool) *swarmStack {
	return &swarmStack{
		name:                 name,
		repo:                 repo,
		branch:               branch,
		tag:                  tag,
		composePath:          composePath,
		sopsFiles:            sopsFiles,
		valuesFile:           valuesFile,
		discoverSecrets:      discoverSecrets,
		alwaysPullContainers: alwaysPullContainers,
	}
}

// newSwarmStackFromConfig never fails: an invalid stack configuration is
// recorded in configErr so the stack is reported as failed at every update
// cycle instead of preventing SwarmCD from starting.
func newSwarmStackFromConfig(name string, repo *stackRepo, branch string, tag string, stackConfig *util.StackConfig, globalSecretsDiscovery bool) *swarmStack {
	swarmStack := newSwarmStack(
		name,
		repo,
		branch,
		tag,
		stackConfig.ComposeFile,
		stackConfig.SopsFiles,
		stackConfig.ValuesFile,
		globalSecretsDiscovery || stackConfig.SopsSecretsDiscovery,
		stackConfig.AlwaysPullContainers,
	)
	window, err := parseUpdateWindow(stackConfig.UpdateWindow)
	if err != nil {
		swarmStack.configErr = fmt.Errorf("invalid configuration of %s stack, it will not be deployed: %w", name, err)
	}
	swarmStack.updateWindow = window
	return swarmStack
}

// refAttr returns an slog attribute for the git ref (branch or tag)
func (swarmStack *swarmStack) refAttr() slog.Attr {
	if swarmStack.tag != "" {
		return slog.String("tag", swarmStack.tag)
	}
	return slog.String("branch", swarmStack.branch)
}

func (swarmStack *swarmStack) updateStack() (result updateResult, err error) {
	log := logger.With(
		slog.String("stack", swarmStack.name),
		swarmStack.refAttr(),
	)

	if swarmStack.configErr != nil {
		return result, swarmStack.configErr
	}

	now := time.Now()
	lastCheck := swarmStack.lastWindowCheck
	if lastCheck.IsZero() {
		lastCheck = now.Add(-time.Duration(config.UpdateInterval) * time.Second)
	}
	swarmStack.lastWindowCheck = now
	if !swarmStack.updateWindow.isOpen(lastCheck, now) {
		nextOpen := swarmStack.updateWindow.nextOpen(now)
		log.Info("update window closed, skipping stack", "window", swarmStack.updateWindow.expr, "next_open", nextOpen)
		result.DeferredUntil = &nextOpen
		return
	}

	var revision string
	if swarmStack.tag != "" {
		log.Debug("fetching tag...")
		revision, err = swarmStack.repo.fetchTag(swarmStack.tag)
	} else {
		log.Debug("pulling changes...")
		revision, err = swarmStack.repo.pullChanges(swarmStack.branch)
	}
	if err != nil {
		return
	}
	log.Debug("changes fetched", "revision", revision)
	result.Revision = revision

	log.Debug("reading stack file...")
	stackBytes, err := swarmStack.readStack()
	if err != nil {
		return
	}

	if swarmStack.valuesFile != "" {
		log.Debug("rendering template...")
		stackBytes, err = swarmStack.renderComposeTemplate(stackBytes)
	}
	if err != nil {
		return
	}

	log.Debug("parsing stack content...")
	stackContents, err := swarmStack.parseStackString([]byte(stackBytes))
	if err != nil {
		return
	}

	log.Debug("decrypting secrets...")
	err = swarmStack.decryptSopsFiles(stackContents)
	if err != nil {
		return result, fmt.Errorf("failed to decrypt one or more sops files for %s stack: %w", swarmStack.name, err)
	}

	if config.AutoRotate {
		log.Debug("rotating configs and secrets...")
		err = swarmStack.rotateConfigsAndSecrets(stackContents)
		if err != nil {
			return
		}
	}

	log.Debug("checking service update windows...")
	result.DeferredServices, err = holdBackServices(stackContents, lastCheck, now)
	if err != nil {
		return result, fmt.Errorf("could not evaluate update windows of stack %s: %w", swarmStack.name, err)
	}
	for serviceName, nextOpen := range result.DeferredServices {
		log.Info("update window closed, holding back service", "service", serviceName, "next_open", nextOpen)
	}
	if services, _ := getServices(stackContents); len(services) == 0 && len(result.DeferredServices) > 0 {
		log.Info("all services held back, skipping deploy")
		return
	}

	log.Debug("applying image pull policy...")
	resolveImage, err := applyImagePullPolicy(stackContents, swarmStack.resolveImageMode(), registryDigest)
	if err != nil {
		return result, fmt.Errorf("could not apply image pull policy of stack %s: %w", swarmStack.name, err)
	}

	log.Debug("writing stack to file...")
	err = swarmStack.writeStack(stackContents)
	if err != nil {
		return
	}

	log.Debug("deploying stack...")
	err = swarmStack.deployStack(resolveImage)
	return
}

func (swarmStack *swarmStack) readStack() ([]byte, error) {
	composeFile := path.Join(swarmStack.repo.path, swarmStack.composePath)
	composeFileBytes, err := os.ReadFile(composeFile)
	if err != nil {
		return nil, fmt.Errorf("could not read compose file %s: %w", composeFile, err)
	}
	return composeFileBytes, nil
}

func (swarmStack *swarmStack) renderComposeTemplate(templateContents []byte) ([]byte, error) {
	valuesFile := path.Join(swarmStack.repo.path, swarmStack.valuesFile)
	valuesBytes, err := os.ReadFile(valuesFile)
	if err != nil {
		return nil, fmt.Errorf("could not read %s stack values file: %w", swarmStack.name, err)
	}
	var valuesMap map[string]any
	if err = yaml.Unmarshal(valuesBytes, &valuesMap); err != nil {
		return nil, fmt.Errorf("could not parse %s stack values file: %w", swarmStack.name, err)
	}
	templ, err := template.New(swarmStack.name).Parse(string(templateContents[:]))
	if err != nil {
		return nil, fmt.Errorf("could not parse %s stack compose file as a Go template: %w", swarmStack.name, err)
	}
	var stackContents bytes.Buffer
	err = templ.Execute(&stackContents, map[string]map[string]any{"Values": valuesMap})
	if err != nil {
		return nil, fmt.Errorf("error rending %s stack compose template: %w", swarmStack.name, err)
	}
	return stackContents.Bytes(), nil
}

func (swarmStack *swarmStack) parseStackString(stackContent []byte) (map[string]any, error) {
	var composeMap map[string]any
	err := yaml.Unmarshal(stackContent, &composeMap)
	if err != nil {
		return nil, fmt.Errorf("could not parse stack yaml: %w", err)
	}
	return composeMap, nil
}

func (swarmStack *swarmStack) decryptSopsFiles(composeMap map[string]any) (err error) {
	var sopsFiles []string
	if !swarmStack.discoverSecrets {
		sopsFiles = swarmStack.sopsFiles
	} else {
		sopsFiles, err = discoverSecrets(composeMap, swarmStack.composePath)
		if err != nil {
			return
		}
	}
	log := logger.With(
		slog.String("stack", swarmStack.name),
		swarmStack.refAttr(),
	)
	for _, sopsFile := range sopsFiles {
		log.Debug("decrypting secret...", "secret", sopsFile)
		err = util.DecryptFile(path.Join(swarmStack.repo.path, sopsFile))
		if err != nil {
			return
		}
	}
	return
}

// filters objects to only those with a "file" key, validating
// types along the way. The returned maps are references to the originals, so
// callers may mutate them (e.g. to set a "name" field).
func getFileObjects(objects map[string]any) (map[string]map[string]any, error) {
	result := make(map[string]map[string]any)
	for objectName, object := range objects {
		objectMap, ok := object.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("invalid compose file: %s object must be a map", objectName)
		}
		// only process objects with a file: key
		objectFileObj, ok := objectMap["file"]
		if !ok {
			continue
		}
		if _, ok = objectFileObj.(string); !ok {
			return nil, fmt.Errorf("invalid compose file: %s file field must be a string", objectName)
		}
		result[objectName] = objectMap
	}
	return result, nil
}

func discoverSecrets(composeMap map[string]any, composePath string) ([]string, error) {
	var sopsFiles []string
	if secrets, ok := composeMap["secrets"].(map[string]any); ok {
		fileObjects, err := getFileObjects(secrets)
		if err != nil {
			return nil, err
		}
		for _, secretMap := range fileObjects {
			secretFile := secretMap["file"].(string)
			secretPath := path.Join(path.Dir(composePath), secretFile)
			sopsFiles = append(sopsFiles, secretPath)
		}
	}
	return sopsFiles, nil
}

func (swarmStack *swarmStack) rotateConfigsAndSecrets(composeMap map[string]any) error {
	if configs, ok := composeMap["configs"].(map[string]any); ok {
		err := swarmStack.rotateObjects(configs, "configs")
		if err != nil {
			return fmt.Errorf("could not rotate one or more config files of stack %s: %w", swarmStack.name, err)
		}
	}
	if secrets, ok := composeMap["secrets"].(map[string]any); ok {
		err := swarmStack.rotateObjects(secrets, "secrets")
		if err != nil {
			return fmt.Errorf("could not rotate one or more secret files of stack %s: %w", swarmStack.name, err)
		}
	}
	return nil
}

func (swarmStack *swarmStack) rotateObjects(objects map[string]any, objectType string) error {
	objectsDir := path.Dir(path.Join(swarmStack.repo.path, swarmStack.composePath))
	fileObjects, err := getFileObjects(objects)
	if err != nil {
		return err
	}
	for objectName, objectMap := range fileObjects {
		log := logger.With(
			slog.String("stack", swarmStack.name),
			swarmStack.refAttr(),
			slog.String(objectType, objectName),
		)
		objectFile := objectMap["file"].(string)
		log.Debug("reading...", "file", objectFile)
		objectFilePath := path.Join(objectsDir, objectFile)
		configFileBytes, err := os.ReadFile(objectFilePath)
		if err != nil {
			return fmt.Errorf("could not read file %s for rotation: %w", objectFilePath, err)
		}
		log.Debug("computing hash...", "file", objectFile)
		hash := fmt.Sprintf("%x", md5.Sum(configFileBytes))[:8]
		newObjectName := swarmStack.name + "-" + objectName + "-" + hash
		log.Debug("renaming...", "new_name", newObjectName)
		objectMap["name"] = newObjectName
	}
	return nil
}

func (swarmStack *swarmStack) writeStack(composeMap map[string]any) error {
	composeFileBytes, err := yaml.Marshal(composeMap)
	if err != nil {
		return fmt.Errorf("could not store compose file as yaml after calculating hashes for stack %s", swarmStack.name)
	}
	composeFile := path.Join(swarmStack.repo.path, swarmStack.composePath)
	fileInfo, err := os.Stat(composeFile)
	if err != nil {
		return fmt.Errorf("could not stat compose file %s: %w", composeFile, err)
	}
	if err = os.WriteFile(composeFile, composeFileBytes, fileInfo.Mode()); err != nil {
		return fmt.Errorf("could not write compose file %s: %w", composeFile, err)
	}
	return nil
}

func (swarmStack *swarmStack) deployStack(resolveImage string) error {
	cmd := stack.NewStackCommand(dockerCli)
	cmd.SetArgs([]string{
		"deploy", "--detach", "--with-registry-auth",
		"--resolve-image", resolveImage,
		"-c", path.Join(swarmStack.repo.path, swarmStack.composePath),
		swarmStack.name,
	})
	// To stop printing errors and
	// usage message to stdout
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	err := cmd.Execute()
	if err != nil {
		return fmt.Errorf("could not deploy stack %s: %s", swarmStack.name, err)
	}
	return nil
}

// Returns "always" when alwaysPullContainers is true, "changed" otherwise.
// The stack-level setting overrides the global config setting.
func (swarmStack *swarmStack) resolveImageMode() string {
	alwaysPull := config.AlwaysPullContainers
	if swarmStack.alwaysPullContainers != nil {
		alwaysPull = *swarmStack.alwaysPullContainers
	}
	if alwaysPull {
		return imagePullAlways
	}
	return imagePullChanged
}
