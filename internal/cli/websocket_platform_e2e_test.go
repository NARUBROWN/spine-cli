package cli

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestGeneratedWebSocketPublicAssembly(t *testing.T) {
	binary := buildUpdateCLI(t)
	fixture, err := os.ReadFile("testdata/assembly_websocket_test.go.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, layout := range []string{layeredLayout, "legacy"} {
		for _, validator := range []string{"none", "playground"} {
			t.Run(layout+"/"+validator, func(t *testing.T) {
				if validator == "playground" {
					requireValidatorToolchain(t)
				}
				workspace := t.TempDir()
				applyAssemblyPlan(t, binary, workspace, "new", "sample", "--module", "example.com/sample", "--layout", layout, "--websocket-platform", "gorilla", "--validator", validator)
				project := filepath.Join(workspace, "sample")
				path := filepath.Join(project, "internal/application/application.go")
				source, _ := os.ReadFile(path)
				for _, command := range [][]string{{"context", "--json"}, {"contract", "check", "--json"}, {"inspect", "--json"}} {
					result := runE2E(binary, project, nil, command...)
					assertE2ESuccess(t, result, "WS static assembly")
					if !strings.Contains(result.stdout, "gorilla") {
						t.Fatal("WS selection missing:", result.stdout)
					}
				}
				text := strings.ReplaceAll(string(fixture), "{{VALIDATOR}}", strconv.FormatBool(validator == "playground"))
				addGeneratedTest(t, project, "websocket_e2e_test.go", text)
				generatedGo(t, project, "mod", "tidy")
				assertE2ESuccess(t, runE2E(binary, project, nil, "verify", "--json"), "generated WS local verify")
				t.Log(generatedGo(t, project, "test", "-race", "-v", "./...", "-count=1", "-timeout=90s"))
				after, _ := os.ReadFile(path)
				if string(source) != string(after) {
					t.Fatal("WS runtime test modified generated assembly")
				}
				if replaces := strings.TrimSpace(generatedGo(t, project, "list", "-m", "-f", "{{if .Replace}}{{.Path}}{{end}}", "all")); replaces != "" {
					t.Fatalf("local replacements: %s", replaces)
				}
				t.Logf("unchanged generated WS assembly sha256 %x", sha256.Sum256(source))
			})
		}
	}
}
