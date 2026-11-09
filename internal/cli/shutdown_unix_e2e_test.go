//go:build darwin || linux

package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

func TestProcessGroupInterruptAndDevRestart(t *testing.T) {
	binary := buildUpdateCLI(t)
	for _, mode := range []string{"run", "dev"} {
		t.Run(mode+"/process-group", func(t *testing.T) {
			root := shutdownFixture(t)
			cmd := exec.Command(binary, mode)
			cmd.Dir = root
			cmd.Env = mergeE2EEnvironment([]string{"GOWORK=off", "SPINE_CLI_SHUTDOWN_TIMEOUT=15s"})
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			log, err := os.Create(filepath.Join(t.TempDir(), "output"))
			if err != nil {
				t.Fatal(err)
			}
			defer log.Close()
			cmd.Stdout, cmd.Stderr = log, log
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) })
			waitShutdownFile(t, filepath.Join(root, "started"), 30*time.Second)
			if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGINT); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("CLI did not complete shutdown: %v", err)
				}
			case <-time.After(12 * time.Second):
				t.Fatal("CLI did not wait for child")
			}
			waitShutdownFile(t, filepath.Join(root, "completed"), time.Second)
		})
	}
	t.Run("dev/restart", func(t *testing.T) {
		root := shutdownFixture(t)
		cmd := exec.Command(binary, "dev")
		cmd.Dir = root
		cmd.Env = mergeE2EEnvironment([]string{"GOWORK=off", "SPINE_CLI_SHUTDOWN_TIMEOUT=15s"})
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		log, err := os.Create(filepath.Join(t.TempDir(), "output"))
		if err != nil {
			t.Fatal(err)
		}
		defer log.Close()
		cmd.Stdout, cmd.Stderr = log, log
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) })
		first := waitShutdownFile(t, filepath.Join(root, "started"), 30*time.Second)
		f, err := os.OpenFile(filepath.Join(root, "main.go"), os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.WriteString("\n// trigger rebuild\n")
		_ = f.Close()
		if err != nil {
			t.Fatal(err)
		}
		waitShutdownFile(t, filepath.Join(root, "completed"), 30*time.Second)
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			next, _ := os.ReadFile(filepath.Join(root, "started"))
			if len(next) > 0 && string(next) != string(first) {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		next, _ := os.ReadFile(filepath.Join(root, "started"))
		if string(next) == string(first) {
			t.Fatal("dev did not restart")
		}
		if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(12 * time.Second):
			t.Fatal("dev did not stop")
		}
	})
}

func shutdownFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeE2EFile(t, filepath.Join(root, "go.mod"), "module example.com/shutdown\n\ngo 1.25.5\n")
	writeE2EFile(t, filepath.Join(root, "spine.toml"), "module = \"example.com/shutdown\"\nentrypoint = \".\"\ncomposition = \".\"\nport = 8080\n")
	writeE2EFile(t, filepath.Join(root, "main.go"), `package main
import("fmt";"os";"os/signal";"syscall";"time")
func main(){ ch:=make(chan os.Signal,8); signal.Notify(ch,os.Interrupt,syscall.SIGTERM); os.WriteFile("started",[]byte(fmt.Sprint(os.Getpid())),0600); <-ch; time.Sleep(4*time.Second); os.WriteFile("completed",[]byte(fmt.Sprint(os.Getpid())),0600) }
`)
	return root
}

func waitShutdownFile(t *testing.T, path string, timeout time.Duration) []byte {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
			return data
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal(fmt.Sprintf("timed out waiting for %s after %s", path, strconv.FormatFloat(timeout.Seconds(), 'f', 1, 64)))
	return nil
}
