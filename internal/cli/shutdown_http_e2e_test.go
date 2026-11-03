package cli

import (
	"io"
	"net"
	"net/http"
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

func TestGeneratedHTTPShutdownDrainsRequests(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix signal drain; Windows uses kill fallback")
	}
	binary := buildUpdateCLI(t)
	for _, layout := range []string{layeredLayout, "legacy"} {
		for _, mode := range []string{"run", "dev"} {
			t.Run(layout+"/"+mode, func(t *testing.T) {
				workspace := t.TempDir()
				applyAssemblyPlan(t, binary, workspace, "new", "sample", "--module", "example.com/sample", "--layout", layout)
				project := filepath.Join(workspace, "sample")
				mainPath := filepath.Join(project, "main.go")
				source, _ := os.ReadFile(mainPath)
				text := strings.Replace(string(source), "if err := app.Validate(options);", "app.Constructor(application.NewSlowController)\napp.Route(\"GET\", \"/slow\", (*application.SlowController).Get)\nif err := app.Validate(options);", 1)
				if text == string(source) {
					t.Fatal("fixture entrypoint no longer matches generated source")
				}
				writeE2EFile(t, mainPath, text)
				writeE2EFile(t, filepath.Join(project, "internal/application/slow.go"), `package application
import("os";"time";"github.com/NARUBROWN/spine/pkg/httpx")
type SlowController struct{}
func NewSlowController() *SlowController { return &SlowController{} }
func (*SlowController) Get() httpx.Response[string] {
 _ = os.WriteFile("request-started", []byte("yes"), 0600)
 time.Sleep(4*time.Second)
 return httpx.Response[string]{Body:"completed"}
}
`)
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				port := listener.Addr().(*net.TCPAddr).Port
				_ = listener.Close()
				configPath := filepath.Join(project, "spine.toml")
				config, _ := os.ReadFile(configPath)
				writeE2EFile(t, configPath, strings.Replace(string(config), "port = 8080", "port = "+strconv.Itoa(port), 1))
				log, err := os.Create(filepath.Join(t.TempDir(), "http.log"))
				if err != nil {
					t.Fatal(err)
				}
				defer log.Close()
				command := exec.Command(binary, mode)
				command.Dir = project
				command.Env = mergeE2EEnvironment([]string{"GOWORK=off", "SPINE_CLI_SHUTDOWN_TIMEOUT=15s"})
				command.Stdout, command.Stderr = log, log
				if err := command.Start(); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = command.Process.Kill() })
				done := make(chan error, 1)
				go func() { done <- command.Wait() }()
				transport := http.DefaultTransport.(*http.Transport).Clone()
				defer transport.CloseIdleConnections()
				client := &http.Client{Timeout: 12 * time.Second, Transport: transport}
				address := "http://127.0.0.1:" + strconv.Itoa(port)
				deadline := time.Now().Add(30 * time.Second)
				ready := false
				for time.Now().Before(deadline) {
					response, err := client.Get(address + "/health")
					if err == nil {
						_ = response.Body.Close()
						if response.StatusCode == 200 {
							ready = true
							break
						}
					}
					time.Sleep(30 * time.Millisecond)
				}
				if !ready {
					contents, _ := os.ReadFile(log.Name())
					t.Fatalf("generated server never became ready: %s", contents)
				}
				result := make(chan string, 1)
				go func() {
					response, err := client.Get(address + "/slow")
					if err != nil {
						result <- err.Error()
						return
					}
					defer response.Body.Close()
					body, _ := io.ReadAll(response.Body)
					result <- strconv.Itoa(response.StatusCode) + ":" + string(body)
				}()
				deadline = time.Now().Add(5 * time.Second)
				started := false
				for time.Now().Before(deadline) {
					if _, err := os.Stat(filepath.Join(project, "request-started")); err == nil {
						started = true
						break
					}
					time.Sleep(10 * time.Millisecond)
				}
				if !started {
					t.Fatal("slow request did not start")
				}
				if err := command.Process.Signal(syscall.SIGTERM); err != nil {
					t.Fatal(err)
				}
				if response := <-result; !strings.HasPrefix(response, "200:") || !strings.Contains(response, "completed") {
					t.Fatalf("request was interrupted: %s", response)
				}
				transport.CloseIdleConnections()
				select {
				case err := <-done:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(10 * time.Second):
					t.Fatal("CLI failed to finish draining")
				}
				rebound, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
				if err != nil {
					t.Fatalf("listener was not released: %v", err)
				}
				_ = rebound.Close()
			})
		}
	}
}
