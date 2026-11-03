package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestApplicationShutdownTimeout(t *testing.T) {
	for _, value := range []string{"", "30s", "200ms"} {
		t.Setenv("SPINE_CLI_SHUTDOWN_TIMEOUT", value)
		if timeout, err := applicationShutdownTimeout(); err != nil || timeout <= 0 {
			t.Fatalf("%q: %v %v", value, timeout, err)
		}
	}
	for _, value := range []string{"0s", "-1s", "invalid"} {
		t.Setenv("SPINE_CLI_SHUTDOWN_TIMEOUT", value)
		if _, err := applicationShutdownTimeout(); err == nil {
			t.Fatalf("invalid timeout accepted: %s", value)
		}
	}
}

func TestRunAndDevGracefulShutdown(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix signal lifecycle; Windows uses the documented kill fallback")
	}
	binary := buildUpdateCLI(t)
	for _, mode := range []string{"run", "dev"} {
		for _, forced := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/forced=%v", mode, forced), func(t *testing.T) {
				root := t.TempDir()
				writeE2EFile(t, filepath.Join(root, "go.mod"), "module example.com/shutdown\n\ngo 1.25.5\n")
				writeE2EFile(t, filepath.Join(root, "spine.toml"), "module = \"example.com/shutdown\"\nentrypoint = \".\"\ncomposition = \".\"\nport = 8080\n")
				writeE2EFile(t, filepath.Join(root, "main.go"), `package main
import("fmt";"os";"os/signal";"syscall";"time")
func main() {
 signals := make(chan os.Signal, 8)
 signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
 _ = os.WriteFile("child.pid", []byte(fmt.Sprint(os.Getpid())), 0600)
 <-signals
 _ = os.WriteFile("received", []byte("yes"), 0600)
 time.Sleep(4*time.Second)
 _ = os.WriteFile("completed", []byte("yes"), 0600)
}
`)
				log, err := os.Create(filepath.Join(t.TempDir(), "cli.log"))
				if err != nil {
					t.Fatal(err)
				}
				defer log.Close()
				command := exec.Command(binary, mode)
				command.Dir = root
				timeout := "15s"
				if forced {
					timeout = "200ms"
				}
				command.Env = mergeE2EEnvironment([]string{"GOWORK=off", "SPINE_CLI_SHUTDOWN_TIMEOUT=" + timeout})
				command.Stdout, command.Stderr = log, log
				if err := command.Start(); err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() { done <- command.Wait() }()
				var child *os.Process
				t.Cleanup(func() {
					_ = command.Process.Kill()
					if child != nil {
						_ = child.Kill()
					}
				})
				deadline := time.Now().Add(20 * time.Second)
				for time.Now().Before(deadline) {
					if contents, err := os.ReadFile(filepath.Join(root, "child.pid")); err == nil {
						pid, err := strconv.Atoi(string(contents))
						if err == nil {
							child, _ = os.FindProcess(pid)
							break
						}
					}
					time.Sleep(20 * time.Millisecond)
				}
				if child == nil {
					t.Fatal("application never became ready")
				}
				if err := command.Process.Signal(syscall.SIGTERM); err != nil {
					t.Fatal(err)
				}
				select {
				case err := <-done:
					if forced != (err != nil) {
						t.Fatalf("forced=%v exit=%v", forced, err)
					}
				case <-time.After(10 * time.Second):
					t.Fatal("CLI did not finish shutdown")
				}
				if _, err := os.Stat(filepath.Join(root, "received")); err != nil {
					t.Fatal("application did not receive the forwarded signal")
				}
				_, completeErr := os.Stat(filepath.Join(root, "completed"))
				if forced == (completeErr == nil) {
					t.Fatalf("forced=%v completed=%v", forced, completeErr == nil)
				}
				if err := child.Signal(syscall.Signal(0)); err == nil {
					t.Fatal("CLI left the child running")
				}
				if forced {
					contents, _ := os.ReadFile(log.Name())
					if !strings.Contains(string(contents), "shutdown timeout") {
						t.Fatal("forced shutdown was not reported")
					}
				}
			})
		}
	}
}
