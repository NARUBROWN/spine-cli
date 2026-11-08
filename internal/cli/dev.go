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
func runDev(args []string, out, errOut io.Writer) (code int) {
	eventMode := ""
	for index := 0; index < len(args); index++ {
		if args[index] != "--events" || index+1 >= len(args) || args[index+1] != "jsonl" || eventMode != "" {
			fmt.Fprintln(errOut, "usage: spine dev [--events jsonl]")
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
	frontendFailures := map[string]string{}
	reportFrontendFailure := func(category, event, message string) {
		if previous, exists := frontendFailures[category]; exists && previous == message {
			return
		}
		frontendFailures[category] = message
		fail(event, message)
	}
	clearFrontendFailure := func(category string) { delete(frontendFailures, category) }
	if _, err := applicationShutdownTimeout(); err != nil {
		fail("configuration_failed", err.Error())
		return 1
	}
	ctx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	config, err := loadProjectConfig()
	if err != nil {
		fail("configuration_failed", err.Error())
		return 1
	}
	projectRoot, err := filepath.Abs(".")
	if err != nil {
		fail("configuration_failed", fmt.Sprintf("could not resolve project directory: %v", err))
		return 1
	}
	lastFrontendAttemptState, frontendEnabled, err := frontendSourceState(projectRoot, config)
	if err != nil {
		fail("frontend_scan_failed", err.Error())
		return 1
	}
	lastFrontendAttemptEnabled := frontendEnabled
	var frontend *frontendBuilder
	if frontendEnabled {
		frontend, _, err = newFrontendBuilder(projectRoot, config, false, processErr)
		if err != nil {
			fail("frontend_build_failed", fmt.Sprintf("could not initialize frontend builder: %v", err))
			return 1
		}
		if err := frontend.rebuild(processErr); err != nil {
			frontend.close()
			fail("frontend_build_failed", err.Error())
			return 1
		}
		emit("frontend_build_succeeded", "Frontend build succeeded.", map[string]any{"initial": true})
	}
	defer func() { frontend.close() }()
	lastFrontendAppSettings := frontendSettingsOf(frontend)
	lastFrontendAppAttemptSettings := frontendSettingsOf(frontend)
	last, err := applicationSourceState(ctx, ".", config)
	if err != nil {
		if ctx.Err() != nil {
			return 0
		}
		fail("source_scan_failed", err.Error())
		return 1
	}
	emit("build_started", "Building application...", map[string]any{"initial": true})
	binary, binaryDir, cached, err := cachedDevBinaryContext(ctx, config, last, processErr, frontendSettingsOf(frontend))
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
	process, err := startDevProcess(binary, binaryDir, config, processOut, processErr, frontendSettingsOf(frontend))
	if err != nil {
		_ = os.RemoveAll(binaryDir)
		fail("process_start_failed", fmt.Sprintf("could not start app: %v", err))
		return 1
	}
	emit("process_started", "Application process started.", map[string]any{"pid": process.cmd.Process.Pid, "address": fmt.Sprintf("http://127.0.0.1:%d", config.Port), "readiness_observed": false})
	processDone := process.done
	defer func() {
		if stopDevProcess(process, false) {
			fail("process_killed", forcedTerminationMessage("Application", runtime.GOOS))
			code = 1
		}
	}()

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
			nextFrontendState, nextFrontendEnabled, frontendStateErr := frontendSourceState(projectRoot, updatedConfig)
			if frontendStateErr != nil {
				reportFrontendFailure("scan", "frontend_scan_failed", frontendStateErr.Error())
				nextFrontendState, nextFrontendEnabled = lastFrontendAttemptState, lastFrontendAttemptEnabled
			} else {
				clearFrontendFailure("scan")
			}
			next, stateErr := applicationSourceState(ctx, ".", updatedConfig)
			if stateErr != nil {
				if ctx.Err() != nil {
					return 0
				}
				fail("source_scan_failed", stateErr.Error())
				continue
			}
			frontendChanged := nextFrontendState != lastFrontendAttemptState || nextFrontendEnabled != lastFrontendAttemptEnabled
			currentFrontendSettings := frontendSettingsOf(frontend)
			frontendActivationChanged := !sameFrontendSettings(currentFrontendSettings, lastFrontendAppSettings)
			frontendActivationPending := frontendActivationChanged && !sameFrontendSettings(currentFrontendSettings, lastFrontendAppAttemptSettings)
			if next == last && !frontendChanged && !frontendActivationPending {
				continue
			}
			if frontendChanged || next != last {
				frontendUpdated := true
				settings, settingsErr := resolveFrontendSettings(projectRoot, updatedConfig)
				if settingsErr != nil {
					reportFrontendFailure("settings", "frontend_scan_failed", settingsErr.Error())
					frontendUpdated = false
				} else {
					clearFrontendFailure("settings")
					if !sameFrontendSettings(frontendSettingsOf(frontend), settings) {
						var replacement *frontendBuilder
						if settings != nil {
							replacement, _, settingsErr = newFrontendBuilder(projectRoot, updatedConfig, false, processErr)
							if settingsErr == nil {
								settingsErr = replacement.rebuild(processErr)
							}
						}
						if settingsErr != nil {
							if replacement != nil {
								replacement.close()
							}
							reportFrontendFailure("build", "frontend_build_failed", settingsErr.Error())
							frontendUpdated = false
						} else {
							frontend.close()
							frontend = replacement
							emit("frontend_build_succeeded", "Frontend build succeeded.", map[string]any{"initial": false})
						}
					} else if frontendChanged && frontend != nil {
						if buildErr := frontend.rebuild(processErr); buildErr != nil {
							reportFrontendFailure("build", "frontend_build_failed", buildErr.Error())
							frontendUpdated = false
						} else {
							emit("frontend_build_succeeded", "Frontend build succeeded.", map[string]any{"initial": false})
						}
					}
				}
				lastFrontendAttemptState = nextFrontendState
				lastFrontendAttemptEnabled = nextFrontendEnabled
				if frontendUpdated {
					clearFrontendFailure("build")
				}
			}
			frontendSettings := frontendSettingsOf(frontend)
			frontendActivationChanged = !sameFrontendSettings(frontendSettings, lastFrontendAppSettings)
			frontendActivationPending = frontendActivationChanged && !sameFrontendSettings(frontendSettings, lastFrontendAppAttemptSettings)
			if next == last && !frontendActivationPending {
				continue
			}
			last = next
			emit("change_detected", "Application source change detected.", map[string]any{"source_hash": next})
			emit("build_started", "Building changed application...", map[string]any{"initial": false})
			newBinary, newBinaryDir, rebuiltCached, buildErr := cachedDevBinaryContext(ctx, updatedConfig, next, processErr, frontendSettingsOf(frontend))
			if buildErr != nil {
				if ctx.Err() != nil {
					return 0
				}
				lastFrontendAppAttemptSettings = frontendSettings
				fail("build_failed", fmt.Sprintf("Build failed; the previous application process is unchanged: %v", buildErr))
				continue
			}
			lastFrontendAppAttemptSettings = frontendSettings
			emit("build_succeeded", "Changed application build succeeded.", map[string]any{"initial": false, "cached": rebuiltCached})
			if ctx.Err() != nil {
				return 0
			}
			config = updatedConfig
			if stopDevProcess(process, false) {
				fail("process_killed", forcedTerminationMessage("Previous application", runtime.GOOS))
			}
			process, err = startDevProcess(newBinary, newBinaryDir, config, processOut, processErr, frontendSettingsOf(frontend))
			if err != nil {
				_ = os.RemoveAll(newBinaryDir)
				fail("process_start_failed", fmt.Sprintf("could not start rebuilt app: %v", err))
				process = nil
				processDone = nil
				continue
			}
			processDone = process.done
			lastFrontendAppSettings = frontendSettings
			emit("process_started", "Rebuilt application process started.", map[string]any{"pid": process.cmd.Process.Pid, "address": fmt.Sprintf("http://127.0.0.1:%d", config.Port), "readiness_observed": false})
		}
	}
}

func buildDevBinary(config projectConfig, errOut io.Writer, frontend ...*frontendSettings) (string, string, error) {
	return buildDevBinaryContext(context.Background(), config, errOut, frontend...)
}

func buildDevBinaryContext(ctx context.Context, config projectConfig, errOut io.Writer, frontend ...*frontendSettings) (string, string, error) {
	directory, err := os.MkdirTemp("", "spine-dev-build-*")
	if err != nil {
		return "", "", err
	}
	projectRoot, err := filepath.Abs(".")
	if err != nil {
		_ = os.RemoveAll(directory)
		return "", "", err
	}
	overlay, err := createFrontendOverlay(projectRoot, config, frontendSettingsArgument(frontend))
	if err != nil {
		_ = os.RemoveAll(directory)
		return "", "", err
	}
	if overlay != nil {
		defer overlay.cleanup()
	}
	binary := devBinaryPath(directory, runtime.GOOS)
	cmd := exec.CommandContext(ctx, "go", applicationBuildArgs(config, binary, overlay)...)
	cmd.Stdout, cmd.Stderr = errOut, errOut
	if err := cmd.Run(); err != nil {
		_ = os.RemoveAll(directory)
		return "", "", err
	}
	return binary, directory, nil
}

func cachedDevBinaryContext(ctx context.Context, config projectConfig, sourceDigest string, errOut io.Writer, frontend ...*frontendSettings) (string, string, bool, error) {
	userCache, err := os.UserCacheDir()
	if err != nil {
		return "", "", false, fmt.Errorf("could not locate user cache directory: %w", err)
	}
	projectRoot, err := filepath.Abs(".")
	if err != nil {
		return "", "", false, err
	}
	cacheRoot := filepath.Join(userCache, "spine", "dev")
	return cachedDevBinaryAtContext(ctx, cacheRoot, projectRoot, config, sourceDigest, errOut, frontend...)
}

func cachedDevBinaryAt(cacheRoot, projectRoot string, config projectConfig, sourceDigest string, errOut io.Writer, frontend ...*frontendSettings) (string, string, bool, error) {
	return cachedDevBinaryAtContext(context.Background(), cacheRoot, projectRoot, config, sourceDigest, errOut, frontend...)
}

func cachedDevBinaryAtContext(ctx context.Context, cacheRoot, projectRoot string, config projectConfig, sourceDigest string, errOut io.Writer, frontend ...*frontendSettings) (string, string, bool, error) {
	settings := frontendSettingsArgument(frontend)
	cacheDigest := sourceDigest
	if settings != nil {
		cacheDigest += "\nfrontend-overlay-v1"
	}
	key := devCacheKey(config, cacheDigest, projectRoot)
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
	overlay, err := createFrontendOverlay(projectRoot, config, settings)
	if err != nil {
		_ = os.RemoveAll(staging)
		return "", "", false, err
	}
	if overlay != nil {
		defer overlay.cleanup()
	}
	stagingBinary := devBinaryPath(staging, runtime.GOOS)
	cmd := exec.CommandContext(ctx, "go", applicationBuildArgs(config, stagingBinary, overlay)...)
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

func startDevProcess(binary, binaryDir string, config projectConfig, out, errOut io.Writer, frontend ...*frontendSettings) (*devProcess, error) {
	return startApplicationProcess(binary, binaryDir, config, "development", out, errOut, frontend...)
}

func startApplicationProcess(binary, binaryDir string, config projectConfig, environment string, out, errOut io.Writer, frontend ...*frontendSettings) (*devProcess, error) {
	cmd := exec.Command(binary)
	cmd.Stdout, cmd.Stderr, cmd.Stdin = out, errOut, os.Stdin
	environmentValues := []string{fmt.Sprintf("SPINE_PORT=%d", config.Port), "ENV=" + environment}
	if len(frontend) > 0 && frontend[0] != nil {
		environmentValues = append(environmentValues, "SPINE_STATIC_DIR="+frontend[0].output, "SPINE_FRONTEND_API_PREFIX="+frontend[0].apiPrefix)
	}
	cmd.Env = replaceEnvironmentValues(os.Environ(), environmentValues...)
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

func replaceEnvironmentValues(environment []string, values ...string) []string {
	replaced := make(map[string]struct{}, len(values))
	for _, value := range values {
		if key, _, ok := strings.Cut(value, "="); ok {
			replaced[key] = struct{}{}
		}
	}
	result := make([]string, 0, len(environment)+len(values))
	for _, value := range environment {
		key, _, ok := strings.Cut(value, "=")
		if _, exists := replaced[key]; ok && exists {
			continue
		}
		result = append(result, value)
	}
	return append(result, values...)
}

func flushDevEventWriter(writer io.Writer) {
	if flusher, ok := writer.(interface{ Flush() }); ok {
		flusher.Flush()
	}
}

// CLI의 기본 대기는 Spine의 기본 정상 종료 제한 10초보다 길게 둡니다.
func applicationShutdownTimeout() (time.Duration, error) {
	value := os.Getenv("SPINE_CLI_SHUTDOWN_TIMEOUT")
	if value == "" {
		return 15 * time.Second, nil
	}
	timeout, err := time.ParseDuration(value)
	if err != nil || timeout <= 0 {
		return 0, fmt.Errorf("SPINE_CLI_SHUTDOWN_TIMEOUT must be a positive duration (for example 30s)")
	}
	return timeout, nil
}

func forcedTerminationMessage(subject, goos string) string {
	if devTerminationUsesKill(goos) {
		return subject + " was forcibly terminated using the Windows fallback."
	}
	return subject + " exceeded the shutdown timeout and was killed."
}

// stopDevProcess는 자식을 강제 종료했는지 반환합니다.
func stopDevProcess(process *devProcess, removeBinary bool) bool {
	return stopDevProcessForOS(process, removeBinary, runtime.GOOS)
}

func stopDevProcessForOS(process *devProcess, removeBinary bool, goos string) bool {
	if process == nil {
		return false
	}
	if removeBinary {
		defer os.RemoveAll(process.binaryDir)
	}
	if process.cmd == nil || process.cmd.Process == nil {
		return false
	}
	if err := terminateDevProcess(process.cmd.Process, goos); err != nil {
		select {
		case <-process.done:
			return false
		default:
		}
	} else if devTerminationUsesKill(goos) {
		// Windows의 즉시 강제 종료도 정상 종료와 구분해 보고합니다.
		<-process.done
		return true
	}
	timeout, err := applicationShutdownTimeout()
	if err != nil {
		timeout = 15 * time.Second
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-process.done:
		return false
	case <-timer.C:
		_ = process.cmd.Process.Kill()
		<-process.done
		return true
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
