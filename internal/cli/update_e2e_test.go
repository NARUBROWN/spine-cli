package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

func buildUpdateCLI(t *testing.T) string {
	t.Helper()
	name := "spine-cli"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binary := filepath.Join(t.TempDir(), name)
	command := exec.Command("go", "build", "-o", binary, ".")
	command.Dir = filepath.Join("..", "..")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v %s", err, output)
	}
	return binary
}

func newUpdateFixture(t *testing.T, binary string) string {
	t.Helper()
	root := t.TempDir()
	result := runE2E(binary, root, []string{"GOWORK=off", "GOFLAGS= "}, "new", "app", "--module", "example.com/update", "--skip-install")
	assertE2ESuccess(t, result, "create update fixture")
	return filepath.Join(root, "app")
}

func assertUpdateUnpublished(t *testing.T, project string, before []byte) {
	t.Helper()
	after, _ := os.ReadFile(filepath.Join(project, "go.mod"))
	if !bytes.Equal(before, after) {
		t.Fatalf("failed update published go.mod: %s", after)
	}
	if _, err := os.Stat(filepath.Join(project, "go.sum")); !os.IsNotExist(err) {
		t.Fatalf("failed update published go.sum: %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, generationLockPath)); !os.IsNotExist(err) {
		t.Fatalf("update lock was not released: %v", err)
	}
}

func TestUpdateCLICompatibleSourcesAndPublicHTTP(t *testing.T) {
	binary := buildUpdateCLI(t)
	project := newUpdateFixture(t, binary)
	writeE2EFile(t, filepath.Join(project, "go.mod"), "module example.com/update\n\ngo 1.25.5\n\nrequire github.com/NARUBROWN/spine v0.5.2\n")
	marker := filepath.Join(t.TempDir(), "executed")
	writeE2EFile(t, filepath.Join(project, "internal/application/update_guard.go"), `package application
import "os"
func init() { if path := os.Getenv("SPINE_UPDATE_EXEC_MARKER"); path != "" { _ = os.WriteFile(path, []byte("executed"), 0600) } }
`)
	writeE2EFile(t, filepath.Join(project, "src/controller/health.txt"), "ok")
	healthPath := filepath.Join(project, "src/controller/health_controller.go")
	health, _ := os.ReadFile(healthPath)
	writeE2EFile(t, filepath.Join(project, "src/controller/embedded_health.go"), "package controller\nimport _ \"embed\"\n//go:embed health.txt\nvar healthStatus string\n")
	health = bytes.Replace(health, []byte(`"status": "ok"`), []byte(`"status": healthStatus`), 1)
	writeE2EFile(t, healthPath, string(health))
	// 설정한 실행 패키지와 embed 파일을 함께 복사해 빌드하는지 확인합니다.
	if err := os.MkdirAll(filepath.Join(project, "cmd/server"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(project, "main.go"), filepath.Join(project, "cmd/server/main.go")); err != nil {
		t.Fatal(err)
	}
	config, _ := os.ReadFile(filepath.Join(project, "spine.toml"))
	writeE2EFile(t, filepath.Join(project, "spine.toml"), strings.Replace(string(config), `entrypoint = "."`, `entrypoint = "./cmd/server"`, 1))
	env := []string{"GOWORK=off", "GOFLAGS= ", "SPINE_UPDATE_EXEC_MARKER=" + marker}
	before, _ := os.ReadFile(filepath.Join(project, "go.mod"))
	plan := runE2E(binary, filepath.Dir(project), env, "update", "--root", project, "--dry-run", "--json")
	assertE2ESuccess(t, plan, "update dry-run from outside project")
	assertUpdateUnpublished(t, project, before)
	var envelope struct {
		Result struct {
			Hash string `json:"base_hash"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(plan.stdout), &envelope); err != nil || envelope.Result.Hash == "" {
		t.Fatalf("missing update plan: %v %s", err, plan.stdout)
	}
	apply := runE2E(binary, filepath.Join(project, "src/controller"), env, "update", "--if-match", envelope.Result.Hash)
	assertE2ESuccess(t, apply, "apply update from nested package")
	for _, text := range []string{"v0.5.2 -> " + platformSpineVersion, "go.mod", "go.sum", "static contract and build verified"} {
		if !strings.Contains(apply.stdout, text) {
			t.Fatalf("completion lost %q: %s", text, apply.stdout)
		}
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("update executed application init or tests")
	}
	// 갱신한 공개 모듈 조합으로 TCP 요청까지 별도로 확인합니다.
	writeE2EFile(t, filepath.Join(project, "internal/application/updated_http_test.go"), `package application
import (
 "context"
 "io"
 "net/http"
 "net/http/httptest"
 "testing"
)
func TestUpdatedRecipeHTTP(t *testing.T) {
 app, options, err := BuildApplication(); if err != nil { t.Fatal(err) }
 prepared, err := app.Prepare(context.Background(), options); if err != nil { t.Fatal(err) }
 defer prepared.Close(context.Background())
 server := httptest.NewServer(prepared.Handler()); defer server.Close()
 response, err := http.Get(server.URL+"/health"); if err != nil { t.Fatal(err) }
 defer response.Body.Close(); body, _ := io.ReadAll(response.Body)
 if response.StatusCode != 200 || string(body) != "{\"status\":\"ok\"}\n" { t.Fatalf("HTTP %d %s", response.StatusCode, body) }
}
`)
	command := exec.Command("go", "test", "./internal/application", "-run", "TestUpdatedRecipeHTTP", "-count=1")
	command.Dir, command.Env = project, goEnvironment(false)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("updated actual HTTP: %v %s", err, output)
	}
}

func TestUpdateCLITransactionFailures(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake Go driver and signal handling use a Unix shell")
	}
	binary := buildUpdateCLI(t)
	realGo, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	driver := t.TempDir()
	writeE2EFile(t, filepath.Join(driver, "go"), `#!/bin/sh
if [ "$1" = "mod" ] && [ "$2" = "tidy" ]; then
 case "$SPINE_TEST_UPDATE_MODE" in
 fail) echo "requires lock: deliberate dependency failure" >&2; exit 7;;
 cancel) echo started > "$SPINE_TEST_UPDATE_MARKER"; exec /bin/sleep 30;;
 esac
fi
if [ "$1" = "build" ]; then
 case "$SPINE_TEST_UPDATE_MODE" in
 build-fail) echo "requires lock: deliberate build failure" >&2; exit 7;;
 build-cancel) echo started > "$SPINE_TEST_UPDATE_MARKER"; exec /bin/sleep 30;;
 mutate) echo '// concurrent edit' >> "$SPINE_TEST_UPDATE_PROJECT/go.mod";;
 esac
fi
exec "$SPINE_TEST_REAL_GO" "$@"
`)
	if err := os.Chmod(filepath.Join(driver, "go"), 0o755); err != nil {
		t.Fatal(err)
	}
	baseEnv := []string{"PATH=" + driver + string(os.PathListSeparator) + os.Getenv("PATH"), "SPINE_TEST_REAL_GO=" + realGo, "GOWORK=off", "GOFLAGS= "}
	for _, test := range []struct {
		mode, stage string
		code        int
	}{{"fail", "dependencies", 3}, {"build-fail", "build", 5}} {
		for _, jsonOutput := range []bool{false, true} {
			t.Run(test.mode+map[bool]string{true: "-json", false: "-plain"}[jsonOutput], func(t *testing.T) {
				project := newUpdateFixture(t, binary)
				before, _ := os.ReadFile(filepath.Join(project, "go.mod"))
				args := []string{"update"}
				if jsonOutput {
					args = append(args, "--json")
				}
				result := runE2E(binary, project, append(append([]string{}, baseEnv...), "SPINE_TEST_UPDATE_MODE="+test.mode), args...)
				if result.code != test.code {
					t.Fatalf("got %d want %d: %s %s", result.code, test.code, result.stdout, result.stderr)
				}
				if jsonOutput && !strings.Contains(result.stdout, `"failed_stage": "`+test.stage+`"`) {
					t.Fatalf("wrong stage: %s", result.stdout)
				}
				assertUpdateUnpublished(t, project, before)
			})
		}
	}
	t.Run("concurrent edit", func(t *testing.T) {
		project := newUpdateFixture(t, binary)
		before, _ := os.ReadFile(filepath.Join(project, "go.mod"))
		env := append(append([]string{}, baseEnv...), "SPINE_TEST_UPDATE_MODE=mutate", "SPINE_TEST_UPDATE_PROJECT="+project)
		result := runE2E(binary, project, env, "update", "--json")
		if result.code != 4 {
			t.Fatalf("concurrent input was accepted: %+v", result)
		}
		assertUpdateUnpublished(t, project, append(before, []byte("// concurrent edit\n")...))
	})
	for _, cancellationMode := range []string{"cancel", "build-cancel"} {
		t.Run(cancellationMode, func(t *testing.T) {
			project := newUpdateFixture(t, binary)
			before, _ := os.ReadFile(filepath.Join(project, "go.mod"))
			marker := filepath.Join(t.TempDir(), "started")
			command := exec.Command(binary, "update", "--json")
			command.Dir = project
			command.Env = mergeE2EEnvironment(append(append([]string{}, baseEnv...), "SPINE_TEST_UPDATE_MODE="+cancellationMode, "SPINE_TEST_UPDATE_MARKER="+marker))
			var out, errOut bytes.Buffer
			command.Stdout, command.Stderr = &out, &errOut
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			defer command.Process.Kill()
			done := make(chan error, 1)
			go func() { done <- command.Wait() }()
			deadline := time.Now().Add(10 * time.Second)
			for {
				if _, err := os.Stat(marker); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("dependency process did not start")
				}
				time.Sleep(10 * time.Millisecond)
			}
			if err := command.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 3 {
					t.Fatalf("cancel: %v %s %s", err, out.String(), errOut.String())
				}
			case <-time.After(5 * time.Second):
				t.Fatal("cancel did not stop update")
			}
			assertUpdateUnpublished(t, project, before)
		})
	}
}
