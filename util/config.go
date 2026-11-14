package util

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

// ConfigConflict records whether stacks/repos are defined inline in
// config.yaml and whether the corresponding split files also exist in the
// configs path (in which case the split files are silently ignored).
type ConfigConflict struct {
	StacksInline     bool
	ReposInline      bool
	StacksFileExists bool
	ReposFileExists  bool
}

// Conflict is populated during LoadConfigs and reflects the inline vs.
// split-file state detected at startup.
var Conflict ConfigConflict

type StackConfig struct {
	Repo                 string
	Branch               string
	Tag                  string
	ComposeFile          string   `mapstructure:"compose_file"`
	ValuesFile           string   `mapstructure:"values_file"`
	SopsFiles            []string `mapstructure:"sops_files"`
	SopsSecretsDiscovery bool     `mapstructure:"sops_secrets_discovery"`
	AlwaysPullContainers *bool    `mapstructure:"always_pull_containers"`
	UpdateWindow         string   `mapstructure:"update_window"`
}

type RepoConfig struct {
	Url          string
	Username     string
	Password     string
	PasswordFile string `mapstructure:"password_file"`
}

type Config struct {
	ReposPath            string                  `mapstructure:"repos_path"`
	UpdateInterval       int                     `mapstructure:"update_interval"`
	Concurrency          int                     `mapstructure:"concurrency"`
	AutoRotate           bool                    `mapstructure:"auto_rotate"`
	StackConfigs         map[string]*StackConfig `mapstructure:"stacks"`
	RepoConfigs          map[string]*RepoConfig  `mapstructure:"repos"`
	SopsSecretsDiscovery bool                    `mapstructure:"sops_secrets_discovery"`
	Address              string                  `mapstructure:"address"`
	AlwaysPullContainers bool                    `mapstructure:"always_pull_containers"`
}

var Configs Config

func LoadConfigs() (err error) {
	configsPath := getConfigsPath()
	Logger.Info(fmt.Sprintf("using configuration path: %s", configsPath))
	err = readConfig(configsPath)
	if err != nil {
		return fmt.Errorf("could not read configuration file: %w", err)
	}
	Conflict = detectConflicts(configsPath)
	if Configs.RepoConfigs == nil {
		err = readRepoConfigs(configsPath)
		if err != nil {
			return fmt.Errorf("could not read repos file: %w", err)
		}
	}
	if Configs.StackConfigs == nil {
		err = readStackConfigs(configsPath)
		if err != nil {
			return fmt.Errorf("could not load stacks file: %w", err)
		}
	}
	validateConfig()
	for _, warning := range ConfigWarnings() {
		Logger.Warn(warning)
	}
	return nil
}

// detectConflicts checks whether stacks/repos were loaded inline from
// config.yaml and whether the corresponding split files exist in path.
// It must run before the split files are read.
func detectConflicts(path string) ConfigConflict {
	return ConfigConflict{
		StacksInline:     Configs.StackConfigs != nil,
		ReposInline:      Configs.RepoConfigs != nil,
		StacksFileExists: yamlFileExists(path, "stacks"),
		ReposFileExists:  yamlFileExists(path, "repos"),
	}
}

func yamlFileExists(dir, name string) bool {
	for _, ext := range []string{".yaml", ".yml"} {
		if _, err := os.Stat(filepath.Join(dir, name+ext)); err == nil {
			return true
		}
	}
	return false
}

// ConfigWarnings returns human-readable warnings for split config files
// that are ignored because config.yaml defines the same section inline.
// The returned slice is empty when no conflicts exist.
func ConfigWarnings() []string {
	return configWarnings(Conflict)
}

func configWarnings(c ConfigConflict) []string {
	warnings := []string{}
	if c.StacksInline && c.StacksFileExists {
		warnings = append(warnings, "stacks.yaml is ignored because stacks are defined in config.yaml")
	}
	if c.ReposInline && c.ReposFileExists {
		warnings = append(warnings, "repos.yaml is ignored because repos are defined in config.yaml")
	}
	return warnings
}

func getConfigsPath() string {
	if path := os.Getenv("CONFIGS_PATH"); path != "" {
		return path
	}
	return "."
}

const defaultWorkers = 3

func readConfig(path string) (err error) {
	configViper := viper.New()
	configViper.SetConfigName("config")
	configViper.AddConfigPath(path)
	configViper.SetDefault("update_interval", 120)
	configViper.SetDefault("concurrency", defaultWorkers)
	configViper.SetDefault("repos_path", "repos")
	configViper.SetDefault("auto_rotate", true)
	configViper.SetDefault("sops_secrets_discovery", false)
	configViper.SetDefault("always_pull_containers", true)
	configViper.SetDefault("address", "0.0.0.0:8080")
	err = configViper.ReadInConfig()
	if err != nil && !errors.As(err, &viper.ConfigFileNotFoundError{}) {
		return
	}
	return configViper.Unmarshal(&Configs)
}

func readRepoConfigs(path string) (err error) {
	reposViper := viper.New()
	reposViper.SetConfigName("repos")
	reposViper.AddConfigPath(path)
	err = reposViper.ReadInConfig()
	if err != nil {
		return
	}
	return reposViper.Unmarshal(&Configs.RepoConfigs)
}

func readStackConfigs(path string) (err error) {
	stacksViper := viper.New()
	stacksViper.SetConfigName("stacks")
	stacksViper.AddConfigPath(path)
	err = stacksViper.ReadInConfig()
	if err != nil {
		return
	}
	return stacksViper.Unmarshal(&Configs.StackConfigs)
}

func validateConfig() {
	if Configs.Concurrency <= 0 {
		Logger.Warn(fmt.Sprintf("Invalid `config.concurrency value`, using default: %v", defaultWorkers))
		Configs.Concurrency = defaultWorkers
	}
}
