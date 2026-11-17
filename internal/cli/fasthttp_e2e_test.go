package cli

import (
	goversion "go/version"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func requireFastHTTPToolchain(t *testing.T) {
	t.Helper()
	if goversion.Compare(runtime.Version(), "go1.26.0") < 0 {
		t.Skip("fasthttp requires Go 1.26.0+")
	}
}

func TestGeneratedFastHTTPInteractiveEndToEnd(t *testing.T) {
	requireFastHTTPToolchain(t)
	binary := buildUpdateCLI(t)
	workspace := t.TempDir()
	command := exec.CommandContext(t.Context(), binary, "new", "wizard-api", "--module", "example.com/wizard-api", "--interactive")
	command.Dir = workspace
	command.Env = mergeE2EEnvironment([]string{"GOWORK=off"})
	command.Stdin = strings.NewReader("\n3\n\n\n")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("interactive fasthttp generation: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "  3. fasthttp\n") {
		t.Fatalf("fasthttp was not offered as a numbered HTTP choice: %s", output)
	}
	project := filepath.Join(workspace, "wizard-api")
	config, err := os.ReadFile(filepath.Join(project, "spine.toml"))
	if err != nil || !strings.Contains(string(config), `platform = "fasthttp"`) {
		t.Fatalf("interactive selection was not applied: %s %v", config, err)
	}
	mod, err := os.ReadFile(filepath.Join(project, "go.mod"))
	if err != nil || !strings.Contains(string(mod), "go 1.26.0") || !strings.Contains(string(mod), providerModule("fasthttp")) {
		t.Fatalf("interactive project dependencies: %s %v", mod, err)
	}
	assertE2ESuccess(t, runE2E(binary, project, nil, "contract", "check", "--warnings-as-errors", "--json"), "interactive fasthttp static contract")
	generatedGo(t, project, "test", "./...", "-count=1")
}

func TestGeneratedFastHTTPPublicEndToEnd(t *testing.T) {
	requireFastHTTPToolchain(t)
	binary := buildUpdateCLI(t)
	fixture, err := os.ReadFile("testdata/fasthttp_http_test.go.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, layout := range []string{layeredLayout, "legacy"} {
		for _, validator := range []string{"none", "playground"} {
			t.Run(layout+"/"+validator, func(t *testing.T) {
				workspace := t.TempDir()
				applyAssemblyPlan(t, binary, workspace, "new", "fast-api", "--module", "example.com/fast-api", "--platform", "fasthttp", "--channel", "preview", "--layout", layout, "--validator", validator, "--no-interactive")
				project := filepath.Join(workspace, "fast-api")
				mod, err := os.ReadFile(filepath.Join(project, "go.mod"))
				if err != nil || !strings.Contains(string(mod), "go 1.26.0") || strings.Contains(string(mod), "replace") {
					t.Fatalf("public recipe and minimum Go: %s %v", mod, err)
				}
				check := runE2E(binary, project, nil, "contract", "check", "--warnings-as-errors", "--json")
				assertE2ESuccess(t, check, "fasthttp generated static contract")
				for _, want := range []string{`"provider": "fasthttp"`, `"expected_contract": "spine.http-adapter/v3"`, providerModule("fasthttp") + ".Option"} {
					if !strings.Contains(check.stdout, want) {
						t.Fatalf("missing %s in contract: %s", want, check.stdout)
					}
				}
				addGeneratedTest(t, project, "fasthttp_e2e_test.go", strings.ReplaceAll(string(fixture), "{{VALIDATOR}}", strconv.FormatBool(validator == "playground")))
				generatedGo(t, project, "mod", "tidy")
				verify := runE2E(binary, project, nil, "verify", "--json")
				assertE2ESuccess(t, verify, "fasthttp generated verification with actual TCP requests")
				output := generatedGo(t, project, "test", "-race", "-v", "./...", "-count=1", "-timeout=90s")
				if strings.Contains(output, "--- SKIP") {
					t.Fatalf("fasthttp E2E was skipped: %s", output)
				}
				t.Log(output)
				assertE2ESuccess(t, runE2E(binary, project, nil, "contract", "export", "--format", "openapi", "--source", "runtime", "--prepare", "--output", "openapi.json"), "fasthttp runtime OpenAPI export")
				before, _ := os.ReadFile(filepath.Join(project, "internal/application/application.go"))
				applyAssemblyPlan(t, binary, project, "update")
				after, _ := os.ReadFile(filepath.Join(project, "internal/application/application.go"))
				if string(before) != string(after) {
					t.Fatal("fasthttp update changed the generated composition")
				}
				t.Log(generatedGo(t, project, "list", "-m", spineImportPath, providerModule("fasthttp")))
			})
		}
	}
}
