package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestGeneratedFirstAPIGuideRunsAndDetectsRawReturn(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "spine")
	build := exec.Command("go", "build", "-o", binary, "./cmd/spine")
	build.Dir = filepath.Join("..", "..")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v %s", err, output)
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
		assertE2ESuccess(t, runE2E(binary, root, nil, append(append([]string{}, args...), "--if-match", result.Result.Hash)...), "guide apply")
	}
	apply(workspace, "new", "shop-api", "--module", "example.com/first-api")
	project := filepath.Join(workspace, "shop-api")
	contents, err := os.ReadFile(filepath.Join(project, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	guide := string(contents)
	if strings.Contains(guide, "    go mod tidy\n") || strings.Contains(guide, "{{MODULE}}") {
		t.Fatalf("README has a stale install step or placeholder: %s", guide)
	}
	code := regexp.MustCompile("(?s)```go\\n(.*?)\\n```").FindStringSubmatch(guide)
	if len(code) != 2 {
		t.Fatal("generated guide lacks complete Go handler")
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
 "context"
 "net/http/httptest"
 "strings"
 "testing"
 "github.com/NARUBROWN/spine"
)
func TestDocumentedProductAPI(t *testing.T) {
 app,options,err:=BuildApplication();if err!=nil {t.Fatal(err)}
 prepared,err:=spine.Prepare(context.Background(),app,options);if err!=nil {t.Fatal(err)}
 defer prepared.Close(context.Background())
 server:=httptest.NewServer(prepared.Handler());defer server.Close()
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
