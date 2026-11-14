package util

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// loadConfigsFrom writes files into a temp dir, points CONFIGS_PATH at it,
// and runs LoadConfigs with fresh global state, restoring it afterwards.
func loadConfigsFrom(t *testing.T, files map[string]string) {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	t.Setenv("CONFIGS_PATH", dir)

	origConfigs, origConflict := Configs, Conflict
	t.Cleanup(func() { Configs, Conflict = origConfigs, origConflict })
	Configs = Config{}
	if err := LoadConfigs(); err != nil {
		t.Fatalf("LoadConfigs: %v", err)
	}
}

const (
	inlineStacksYAML = "stacks:\n  my-stack:\n    repo: my-repo\n    branch: main\n"
	inlineReposYAML  = "repos:\n  my-repo:\n    url: https://example.com/repo\n"
	splitStacksYAML  = "my-stack:\n  repo: my-repo\n  branch: main\n"
	splitReposYAML   = "my-repo:\n  url: https://example.com/repo\n"
)

func TestConfigWarnings(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{
			name: "split files only",
			files: map[string]string{
				"config.yaml": "update_interval: 60\n",
				"stacks.yaml": splitStacksYAML,
				"repos.yaml":  splitReposYAML,
			},
			want: []string{},
		},
		{
			name: "inline stacks without split file",
			files: map[string]string{
				"config.yaml": inlineStacksYAML,
				"repos.yaml":  splitReposYAML,
			},
			want: []string{},
		},
		{
			name: "inline stacks shadow stacks.yaml",
			files: map[string]string{
				"config.yaml": inlineStacksYAML,
				"stacks.yaml": "other-stack:\n  repo: other\n",
				"repos.yaml":  splitReposYAML,
			},
			want: []string{"stacks.yaml is ignored because stacks are defined in config.yaml"},
		},
		{
			name: "inline repos shadow repos.yml",
			files: map[string]string{
				"config.yaml": inlineReposYAML,
				"repos.yml":   "other-repo:\n  url: https://other.com\n",
				"stacks.yaml": splitStacksYAML,
			},
			want: []string{"repos.yaml is ignored because repos are defined in config.yaml"},
		},
		{
			name: "both inline and both shadowed",
			files: map[string]string{
				"config.yaml": inlineStacksYAML + inlineReposYAML,
				"stacks.yaml": splitStacksYAML,
				"repos.yaml":  splitReposYAML,
			},
			want: []string{
				"stacks.yaml is ignored because stacks are defined in config.yaml",
				"repos.yaml is ignored because repos are defined in config.yaml",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loadConfigsFrom(t, tt.files)
			if got := ConfigWarnings(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ConfigWarnings() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestConfigConflict_UsesConfigsPath verifies that split files are looked up
// in CONFIGS_PATH rather than the working directory.
func TestConfigConflict_UsesConfigsPath(t *testing.T) {
	cwd := t.TempDir()
	for _, name := range []string{"stacks.yaml", "repos.yaml"} {
		if err := os.WriteFile(filepath.Join(cwd, name), []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origDir) })

	loadConfigsFrom(t, map[string]string{"config.yaml": inlineStacksYAML + inlineReposYAML})

	if !Conflict.StacksInline || !Conflict.ReposInline {
		t.Errorf("expected inline stacks and repos, got %+v", Conflict)
	}
	if Conflict.StacksFileExists || Conflict.ReposFileExists {
		t.Errorf("split files in the working directory must not count, got %+v", Conflict)
	}
}

func TestGetConfigsPath(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		envSet   bool
		want     string
	}{
		{
			name:     "env var set with path",
			envValue: "/custom/config/path",
			envSet:   true,
			want:     "/custom/config/path",
		},
		{
			name:     "env var not set",
			envValue: "",
			envSet:   false,
			want:     ".",
		},
		{
			name:     "env var set to empty string",
			envValue: "",
			envSet:   true,
			want:     ".",
		},
	}
	const envVar = "CONFIGS_PATH"
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envSet {
				t.Setenv(envVar, tt.envValue)
			} else {
				t.Setenv(envVar, "")
				if err := os.Unsetenv(envVar); err != nil {
					t.Fatal(err)
				}
			}

			if got := getConfigsPath(); got != tt.want {
				t.Errorf("getConfigsPath() = %v, want %v", got, tt.want)
			}
		})
	}
}
