package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestFrontendEnvironmentUsesRootDotenvAndProcessOverrides(t *testing.T) {
	root := t.TempDir()
	t.Setenv("OVERRIDE", "from-process")
	t.Setenv("EMPTY", "")
	t.Setenv("PROCESS_ONLY", "not-declared-in-dotenv")
	writeE2EFile(t, filepath.Join(root, ".env"), `# shared project configuration
SHARED_VALUE=shared-setting
export API_URL="https://example.test/a?x=1#anchor"
OVERRIDE=from-file
EMPTY=from-file
TITLE='shared frontend'
MULTILINE="first\nsecond"
__proto__=ordinary-setting
`)
	values, err := frontendEnvironment(root)
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"API_URL":      "https://example.test/a?x=1#anchor",
		"OVERRIDE":     "from-process",
		"EMPTY":        "",
		"TITLE":        "shared frontend",
		"MULTILINE":    "first\nsecond",
		"SHARED_VALUE": "shared-setting",
		"__proto__":    "ordinary-setting",
	} {
		if got, ok := values[key]; !ok || got != want {
			t.Fatalf("%s = %q (present=%v), want %q", key, got, ok, want)
		}
	}
	if _, found := values["PROCESS_ONLY"]; found {
		t.Fatal("process variable absent from .env was added to the frontend")
	}
	if os.Getenv("OVERRIDE") != "from-process" {
		t.Fatal("reading frontend variables changed the process environment")
	}
}

func TestFrontendEnvironmentRebuildsAndPreservesLastGoodOutput(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is required to execute the generated browser bundle")
	}
	for _, production := range []bool{false, true} {
		t.Run(map[bool]string{false: "development", true: "production"}[production], func(t *testing.T) {
			root := t.TempDir()
			config := defaultProjectConfig("example.com/frontend-env")
			config.FrontendConfigured = true
			config.FrontendRoot = "web"
			if err := os.MkdirAll(filepath.Join(root, "web/src"), 0o755); err != nil {
				t.Fatal(err)
			}
			writeE2EFile(t, filepath.Join(root, "web/src/main.ts"), `const key = "API_URL";
console.log(JSON.stringify({
  direct: import.meta.env.API_URL ?? null,
  computed: import.meta.env[key] ?? null,
  all: import.meta.env
}));
`)
			writeE2EFile(t, filepath.Join(root, "web/.env"), "API_URL=wrong-directory\n")
			builder, enabled, err := newFrontendBuilder(root, config, production, io.Discard)
			if err != nil || !enabled {
				t.Fatalf("new builder: enabled=%v error=%v", enabled, err)
			}
			defer builder.close()
			bundle := filepath.Join(root, "bin/frontend/assets/app.js")
			state := func() string {
				t.Helper()
				value, _, err := frontendSourceState(root, config)
				if err != nil {
					t.Fatal(err)
				}
				return value
			}
			assertRebuild := func(want any) {
				t.Helper()
				if err := builder.rebuild(io.Discard); err != nil {
					t.Fatal(err)
				}
				contents, err := os.ReadFile(bundle)
				if err != nil {
					t.Fatal(err)
				}
				command := exec.Command("node", "--input-type=module")
				command.Stdin = bytes.NewReader(contents)
				output, err := command.CombinedOutput()
				if err != nil {
					t.Fatalf("execute bundle: %v\n%s", err, output)
				}
				var result struct {
					Direct   any               `json:"direct"`
					Computed any               `json:"computed"`
					All      map[string]string `json:"all"`
				}
				if err := json.Unmarshal(output, &result); err != nil {
					t.Fatal(err)
				}
				if result.Direct != want || result.Computed != want {
					t.Fatalf("unexpected bundle environment: %s", output)
				}
				if want != nil {
					for key, value := range map[string]string{"SHARED_VALUE": "shared-setting", "__proto__": "ordinary-setting"} {
						if result.All[key] != value {
							t.Fatalf(".env key %s was filtered or changed: %s", key, output)
						}
					}
				}
			}
			assertRebuild(nil)
			for _, value := range []string{"first", "second with \"quotes\""} {
				before := state()
				writeE2EFile(t, filepath.Join(root, ".env"), "SHARED_VALUE=shared-setting\n__proto__=ordinary-setting\nAPI_URL='"+value+"'\n")
				if state() == before {
					t.Fatal("root .env change did not invalidate the frontend source state")
				}
				assertRebuild(value)
			}
			lastGood, err := os.ReadFile(bundle)
			if err != nil {
				t.Fatal(err)
			}
			writeE2EFile(t, filepath.Join(root, ".env"), "PRIVATE$KEY=private-dotenv-value\n")
			if err := builder.rebuild(io.Discard); err == nil || strings.Contains(err.Error(), "private-dotenv-value") {
				t.Fatalf("malformed dotenv did not fail with a redacted diagnostic: %v", err)
			}
			afterFailure, _ := os.ReadFile(bundle)
			if !bytes.Equal(afterFailure, lastGood) {
				t.Fatal("failed environment rebuild replaced the last good bundle")
			}
			before := state()
			if err := os.Remove(filepath.Join(root, ".env")); err != nil {
				t.Fatal(err)
			}
			if state() == before {
				t.Fatal("root .env deletion did not invalidate the frontend source state")
			}
			assertRebuild(nil)
		})
	}
}
