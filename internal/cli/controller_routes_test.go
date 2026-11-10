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
 type Option func(); type OptionSet struct{}; type Group struct{}
 func Options(...Option)OptionSet{return OptionSet{}}
 func Tags(...string)Option{return nil}; func Summary(string)Option{return nil};func Description(string)Option{return nil};func Interceptors(...any)Option{return nil}
 func (*Registry) Group(string,...Option)*Group{return nil}
 func (*Group) Group(string,...Option)*Group{return nil}
 func (*Group) GET(...any){}
 func (*Group) POST(...any){}
 func (*Registry) POST(string,any,...Option){}
 func (*Registry) GET(string,any,...Option){}
 `)
	write("core/pkg/input/input.go", "package input\ntype Body[T any] *T\ntype Query[T any] *T\ntype Path[T any] *T\ntype Header[T any] *T\n")
	write("core/pkg/httpx/httpx.go", "package httpx\ntype Response[T any] struct{Body T}\n")
	write("controller.go", `package typed
 import (
 "github.com/NARUBROWN/spine/pkg/api"
 "github.com/NARUBROWN/spine/pkg/input"
 "github.com/NARUBROWN/spine/pkg/httpx"
 )
 type DTO struct{Name string; UserID int}
 type PathDTO struct{ID int `+"`path:\"id\"`"+`}
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
  r.POST(endpoint,func(a Payload,p *PathDTO,q input.Query[DTO])(Result,error){return Result{Body:DTO{Name:a.Name}},nil})
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
 func registerUsers(c *UserController,r *api.Registry){r.POST(endpoint,func(a Payload,p *PathDTO)(Result,error){return Result{},nil})}
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

func TestStaticGroupedRoutes(t *testing.T) {
	inspector := controllerFixture(t, `func(c *UserController)RegisterRoutes(r *api.Registry){ admin:=r.Group("/admin",api.Tags("Admin")); users:=admin.Group("/users",api.Tags("Users")); users.GET("/",func(q input.Query[DTO])(Result,error){return Result{},nil},api.Summary("Find")) }`)
	inspector.inspectControllerRoutes()
	inspector.resolveHandlerSignatures()
	if len(inspector.contract.HTTP) != 1 || contractHasErrors(*inspector.contract) {
		t.Fatalf("routes=%+v diagnostics=%+v", inspector.contract.HTTP, inspector.contract.Diagnostics)
	}
	route := inspector.contract.HTTP[0]
	if route.Path != "/admin/users" || strings.Join(route.Tags, ",") != "Admin,Users" || route.Summary != "Find" {
		t.Fatalf("route=%+v", route)
	}
}

func TestStaticLegacyGroupedRouteOrderRemainsInspectable(t *testing.T) {
	inspector := controllerFixture(t, `func(c *UserController)RegisterRoutes(r *api.Registry){ users:=r.Group("/users",api.Tags("Users")); users.GET("/",api.Options(api.Summary("Find")),func()error{return nil}) }`)
	inspector.inspectControllerRoutes()
	if len(inspector.contract.HTTP) != 1 || contractHasErrors(*inspector.contract) {
		t.Fatalf("routes=%+v diagnostics=%+v", inspector.contract.HTTP, inspector.contract.Diagnostics)
	}
	route := inspector.contract.HTTP[0]
	if route.Path != "/users" || strings.Join(route.Tags, ",") != "Users" || route.Summary != "Find" {
		t.Fatalf("route=%+v", route)
	}
}

func TestStaticRegistryAndGroupShareRouteOptionOrder(t *testing.T) {
	inspector := controllerFixture(t, `var direct any
func(c *UserController)RegisterRoutes(r *api.Registry){
r.GET("/direct",func()error{return nil},api.Tags("Direct"),api.Summary("Find"),api.Interceptors(direct))
group:=r.Group("/group")
group.GET("/",func()error{return nil},api.Tags("Grouped"))
}`)
	inspector.inspectControllerRoutes()
	if len(inspector.contract.HTTP) != 2 || contractHasErrors(*inspector.contract) {
		t.Fatalf("routes=%+v diagnostics=%+v", inspector.contract.HTTP, inspector.contract.Diagnostics)
	}
	directRoute, groupedRoute := inspector.contract.HTTP[0], inspector.contract.HTTP[1]
	if directRoute.Path != "/direct" || strings.Join(directRoute.Tags, ",") != "Direct" || directRoute.Summary != "Find" || strings.Join(directRoute.Interceptors, ",") != "direct" {
		t.Fatalf("direct route=%+v", directRoute)
	}
	if groupedRoute.Path != "/group" || strings.Join(groupedRoute.Tags, ",") != "Grouped" {
		t.Fatalf("grouped route=%+v", groupedRoute)
	}
}

func TestStaticRouteOptionsRejectUnrelatedWithInterceptors(t *testing.T) {
	inspector := controllerFixture(t, `type localOptions struct{}
func(localOptions)WithInterceptors(...any)api.Option{return nil}
var local localOptions
func(c *UserController)RegisterRoutes(r *api.Registry){r.GET("/",func()error{return nil},local.WithInterceptors(c))}`)
	inspector.inspectControllerRoutes()
	for _, diagnostic := range inspector.contract.Diagnostics {
		if diagnostic.Code == "CONTROLLER_OPTIONS_UNRESOLVED" {
			return
		}
	}
	t.Fatalf("diagnostics=%+v", inspector.contract.Diagnostics)
}

func TestStaticGroupForwardingAndInterceptors(t *testing.T) {
	inspector := controllerFixture(t, `var outer,inner,local any
func(c *UserController)RegisterRoutes(r *api.Registry){ var users=r.Group("/users",api.Tags("Users"),api.Interceptors(outer)); register(users) }
func register(g *api.Group){ var alias=g; nested:=alias.Group("/admin",api.Interceptors(inner)); nested.GET("/",func()error{return nil},api.Interceptors(local)) }`)
	inspector.inspectControllerRoutes()
	if len(inspector.contract.HTTP) != 1 || contractHasErrors(*inspector.contract) {
		t.Fatalf("routes=%+v diagnostics=%+v", inspector.contract.HTTP, inspector.contract.Diagnostics)
	}
	route := inspector.contract.HTTP[0]
	if route.Path != "/users/admin" || strings.Join(route.Tags, ",") != "Users" || strings.Join(route.Interceptors, ",") != "outer,inner,local" {
		t.Fatalf("route=%+v", route)
	}
	if len(route.InterceptorSources) != 3 || !strings.HasPrefix(route.InterceptorSources[0], "controller.go:") || route.InterceptorSources[0] == route.InterceptorSources[1] || route.InterceptorSources[1] == route.InterceptorSources[2] {
		t.Fatalf("interceptor sources=%+v", route.InterceptorSources)
	}
}
func TestStaticGroupDynamicInterceptorDiagnosed(t *testing.T) {
	inspector := controllerFixture(t, `func factory()any{return nil};func(c *UserController)RegisterRoutes(r *api.Registry){ users:=r.Group("/users",api.Interceptors(factory())); users.GET("/",func()error{return nil}) }`)
	inspector.inspectControllerRoutes()
	for _, d := range inspector.contract.Diagnostics {
		if d.Code == "CONTROLLER_OPTIONS_UNRESOLVED" {
			return
		}
	}
	t.Fatalf("diagnostics=%+v", inspector.contract.Diagnostics)
}

func TestStaticGroupDynamicCompositeInterceptorDiagnosed(t *testing.T) {
	inspector := controllerFixture(t, `type Config struct{ Value any };func factory()any{panic("must not run")};func(c *UserController)RegisterRoutes(r *api.Registry){ users:=r.Group("/users",api.Interceptors(&Config{Value:factory()})); users.GET("/",func()error{return nil}) }`)
	inspector.inspectControllerRoutes()
	for _, d := range inspector.contract.Diagnostics {
		if d.Code == "CONTROLLER_OPTIONS_UNRESOLVED" {
			return
		}
	}
	t.Fatalf("diagnostics=%+v", inspector.contract.Diagnostics)
}

func TestStaticGroupForwardingWithCoreCandidate(t *testing.T) {
	core := os.Getenv("SPINE_CONTROLLER_CORE_SOURCE")
	if core == "" {
		t.Skip("SPINE_CONTROLLER_CORE_SOURCE is required")
	}
	core, err := filepath.Abs(core)
	if err != nil {
		t.Fatal(err)
	}
	inspector := controllerFixture(t, `
 type auth struct{}
 func(*auth)PreHandle(core.ExecutionContext,core.HandlerMeta)error{return nil}
 func(*auth)PostHandle(core.ExecutionContext,core.HandlerMeta){}
 func(*auth)BeforeResponse(core.ExecutionContext,core.HandlerMeta,error)error{return nil}
 func(*auth)AfterCompletion(core.ExecutionContext,core.HandlerMeta,error){}
 var outer,inner,local = &auth{},&auth{},&auth{}
 func(c *UserController)RegisterRoutes(r *api.Registry){ var users=r.Group("/users",api.Tags("Users"),api.Interceptors(outer)); register(users) }
	func register(g *api.Group){var alias=g;nested:=alias.Group("/admin",api.Interceptors(inner));nested.GET("/",func(q input.Query[DTO])(Result,error){return Result{},nil},route.WithInterceptors(local))}
 `)
	source, err := os.ReadFile("controller.go")
	if err != nil {
		t.Fatal(err)
	}
	source = []byte(strings.Replace(string(source), `"github.com/NARUBROWN/spine/pkg/api"`, `"github.com/NARUBROWN/spine/pkg/api"`+"\n"+`"github.com/NARUBROWN/spine/core"`+"\n"+`"github.com/NARUBROWN/spine/pkg/route"`, 1))
	if err = os.WriteFile("controller.go", source, 0644); err != nil {
		t.Fatal(err)
	}
	module, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	module = []byte(strings.Replace(string(module), "=> ./core", "=> "+core, 1))
	if err = os.WriteFile("go.mod", module, 0644); err != nil {
		t.Fatal(err)
	}
	sources, _, err := readLayoutSources()
	if err != nil {
		t.Fatal(err)
	}
	inspector = newContractInspector(projectConfig{Layout: layeredLayout}, "example.com/typed", sources)
	inspector.contract.Constructors = []contractRegistration{{Target: "example.com/typed.NewUserController"}}
	inspector.contract.HTTPAdapter.SelectedModules = []selectedModule{{Path: spineImportPath, Version: "v0.7.0-preview.5"}}
	inspector.inspectControllerRoutes()
	if len(inspector.contract.HTTP) != 1 || contractHasErrors(*inspector.contract) {
		t.Fatalf("routes=%+v diagnostics=%+v", inspector.contract.HTTP, inspector.contract.Diagnostics)
	}
	route := inspector.contract.HTTP[0]
	if route.Path != "/users/admin" || strings.Join(route.Tags, ",") != "Users" || strings.Join(route.Interceptors, ",") != "outer,inner,local" {
		t.Fatalf("route=%+v", route)
	}
}

func TestStaticGroupRecursiveAndDeferredAreDiagnosed(t *testing.T) {
	cases := []struct{ name, body, code string }{
		{"recursive", `func(c *UserController)RegisterRoutes(r *api.Registry){register(r.Group("/users"))};func register(g *api.Group){register(g)}`, "CONTROLLER_REGISTRATION_RECURSIVE"},
		{"deferred", `func(c *UserController)RegisterRoutes(r *api.Registry){g:=r.Group("/users");f:=func(){g.GET("/",func()error{return nil})};f()}`, "CONTROLLER_REGISTRATION_DEFERRED_UNRESOLVED"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			inspector := controllerFixture(t, test.body)
			inspector.inspectControllerRoutes()
			for _, d := range inspector.contract.Diagnostics {
				if d.Code == test.code {
					return
				}
			}
			t.Fatalf("diagnostics=%+v", inspector.contract.Diagnostics)
		})
	}
}

func TestStaticGroupInlineForwarding(t *testing.T) {
	inspector := controllerFixture(t, `var guard any
func(c *UserController)RegisterRoutes(r *api.Registry){register(r.Group("/v1",api.Tags("API")).Group("/users",api.Interceptors(guard)))}
func register(g *api.Group){g.GET("/",func()error{return nil})}`)
	inspector.inspectControllerRoutes()
	if len(inspector.contract.HTTP) != 1 || contractHasErrors(*inspector.contract) {
		t.Fatalf("routes=%+v diagnostics=%+v", inspector.contract.HTTP, inspector.contract.Diagnostics)
	}
	route := inspector.contract.HTTP[0]
	if route.Path != "/v1/users" || strings.Join(route.Tags, ",") != "API" || strings.Join(route.Interceptors, ",") != "guard" {
		t.Fatalf("route=%+v", route)
	}
}

func TestStaticGroupDynamicArgumentIsDiagnosed(t *testing.T) {
	inspector := controllerFixture(t, `func dynamic()*api.Group{return nil};func(c *UserController)RegisterRoutes(r *api.Registry){register(dynamic())};func register(g *api.Group){g.GET("/",func()error{return nil})}`)
	inspector.inspectControllerRoutes()
	for _, d := range inspector.contract.Diagnostics {
		if d.Code == "CONTROLLER_GROUP_VALUE_UNRESOLVED" {
			return
		}
	}
	t.Fatalf("diagnostics=%+v", inspector.contract.Diagnostics)
}
