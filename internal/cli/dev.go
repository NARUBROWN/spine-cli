package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// runDev runs the app and restarts it whenever a Go source file changes.
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
	fmt.Fprintln(out, "Spine development server: watching Go files (Ctrl+C to stop)")
	cmd := startDevProcess(config, out, errOut)
	defer stopProcess(cmd)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		next, err := sourceState(".")
		if err != nil {
			fmt.Fprintln(errOut, err)
			continue
		}
		if next != last {
			last = next
			fmt.Fprintln(out, "Change detected; restarting...")
			stopProcess(cmd)
			cmd = startDevProcess(config, out, errOut)
		}
	}
	return 0
}

func startDevProcess(config projectConfig, out, errOut io.Writer) *exec.Cmd {
	cmd := exec.Command("go", "run", config.Entrypoint)
	cmd.Stdout, cmd.Stderr, cmd.Stdin = out, errOut, os.Stdin
	cmd.Env = append(os.Environ(), fmt.Sprintf("SPINE_PORT=%d", config.Port))
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(errOut, "could not start app: %v\n", err)
		return nil
	}
	return cmd
}

func stopProcess(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
	_, _ = cmd.Process.Wait()
}

func sourceState(root string) (int64, error) {
	var newest int64
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && (entry.Name() == ".git" || entry.Name() == "bin" || entry.Name() == ".gocache") {
			return filepath.SkipDir
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if stamp := info.ModTime().UnixNano(); stamp > newest {
			newest = stamp
		}
		return nil
	})
	return newest, err
}
