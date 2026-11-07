package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGeneratedProjectsHandleHTTPInputErrors(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "spine")
	build := exec.Command("go", "build", "-o", binary, "./cmd/spine")
	build.Dir = filepath.Join("..", "..")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v %s", err, output)
	}
	for _, layout := range []string{layeredLayout, "legacy"} {
		t.Run(layout, func(t *testing.T) {
			workspace := t.TempDir()
			apply := func(directory string, args ...string) {
				t.Helper()
				plan := runE2E(binary, directory, nil, append(append([]string{}, args...), "--dry-run", "--json")...)
				assertE2ESuccess(t, plan, "HTTP fixture plan")
				var envelope struct {
					Result struct {
						BaseHash string `json:"base_hash"`
						Diff     string `json:"diff"`
					} `json:"result"`
				}
				if err := json.Unmarshal([]byte(plan.stdout), &envelope); err != nil || envelope.Result.BaseHash == "" || envelope.Result.Diff == "" {
					t.Fatalf("missing reviewed plan: %v %s", err, plan.stdout)
				}
				result := runE2E(binary, directory, nil, append(append([]string{}, args...), "--if-match", envelope.Result.BaseHash, "--json")...)
				assertE2ESuccess(t, result, "HTTP fixture apply")
			}
			apply(workspace, "new", "todo", "--module", "example.com/todo", "--layout", layout, "--websocket-platform", "gorilla")
			project := filepath.Join(workspace, "todo")
			mod, _ := os.ReadFile(filepath.Join(project, "go.mod"))
			selected := strings.TrimSpace(generatedGo(t, project, "list", "-m", "-f", "{{.Version}}", spineImportPath))
			if !strings.Contains(string(mod), "github.com/NARUBROWN/spine "+selected) || selected == "" {
				t.Fatalf("generated project uses an unfixed runtime: %s", mod)
			}
			if err := os.MkdirAll(filepath.Join(project, "handlers"), 0o755); err != nil {
				t.Fatal(err)
			}
			_, legacyRouteErr := os.Stat(filepath.Join(project, "src/route/route.go"))
			if legacyRouteErr != nil && !os.IsNotExist(legacyRouteErr) {
				t.Fatal(legacyRouteErr)
			}
			modern := layout == layeredLayout && os.IsNotExist(legacyRouteErr)
			handlerSource := `package handlers
import (
 "errors"
 "github.com/NARUBROWN/spine/pkg/httpx"
 "github.com/NARUBROWN/spine/pkg/path"
)
type Controller struct{}
func NewController() *Controller { return &Controller{} }
type Request struct { Quantity int64 }
func (*Controller) Create(req *Request) httpx.Response[map[string]int64] {
 return httpx.Response[map[string]int64]{Body: map[string]int64{"quantity":req.Quantity}}
}
func (*Controller) Detail(id path.Int) httpx.Response[map[string]int64] {
 return httpx.Response[map[string]int64]{Body: map[string]int64{"id":id.Value}}
}
type WrapperBody struct { Value int64 }
func (*Controller) Wrapped(req *WrapperBody) httpx.Response[map[string]int64] {
 return httpx.Response[map[string]int64]{Body: map[string]int64{"value":req.Value}}
}
func (*Controller) Fail() error { return errors.New("internal fixture failure") }
type TextResponse = httpx.Response[string]
func (*Controller) Text() TextResponse { return TextResponse{Body:"alias text"} }
func (*Controller) Download() *httpx.Binary { return &httpx.Binary{ContentType:"application/octet-stream",Data:[]byte("binary data")} }
func (*Controller) Redirect() httpx.Redirect { return httpx.Redirect{Location:"/health"} }

`
			handlerPath := filepath.Join(project, "handlers", "controller.go")
			writeE2EFile(t, handlerPath, handlerSource)
			if modern {
				rejected := runE2E(binary, project, nil, "g", "route", "create", "--method", "POST", "--path", "/products", "--handler", "handlers:Controller.Create", "--dry-run", "--json")
				if rejected.code == 0 || !strings.Contains(rejected.stdout, "requires a Constructor method") {
					t.Fatalf("factory controller must fail safely: %+v", rejected)
				}
				handlerSource = strings.Replace(handlerSource, "func NewController() *Controller", "func (*Controller) Constructor() *Controller", 1)
				writeE2EFile(t, handlerPath, handlerSource)
			}
			if layout == layeredLayout {
				for _, route := range []struct{ name, method, path, handler string }{
					{"create", "POST", "/products", "Create"}, {"detail", "GET", "/products/:id", "Detail"}, {"wrapper", "POST", "/wrapper", "Wrapped"}, {"fail", "GET", "/failure", "Fail"}, {"text", "GET", "/text", "Text"}, {"download", "GET", "/download", "Download"}, {"redirect", "GET", "/redirect", "Redirect"},
				} {
					apply(project, "g", "route", route.name, "--method", route.method, "--path", route.path, "--handler", "handlers:Controller."+route.handler)
				}
			} else {
				path := filepath.Join(project, "internal", "application", "application.go")
				source, _ := os.ReadFile(path)
				text := strings.Replace(string(source), "import (", "import (\n\"example.com/todo/handlers\"", 1)
				text = strings.Replace(text, "return app, boot.Options{", `app.Constructor(handlers.NewController)
 app.Route("POST", "/products", (*handlers.Controller).Create)
 app.Route("GET", "/products/:id", (*handlers.Controller).Detail)
 app.Route("POST", "/wrapper", (*handlers.Controller).Wrapped)
 app.Route("GET", "/failure", (*handlers.Controller).Fail)
 app.Route("GET", "/text", (*handlers.Controller).Text)
 app.Route("GET", "/download", (*handlers.Controller).Download)
 app.Route("GET", "/redirect", (*handlers.Controller).Redirect)
 return app, boot.Options{`, 1)
				writeE2EFile(t, path, text)
			}
			writeE2EFile(t, filepath.Join(project, "internal", "application", "http_test.go"), generatedHTTPInputTest)
			writeE2EFile(t, filepath.Join(project, "internal", "application", "platform_test.go"), generatedPlatformLifecycleTest)
			format := exec.Command("go", "fmt", "./...")
			format.Dir = project
			if output, err := format.CombinedOutput(); err != nil {
				t.Fatalf("format HTTP fixture: %v %s", err, output)
			}
			tidy := exec.Command("go", "mod", "tidy")
			tidy.Dir = project
			if output, err := tidy.CombinedOutput(); err != nil {
				t.Fatalf("HTTP fixture dependencies: %v %s", err, output)
			}
			check := runE2E(binary, project, nil, "contract", "check", "--json")
			assertE2ESuccess(t, check, "default HTTP binding contract")
			verify := runE2E(binary, project, nil, "verify", "--json")
			assertE2ESuccess(t, verify, "generated project tests including real HTTP listener")
			assertProtocolOK(t, verify.stdout)
		})
	}
}

const generatedHTTPInputTest = `package application
import (
 "context"
 "io"
 "net"
 "net/http"
 "strings"
 "testing"
 "time"
)
func TestDefaultHTTPInputErrors(t *testing.T) {
 listener, err := net.Listen("tcp", "127.0.0.1:0")
 if err != nil { t.Fatal(err) }
 address := listener.Addr().String()
 _ = listener.Close()
 app, options, err := BuildApplication()
 if err != nil { t.Fatal(err) }
 options.Address = address
 options.EnableGracefulShutdown = true
 options.ShutdownTimeout = time.Second
 ctx, cancel := context.WithCancel(context.Background())
 done := make(chan error, 1)
 go func() { done <- app.RunContext(ctx, options) }()
 defer func() {
  cancel()
  select {
  case err := <-done: if err != nil { t.Error(err) }
  case <-time.After(5*time.Second): t.Error("HTTP fixture did not stop")
  }
 }()
 client := &http.Client{Timeout: time.Second, CheckRedirect:func(*http.Request,[]*http.Request)error{return http.ErrUseLastResponse}}
 base := "http://"+address
 deadline := time.Now().Add(10*time.Second)
 for {
  response, err := client.Get(base+"/health")
  if err == nil { _ = response.Body.Close(); if response.StatusCode == 200 { break } }
  select { case err := <-done: done <- err; t.Fatalf("HTTP server exited before readiness: %v", err); default: }
  if time.Now().After(deadline) { t.Fatal("HTTP readiness timeout") }
  time.Sleep(20*time.Millisecond)
 }
 for _, test := range []struct { name, method, path, body string; status int; response string } {
  {"valid body", "POST", "/products", "{\"quantity\":2}", 200, "\"quantity\":2"},
  {"fractional integer", "POST", "/products", "{\"quantity\":1.5}", 400, ""},
  {"malformed JSON", "POST", "/products", "{", 400, ""},
  {"valid path", "GET", "/products/2", "", 200, "\"id\":2"},
  {"invalid integer path", "GET", "/products/not-an-int", "", 400, ""},
  {"wrapper pointer body", "POST", "/wrapper", "{\"Value\":2}", 200, "\"value\":2"},
  {"internal error", "GET", "/failure", "", 500, ""},
  {"response alias", "GET", "/text", "", 200, "alias text"},
  {"binary pointer", "GET", "/download", "", 200, "binary data"},
  {"redirect", "GET", "/redirect", "", 302, ""},
 } {
  t.Run(test.name, func(t *testing.T) {
   request, err := http.NewRequest(test.method, base+test.path, strings.NewReader(test.body))
   if err != nil { t.Fatal(err) }
   request.Header.Set("Content-Type", "application/json")
   response, err := client.Do(request)
   if err != nil { t.Fatal(err) }
   defer response.Body.Close()
   if response.StatusCode != test.status { t.Fatalf("got %d, want %d", response.StatusCode, test.status) }
   body, err := io.ReadAll(response.Body)
   if err != nil { t.Fatal(err) }
   if test.response != "" && !strings.Contains(string(body), test.response) { t.Fatalf("unexpected response body: %s", body) }
  })
 }
}
`
