package cli

import (
	"encoding/json"
	"fmt"
	"go/format"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func runNew(args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(errOut, "project name is required")
		return 1
	}
	name, err := parseResourceName(args[0])
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	module := "github.com/your-org/" + name.Route
	options := writeOptions{}
	jsonOutput := false
	layout := layeredLayout
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--layout":
			if i+1 >= len(args) {
				fmt.Fprintln(errOut, "--layout requires a layout")
				return 1
			}
			i++
			layout = args[i]
			if layout != layeredLayout && layout != "legacy" {
				fmt.Fprintln(errOut, "unsupported layout: "+layout)
				return 1
			}
		case "--module":
			if i+1 >= len(args) {
				fmt.Fprintln(errOut, "--module requires a module path")
				return 1
			}
			i++
			module = args[i]
		case "--dry-run":
			options.DryRun = true
		case "--force":
			options.Force = true
		case "--if-match":
			if i+1 >= len(args) {
				fmt.Fprintln(errOut, "--if-match requires a base hash")
				return 1
			}
			i++
			options.IfMatch = args[i]
		case "--json":
			jsonOutput = true
		default:
			fmt.Fprintf(errOut, "unknown option: %s\n", args[i])
			return 1
		}
	}
	dir := filepath.Join(".", name.Route)
	if _, err := os.Stat(dir); err == nil && !options.Force {
		fmt.Fprintf(errOut, "destination already exists: %s\n", dir)
		return 1
	} else if err != nil && !os.IsNotExist(err) {
		fmt.Fprintln(errOut, err)
		return 1
	}

	files := map[string]string{
		"go.mod":  fmt.Sprintf("module %s\n\ngo 1.25.5\n\nrequire github.com/NARUBROWN/spine v0.5.1\n", module),
		"main.go": strings.ReplaceAll(projectMain, "{{MODULE}}", module),
		filepath.Join("internal", "application", "application.go"):      projectApplication,
		filepath.Join("internal", "application", "application_test.go"): projectApplicationTest,
		"README.md":  strings.ReplaceAll(projectReadme, "{{MODULE}}", module),
		"spine.toml": projectConfigTemplate(defaultProjectConfig(module)),
		".gitignore": ".env\nbin/\ncoverage.out\n",
	}
	if layout == layeredLayout {
		files[filepath.Join("internal", "application", "application.go")] = strings.ReplaceAll(layeredProjectApplication, "{{MODULE}}", module)
		files[filepath.Join("src", "controller", "health_controller.go")] = layeredHealthController
		files[filepath.Join("src", "route", "route.go")] = strings.ReplaceAll(layeredHealthRoute, "{{MODULE}}", module)
	} else {
		config := defaultProjectConfig(module)
		config.Layout = "legacy"
		files["spine.toml"] = projectConfigTemplate(config)
		files["README.md"] = strings.ReplaceAll(legacyProjectReadme, "{{MODULE}}", module)
	}
	for path, source := range files {
		if strings.HasSuffix(path, ".go") {
			formatted, err := format.Source([]byte(source))
			if err != nil {
				fmt.Fprintln(errOut, err)
				return 1
			}
			files[path] = string(formatted)
		}
	}
	paths := []string{"go.mod", "main.go", filepath.Join("internal", "application", "application.go"), filepath.Join("internal", "application", "application_test.go"), "README.md", "spine.toml", ".gitignore"}
	if layout == layeredLayout {
		paths = append(paths, filepath.Join("src", "controller", "health_controller.go"), filepath.Join("src", "route", "route.go"))
	}
	changes := make([]fileChange, 0, len(paths))
	for _, path := range paths {
		changes = append(changes, fileChange{Path: filepath.Join(dir, path), Contents: files[path], AllowOverwrite: options.Force})
	}
	diff := ""
	baseHash, err := fileChangesBaseHash(changes)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	if options.IfMatch != "" && options.IfMatch != baseHash {
		fmt.Fprintf(errOut, "generation targets changed since dry-run: expected base hash %s, current %s\n", options.IfMatch, baseHash)
		return 1
	}
	plan, err := planFileChanges(changes)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	if options.DryRun {
		diff, err = renderFileChanges(changes)
		if err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
	}
	if err := applyFileChanges(changes, options.DryRun); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	if jsonOutput {
		_ = json.NewEncoder(out).Encode(map[string]any{"project": name.Route, "module": module, "layout": layout, "directory": dir, "files": paths, "dry_run": options.DryRun, "base_hash": baseHash, "changes": plan, "diff": diff})
		return 0
	}
	if options.DryRun {
		fmt.Fprintf(out, "Would create Spine application in %s\n%s", dir, diff)
		return 0
	}
	fmt.Fprintf(out, "Created Spine application in %s\n\nNext steps:\n  cd %s\n  go mod tidy\n  go run .\n", dir, name.Route)
	return 0
}

const projectMain = `package main

import (
	"log"

	"{{MODULE}}/internal/application"
)

func main() {
	app, options, err := application.BuildApplication()
	if err != nil {
		log.Fatal(err)
	}
	if err := app.Validate(options); err != nil {
		log.Fatal(err)
	}
	if err := app.Run(options); err != nil {
		log.Fatal(err)
	}
}
`

const projectApplication = `package application

import (
	"os"

	"github.com/NARUBROWN/spine"
	"github.com/NARUBROWN/spine/pkg/boot"
	"github.com/NARUBROWN/spine/pkg/httpx"
)

type HealthController struct{}

func NewHealthController() *HealthController { return &HealthController{} }

func (c *HealthController) Get() httpx.Response[map[string]string] {
	return httpx.Response[map[string]string]{Body: map[string]string{"status": "ok"}}
}

// BuildApplication은 서버 실행, 계약 검사와 오프라인 검증이 함께 사용하는 composition root입니다.
func BuildApplication() (spine.App, boot.Options, error) {
	app := spine.New()
	app.Constructor(NewHealthController)
	app.Route("GET", "/health", (*HealthController).Get)

	address := ":8080"
	if port := os.Getenv("SPINE_PORT"); port != "" {
		address = ":" + port
	}
	return app, boot.Options{Address: address, HTTP: &boot.HTTPOptions{}}, nil
}
`

const projectApplicationTest = `package application

import "testing"

func TestBuildApplicationIsValid(t *testing.T) {
	app, options, err := BuildApplication()
	if err != nil {
		t.Fatalf("generated application registration is invalid: %v", err)
	}
	if err := app.Validate(options); err != nil {
		t.Fatalf("generated application is invalid: %v", err)
	}
}
`

const projectReadme = `# Spine application

Generated with Spine CLI.

Recommended Go version: 1.27.3.

## Run

    go mod tidy
    spine dev
    curl http://localhost:8080/health

## Generate a resource

    spine g domain account/user
    spine g repository account/user
    spine g service user --depends database/repository/account:UserRepository
    spine g controller admin/user --depends src/service:UserService

domain은 database/domain, repository는 database/repository에 생성합니다.
DTO, service, controller는 각각 src/dto, src/service, src/controller에 생성합니다.
이름 앞에 선택적인 하위 경로를 붙일 수 있으며, 각 역할에서 독립적으로 그룹을 정합니다.
controller 생성만으로 HTTP 경로가 등록되지는 않습니다.
기존 핸들러를 등록하려면 spine g route 명령에 method, path, handler를 명시합니다.
생성 전에 --dry-run --json으로 계획을 확인하고 --if-match로 같은 계획을 적용합니다.

Module: {{MODULE}}
`

const legacyProjectReadme = `# Spine application

이 프로젝트는 layout = "legacy"를 사용합니다.

    go mod tidy
    spine dev
    curl http://localhost:8080/health

기존 생성기 규칙에 따라 resource는 src/<name>에 생성하고 GET 경로를 등록합니다.
개별 구성 요소의 --depends는 같은 패키지의 파일 이름을 받습니다.

    spine g resource users --dry-run --json
    spine g resource users --if-match <base_hash> --json

Module: {{MODULE}}
`

const layeredProjectApplication = `package application

import (
 "os"
 controller "{{MODULE}}/src/controller"
 route "{{MODULE}}/src/route"
 "github.com/NARUBROWN/spine"
 "github.com/NARUBROWN/spine/pkg/boot"
)

// BuildApplication은 생성자와 라우트를 연결하고 실행 옵션을 구성합니다.
func BuildApplication() (spine.App, boot.Options, error) {
 app := spine.New()
 app.Constructor(controller.NewHealthController)
 if err := route.Register(app); err != nil { return nil, boot.Options{}, err }
 address := ":8080"
 if port := os.Getenv("SPINE_PORT"); port != "" { address = ":" + port }
 return app, boot.Options{Address: address, HTTP: &boot.HTTPOptions{}}, nil
}
`

const layeredHealthController = `package controller

import "github.com/NARUBROWN/spine/pkg/httpx"

type HealthController struct{}
func NewHealthController() *HealthController { return &HealthController{} }
func (c *HealthController) Get() httpx.Response[map[string]string] {
 return httpx.Response[map[string]string]{Body: map[string]string{"status":"ok"}}
}
`

const layeredHealthRoute = `package route

import (
 "github.com/NARUBROWN/spine"
 controller "{{MODULE}}/src/controller"
)

func Register(app spine.App) error {
 app.Route("GET", "/health", (*controller.HealthController).Get)
 return nil
}
`
