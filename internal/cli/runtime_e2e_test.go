package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"
)

type devFixture struct {
	command    *exec.Cmd
	events     chan map[string]any
	done       chan error
	journal    []map[string]any
	children   map[int]*os.Process
	stopped    bool
	stderrPath string
}

func startDevFixture(t *testing.T, binary string, logging bool) (*devFixture, string) {
	t.Helper()
	root := t.TempDir()
	writeE2EFile(t, filepath.Join(root, "go.mod"), "module example.com/devfixture\n\ngo 1.25\n")
	config := "module = \"example.com/devfixture\"\nentrypoint = \".\"\ncomposition = \".\"\nport = 8080\n"
	writeE2EFile(t, filepath.Join(root, "spine.toml"), config)
	writeE2EFile(t, filepath.Join(root, "main.go"), `package main
import("context";"fmt";"os";"os/signal";"time")
func main() {
 ctx, stop := signal.NotifyContext(context.Background(),os.Interrupt)
 defer stop()
 fmt.Println("port="+os.Getenv("SPINE_PORT"))
 ticker:=time.NewTicker(100*time.Millisecond)
 defer ticker.Stop()
 for { select {
 case <-ctx.Done(): return
 case <-ticker.C:
  if os.Getenv("WRITE_RUNTIME_LOG")=="1" { _=os.WriteFile("runtime.log",[]byte(time.Now().String()),0600) }
 } }
}
`)
	command := exec.Command(binary, "dev", "--events", "jsonl")
	command.Dir = root
	env := e2eCacheEnvironment(t.TempDir())
	if logging {
		env = append(env, "WRITE_RUNTIME_LOG=1")
	} else {
		env = append(env, "WRITE_RUNTIME_LOG=0")
	}
	command.Env = mergeE2EEnvironment(env)
	stderrPath := filepath.Join(t.TempDir(), "stderr.log")
	stderr, err := os.Create(stderrPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stderr.Close() })
	command.Stderr = stderr
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	fixture := &devFixture{command: command, events: make(chan map[string]any, 256), done: make(chan error, 1), children: map[int]*os.Process{}, stderrPath: stderrPath}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		defer close(fixture.events)
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			var event map[string]any
			if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
				fixture.events <- map[string]any{"event": "invalid_json", "line": scanner.Text()}
				return
			}
			fixture.events <- event
		}
	}()
	go func() { fixture.done <- command.Wait() }()
	t.Cleanup(func() {
		if !fixture.stopped {
			_ = command.Process.Signal(syscall.SIGTERM)
			select {
			case <-fixture.done:
			case <-time.After(4 * time.Second):
				_ = command.Process.Kill()
				<-fixture.done
			}
		}
		for _, child := range fixture.children {
			_ = child.Kill()
		}
	})
	fixture.wait(t, func(event map[string]any) bool { return event["event"] == "watching" })
	return fixture, root
}

func (fixture *devFixture) record(t *testing.T, event map[string]any) {
	t.Helper()
	fixture.journal = append(fixture.journal, event)
	if event["event"] == "invalid_json" {
		t.Fatalf("dev emitted invalid JSON: %#v", event)
	}
	if event["event"] == "process_started" {
		pid := int(event["pid"].(float64))
		process, err := os.FindProcess(pid)
		if err != nil {
			t.Fatal(err)
		}
		fixture.children[pid] = process
	}
}

func (fixture *devFixture) wait(t *testing.T, match func(map[string]any) bool) map[string]any {
	t.Helper()
	for _, event := range fixture.journal {
		if match(event) {
			return event
		}
	}
	timer := time.NewTimer(20 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event, open := <-fixture.events:
			if !open {
				contents, _ := os.ReadFile(fixture.stderrPath)
				t.Fatalf("dev stopped unexpectedly: %s", contents)
			}
			fixture.record(t, event)
			if match(event) {
				return event
			}
		case <-timer.C:
			contents, _ := os.ReadFile(fixture.stderrPath)
			t.Fatalf("dev event timeout: %s; events=%#v", contents, fixture.journal)
		}
	}
}

func (fixture *devFixture) stop(t *testing.T) {
	t.Helper()
	if err := fixture.command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-fixture.done:
		fixture.stopped = true
		if err != nil {
			t.Errorf("SIGTERM did not use normal shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("dev did not terminate after SIGTERM")
	}
	for pid, child := range fixture.children {
		if err := child.Signal(syscall.Signal(0)); err == nil {
			t.Errorf("dev left child %d running", pid)
		}
	}
}

func TestDevRuntimeLifecycleEndToEnd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix signal lifecycle test")
	}
	binary := filepath.Join(t.TempDir(), "spine-cli")
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Dir = filepath.Join("..", "..")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v %s", err, output)
	}
	t.Run("runtime logs do not restart the app", func(t *testing.T) {
		fixture, root := startDevFixture(t, binary, true)
		timer := time.NewTimer(1600 * time.Millisecond)
		defer timer.Stop()
	watch:
		for {
			select {
			case event, open := <-fixture.events:
				if !open {
					t.Fatal("dev exited during log observation")
				}
				fixture.record(t, event)
				if event["event"] == "change_detected" || event["event"] == "process_started" {
					t.Fatalf("runtime logs restarted the app: %#v", event)
				}
			case <-timer.C:
				break watch
			}
		}
		if _, err := os.Stat(filepath.Join(root, "runtime.log")); err != nil {
			t.Fatal("fixture did not write runtime logs")
		}
		fixture.stop(t)
	})
	t.Run("configuration reload uses the new port", func(t *testing.T) {
		fixture, root := startDevFixture(t, binary, false)
		writeE2EFile(t, filepath.Join(root, "spine.toml"), "module = \"example.com/devfixture\"\nentrypoint = \".\"\ncomposition = \".\"\nport = 9090\n")
		fixture.wait(t, func(event map[string]any) bool {
			return event["event"] == "process_started" && event["address"] == "http://127.0.0.1:9090"
		})
		fixture.wait(t, func(event map[string]any) bool {
			return event["event"] == "process_output" && event["line"] == "port=9090"
		})
		fixture.stop(t)
	})
	t.Run("SIGTERM reaps the application", func(t *testing.T) {
		fixture, _ := startDevFixture(t, binary, false)
		fixture.stop(t)
		if !fixture.stopped {
			t.Fatal(fmt.Sprint(fixture.journal))
		}
	})
	t.Run("configuration errors are emitted once and recover", func(t *testing.T) {
		fixture, root := startDevFixture(t, binary, false)
		invalid := "module = \"example.com/devfixture\"\nentrypoint = \".\"\ncomposition = \".\"\nport = 99999\n"
		writeE2EFile(t, filepath.Join(root, "spine.toml"), invalid)
		first := fixture.wait(t, func(event map[string]any) bool { return event["event"] == "configuration_failed" })
		timer := time.NewTimer(1300 * time.Millisecond)
		defer timer.Stop()
	observe:
		for {
			select {
			case event, open := <-fixture.events:
				if !open {
					t.Fatal("dev exited while configuration was invalid")
				}
				fixture.record(t, event)
				if event["event"] == "configuration_failed" || event["event"] == "process_started" {
					t.Fatalf("unchanged invalid config produced repeated work: %#v", event)
				}
			case <-timer.C:
				break observe
			}
		}
		writeE2EFile(t, filepath.Join(root, "spine.toml"), "module = \"example.com/devfixture\"\nentrypoint = \".\"\ncomposition = \".\"\nport = 9090\n")
		fixture.wait(t, func(event map[string]any) bool {
			return event["event"] == "process_started" && event["address"] == "http://127.0.0.1:9090"
		})
		writeE2EFile(t, filepath.Join(root, "spine.toml"), invalid)
		fixture.wait(t, func(event map[string]any) bool {
			return event["event"] == "configuration_failed" && event["time"] != first["time"]
		})
		fixture.stop(t)
	})
}
