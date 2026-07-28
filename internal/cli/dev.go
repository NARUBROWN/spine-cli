package cli

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type devProcess struct {
	cmd       *exec.Cmd
	done      chan error
	binaryDir string
}

// runDev는 변경된 앱을 먼저 빌드하고, 빌드에 성공했을 때만 실행 프로세스를 교체합니다.
func runDev(args []string, out, errOut io.Writer) int {
	if len(args) != 0 {
		fmt.Fprintln(errOut, "usage: spine dev")
		return 1
	}
	config, err := loadProjectConfig()
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	last, err := sourceState(".")
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	binary, binaryDir, err := buildDevBinary(config, errOut)
	if err != nil {
		fmt.Fprintf(errOut, "initial development build failed: %v\n", err)
		return 1
	}
	process, err := startDevProcess(binary, binaryDir, config, out, errOut)
	if err != nil {
		_ = os.RemoveAll(binaryDir)
		fmt.Fprintf(errOut, "could not start app: %v\n", err)
		return 1
	}
	defer func() { stopDevProcess(process) }()

	ctx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stopSignals()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	fmt.Fprintln(out, "Spine development server: watching application sources (Ctrl+C to stop)")
	for {
		select {
		case <-ctx.Done():
			fmt.Fprintln(out, "Stopping Spine development server...")
			return 0
		case <-ticker.C:
			next, stateErr := sourceState(".")
			if stateErr != nil {
				fmt.Fprintln(errOut, stateErr)
				continue
			}
			if next == last {
				continue
			}
			last = next
			fmt.Fprintln(out, "Change detected; building...")
			newBinary, newBinaryDir, buildErr := buildDevBinary(config, errOut)
			if buildErr != nil {
				fmt.Fprintln(errOut, "Build failed; the previous application process is unchanged.")
				continue
			}
			stopDevProcess(process)
			process, err = startDevProcess(newBinary, newBinaryDir, config, out, errOut)
			if err != nil {
				_ = os.RemoveAll(newBinaryDir)
				fmt.Fprintf(errOut, "could not start rebuilt app: %v\n", err)
				process = nil
				continue
			}
			fmt.Fprintln(out, "Rebuilt application started.")
		}
	}
}

func buildDevBinary(config projectConfig, errOut io.Writer) (string, string, error) {
	directory, err := os.MkdirTemp("", "spine-dev-build-*")
	if err != nil {
		return "", "", err
	}
	binary := filepath.Join(directory, "app")
	cmd := exec.Command("go", "build", "-o", binary, config.Entrypoint)
	cmd.Stdout, cmd.Stderr = errOut, errOut
	if err := cmd.Run(); err != nil {
		_ = os.RemoveAll(directory)
		return "", "", err
	}
	return binary, directory, nil
}

func startDevProcess(binary, binaryDir string, config projectConfig, out, errOut io.Writer) (*devProcess, error) {
	cmd := exec.Command(binary)
	cmd.Stdout, cmd.Stderr, cmd.Stdin = out, errOut, os.Stdin
	cmd.Env = append(os.Environ(), fmt.Sprintf("SPINE_PORT=%d", config.Port))
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	process := &devProcess{cmd: cmd, done: make(chan error, 1), binaryDir: binaryDir}
	go func() { process.done <- cmd.Wait() }()
	return process, nil
}

func stopDevProcess(process *devProcess) {
	if process == nil {
		return
	}
	defer os.RemoveAll(process.binaryDir)
	if process.cmd == nil || process.cmd.Process == nil {
		return
	}
	if err := process.cmd.Process.Signal(os.Interrupt); err != nil {
		select {
		case <-process.done:
			return
		default:
		}
	}
	select {
	case <-process.done:
		return
	case <-time.After(3 * time.Second):
		_ = process.cmd.Process.Kill()
		<-process.done
	}
}

func sourceState(root string) (string, error) {
	var entries []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && (entry.Name() == ".git" || entry.Name() == "bin" || entry.Name() == ".gocache" || strings.HasPrefix(entry.Name(), ".spine")) {
			return filepath.SkipDir
		}
		if entry.IsDir() || !isWatchedSource(path) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		entries = append(entries, fmt.Sprintf("%s:%d:%d", filepath.ToSlash(relative), info.Size(), info.ModTime().UnixNano()))
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(entries)
	digest := sha256.Sum256([]byte(strings.Join(entries, "\n")))
	return fmt.Sprintf("%x", digest), nil
}

func isWatchedSource(path string) bool {
	if filepath.Ext(path) == ".go" {
		return true
	}
	switch filepath.Base(path) {
	case "go.mod", "go.sum", "spine.toml":
		return true
	default:
		return false
	}
}
