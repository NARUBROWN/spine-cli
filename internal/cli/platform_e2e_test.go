package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"
)

func TestNewInstallationTransactions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake Go driver and signal test use a Unix shell")
	}
	binary := filepath.Join(t.TempDir(), "spine")
	build := exec.Command("go", "build", "-o", binary, "./cmd/spine")
	build.Dir = filepath.Join("..", "..")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v %s", err, output)
	}
	realGo, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	driver := t.TempDir()
	writeE2EFile(t, filepath.Join(driver, "go"), `#!/bin/sh
if [ "$1" = "mod" ] && [ "$2" = "tidy" ]; then
 case "$SPINE_TEST_INSTALL_MODE" in
 fail) echo "contract requires lock: deliberate dependency failure" >&2; exit 7;;
 cancel) echo started > "$SPINE_TEST_INSTALL_MARKER"; exec /bin/sleep 30;;
 esac
fi
if [ "$1" = "build" ] && [ "$SPINE_TEST_INSTALL_MODE" = "build-fail" ]; then
 echo "requires lock: deliberate compiler failure" >&2; exit 7
fi
exec "$SPINE_TEST_REAL_GO" "$@"
`)
	if err := os.Chmod(filepath.Join(driver, "go"), 0o755); err != nil {
		t.Fatal(err)
	}
	environment := []string{"PATH=" + driver + string(os.PathListSeparator) + os.Getenv("PATH"), "SPINE_TEST_REAL_GO=" + realGo, "GOWORK=off"}
	for _, scenario := range []struct {
		mode  string
		code  int
		stage string
	}{{"fail", 3, "dependencies"}, {"build-fail", 5, "build"}} {
		t.Run(scenario.mode, func(t *testing.T) {
			for _, jsonOutput := range []bool{false, true} {
				root := t.TempDir()
				args := []string{"new", "inventory", "--module", "example.com/inventory"}
				if jsonOutput {
					args = append(args, "--json")
				}
				result := runE2E(binary, root, append(append([]string{}, environment...), "SPINE_TEST_INSTALL_MODE="+scenario.mode), args...)
				if result.code != scenario.code {
					t.Fatalf("failure code=%d want %d: %s %s", result.code, scenario.code, result.stdout, result.stderr)
				}
				if jsonOutput {
					var envelope struct {
						Result struct {
							Stage string `json:"failed_stage"`
						} `json:"result"`
					}
					if err := json.Unmarshal([]byte(result.stdout), &envelope); err != nil || envelope.Result.Stage != scenario.stage {
						t.Fatalf("stage not reported: %v %s", err, result.stdout)
					}
				}
				assertNoNewProjectFiles(t, root)
			}
		})
	}
	t.Run("cancel", func(t *testing.T) {
		root := t.TempDir()
		marker := filepath.Join(t.TempDir(), "started")
		command := exec.Command(binary, "new", "inventory", "--module", "example.com/inventory", "--json")
		command.Dir = root
		command.Env = mergeE2EEnvironment(append(append([]string{}, environment...), "SPINE_TEST_INSTALL_MODE=cancel", "SPINE_TEST_INSTALL_MARKER="+marker))
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
				t.Fatal("installation did not start")
			}
			time.Sleep(10 * time.Millisecond)
		}
		if err := command.Process.Signal(syscall.SIGTERM); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-done:
			if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 3 {
				t.Fatalf("cancel exit: %v %s %s", err, out.String(), errOut.String())
			}
		case <-time.After(5 * time.Second):
			t.Fatal("cancel did not stop dependency subprocess")
		}
		assertNoNewProjectFiles(t, root)
	})
	t.Run("mode hash and workspace", func(t *testing.T) {
		root := t.TempDir()
		plan := runE2E(binary, root, nil, "new", "inventory", "--skip-install", "--dry-run", "--json")
		assertE2ESuccess(t, plan, "skip plan")
		var envelope struct {
			Result struct {
				Hash string `json:"base_hash"`
			} `json:"result"`
		}
		if err := json.Unmarshal([]byte(plan.stdout), &envelope); err != nil {
			t.Fatal(err)
		}
		result := runE2E(binary, root, nil, "new", "inventory", "--if-match", envelope.Result.Hash, "--json")
		if result.code != 4 {
			t.Fatalf("reused skip plan: %+v", result)
		}
		assertNoNewProjectFiles(t, root)
		writeE2EFile(t, filepath.Join(root, "go.work"), "go 1.25.5\n")
		result = runE2E(binary, root, []string{"GOWORK=" + filepath.Join(root, "go.work")}, "new", "inventory", "--json")
		if result.code != 3 {
			t.Fatalf("workspace accepted: %+v", result)
		}
		if _, err := os.Stat(filepath.Join(root, "inventory")); !os.IsNotExist(err) {
			t.Fatal("workspace failure published project")
		}
	})
}

func assertNoNewProjectFiles(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("failed operation left files: %v", entries)
	}
}
