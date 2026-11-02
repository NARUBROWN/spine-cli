package cli

import (
	_ "embed"
	"strings"
)

//go:embed first_api.md
var firstAPIGuide string

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
	platform "github.com/NARUBROWN/spine-echo-platform"
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
	app := platform.New()
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

var projectReadme = `# Spine application

Generated with Spine CLI.

Minimum Go version: 1.25.5.

기본 new는 Echo 플랫폼 설치와 빌드까지 마쳤습니다. --skip-install로 생성했다면 먼저 go mod tidy를 실행하세요.

## Run

    spine dev
    curl http://localhost:8080/health

## Update dependencies

    spine update --dry-run
    spine update

Spine과 Echo 플랫폼을 현재 CLI의 검증된 버전 조합으로 갱신합니다.
임시 복사본에서 정적 계약과 빌드를 검사한 뒤 go.mod와 go.sum을 반영합니다.
업무 소스와 CLI 실행 파일은 이 명령으로 갱신하지 않습니다.

## Generate a resource

    spine g domain account/user
    spine g repository account/user
    spine g service user --depends src/repository/account:UserRepository
    spine g controller admin/user --depends src/service:UserService

domain은 database/domain, repository는 src/repository에 생성합니다.
DTO, service, controller는 각각 src/dto, src/service, src/controller에 생성합니다.
이름 앞에 선택적인 하위 경로를 붙일 수 있으며, 각 역할에서 독립적으로 그룹을 정합니다.
controller 생성만으로 HTTP 경로가 등록되지는 않습니다.
기존 핸들러를 등록하려면 spine g route 명령에 method, path, handler를 명시합니다.
직접 생성하거나 --dry-run으로 변경을 먼저 확인할 수 있습니다. 자동화에서는 --dry-run --json과 --if-match로 검토한 계획을 적용합니다.

Module: {{MODULE}}
` + generatedFirstAPIGuide()

const legacyProjectReadme = `# Spine application

이 프로젝트는 layout = "legacy"를 사용합니다. 기본 new는 플랫폼 설치를 완료했습니다.
--skip-install로 생성한 경우에만 먼저 go mod tidy를 실행합니다.

    spine dev
    curl http://localhost:8080/health

의존성을 갱신하려면 spine update --dry-run으로 변경을 확인한 뒤 spine update를 실행합니다.
현재 CLI의 Echo 레시피를 사용하며, 시작 코드의 API 변경은 직접 적용해야 합니다.

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
 platform "github.com/NARUBROWN/spine-echo-platform"
 "github.com/NARUBROWN/spine/pkg/boot"
)

// BuildApplication은 생성자와 라우트를 연결하고 실행 옵션을 구성합니다.
func BuildApplication() (spine.App, boot.Options, error) {
 app := platform.New()
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

func generatedFirstAPIGuide() string {
	_, guide, _ := strings.Cut(firstAPIGuide, "<!-- existing-project -->")
	return "\n# 첫 HTTP API 만들기\n\n이름과 수량을 검사하고 JSON을 반환하는 POST /products 예제입니다.\n" + strings.ReplaceAll(guide, "example.com/shop", "{{MODULE}}")
}
