package cli

import (
	goversion "go/version"
	"os"
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
