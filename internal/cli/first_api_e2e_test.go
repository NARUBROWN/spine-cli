package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestGeneratedFirstAPIGuideRunsAndDetectsRawReturn(t *testing.T) {
	name := "spine"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binDir := t.TempDir()
	binary := filepath.Join(binDir, name)
	readme, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	install := regexp.MustCompile(`(?m)^go install (github.com/NARUBROWN/spine-cli(?:/[^@\s]+)?)@latest$`).FindStringSubmatch(string(readme))
	if len(install) != 2 {
		t.Fatal("README lacks the public installation command")
	}
	// README의 공개 패키지와 같은 소스를 격리한 GOBIN에 설치합니다.
	packagePath := "." + strings.TrimPrefix(install[1], "github.com/NARUBROWN/spine-cli")
	build := exec.Command("go", "install", packagePath)
	build.Dir = filepath.Join("..", "..")
	build.Env = append(os.Environ(), "GOBIN="+binDir)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("install documented CLI package: %v %s", err, output)
	}
	if _, err := os.Stat(binary); err != nil {
		t.Fatalf("README installation %s did not provide the spine executable: %v", install[1], err)
	}
	workspace := t.TempDir()
	apply := func(root string, args ...string) {
		t.Helper()
		plan := runE2E(binary, root, nil, append(append([]string{}, args...), "--dry-run", "--json")...)
		assertE2ESuccess(t, plan, "guide plan")
		var result struct {
			Result struct {
				Hash string `json:"base_hash"`
			} `json:"result"`
		}
		if err := json.Unmarshal([]byte(plan.stdout), &result); err != nil {
			t.Fatal(err)
		}
		applied := runE2E(binary, root, nil, append(append([]string{}, args...), "--if-match", result.Result.Hash)...)
		assertE2ESuccess(t, applied, "guide apply")
		if args[0] == "new" && (!strings.Contains(applied.stdout, "  spine dev\n") || strings.Contains(applied.stdout, "spine-cli dev")) {
			t.Fatalf("new suggested a command outside the documented installation: %s", applied.stdout)
		}
	}
	for _, layout := range []string{"layered-v1", "legacy"} {
		apply(workspace, "new", "offline-"+layout, "--layout", layout, "--skip-install")
	}
	apply(workspace, "new", "shop-api", "--module", "example.com/first-api")
	project := filepath.Join(workspace, "shop-api")
	contents, err := os.ReadFile(filepath.Join(project, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	guide := string(contents)
	if strings.Contains(guide, "    go mod tidy\n") || strings.Contains(guide, "{{MODULE}}") || strings.Contains(guide, "spine-cli ") {
		t.Fatalf("README has a stale install step or placeholder: %s", guide)
	}
	code := regexp.MustCompile("(?s)```go\\n(.*?)\\n```").FindStringSubmatch(guide)
	if len(code) != 2 {
		t.Fatal("generated guide lacks complete Go handler")
	}
	if strings.Contains(guide, "RegisterRoutes(*api.Registry)") {
		apply(project, "g", "controller", "product")
		controller := filepath.Join(project, "src/controller/product_controller.go")
		generated, err := os.ReadFile(controller)
		if err != nil {
			t.Fatal(err)
		}
		body := strings.Replace(string(generated), "func (c *ProductController) RegisterRoutes(r *api.Registry) {}", code[1], 1)
		if body == string(generated) {
			t.Fatal("generated RegisterRoutes stub missing")
		}
		body = strings.Replace(body, "import (", "import (\n\"github.com/NARUBROWN/spine/pkg/httpx\"\n\"github.com/NARUBROWN/spine/pkg/input\"", 1)
		writeE2EFile(t, controller, body)
		writeE2EFile(t, filepath.Join(project, "internal/application/guide_test.go"), modernFirstAPIGuideHTTPTest)
		format := exec.Command("go", "fmt", "./...")
		format.Dir = project
		if output, err := format.CombinedOutput(); err != nil {
			t.Fatalf("format modern guide: %v %s", err, output)
		}
		assertE2ESuccess(t, runE2E(binary, project, nil, "verify", "--json"), "modern documented API and real HTTP response")
		bad := strings.Replace(body, "func(a input.Body[CreateProduct]) httpx.Response[CreateProduct]", "func(a input.Body[CreateProduct]) string", 1)
		bad = strings.Replace(bad, "return httpx.Response[CreateProduct]{Body: CreateProduct{Name: a.Name}}", `return "bad"`, 1)
		bad = strings.Replace(bad, "\"github.com/NARUBROWN/spine/pkg/httpx\"\n", "", 1)
		if bad == body {
			t.Fatal("modern raw return fixture was not changed")
		}
		writeE2EFile(t, controller, bad)
		result := runE2E(binary, project, nil, "contract", "check", "--json")
		if result.code != 5 || !strings.Contains(result.stdout, "HTTP_RETURN_TYPE_UNSUPPORTED") {
			t.Fatalf("modern raw return was not rejected: %+v", result)
		}
		return
	}
	for _, line := range strings.Split(guide, "\n") {
		if strings.HasPrefix(line, "spine g dto ") || strings.HasPrefix(line, "spine g controller ") {
			apply(project, strings.Fields(strings.TrimPrefix(line, "spine "))...)
		}
	}
	controller := filepath.Join(project, "src/controller/api/product_controller.go")
	writeE2EFile(t, controller, code[1]+"\n")
	format := exec.Command("gofmt", "-w", controller)
	if output, err := format.CombinedOutput(); err != nil {
		t.Fatalf("format guide: %v %s", err, output)
	}
	for _, line := range strings.Split(guide, "\n") {
		if strings.HasPrefix(line, "spine g route ") {
			apply(project, strings.Fields(strings.TrimPrefix(line, "spine "))...)
		}
	}
	writeE2EFile(t, filepath.Join(project, "internal/application/guide_test.go"), firstAPIGuideHTTPTest)
	format = exec.Command("gofmt", "-w", filepath.Join(project, "internal/application/guide_test.go"))
	if output, err := format.CombinedOutput(); err != nil {
		t.Fatalf("format guide HTTP test: %v %s", err, output)
	}
	assertE2ESuccess(t, runE2E(binary, project, nil, "verify", "--json"), "documented API and real HTTP response")
	// 컴파일 가능한 raw 반환형으로 바꾸면 Go 테스트 실행 전에 계약 gate가 거부해야 합니다.
	writeE2EFile(t, controller, "package api\ntype ProductController struct{}\nfunc NewProductController() *ProductController { return &ProductController{} }\nfunc (*ProductController) Create() string { return \"bad\" }\n")
	for _, args := range [][]string{{"contract", "check", "--json"}, {"verify", "--json"}} {
		result := runE2E(binary, project, nil, args...)
		if result.code != 5 || !strings.Contains(result.stdout, "HTTP_RETURN_TYPE_UNSUPPORTED") || !strings.Contains(result.stdout, "product_controller.go:") {
			t.Fatalf("raw return was not rejected: %+v", result)
		}
	}
}

const firstAPIGuideHTTPTest = `package application
import (
 "github.com/NARUBROWN/spine"
 "context"
 "net/http/httptest"
 "strings"
 "testing"
)
func TestDocumentedProductAPI(t *testing.T) {
 app,options,err:=BuildApplication();if err!=nil {t.Fatal(err)}
 prepared,err:=app.Prepare(context.Background(),options);if err!=nil {t.Fatal(err)}
 defer prepared.Close(context.Background())
 handler,err:=spine.NetHTTPHandler(prepared);if err!=nil {t.Fatal(err)}
 server:=httptest.NewServer(handler);defer server.Close()
 for _,scenario:=range []struct {body string;status int}{
  {"{\"name\":\"Bolt\",\"quantity\":5}",201},
  {"{\"name\":\"\",\"quantity\":-1}",400},
  {"{\"name\":\"Bolt\",\"quantity\":\"bad\"}",400},
 } {
  response,err:=server.Client().Post(server.URL+"/products","application/json",strings.NewReader(scenario.body));if err!=nil {t.Fatal(err)}
  response.Body.Close();if response.StatusCode!=scenario.status {t.Fatalf("got %d want %d",response.StatusCode,scenario.status)}
 }
}
`

const modernFirstAPIGuideHTTPTest = `package application
import (
 "github.com/NARUBROWN/spine"
 "context"
 "io"
 "net/http/httptest"
 "strings"
 "testing"
)
func TestModernDocumentedProductAPI(t *testing.T) {
 app,options,err:=BuildApplication();if err!=nil {t.Fatal(err)}
 prepared,err:=app.Prepare(context.Background(),options);if err!=nil {t.Fatal(err)}
 defer prepared.Close(context.Background())
 handler,err:=spine.NetHTTPHandler(prepared);if err!=nil {t.Fatal(err)}
 server:=httptest.NewServer(handler);defer server.Close()
 response,err:=server.Client().Post(server.URL+"/products","application/json",strings.NewReader("{\"name\":\"Bolt\"}"));if err!=nil {t.Fatal(err)}
 defer response.Body.Close();body,err:=io.ReadAll(response.Body);if err!=nil {t.Fatal(err)}
 if response.StatusCode!=200 || !strings.Contains(string(body), "Bolt") {t.Fatalf("got %d %s",response.StatusCode,body)}
}
`
