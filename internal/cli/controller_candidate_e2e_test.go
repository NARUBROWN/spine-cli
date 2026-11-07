package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestControllerCandidateEndToEnd는 공개 전 후보를 명시한 소스 조합으로 검증합니다.
// 공개 태그 설치 검증과 구분하며, 일반 단위 테스트에서 외부 체크아웃을 추측하지 않습니다.
func TestControllerCandidateEndToEnd(t *testing.T) {
	core := os.Getenv("SPINE_CONTROLLER_CORE_SOURCE")
	if core == "" {
		t.Skip("SPINE_CONTROLLER_CORE_SOURCE is required for source-candidate E2E")
	}
	absolute, err := filepath.Abs(core)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	options := newProjectOptions{Module: "example.com/controller-candidate", Layout: layeredLayout, Platform: "nethttp", Validator: "none", WebSocketPlatform: "none", Modules: []selectedModule{{Path: spineImportPath, Version: controllerRoutesMinimum}, {Path: nethttpPlatformPath, Version: "v0.1.0-preview.2"}}}
	files, err := newProjectFiles(options)
	if err != nil {
		t.Fatal(err)
	}
	for name, contents := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("GOWORK", "off")
	runGo := func(arguments ...string) {
		t.Helper()
		command := exec.Command("go", arguments...)
		command.Dir = root
		command.Env = os.Environ()
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("go %v: %v\n%s", arguments, err, output)
		}
	}
	runGo("mod", "edit", "-replace="+spineImportPath+"="+absolute)
	runGo("mod", "tidy")
	runtimeTest := `package application
 import(
 "context"
 "net/http/httptest"
 "strings"
 "testing"
 "github.com/NARUBROWN/spine"
 )
 func TestControllerCandidateHTTP(t *testing.T){
 app,options,err:=BuildApplication();if err!=nil{t.Fatal(err)}
 if _,err:=spine.DescribeHTTP(app,options);err!=spine.ErrRoutesNotCollected{t.Fatal(err)}
 prepared,err:=app.Prepare(context.Background(),options);if err!=nil{t.Fatal(err)};defer prepared.Close(context.Background())
 handler,err:=spine.NetHTTPHandler(prepared);if err!=nil{t.Fatal(err)}
 response:=httptest.NewRecorder();handler.ServeHTTP(response,httptest.NewRequest("GET","/health",nil))
 if response.Code!=200 || !strings.Contains(response.Body.String(),"ok"){t.Fatal(response.Code,response.Body.String())}
 doc,err:=spine.DescribePreparedHTTP(prepared);if err!=nil || len(doc.Routes)!=1{t.Fatal(doc,err)}
 }`
	if err := os.WriteFile(filepath.Join(root, "internal/application/controller_candidate_test.go"), []byte(runtimeTest), 0644); err != nil {
		t.Fatal(err)
	}
	runGo("test", "./...")
	t.Chdir(root)
	contract, err := inspectApplication()
	if err != nil || contractHasErrors(contract) || len(contract.HTTP) != 1 {
		t.Fatalf("contract=%+v error=%v", contract, err)
	}
	if _, err := os.Stat("src/route/route.go"); !os.IsNotExist(err) {
		t.Fatal("modern scaffold created route.go")
	}
	if !strings.Contains(contract.HTTP[0].Handler, "RegisterRoutes") {
		t.Fatal(contract.HTTP)
	}
	applicationPath := filepath.Join(root, "internal/application/application.go")
	application, err := os.ReadFile(applicationPath)
	if err != nil {
		t.Fatal(err)
	}
	aliased := strings.ReplaceAll(string(application), "constructor.", "makers.")
	aliased = strings.Replace(aliased, `"github.com/NARUBROWN/spine/pkg/constructor"`, `makers "github.com/NARUBROWN/spine/pkg/constructor"`, 1)
	if err := os.WriteFile(applicationPath, []byte(aliased), 0644); err != nil {
		t.Fatal(err)
	}
	layeredGenerate(t, "controller", "audit")
	application, err = os.ReadFile(applicationPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(application), "app.Constructor(") != 1 || !strings.Contains(string(application), "makers.Controller(&controller.AuditController{})") || strings.Contains(string(application), "), makers.Controller(") {
		t.Fatalf("generation did not retain one aliased role registration block:\n%s", application)
	}
	handlers := `package controller
 import("context";models "github.com/NARUBROWN/spine/pkg/httpx")
 type AuditBody struct{Name string ` + "`json:\"name\"`" + `}
 type AuditResponse = models.Response[AuditBody]
 func(c *AuditController)create(ctx context.Context,a *AuditBody)(AuditResponse,error){return AuditResponse{Body:*a},nil}
 `
	if err := os.WriteFile(filepath.Join(root, "src/controller/audit_handlers.go"), []byte(handlers), 0644); err != nil {
		t.Fatal(err)
	}
	details := layeredGenerate(t, "route", "audit-create", "--method", "POST", "--path", "/audit", "--handler", "src/controller:AuditController.create")
	if strings.Contains(details["diff"].(string), "src/route/route.go") {
		t.Fatal("modern route edited legacy route table")
	}
	repeated := layeredGenerate(t, "route", "audit-create", "--method", "POST", "--path", "/audit", "--handler", "src/controller:AuditController.create")
	if len(repeated["changes"].([]any)) != 0 {
		t.Fatalf("route generation is not idempotent: %+v", repeated)
	}
	generated, err := os.ReadFile(filepath.Join(root, "src/controller/audit_controller.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(generated), "input.Body[AuditBody]") {
		t.Fatal(string(generated))
	}
	runtimeTest = strings.ReplaceAll(runtimeTest, "len(doc.Routes)!=1", "len(doc.Routes)!=2")
	runtimeTest = strings.Replace(runtimeTest, "response:=httptest.NewRecorder();", `response:=httptest.NewRecorder();`, 1)
	runtimeTest += `
func TestGeneratedAuditCallback(t *testing.T){
 app,options,err:=BuildApplication();if err!=nil{t.Fatal(err)}
 prepared,err:=app.Prepare(context.Background(),options);if err!=nil{t.Fatal(err)};defer prepared.Close(context.Background())
 handler,err:=spine.NetHTTPHandler(prepared);if err!=nil{t.Fatal(err)}
 request:=httptest.NewRequest("POST","/audit",strings.NewReader("{\"name\":\"audit\"}"));request.Header.Set("Content-Type","application/json")
 response:=httptest.NewRecorder();handler.ServeHTTP(response,request)
 if response.Code!=200 || !strings.Contains(response.Body.String(),"audit"){t.Fatal(response.Code,response.Body.String())}
 }`
	if err := os.WriteFile(filepath.Join(root, "internal/application/controller_candidate_test.go"), []byte(runtimeTest), 0644); err != nil {
		t.Fatal(err)
	}
	runGo("test", "./...")
	contract, err = inspectApplication()
	if err != nil || contractHasErrors(contract) || len(contract.HTTP) != 2 {
		t.Fatalf("generated route contract=%+v error=%v", contract, err)
	}

	healthPath := filepath.Join(root, "src/controller/health_controller.go")
	health, err := os.ReadFile(healthPath)
	if err != nil {
		t.Fatal(err)
	}
	guarded := strings.Replace(string(health), "return &HealthController{}", `panic("runtime export executed a controller constructor")`, 1)
	if guarded == string(health) {
		t.Fatal("health constructor guard not installed")
	}
	if err := os.WriteFile(healthPath, []byte(guarded), 0644); err != nil {
		t.Fatal(err)
	}
	_, exportErr := executeRuntimeOpenAPI()
	if err := os.WriteFile(healthPath, health, 0644); err != nil {
		t.Fatal(err)
	}
	if exportErr == nil || !strings.Contains(exportErr.Error(), "controller routes are not collected") || !strings.Contains(exportErr.Error(), "did not call Prepare") {
		t.Fatalf("runtime export boundary: %v", exportErr)
	}

}
