package cli

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestRuntimeOpenAPIGeneratedApplications(t *testing.T) {
	binary := buildUpdateCLI(t)
	for _, layout := range []string{layeredLayout, "legacy"} {
		for _, validator := range []string{"none", "playground"} {
			t.Run(layout+"/"+validator, func(t *testing.T) {
				if validator == "playground" {
					requireValidatorToolchain(t)
				}
				workspace := t.TempDir()
				applyAssemblyPlan(t, binary, workspace, "new", "sample", "--module", "example.com/sample", "--layout", layout, "--validator", validator)
				project := filepath.Join(workspace, "sample")
				path := filepath.Join(project, "internal/application/application.go")
				source, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				updated := strings.Replace(string(source), "app.Constructor(", "app.Constructor(NewPreviewController)\napp.Route(\"GET\", \"/preview/:id\", (*PreviewController).Get)\napp.Constructor(", 1)
				if updated == string(source) {
					t.Fatal("generated health registration was not found")
				}
				writeE2EFile(t, path, updated)
				for _, fixture := range []struct{ from, to string }{{"preview_http.go.txt", "preview_http.go"}, {"preview_openapi_test.go.txt", "preview_openapi_test.go"}} {
					contents, err := os.ReadFile(filepath.Join("testdata", fixture.from))
					if err != nil {
						t.Fatal(err)
					}
					addGeneratedTest(t, project, fixture.to, strings.ReplaceAll(string(contents), "{{VALIDATOR}}", strconv.FormatBool(validator == "playground")))
				}
				assertE2ESuccess(t, runE2E(binary, project, nil, "contract", "check", "--json"), "structured HTTP contract")
				assertE2ESuccess(t, runE2E(binary, project, nil, "contract", "export", "--format", "openapi", "--source", "runtime", "--output", "exported-openapi.json"), "runtime OpenAPI export")
				t.Log(generatedGo(t, project, "test", "-race", "-v", "./...", "-count=1", "-timeout=90s"))
			})
		}
	}
}
