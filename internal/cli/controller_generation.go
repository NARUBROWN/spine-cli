package cli

import (
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"
	"os"
)

const controllerRoutesMinimum = "v0.7.0-preview.5"

func controllerRoutesVersion(version string) bool {
	return semver.IsValid(version) && semver.Compare(version, controllerRoutesMinimum) >= 0
}
func controllerRoutesProject() bool {
	data, err := os.ReadFile("go.mod")
	if err != nil {
		return false
	}
	file, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return false
	}
	for _, require := range file.Require {
		if require.Mod.Path == spineImportPath {
			return controllerRoutesVersion(require.Mod.Version)
		}
	}
	return false
}
func controllerRoutesScaffold(options newProjectOptions) bool {
	return options.Layout == layeredLayout && controllerRoutesVersion(selectedModuleVersion(options.Modules, spineImportPath))
}

const registeredHealthController = `package controller

import (
 "github.com/NARUBROWN/spine/pkg/api"
 "github.com/NARUBROWN/spine/pkg/httpx"
)

type HealthController struct{}
func NewHealthController() *HealthController {return &HealthController{}}
var _ api.Controller = (*HealthController)(nil)
func (c *HealthController) RegisterRoutes(r *api.Registry) {
 r.GET("/health",func() httpx.Response[map[string]any] {
  return httpx.Response[map[string]any]{Body:map[string]any{"status":"ok"}}
 })
}
`

func controllerRouteSchema() map[string]any {
	return map[string]any{
		"minimum_core":           controllerRoutesMinimum,
		"interface":              spineImportPath + "/pkg/api.Controller",
		"method":                 "RegisterRoutes(*api.Registry)",
		"route_root":             "src/controller",
		"legacy_route_root":      layeredRoutePath,
		"inputs":                 []string{"input.Path[T]", "input.Query[T]", "input.Body[T]"},
		"collection":             "once per prepared app, after DI resolution",
		"unprepared_description": "ErrRoutesNotCollected; no automatic Prepare",
	}
}

func projectGenerationRoles() []generationRole {
	roles := generationRoles()
	if controllerRoutesProject() {
		for index := range roles {
			if roles[index].Name == "route" {
				roles[index].Root = "src/controller"
			}
		}
	}
	return roles
}

func registeredFirstAPIGuide() string {
	return "\n## 첫 API 작성\n\n`spine g controller product --dry-run --json`으로 생성 계획을 확인하고 `--if-match <base_hash>`로 적용합니다. 생성자는 앱에 등록되며 컨트롤러는 `RegisterRoutes(*api.Registry)`를 구현합니다. 별도 route.go는 생성하지 않습니다.\n\n컨트롤러 파일에 요청 DTO를 선언하고 등록 메서드에 콜백을 작성합니다. `api`, `httpx`, `input`은 각각 `github.com/NARUBROWN/spine/pkg/` 아래 패키지입니다.\n\n```go\ntype CreateProduct struct { Name string `json:\"name\"` }\n\nfunc (c *ProductController) RegisterRoutes(r *api.Registry) {\n    r.POST(\"/products\", func(a input.Body[CreateProduct]) httpx.Response[CreateProduct] {\n        return httpx.Response[CreateProduct]{Body: CreateProduct{Name: a.Name}}\n    })\n}\n```\n\n`a.Name`으로 필드에 직접 접근합니다. Path와 Query도 구조체를 받으며 태그가 없으면 lowerCamel 이름을 사용합니다. 기존 핸들러 메서드를 등록하려면 `spine g route`가 같은 컨트롤러에 typed callback을 추가합니다. 변경 전 dry-run과 hash 확인 절차는 동일합니다.\n\n`spine contract check --json`, `spine verify --json`으로 정적 계약과 로컬 Go 검사를 수행하세요. 실제 HTTP 요청과 DB, 브로커 동작은 별도로 확인합니다.\n"
}
