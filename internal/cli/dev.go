package cli

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
)

type devProcess struct {
	cmd       *exec.Cmd
	done      chan error
	binaryDir string
}

type devEventSink struct {
	mu      sync.Mutex
	encoder *json.Encoder
}

type devEventWriter struct {
	mu      sync.Mutex
	sink    *devEventSink
	stream  string
	pending strings.Builder
}

func newDevEventSink(out io.Writer) *devEventSink {
	return &devEventSink{encoder: json.NewEncoder(out)}
}

func (sink *devEventSink) emit(event string, fields map[string]any) {
	payload := map[string]any{"schema_version": "spine.dev-event/v1alpha1", "event": event, "time": time.Now().UTC().Format(time.RFC3339Nano)}
	for key, value := range fields {
		payload[key] = value
	}
	sink.mu.Lock()
	_ = sink.encoder.Encode(payload)
	sink.mu.Unlock()
}

func newDevEventWriter(sink *devEventSink, stream string) *devEventWriter {
	return &devEventWriter{sink: sink, stream: stream}
}

func (writer *devEventWriter) Write(contents []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	writer.pending.Write(contents)
	for {
		buffered := writer.pending.String()
		newline := strings.IndexByte(buffered, '\n')
		if newline < 0 {
			break
		}
		line := strings.TrimSuffix(buffered[:newline], "\r")
		writer.pending.Reset()
		writer.pending.WriteString(buffered[newline+1:])
		writer.sink.emit("process_output", map[string]any{"stream": writer.stream, "line": line})
	}
	return len(contents), nil
}

func (writer *devEventWriter) Flush() {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.pending.Len() == 0 {
		return
	}
	line := strings.TrimSuffix(writer.pending.String(), "\r")
	writer.pending.Reset()
	writer.sink.emit("process_output", map[string]any{"stream": writer.stream, "line": line})
}

// runDev는 변경된 앱을 먼저 빌드하고, 빌드에 성공했을 때만 실행 프로세스를 교체합니다.
func runDev(args []string, out, errOut io.Writer) int {
	eventMode := ""
	for index := 0; index < len(args); index++ {
		if args[index] != "--events" || index+1 >= len(args) || args[index+1] != "jsonl" || eventMode != "" {
			fmt.Fprintln(errOut, "usage: spine-cli dev [--events jsonl]")
			return 1
		}
		eventMode = "jsonl"
		index++
	}
	var eventSink *devEventSink
	processOut, processErr := out, errOut
	if eventMode == "jsonl" {
		eventSink = newDevEventSink(out)
		processOut = newDevEventWriter(eventSink, "stdout")
		processErr = newDevEventWriter(eventSink, "stderr")
	}
	emit := func(event, message string, fields map[string]any) {
		if eventSink != nil {
			if message != "" {
				fields["message"] = message
			}
			eventSink.emit(event, fields)
			return
		}
		fmt.Fprintln(out, message)
	}
	fail := func(event, message string) {
		if eventSink != nil {
			eventSink.emit(event, map[string]any{"message": message})
		} else {
			fmt.Fprintln(errOut, message)
		}
	}
	ctx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	config, err := loadProjectConfig()
	if err != nil {
		fail("configuration_failed", err.Error())
		return 1
	}
	last, err := applicationSourceState(ctx, ".", config)
	if err != nil {
		if ctx.Err() != nil {
			return 0
		}
		fail("source_scan_failed", err.Error())
		return 1
	}
	emit("build_started", "Building application...", map[string]any{"initial": true})
	binary, binaryDir, cached, err := cachedDevBinaryContext(ctx, config, last, processErr)
	if err != nil {
		if ctx.Err() != nil {
			return 0
		}
		fail("build_failed", fmt.Sprintf("initial development build failed: %v", err))
		return 1
	}
	if cached && eventSink == nil {
		fmt.Fprintln(out, "Using cached development build.")
	}
	emit("build_succeeded", "Application build succeeded.", map[string]any{"initial": true, "cached": cached})
	process, err := startDevProcess(binary, binaryDir, config, processOut, processErr)
	if err != nil {
		_ = os.RemoveAll(binaryDir)
		fail("process_start_failed", fmt.Sprintf("could not start app: %v", err))
		return 1
	}
	emit("process_started", "Application process started.", map[string]any{"pid": process.cmd.Process.Pid, "address": fmt.Sprintf("http://127.0.0.1:%d", config.Port), "readiness_observed": false})
	processDone := process.done
	defer func() { stopDevProcess(process, false) }()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	lastConfigError := ""
	emit("watching", "Spine development server is watching application sources.", map[string]any{})
	for {
		select {
		case <-ctx.Done():
			emit("stopping", "Stopping Spine development server...", map[string]any{})
			return 0
		case processErr := <-processDone:
			exitCode := 0
			if processErr != nil {
				exitCode = -1
				if exitError, ok := processErr.(*exec.ExitError); ok {
					exitCode = exitError.ExitCode()
				}
			}
			emit("process_exited", "Application process exited; source watching continues.", map[string]any{"exit_code": exitCode})
			process = nil
			processDone = nil
		case <-ticker.C:
			updatedConfig, configErr := loadProjectConfig()
			if configErr != nil {
				if configErr.Error() != lastConfigError {
					lastConfigError = configErr.Error()
					fail("configuration_failed", lastConfigError)
				}
				continue
			}
			lastConfigError = ""
			next, stateErr := applicationSourceState(ctx, ".", updatedConfig)
			if stateErr != nil {
				if ctx.Err() != nil {
					return 0
				}
				fail("source_scan_failed", stateErr.Error())
				continue
			}
			if next == last {
				continue
			}
			last = next
			emit("change_detected", "Application source change detected.", map[string]any{"source_hash": next})
			emit("build_started", "Building changed application...", map[string]any{"initial": false})
			newBinary, newBinaryDir, rebuiltCached, buildErr := cachedDevBinaryContext(ctx, updatedConfig, next, processErr)
			if buildErr != nil {
				if ctx.Err() != nil {
					return 0
				}
				fail("build_failed", "Build failed; the previous application process is unchanged.")
				continue
			}
			emit("build_succeeded", "Changed application build succeeded.", map[string]any{"initial": false, "cached": rebuiltCached})
			if ctx.Err() != nil {
				return 0
			}
			config = updatedConfig
			stopDevProcess(process, false)
			process, err = startDevProcess(newBinary, newBinaryDir, config, processOut, processErr)
			if err != nil {
				_ = os.RemoveAll(newBinaryDir)
				fail("process_start_failed", fmt.Sprintf("could not start rebuilt app: %v", err))
				process = nil
				processDone = nil
				continue
			}
			processDone = process.done
			emit("process_started", "Rebuilt application process started.", map[string]any{"pid": process.cmd.Process.Pid, "address": fmt.Sprintf("http://127.0.0.1:%d", config.Port), "readiness_observed": false})
		}
	}
}

func buildDevBinary(config projectConfig, errOut io.Writer) (string, string, error) {
	directory, err := os.MkdirTemp("", "spine-dev-build-*")
	if err != nil {
		return "", "", err
	}
	binary := devBinaryPath(directory, runtime.GOOS)
	cmd := exec.Command("go", "build", "-o", binary, config.Entrypoint)
	cmd.Stdout, cmd.Stderr = errOut, errOut
	if err := cmd.Run(); err != nil {
		_ = os.RemoveAll(directory)
		return "", "", err
	}
	return binary, directory, nil
}

func cachedDevBinaryContext(ctx context.Context, config projectConfig, sourceDigest string, errOut io.Writer) (string, string, bool, error) {
	userCache, err := os.UserCacheDir()
	if err != nil {
		return "", "", false, fmt.Errorf("could not locate user cache directory: %w", err)
	}
	projectRoot, err := filepath.Abs(".")
	if err != nil {
		return "", "", false, err
	}
	cacheRoot := filepath.Join(userCache, "spine", "dev")
	return cachedDevBinaryAtContext(ctx, cacheRoot, projectRoot, config, sourceDigest, errOut)
}

func cachedDevBinaryAt(cacheRoot, projectRoot string, config projectConfig, sourceDigest string, errOut io.Writer) (string, string, bool, error) {
	return cachedDevBinaryAtContext(context.Background(), cacheRoot, projectRoot, config, sourceDigest, errOut)
}

func cachedDevBinaryAtContext(ctx context.Context, cacheRoot, projectRoot string, config projectConfig, sourceDigest string, errOut io.Writer) (string, string, bool, error) {
	key := devCacheKey(config, sourceDigest, projectRoot)
	directory := filepath.Join(cacheRoot, key)
	binary := devBinaryPath(directory, runtime.GOOS)
	if info, err := os.Lstat(binary); err == nil {
		if info.Mode().IsRegular() {
			return binary, directory, true, nil
		}
		return "", "", false, fmt.Errorf("development cache target is not a regular file: %s", binary)
	} else if !os.IsNotExist(err) {
		return "", "", false, err
	}
	if err := os.MkdirAll(cacheRoot, 0o755); err != nil {
		return "", "", false, err
	}
	staging, err := os.MkdirTemp(cacheRoot, ".build-*")
	if err != nil {
		return "", "", false, err
	}
	stagingBinary := devBinaryPath(staging, runtime.GOOS)
	cmd := exec.CommandContext(ctx, "go", "build", "-o", stagingBinary, config.Entrypoint)
	cmd.Stdout, cmd.Stderr = errOut, errOut
	if err := cmd.Run(); err != nil {
		_ = os.RemoveAll(staging)
		return "", "", false, err
	}
	if err := os.Rename(staging, directory); err != nil {
		_ = os.RemoveAll(staging)
		if info, statErr := os.Lstat(binary); statErr == nil && info.Mode().IsRegular() {
			return binary, directory, true, nil
		}
		return "", "", false, err
	}
	return binary, directory, false, nil
}

func devCacheKey(config projectConfig, sourceDigest, projectRoot string) string {
	identity := strings.Join([]string{
		sourceDigest,
		filepath.Clean(projectRoot),
		config.Entrypoint,
		runtime.GOOS,
		runtime.GOARCH,
		runtime.Version(),
		os.Getenv("GOFLAGS"),
		os.Getenv("GOEXPERIMENT"),
		os.Getenv("GOOS"),
		os.Getenv("GOARCH"),
		os.Getenv("CGO_ENABLED"),
		os.Getenv("CC"),
		os.Getenv("CXX"),
		os.Getenv("CGO_CFLAGS"),
		os.Getenv("CGO_CPPFLAGS"),
		os.Getenv("CGO_CXXFLAGS"),
		os.Getenv("CGO_LDFLAGS"),
	}, "\n")
	digest := sha256.Sum256([]byte(identity))
	return fmt.Sprintf("%x", digest)
}

func devBinaryPath(directory, goos string) string {
	name := "app"
	if goos == "windows" {
		name += ".exe"
	}
	return filepath.Join(directory, name)
}

func startDevProcess(binary, binaryDir string, config projectConfig, out, errOut io.Writer) (*devProcess, error) {
	cmd := exec.Command(binary)
	cmd.Stdout, cmd.Stderr, cmd.Stdin = out, errOut, os.Stdin
	cmd.Env = append(os.Environ(), fmt.Sprintf("SPINE_PORT=%d", config.Port), "ENV=development")
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	process := &devProcess{cmd: cmd, done: make(chan error, 1), binaryDir: binaryDir}
	go func() {
		err := cmd.Wait()
		flushDevEventWriter(out)
		flushDevEventWriter(errOut)
		process.done <- err
	}()
	return process, nil
}

func flushDevEventWriter(writer io.Writer) {
	if flusher, ok := writer.(interface{ Flush() }); ok {
		flusher.Flush()
	}
}

func stopDevProcess(process *devProcess, removeBinary bool) {
	if process == nil {
		return
	}
	if removeBinary {
		defer os.RemoveAll(process.binaryDir)
	}
	if process.cmd == nil || process.cmd.Process == nil {
		return
	}
	if err := terminateDevProcess(process.cmd.Process, runtime.GOOS); err != nil {
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

func terminateDevProcess(process *os.Process, goos string) error {
	if devTerminationUsesKill(goos) {
		// Windows에서는 임의의 자식 프로세스에 os.Interrupt를 보낼 수 없어 즉시 종료합니다.
		return process.Kill()
	}
	return process.Signal(os.Interrupt)
}

func devTerminationUsesKill(goos string) bool {
	return goos == "windows"
}
