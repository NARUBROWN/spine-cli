package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNATSAssemblySelection(t *testing.T) {
	for _, value := range []string{"nats", "events:nats"} {
		spec, err := parseEventPlatform(value)
		if err != nil || spec.Provider != "nats" || spec.Name == "" {
			t.Fatalf("selection %q: %+v %v", value, spec, err)
		}
		options := newProjectOptions{Module: "example.com/nats-app", Platform: "none", Layout: layeredLayout, Validator: "none", Events: []eventPlatformSpec{spec}}
		if err := validateAssemblySelection(options.Platform, options.Events, options.Validator); err != nil {
			t.Fatal(err)
		}
		if assemblyGoVersion(options.Validator, options.Events...) != "1.26.0" {
			t.Fatal("NATS minimum Go version was not selected")
		}
		if selectedModuleVersion(currentAssemblyModules(options), natsPlatformPath) != "preview" {
			t.Fatal("NATS did not use the dynamic Preview query")
		}
		files, err := newProjectFiles(options)
		if err != nil {
			t.Fatal(err)
		}
		for path, expected := range map[string][]string{
			"go.mod":                                       {"go 1.26.0", natsPlatformPath},
			"internal/application/application.go":          {"natsplatform.NewFactory", `spine.WithEventAdapter("` + spec.Name + `"`},
			"internal/application/platform_config.go":      {"loadNatsConfig", "CONSUMER_PREFIX", "CREDENTIALS_FILE", "FailureReject"},
			"internal/application/platform_config_test.go": {eventEnvPrefix(spec.Name) + "URLS", eventEnvPrefix(spec.Name) + "STREAM"},
			"internal/application/application_test.go":     {eventEnvPrefix(spec.Name) + "CONSUMER_PREFIX", "nats://127.0.0.1:4222"},
		} {
			for _, text := range expected {
				if !strings.Contains(files[path], text) {
					t.Fatalf("%s is missing %q", path, text)
				}
			}
		}
	}
	if assemblyGoVersion("none") != "1.25.5" {
		t.Fatal("existing minimum Go version changed")
	}
}

func TestNATSPlanIsolationAndLegacyRecipeGuard(t *testing.T) {
	inTempDir(t)
	var out, errOut bytes.Buffer
	args := []string{"worker", "--platform", "none", "--event-platform", "events:nats", "--skip-install"}
	if code := runNew(args, &out, &errOut); code != 0 {
		t.Fatal(errOut.String())
	}
	if err := os.Chdir("worker"); err != nil {
		t.Fatal(err)
	}
	configPath := "spine.toml"
	config, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	writeE2EFile(t, configPath, strings.ReplaceAll(string(config), currentAssemblyRecipe, stableAssemblyRecipe))
	before, _ := os.ReadFile("go.mod")
	out.Reset()
	errOut.Reset()
	if code := runUpdate([]string{"--dry-run", "--json"}, &out, &errOut); code != 3 || !strings.Contains(errOut.String(), "NATS requires") {
		t.Fatalf("legacy NATS recipe was accepted: %d %s %s", code, out.String(), errOut.String())
	}
	after, _ := os.ReadFile(filepath.Clean("go.mod"))
	if !bytes.Equal(before, after) {
		t.Fatal("rejected update changed go.mod")
	}
}
