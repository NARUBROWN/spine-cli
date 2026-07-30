package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

type e2eResult struct {
	code   int
	stdout string
	stderr string
}

func TestCLIEndToEndAgentGenerationAndRuntimeCache(t *testing.T) {
	repositoryRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	binaryName := "spine-e2e"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	binary := filepath.Join(t.TempDir(), binaryName)
	build := exec.Command("go", "build", "-o", binary, "./cmd/spine")
	build.Dir = repositoryRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}

	t.Run("agent generation and verification", func(t *testing.T) {
		workspace := t.TempDir()
		created := runE2E(binary, workspace, nil, "new", "todo", "--module", "example.com/todo", "--json")
		assertE2ESuccess(t, created, "new project")
		assertProtocolOK(t, created.stdout)
		project := filepath.Join(workspace, "todo")

		commands := [][]string{
			{"generate", "repository", "user"},
			{"generate", "service", "user", "--depends", "user_repository"},
			{"generate", "controller", "user", "--depends", "user_service"},
		}
		for _, arguments := range commands {
			result := runE2E(binary, project, nil, arguments...)
			assertE2ESuccess(t, result, strings.Join(arguments, " "))
		}

		contextResult := runE2E(binary, workspace, nil, "context", "--root", project, "--json")
		assertE2ESuccess(t, contextResult, "context from outside project")
		var envelope struct {
			OK     bool            `json:"ok"`
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal([]byte(contextResult.stdout), &envelope); err != nil {
			t.Fatalf("decode context envelope: %v\n%s", err, contextResult.stdout)
		}
		var context projectContext
		if err := json.Unmarshal(envelope.Result, &context); err != nil {
			t.Fatalf("decode project context: %v\n%s", err, envelope.Result)
		}
		if !envelope.OK || len(context.NextActions) != 2 || context.NextActions[1].Safe {
			t.Fatalf("unsafe or incomplete next actions: %#v", context.NextActions)
		}
		for _, action := range context.NextActions {
			if !strings.Contains(action.Command, project) {
				t.Fatalf("next action lost root %q: %#v", project, action)
			}
		}

		contract := runE2E(binary, workspace, nil, "contract", "check", "--root", project, "--json")
		assertE2ESuccess(t, contract, "contract check")
		assertProtocolOK(t, contract.stdout)

		routeDirectory := filepath.Join(project, "src", "route")
		localController := runE2E(binary, routeDirectory, nil, "generate", "controller", "local")
		assertE2ESuccess(t, localController, "controller in route package")
		routeSource, err := os.ReadFile(filepath.Join(routeDirectory, "route.go"))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(routeSource), `"example.com/todo/src/route"`) || !strings.Contains(string(routeSource), "(*LocalController).Get") {
			t.Fatalf("route package self-imported after E2E generation:\n%s", routeSource)
		}
		tidy := exec.Command("go", "mod", "tidy")
		tidy.Dir = project
		if output, err := tidy.CombinedOutput(); err != nil {
			t.Fatalf("go mod tidy generated project: %v\n%s", err, output)
		}

		verification := runE2E(binary, workspace, nil, "verify", "--root", project, "--json")
		assertE2ESuccess(t, verification, "full generated-project verification")
		assertProtocolOK(t, verification.stdout)
	})

	t.Run("cached run tracks embedded assets and supports concurrent startup", func(t *testing.T) {
		project := t.TempDir()
		writeE2EFile(t, filepath.Join(project, "go.mod"), "module example.com/cache-e2e\n\ngo 1.25\n")
		writeE2EFile(t, filepath.Join(project, "spine.toml"), "module = \"example.com/cache-e2e\"\nentrypoint = \".\"\ncomposition = \".\"\nport = 8080\n")
		writeE2EFile(t, filepath.Join(project, "main.go"), `package main

import (
	_ "embed"
	"fmt"
)

//go:embed message.txt
var message string

func main() { fmt.Print(message) }
`)
		asset := filepath.Join(project, "message.txt")
		writeE2EFile(t, asset, "first")
		cacheRoot := filepath.Join(t.TempDir(), "cache")
		environment := e2eCacheEnvironment(cacheRoot)

		first := runE2E(binary, project, environment, "run")
		assertE2ESuccess(t, first, "first cached run")
		if strings.TrimSpace(first.stdout) != "first" {
			t.Fatalf("first embedded output=%q", first.stdout)
		}
		info, err := os.Stat(asset)
		if err != nil {
			t.Fatal(err)
		}
		writeE2EFile(t, asset, "other")
		if err := os.Chtimes(asset, info.ModTime(), info.ModTime()); err != nil {
			t.Fatal(err)
		}
		second := runE2E(binary, project, environment, "run")
		assertE2ESuccess(t, second, "run after same-size embedded asset update")
		if strings.TrimSpace(second.stdout) != "other" {
			t.Fatalf("stale cached embedded output=%q", second.stdout)
		}

		concurrentEnvironment := e2eCacheEnvironment(filepath.Join(t.TempDir(), "concurrent-cache"))
		start := make(chan struct{})
		results := make([]e2eResult, 2)
		var group sync.WaitGroup
		for index := range results {
			group.Add(1)
			go func(index int) {
				defer group.Done()
				<-start
				results[index] = runE2E(binary, project, concurrentEnvironment, "run")
			}(index)
		}
		close(start)
		group.Wait()
		for index, result := range results {
			assertE2ESuccess(t, result, fmt.Sprintf("concurrent cached run %d", index))
			if !strings.HasSuffix(strings.TrimSpace(result.stdout), "other") {
				t.Fatalf("concurrent run %d output=%q", index, result.stdout)
			}
		}

		unsupported := runE2E(binary, project, environment, "run", "--json")
		if unsupported.code != 2 {
			t.Fatalf("run --json code=%d, stdout=%s, stderr=%s", unsupported.code, unsupported.stdout, unsupported.stderr)
		}
		var unsupportedEnvelope protocolResult
		if err := json.Unmarshal([]byte(unsupported.stdout), &unsupportedEnvelope); err != nil || unsupportedEnvelope.Error == nil || unsupportedEnvelope.Error.Code != "USAGE_ERROR" {
			t.Fatalf("invalid unsupported-mode envelope: %v, %#v", err, unsupportedEnvelope)
		}

		assertDevJSONLE2E(t, binary, project, environment)
	})
}

func assertDevJSONLE2E(t *testing.T, binary, project string, environment []string) {
	t.Helper()
	command := exec.Command(binary, "dev", "--events", "jsonl")
	command.Dir = project
	command.Env = mergeE2EEnvironment(environment)
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	events := make(chan map[string]any, 32)
	scanErrors := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			var event map[string]any
			if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
				scanErrors <- fmt.Errorf("invalid JSONL %q: %w", scanner.Text(), err)
				return
			}
			events <- event
		}
		scanErrors <- scanner.Err()
	}()

	deadline := time.After(30 * time.Second)
	foundOutput, foundExit := false, false
	for !foundOutput || !foundExit {
		select {
		case event := <-events:
			switch event["event"] {
			case "process_output":
				if event["line"] == "other" {
					foundOutput = true
				}
			case "process_exited":
				foundExit = true
			}
		case err := <-scanErrors:
			if err != nil {
				t.Fatalf("read dev JSONL: %v; stderr=%s", err, stderr.String())
			}
			t.Fatalf("dev JSONL ended before required events; stderr=%s", stderr.String())
		case <-deadline:
			_ = command.Process.Kill()
			_, _ = command.Process.Wait()
			t.Fatalf("timed out waiting for dev JSONL; stderr=%s", stderr.String())
		}
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatalf("stop dev command: %v", err)
	}
	_ = command.Wait()
}

func runE2E(binary, directory string, environment []string, args ...string) e2eResult {
	command := exec.Command(binary, args...)
	command.Dir = directory
	command.Env = mergeE2EEnvironment(environment)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	code := 0
	if err != nil {
		code = -1
		if exitError, ok := err.(*exec.ExitError); ok {
			code = exitError.ExitCode()
		}
	}
	return e2eResult{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

func mergeE2EEnvironment(overrides []string) []string {
	if len(overrides) == 0 {
		return os.Environ()
	}
	keys := map[string]bool{}
	for _, item := range overrides {
		key, _, _ := strings.Cut(item, "=")
		keys[strings.ToUpper(key)] = true
	}
	result := make([]string, 0, len(os.Environ())+len(overrides))
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if !keys[strings.ToUpper(key)] {
			result = append(result, item)
		}
	}
	return append(result, overrides...)
}

func e2eCacheEnvironment(root string) []string {
	return []string{"LOCALAPPDATA=" + root, "XDG_CACHE_HOME=" + root}
}

func assertE2ESuccess(t *testing.T, result e2eResult, operation string) {
	t.Helper()
	if result.code != 0 {
		t.Fatalf("%s failed with code %d\nstdout:\n%s\nstderr:\n%s", operation, result.code, result.stdout, result.stderr)
	}
}

func assertProtocolOK(t *testing.T, output string) {
	t.Helper()
	var envelope protocolResult
	if err := json.Unmarshal([]byte(output), &envelope); err != nil {
		t.Fatalf("decode protocol envelope: %v\n%s", err, output)
	}
	if !envelope.OK || envelope.ExitCode != 0 {
		t.Fatalf("protocol operation failed: %#v", envelope)
	}
}

func writeE2EFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}
