package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestPreviewCLIPreservesExistingStableAssemblyUpdate(t *testing.T) {
	checkExistingRecipeUpdate(t, stableAssemblyRecipe, "v0.6.5", "v0.1.0", "released")
}

func TestPreviewCLIPreservesExistingPreview2AssemblyUpdate(t *testing.T) {
	checkExistingRecipeUpdate(t, preview2AssemblyRecipe, "v0.7.0-preview.2", "v0.2.0-preview.1", "prerelease")
}

func TestPreviewCLIPreservesExistingPreview1AssemblyUpdate(t *testing.T) {
	checkExistingRecipeUpdate(t, preview1AssemblyRecipe, "v0.7.0-preview.1", "v0.2.0-preview.1", "prerelease")
	source := `import "github.com/NARUBROWN/spine/pkg/path"
type SampleController struct{}
func (*SampleController) Get(id path.Int) int64 { return id.Value }`
	generated := httpControllerInputTemplate(source, "Sample")
	if !strings.Contains(generated, "params *SamplePath") || !strings.Contains(generated, "params.ID") {
		t.Fatalf("Preview 1 path DTO generation changed: %s", generated)
	}
}

func checkExistingRecipeUpdate(t *testing.T, recipe, coreVersion, platformVersion, stability string) {
	t.Helper()
	if err := prepareTestModuleCache(); err != nil {
		t.Fatal(err)
	}
	inTempDir(t)
	var out, errOut bytes.Buffer
	if code := runNew([]string{"sample", "--module", "example.com/sample", "--channel", "preview", "--websocket-platform", "gorilla", "--event-platform", "kafka", "--event-platform", "rabbitmq", "--skip-install"}, &out, &errOut); code != 0 {
		t.Fatal(errOut.String())
	}
	if err := os.Chdir("sample"); err != nil {
		t.Fatal(err)
	}
	config, err := os.ReadFile("spine.toml")
	if err != nil {
		t.Fatal(err)
	}
	writeE2EFile(t, "spine.toml", removeDependencyChannelForLegacyFixture(strings.ReplaceAll(string(config), currentAssemblyRecipe, recipe)))
	mod, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	writeE2EFile(t, "go.mod", strings.ReplaceAll(strings.ReplaceAll(string(mod), currentAssemblySpineVersion, coreVersion), previewPlatformVersion, platformVersion))
	before, _ := os.ReadFile("internal/application/application.go")
	plan, code := updateTestResult(t, "--dry-run")
	if code != 0 || plan["recipe"] != recipe || plan["stability"] != stability {
		t.Fatalf("existing recipe plan changed: %d %+v", code, plan)
	}
	if result, code := updateTestResult(t, "--if-match", plan["base_hash"].(string)); code != 0 {
		t.Fatalf("existing recipe update failed: %d %+v", code, result)
	}
	after, _ := os.ReadFile("internal/application/application.go")
	if !bytes.Equal(before, after) {
		t.Fatal("existing recipe update rewrote the composition")
	}
	mod, _ = os.ReadFile("go.mod")
	if !strings.Contains(string(mod), spineImportPath+" "+coreVersion) {
		t.Fatalf("existing recipe was migrated implicitly: %s", mod)
	}
	for _, module := range []string{echoPlatformPath, gorillaPlatformPath, kafkaPlatformPath, rabbitPlatformPath} {
		if !strings.Contains(string(mod), module+" "+platformVersion) {
			t.Fatalf("existing recipe module was lost: %s\n%s", module, mod)
		}
	}
}

func removeDependencyChannelForLegacyFixture(config string) string {
	lines := []string{}
	for _, line := range strings.Split(config, "\n") {
		if !strings.HasPrefix(line, "dependency_channel = ") {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}
