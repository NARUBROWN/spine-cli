package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeOpenAPIExportUsesCompositionWithoutStartingApplication(t *testing.T) {
	requireValidatorToolchain(t)
	inTempDir(t)
	t.Setenv("SPINE_OPENAPI_CONDITIONAL", "")
	writeE2EFile(t, "go.mod", "module example.com/runtime-api\n\ngo 1.26.0\n\nrequire (\n github.com/NARUBROWN/spine "+currentAssemblySpineVersion+"\n "+validatorPlatformPath+" "+previewPlatformVersion+"\n)\n")
	writeE2EFile(t, "spine.toml", "module = \"example.com/runtime-api\"\nentrypoint = \".\"\ncomposition = \"application\"\nport = 8080\n")
	if err := os.MkdirAll("application", 0o755); err != nil {
		t.Fatal(err)
	}
	writeE2EFile(t, "application/app.go", `package application
import (
 "fmt"
 "os"
 "github.com/NARUBROWN/spine"
 "github.com/NARUBROWN/spine/pkg/boot"
 "github.com/NARUBROWN/spine/pkg/httpx"
 playground "github.com/NARUBROWN/spine-validator-playground"
)
type Data struct { Value int64 `+"`json:\"value\"`"+` }
type Controller struct{}
func (*Controller) Get() httpx.Response[Data] { panic("handler must not run") }
func BuildApplication() (spine.App,boot.Options,error) {
 fmt.Println("composition log must not enter the exported JSON")
 validator, err := playground.New()
 if err != nil { return nil,boot.Options{},err }
 app := spine.New(spine.WithValidator(validator))
 app.Constructor(func()*Controller { panic("constructor must not run") })
 for _,path := range []string{"/first","/second"} { app.Route("GET",path,(*Controller).Get) }
 if os.Getenv("SPINE_OPENAPI_CONDITIONAL") == "true" { app.Route("GET","/conditional",(*Controller).Get) }
 return app,boot.Options{HTTP:&boot.HTTPOptions{GlobalPrefix:"/api",OpenAPI:boot.OpenAPIOptions{Title:"Runtime API"}}},nil
}
`)
	command := exec.Command("go", "mod", "tidy")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("fixture 의존성: %v %s", err, output)
	}
	var out, errOut bytes.Buffer
	if code := Run([]string{"contract", "export", "--format", "openapi", "--source", "runtime"}, &out, &errOut); code != 0 {
		t.Fatalf("런타임 내보내기: %d %s %s", code, out.String(), errOut.String())
	}
	var document struct {
		OpenAPI string
		Paths   map[string]any
		Info    struct{ Title string }
	}
	if err := json.Unmarshal(out.Bytes(), &document); err != nil || document.OpenAPI != "3.1.2" || document.Info.Title != "Runtime API" || len(document.Paths) != 2 || document.Paths["/api/first"] == nil {
		t.Fatalf("실행된 등록을 그대로 내보내야 합니다: %v %s", err, out.String())
	}
	if !strings.Contains(out.String(), "9223372036854775807") {
		t.Fatalf("내보내기 중 정수 정밀도가 바뀌었습니다: %s", out.String())
	}
	t.Setenv("SPINE_OPENAPI_CONDITIONAL", "true")
	out.Reset()
	errOut.Reset()
	if code := Run([]string{"contract", "export", "--format", "openapi", "--source", "runtime"}, &out, &errOut); code != 0 {
		t.Fatalf("조건부 등록: %d %s", code, errOut.String())
	}
	if err := json.Unmarshal(out.Bytes(), &document); err != nil || document.Paths["/api/conditional"] == nil || len(document.Paths) != 3 {
		t.Fatalf("조건부 등록 반영: %v %s", err, out.String())
	}
	t.Setenv("SPINE_OPENAPI_CONDITIONAL", "")
	if files, _ := filepath.Glob(".spine-openapi-*"); len(files) != 0 {
		t.Fatalf("임시 코드가 남았습니다: %v", files)
	}
	out.Reset()
	errOut.Reset()
	if code := Run([]string{"contract", "export", "--format", "openapi", "--source", "runtime", "--output", "openapi.json"}, &out, &errOut); code != 0 {
		t.Fatalf("파일 내보내기: %d %s", code, errOut.String())
	}
	before, err := os.ReadFile("openapi.json")
	if err != nil || !json.Valid(before) {
		t.Fatalf("파일: %v %s", err, before)
	}
	out.Reset()
	errOut.Reset()
	if code := Run([]string{"contract", "export", "--format", "openapi", "--source", "runtime", "--output", "openapi.json"}, &out, &errOut); code == 0 {
		t.Fatal("기존 파일을 덮어쓰면 안 됩니다")
	}
	after, _ := os.ReadFile("openapi.json")
	if !bytes.Equal(before, after) {
		t.Fatal("거부된 내보내기가 기존 파일을 변경했습니다")
	}
}

func TestRuntimeExportOptionsRejectBeforeExecutingProject(t *testing.T) {
	inTempDir(t)
	for _, args := range [][]string{{"--format", "asyncapi", "--source", "runtime"}, {"--format", "openapi", "--source", "unknown"}, {"--format", "openapi", "--source"}} {
		var out, errOut bytes.Buffer
		if code := runContractExport(args, &out, &errOut); code == 0 || !strings.Contains(errOut.String(), "--source") {
			t.Fatalf("실행 전 인자 검사: %d %s", code, errOut.String())
		}
	}
	for _, command := range currentCommandSchema().Commands {
		if command.Name == "contract export" && (!command.Effects.Network || command.Options["output"].Required || len(command.Options["source"].Enum) != 2) {
			t.Fatalf("실행 부작용과 선택 항목을 설명해야 합니다: %+v", command)
		}
	}
}

func TestRuntimeOpenAPIExportRejectsUnsupportedCoreAndHTTPDisabled(t *testing.T) {
	for _, test := range []struct{ name, version, options, message string }{
		{"stable core", "v0.6.5", "boot.Options{HTTP:&boot.HTTPOptions{}}", "v0.7.0-preview.1"},
		{"HTTP disabled", currentAssemblySpineVersion, "boot.Options{}", "HTTP is disabled"},
	} {
		t.Run(test.name, func(t *testing.T) {
			inTempDir(t)
			writeE2EFile(t, "go.mod", "module example.com/runtime-api\n\ngo 1.25.5\n\nrequire github.com/NARUBROWN/spine "+test.version+"\n")
			writeE2EFile(t, "spine.toml", "module = \"example.com/runtime-api\"\nentrypoint = \".\"\ncomposition = \"application\"\nport = 8080\n")
			if err := os.MkdirAll("application", 0o755); err != nil {
				t.Fatal(err)
			}
			writeE2EFile(t, "application/app.go", "package application\nimport (\"github.com/NARUBROWN/spine\"; \"github.com/NARUBROWN/spine/pkg/boot\")\nfunc BuildApplication() (spine.App,boot.Options,error) { return spine.New(),"+test.options+",nil }\n")
			if output, err := exec.Command("go", "mod", "tidy").CombinedOutput(); err != nil {
				t.Fatalf("fixture modules: %v %s", err, output)
			}
			var out, errOut bytes.Buffer
			if code := runContractExport([]string{"--format", "openapi", "--source", "runtime"}, &out, &errOut); code == 0 || out.Len() != 0 || !strings.Contains(errOut.String(), test.message) {
				t.Fatalf("runtime failure was replaced with an export: %d %s %s", code, out.String(), errOut.String())
			}
			if files, _ := filepath.Glob(".spine-openapi-*"); len(files) != 0 {
				t.Fatalf("temporary exporter remained after failure: %v", files)
			}
		})
	}
}
