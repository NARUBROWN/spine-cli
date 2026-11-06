package cli

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func controllerFixture(t *testing.T, body string) *contractInspector {
	t.Helper()
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("GOWORK", "off")
	write := func(name, source string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(name), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(source), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/typed\n\ngo 1.25.5\nrequire github.com/NARUBROWN/spine v0.7.0-preview.5\nreplace github.com/NARUBROWN/spine => ./core\n")
	write("core/go.mod", "module github.com/NARUBROWN/spine\n\ngo 1.25.5\n")
	write("core/pkg/api/api.go", `package api
 type Registry struct{}
 type Controller interface{ RegisterRoutes(*Registry) }
 func (*Registry) POST(string,any,...any){}
 func (*Registry) GET(string,any,...any){}
 `)
	write("core/pkg/input/input.go", "package input\ntype Body[T any] *T\ntype Query[T any] *T\ntype Path[T any] *T\n")
	write("core/pkg/httpx/httpx.go", "package httpx\ntype Response[T any] struct{Body T}\n")
	write("controller.go", `package typed
 import (
 "github.com/NARUBROWN/spine/pkg/api"
 "github.com/NARUBROWN/spine/pkg/input"
 "github.com/NARUBROWN/spine/pkg/httpx"
 )
 type DTO struct{Name string; UserID int}
 type PathDTO struct{ID int}
 type Payload = input.Body[DTO]
 type Result = httpx.Response[DTO]
 type UserController struct{}
 func NewUserController()*UserController{panic("constructor must never run")}
 func init(){panic("static inspection must never run init")}
 const endpoint = "/users"+"/:id"
 `+body)
	sources, _, err := readLayoutSources()
	if err != nil {
		t.Fatal(err)
	}
	inspector := newContractInspector(projectConfig{Layout: layeredLayout}, "example.com/typed", sources)
	inspector.contract.Constructors = []contractRegistration{{Target: "example.com/typed.NewUserController"}}
	inspector.contract.HTTPAdapter.SelectedModules = []selectedModule{{Path: spineImportPath, Version: "v0.7.0-preview.5"}}
	return inspector
}

func TestControllerRoutesResolveTypedContractAndHelpersWithoutExecution(t *testing.T) {
	inspector := controllerFixture(t, `
 func(c *UserController)RegisterRoutes(r *api.Registry){c.users(r)}
 func(c *UserController)users(r *api.Registry){
  r.POST(endpoint,func(a Payload,p input.Path[PathDTO],q input.Query[DTO])(Result,error){return Result{Body:DTO{Name:a.Name}},nil})
 }
 `)
	inspector.inspectControllerRoutes()
	inspector.resolveHandlerSignatures()
	if len(inspector.contract.HTTP) != 1 {
		t.Fatalf("routes=%+v diagnostics=%+v", inspector.contract.HTTP, inspector.contract.Diagnostics)
	}
	for _, diagnostic := range inspector.contract.Diagnostics {
		if diagnostic.Severity == "error" {
			t.Fatalf("%+v", diagnostic)
		}
	}
	route := inspector.contract.HTTP[0]
	if route.Path != "/users/:id" || len(route.Parameters) != 3 || route.Parameters[0].Binding != "body" || route.Parameters[1].Keys[0] != "id" || strings.Join(route.Parameters[2].Keys, ",") != "name,userId" {
		t.Fatalf("%+v", route)
	}
	if !strings.HasPrefix(operationID(route), "post_") {
		t.Fatal(operationID(route))
	}
}

func TestControllerRoutesRejectDynamicAndRecursiveRegistration(t *testing.T) {
	cases := []struct{ name, body, code string }{
		{"recursive", `func(c *UserController) RegisterRoutes(r *api.Registry){c.RegisterRoutes(r)}`, "CONTROLLER_REGISTRATION_RECURSIVE"},
		{"dynamic-path", `func(c *UserController) RegisterRoutes(r *api.Registry){r.POST(dynamic(),func(a input.Body[DTO])httpx.Response[DTO]{return httpx.Response[DTO]{}})};func dynamic()string{return "/users"}`, "CONTROLLER_ROUTE_DYNAMIC"},
		{"scalar", `func(c *UserController) RegisterRoutes(r *api.Registry){r.POST("/users",func(input.Body[int])error{return nil})}`, "HTTP_INPUT_CONTRACT_INVALID"},
		{"deferred", `func(c *UserController) RegisterRoutes(r *api.Registry){run:=func(){r.GET("/users",func()error{return nil})};run()}`, "CONTROLLER_REGISTRATION_DEFERRED_UNRESOLVED"},
		{"asynchronous", `func(c *UserController) RegisterRoutes(r *api.Registry){go r.GET("/users",func()error{return nil})}`, "CONTROLLER_ROUTE_CONDITIONAL"},
		{"registry-reassigned", `func(c *UserController) RegisterRoutes(r *api.Registry){r=new(api.Registry);r.GET("/users",func()error{return nil})}`, "CONTROLLER_REGISTRY_VALUE_UNRESOLVED"},
		{"unsupported-field", `type Bad struct{Value map[string]string};func(c *UserController) RegisterRoutes(r *api.Registry){r.GET("/users",func(input.Query[Bad])error{return nil})}`, "HTTP_INPUT_CONTRACT_INVALID"},
		{"abstract-provider", `func NewAbstract()api.Controller{return &UserController{}};func(c *UserController)RegisterRoutes(r *api.Registry){}`, "HTTP_CONTROLLER_PROVIDER_ABSTRACT"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			inspector := controllerFixture(t, test.body)
			if test.name == "abstract-provider" {
				inspector.contract.Constructors = []contractRegistration{{Target: "example.com/typed.NewAbstract"}}
			}
			inspector.inspectControllerRoutes()
			for _, diagnostic := range inspector.contract.Diagnostics {
				if diagnostic.Code == test.code {
					return
				}
			}
			t.Fatalf("missing %s: %+v", test.code, inspector.contract.Diagnostics)
		})
	}
}

func TestControllerRoutesTrackRegistryVariableAndPackageHelper(t *testing.T) {
	inspector := controllerFixture(t, `
 func(c *UserController)RegisterRoutes(r *api.Registry){var alias=r;registerUsers(c,alias)}
 func registerUsers(c *UserController,r *api.Registry){r.POST(endpoint,func(a Payload,p input.Path[PathDTO])(Result,error){return Result{},nil})}
 `)
	inspector.inspectControllerRoutes()
	inspector.resolveHandlerSignatures()
	if len(inspector.contract.HTTP) != 1 || contractHasErrors(*inspector.contract) {
		t.Fatalf("%+v", inspector.contract)
	}
}

func TestControllerScaffoldVersionCompatibility(t *testing.T) {
	for _, version := range []string{"v0.7.0-preview.4", "v0.7.0-preview.5"} {
		options := newProjectOptions{Module: "example.com/typed", Layout: layeredLayout, Platform: "echo", Validator: "none", Modules: []selectedModule{{Path: spineImportPath, Version: version}}}
		files, err := newProjectFiles(options)
		if err != nil {
			t.Fatal(err)
		}
		_, routeFile := files["src/route/route.go"]
		modern := controllerRoutesVersion(version)
		if routeFile == modern || strings.Contains(files["src/controller/health_controller.go"], "RegisterRoutes") != modern || strings.Contains(files["internal/application/application.go"], "route.Register(app)") == modern {
			t.Fatalf("version %s files=%v", version, files)
		}
	}
}

func TestControllerInputNameConformance(t *testing.T) {
	data, err := os.ReadFile("testdata/input-conformance.json")
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprintf("%x", sha256.Sum256(data)) != "4bbe6523c7a8276ba1df20f02717c392c0181001b5c2903da8381eb0c212c294" {
		t.Fatal("update the shared core and CLI conformance fixture together")
	}
	var fixture struct {
		Fields []struct {
			Go   string `json:"go"`
			Wire string `json:"wire"`
		}
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, field := range fixture.Fields {
		if got := controllerLowerCamel(field.Go); got != field.Wire {
			t.Errorf("%s: %s != %s", field.Go, got, field.Wire)
		}
	}
}
