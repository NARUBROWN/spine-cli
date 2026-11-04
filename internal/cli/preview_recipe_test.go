package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestPreviewCLIPreservesExistingStableAssemblyUpdate(t *testing.T) {
	if err := prepareTestModuleCache(); err != nil {
		t.Fatal(err)
	}
	inTempDir(t)
	var out, errOut bytes.Buffer
	if code := runNew([]string{"sample", "--module", "example.com/sample", "--websocket-platform", "gorilla", "--event-platform", "kafka", "--event-platform", "rabbitmq", "--skip-install"}, &out, &errOut); code != 0 {
		t.Fatal(errOut.String())
	}
	if err := os.Chdir("sample"); err != nil {
		t.Fatal(err)
	}
	config, err := os.ReadFile("spine.toml")
	if err != nil {
		t.Fatal(err)
	}
	writeE2EFile(t, "spine.toml", strings.ReplaceAll(string(config), currentAssemblyRecipe, stableAssemblyRecipe))
	mod, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	writeE2EFile(t, "go.mod", strings.ReplaceAll(strings.ReplaceAll(string(mod), currentAssemblySpineVersion, "v0.6.5"), previewPlatformVersion, "v0.1.0"))
	before, _ := os.ReadFile("internal/application/application.go")
	plan, code := updateTestResult(t, "--dry-run")
	if code != 0 || plan["recipe"] != stableAssemblyRecipe || plan["stability"] != "released" {
		t.Fatalf("stable assembly plan changed: %d %+v", code, plan)
	}
	if result, code := updateTestResult(t, "--if-match", plan["base_hash"].(string)); code != 0 {
		t.Fatalf("stable assembly update failed: %d %+v", code, result)
	}
	after, _ := os.ReadFile("internal/application/application.go")
	if !bytes.Equal(before, after) {
		t.Fatal("stable assembly update rewrote the composition")
	}
	mod, _ = os.ReadFile("go.mod")
	if strings.Contains(string(mod), "-preview.") || !strings.Contains(string(mod), spineImportPath+" v0.6.5") {
		t.Fatalf("stable assembly was migrated implicitly: %s", mod)
	}
	for _, module := range []string{echoPlatformPath, gorillaPlatformPath, kafkaPlatformPath, rabbitPlatformPath} {
		if !strings.Contains(string(mod), module+" v0.1.0") {
			t.Fatalf("stable assembly module was lost: %s\n%s", module, mod)
		}
	}
}
