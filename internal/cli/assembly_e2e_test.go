package cli

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"go/format"
	goversion "go/version"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func requireValidatorToolchain(t *testing.T) {
	t.Helper()
	if goversion.Compare(runtime.Version(), "go1.26.0") < 0 {
		t.Skip("go-playground v10.30.6 requires Go 1.26.0+")
	}
}

func applyAssemblyPlan(t *testing.T, binary, directory string, args ...string) {
	t.Helper()
	plan := runE2E(binary, directory, nil, append(append([]string{}, args...), "--dry-run", "--json")...)
	assertE2ESuccess(t, plan, "assembly dry-run")
	var envelope struct {
		Schema string `json:"schema_version"`
		OK     bool   `json:"ok"`
		Result struct {
			Hash    string                             `json:"base_hash"`
			Diff    string                             `json:"diff"`
			Changes []struct{ Path, Operation string } `json:"changes"`
			Stages  []installationStage                `json:"stages"`
			Modules []selectedModule                   `json:"selected_modules"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(plan.stdout), &envelope); err != nil || !envelope.OK || envelope.Schema != protocolSchemaVersion || envelope.Result.Hash == "" || (envelope.Result.Diff == "" && args[0] != "update") || len(envelope.Result.Changes) == 0 {
		t.Fatalf("unreviewable plan: %v %s", err, plan.stdout)
	}
	for _, module := range envelope.Result.Modules {
		if module.Replace != nil {
			t.Fatalf("non-public module: %+v", module)
		}
	}
	t.Logf("reviewed %s: %d changes, diff sha256 %x, base_hash %s", strings.Join(args, " "), len(envelope.Result.Changes), sha256.Sum256([]byte(envelope.Result.Diff)), envelope.Result.Hash)
	assertE2ESuccess(t, runE2E(binary, directory, nil, append(append([]string{}, args...), "--if-match", envelope.Result.Hash, "--json")...), "assembly apply")
}

func generatedGo(t *testing.T, directory string, args ...string) string {
	t.Helper()
	cmd := exec.Command("go", args...)
	cmd.Dir = directory
	cmd.Env = mergeE2EEnvironment([]string{"GOWORK=off", "GOTOOLCHAIN=local"})
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("generated go %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return string(output)
}

func addGeneratedTest(t *testing.T, project, name, source string) {
	t.Helper()
	contents, err := format.Source([]byte(source))
	if err != nil {
		t.Fatalf("invalid fixture %s: %v", name, err)
	}
	writeE2EFile(t, filepath.Join(project, "internal/application", name), string(contents))
}

func TestGeneratedAssemblyPublicMatrix(t *testing.T) {
	binary := buildUpdateCLI(t)
	for _, layout := range []string{layeredLayout, "legacy"} {
		for _, scenario := range []struct {
			name      string
			args      []string
			validator bool
		}{
			{"http", nil, false},
			{"nethttp", []string{"--platform", "nethttp"}, false},
			{"nethttp-validator", []string{"--platform", "nethttp", "--validator", "playground", "--websocket-platform", "gorilla"}, true},
			{"kafka", []string{"--event-platform", "kafka"}, false},
			{"rabbitmq", []string{"--event-platform", "rabbitmq"}, false},
			{"worker", []string{"--platform", "none", "--event-platform", "events:kafka"}, false},
			{"multiple", []string{"--event-platform", "primary:kafka", "--event-platform", "secondary:kafka", "--event-platform", "jobs:rabbitmq"}, false},
			{"validator", []string{"--validator", "playground", "--websocket-platform", "gorilla"}, true},
			{"all", []string{"--event-platform", "kafka", "--event-platform", "rabbitmq", "--validator", "playground"}, true},
		} {
			t.Run(layout+"/"+scenario.name, func(t *testing.T) {
				if scenario.validator {
					requireValidatorToolchain(t)
				}
				workspace := t.TempDir()
				args := append([]string{"new", "sample", "--module", "example.com/sample", "--layout", layout}, scenario.args...)
				applyAssemblyPlan(t, binary, workspace, args...)
				project := filepath.Join(workspace, "sample")
				mod, _ := os.ReadFile(filepath.Join(project, "go.mod"))
				if strings.Contains(string(mod), "replace") {
					t.Fatal("generated module contains replace")
				}
				generatedGo(t, project, "build", "./...")
				assertE2ESuccess(t, runE2E(binary, project, nil, "context", "--json"), "generated context")
				assertE2ESuccess(t, runE2E(binary, project, nil, "contract", "check", "--json"), "generated static contract")
				fixture := ""
				if scenario.name == "validator" || scenario.name == "nethttp-validator" {
					fixture = "assembly_http_test.go.txt"
				}
				if scenario.name == "http" || scenario.name == "nethttp" {
					fixture = "assembly_no_validator_test.go.txt"
				}
				if fixture != "" {
					data, err := os.ReadFile(filepath.Join("testdata", fixture))
					if err != nil {
						t.Fatal(err)
					}
					addGeneratedTest(t, project, "validation_e2e_test.go", string(data))
				}
				assertE2ESuccess(t, runE2E(binary, project, nil, "verify", "--json"), "generated local verify")
				if scenario.validator {
					t.Log(generatedGo(t, project, "test", "-race", "./...", "-count=1", "-timeout=90s"))
				}
				if scenario.name == "all" {
					before, _ := os.ReadFile(filepath.Join(project, "internal/application/application.go"))
					applyAssemblyPlan(t, binary, project, "update")
					after, _ := os.ReadFile(filepath.Join(project, "internal/application/application.go"))
					if string(before) != string(after) {
						t.Fatal("update rewrote composition")
					}
					testValidatorDiagnostics(t, binary, project)
				}
			})
		}
	}
}

func testValidatorDiagnostics(t *testing.T, binary, project string) {
	t.Helper()
	path := filepath.Join(project, "internal/application/application.go")
	source, _ := os.ReadFile(path)
	for _, scenario := range []struct{ before, after, code string }{
		{"validator, err := playground.New()", "validator, err := unknownValidator()", "VALIDATOR_UNRESOLVED"},
		{"spine.WithValidator(validator),", "spine.WithValidator(validator), spine.WithValidator(validator),", "VALIDATOR_DUPLICATE"},
		{"spine.WithValidator(validator)", "spine.WithValidator(dynamicValidator())", "VALIDATOR_UNRESOLVED"},
	} {
		updated := strings.Replace(string(source), scenario.before, scenario.after, 1)
		if updated == string(source) {
			t.Fatalf("missing validator mutation %s", scenario.before)
		}
		writeE2EFile(t, path, updated)
		result := runE2E(binary, project, nil, "contract", "check", "--json")
		if result.code == 0 || !strings.Contains(result.stdout, scenario.code) {
			t.Fatalf("missing %s: %+v", scenario.code, result)
		}
	}
	writeE2EFile(t, path, string(source))
}

func TestGeneratedAssemblyBrokerE2E(t *testing.T) {
	if os.Getenv("SPINE_TEST_KAFKA_BROKER") == "" || os.Getenv("SPINE_TEST_RABBITMQ_URL") == "" {
		t.Skip("set both SPINE_TEST_KAFKA_BROKER and SPINE_TEST_RABBITMQ_URL to run actual broker E2E")
	}
	requireValidatorToolchain(t)
	binary := buildUpdateCLI(t)
	fixture, err := os.ReadFile("testdata/assembly_broker_test.go.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, layout := range []string{layeredLayout, "legacy"} {
		for _, provider := range []string{"kafka", "rabbitmq"} {
			for _, http := range []string{"echo", "none", "websocket"} {
				t.Run(fmt.Sprintf("%s/%s/%s", layout, provider, http), func(t *testing.T) {
					workspace := t.TempDir()
					selectedHTTP := http
					if selectedHTTP == "websocket" {
						selectedHTTP = "echo"
					}
					args := []string{"new", "sample", "--module", "example.com/sample", "--layout", layout, "--platform", selectedHTTP, "--event-platform", "events:" + provider, "--validator", "playground"}
					if http == "websocket" {
						args = append(args, "--websocket-platform", "gorilla")
					}
					applyAssemblyPlan(t, binary, workspace, args...)
					project := filepath.Join(workspace, "sample")
					source, _ := os.ReadFile(filepath.Join(project, "internal/application/application.go"))
					text := strings.ReplaceAll(string(fixture), "{{PROVIDER}}", provider)
					text = strings.ReplaceAll(text, "{{HTTP}}", selectedHTTP)
					text = strings.ReplaceAll(text, "{{WEBSOCKET}}", fmt.Sprint(http == "websocket"))
					module, loader, group := kafkaPlatformPath, "loadKafkaConfig", "config.GroupID = group"
					if provider == "rabbitmq" {
						module, loader, group = rabbitPlatformPath, "loadRabbitmqConfig", ""
					}
					text = strings.ReplaceAll(text, "{{PLATFORM_PATH}}", module)
					text = strings.ReplaceAll(text, "{{CONFIG_LOADER}}", loader)
					text = strings.ReplaceAll(text, "{{GROUP_CONFIG}}", group)
					addGeneratedTest(t, project, "broker_e2e_test.go", brokerPublishFixture(t, project, text))
					generatedGo(t, project, "mod", "tidy")
					output := generatedGo(t, project, "test", "-race", "-v", "./...", "-count=1", "-timeout=3m")
					if strings.Contains(output, "--- SKIP") {
						t.Fatalf("broker test was skipped: %s", output)
					}
					t.Log(output)
					after, _ := os.ReadFile(filepath.Join(project, "internal/application/application.go"))
					if string(source) != string(after) {
						t.Fatal("runtime fixture modified the generated composition")
					}
					t.Logf("unchanged generated BuildApplication sha256 %x", sha256.Sum256(source))
				})
			}
		}
	}
}

func TestGeneratedAssemblyMinimumToolchain(t *testing.T) {
	go125 := os.Getenv("SPINE_TEST_GO125")
	if go125 == "" {
		t.Skip("set SPINE_TEST_GO125 to a Go 1.25.5 executable for the minimum-toolchain installation check")
	}
	binary := buildUpdateCLI(t)
	env := []string{"PATH=" + filepath.Dir(go125) + string(os.PathListSeparator) + os.Getenv("PATH"), "GOTOOLCHAIN=local"}
	root := t.TempDir()
	args := []string{"new", "sample", "--module", "example.com/sample", "--event-platform", "kafka", "--event-platform", "rabbitmq", "--websocket-platform", "gorilla"}
	plan := runE2E(binary, root, env, append(append([]string{}, args...), "--dry-run", "--json")...)
	assertE2ESuccess(t, plan, "Go 1.25.5 install plan without validator")
	var envelope struct {
		Result struct {
			Hash string `json:"base_hash"`
			Diff string `json:"diff"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(plan.stdout), &envelope); err != nil || envelope.Result.Hash == "" || envelope.Result.Diff == "" {
		t.Fatalf("invalid minimum toolchain plan: %v", err)
	}
	assertE2ESuccess(t, runE2E(binary, root, env, append(append([]string{}, args...), "--if-match", envelope.Result.Hash, "--json")...), "Go 1.25.5 public install")
	assertE2ESuccess(t, runE2E(binary, filepath.Join(root, "sample"), env, "verify", "--json"), "Go 1.25.5 generated test/build")
	failed := runE2E(binary, root, env, "new", "needs-validator", "--validator", "playground", "--json")
	if failed.code != 3 || !strings.Contains(failed.stdout, `"failed_stage": "dependencies"`) || !strings.Contains(failed.stdout, "1.26") {
		t.Fatalf("minimum validator toolchain was not enforced: %+v", failed)
	}
	if _, err := os.Stat(filepath.Join(root, "needs-validator")); !os.IsNotExist(err) {
		t.Fatal("incompatible toolchain published partial project")
	}
	t.Log("Go 1.25.5: HTTP+both event platforms installed and verified; playground rejected before publication with Go 1.26 requirement")
}
