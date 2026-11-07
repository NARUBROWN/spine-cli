package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

func TestComponentMethodConstructorsStaticContracts(t *testing.T) {
	base := `func(c *UserController)RegisterRoutes(r *api.Registry){r.POST(endpoint,func(a input.Body[DTO])httpx.Response[DTO]{return httpx.Response[DTO]{Body:DTO{Name:a.Name}}})}`
	for _, tc := range []struct{ name, constructor, role, target, code string }{
		{"valid", `func(*UserController)Constructor()*UserController{panic("must never run")}`, "Controller", "UserController", ""},
		{"alias", `type Alias=UserController;func(*UserController)Constructor()*Alias{panic("must never run")}`, "Controller", "Alias", ""},
		{"service is not HTTP", `func(*UserController)Constructor()*UserController{return nil}`, "Service", "UserController", ""},
		{"missing", ``, "Controller", "UserController", "COMPONENT_CONSTRUCTOR_INVALID"},
		{"wrong self", `func(*UserController)Constructor()*DTO{return nil}`, "Controller", "UserController", "COMPONENT_CONSTRUCTOR_INVALID"},
		{"variadic", `func(*UserController)Constructor(...string)*UserController{return nil}`, "Controller", "UserController", "COMPONENT_CONSTRUCTOR_INVALID"},
		{"missing dependency", `func(*UserController)Constructor(*DTO)*UserController{return nil}`, "Controller", "UserController", "COMPONENT_DEPENDENCIES_INVALID"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inspector := controllerFixture(t, base+"\n"+tc.constructor)
			inspector.contract.Constructors = []contractRegistration{{Target: "example.com/typed." + tc.target + ".Constructor", Role: tc.role, object: true}}
			inspector.inspectControllerRoutes()
			found := false
			for _, d := range inspector.contract.Diagnostics {
				if d.Code == tc.code {
					found = true
				}
				if tc.code == "" && d.Severity == "error" {
					t.Fatalf("%+v", d)
				}
			}
			if tc.code != "" && !found {
				t.Fatalf("missing %s: %+v", tc.code, inspector.contract.Diagnostics)
			}
			if tc.code == "" {
				expected := 1
				if tc.role == "Service" {
					expected = 0
				}
				if len(inspector.contract.HTTP) != expected {
					t.Fatalf("routes=%+v", inspector.contract.HTTP)
				}
				if inspector.contract.Constructors[0].Output != "*example.com/typed:UserController" {
					t.Fatal(inspector.contract.Constructors[0])
				}
			}
		})
	}
}

func TestRolePrototypeRegistrationExpressions(t *testing.T) {
	source := `package application
 import (
 repo "example.com/test/src/repository"
 makers "github.com/NARUBROWN/spine/pkg/constructor"
 )
 func Build(){app:=unknown();app.Constructor(makers.Repository(&repo.UserRepository{}),makers.Service(new(LocalService)),makers.Controller((*LocalController)(nil)),makers.Custom(&Custom{}),makers.Custom(NewDatabase),makers.Interceptor(&Tx{}))}`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "internal/application/application.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	inspector := newContractInspector(projectConfig{Layout: layeredLayout}, "example.com/test", nil)
	inspector.inspectFile(file, fset, file.Decls[len(file.Decls)-1].(*ast.FuncDecl).Body, "app")
	roles := []string{"Repository", "Service", "Controller", "Custom", "Custom", "Interceptor"}
	if len(inspector.contract.Constructors) != len(roles) {
		t.Fatal(inspector.contract.Constructors)
	}
	for n, p := range inspector.contract.Constructors {
		if p.Role != roles[n] || p.object != (n != 4) || (p.object && !strings.HasSuffix(p.Target, ".Constructor")) {
			t.Fatalf("%+v", p)
		}
	}
}

func TestCustomFactoryRoleContracts(t *testing.T) {
	for _, tc := range []struct{ name, body, factory, code string }{
		{"custom is not HTTP", "", "NewUserController", ""},
		{"missing dependency", `func NewBroken(*DTO)*UserController{panic("must not run")}`, "NewBroken", "COMPONENT_DEPENDENCIES_INVALID"},
		{"invalid result", `func NewBroken()(*UserController,error){panic("must not run")}`, "NewBroken", "COMPONENT_CONSTRUCTOR_INVALID"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inspector := controllerFixture(t, `func(c *UserController)RegisterRoutes(r *api.Registry){r.POST(endpoint,func(a input.Body[DTO])httpx.Response[DTO]{return httpx.Response[DTO]{Body:DTO{Name:a.Name}}})}`+"\n"+tc.body)
			inspector.contract.Constructors = []contractRegistration{{Target: "example.com/typed." + tc.factory, Role: "Custom"}}
			inspector.inspectControllerRoutes()
			if len(inspector.contract.HTTP) != 0 {
				t.Fatal("custom factory leaked HTTP routes", inspector.contract.HTTP)
			}
			found := false
			for _, diagnostic := range inspector.contract.Diagnostics {
				if tc.code == "" && diagnostic.Severity == "error" {
					t.Fatal(diagnostic)
				}
				found = found || diagnostic.Code == tc.code
			}
			if tc.code != "" && !found {
				t.Fatalf("missing %s: %+v", tc.code, inspector.contract.Diagnostics)
			}
		})
	}
}

func TestRoleMarkersUseCanonicalImport(t *testing.T) {
	for _, tc := range []struct {
		name, imports, body string
		marked              bool
	}{
		{"alias", `import c "github.com/NARUBROWN/spine/pkg/constructor"`, `app.Constructor(c.Controller(&Local{}))`, true},
		{"different package", `import c "example.com/constructor"`, `app.Constructor(c.Controller(&Local{}))`, false},
		{"shadowed alias", `import c "github.com/NARUBROWN/spine/pkg/constructor"`, `c:=unknown();app.Constructor(c.Controller(&Local{}))`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, "internal/application/application.go", "package application\n"+tc.imports+"\nfunc Build(){app:=unknown();"+tc.body+"}", 0)
			if err != nil {
				t.Fatal(err)
			}
			inspector := newContractInspector(projectConfig{Layout: layeredLayout}, "example.com/test", nil)
			inspector.inspectFile(file, fset, file.Decls[len(file.Decls)-1].(*ast.FuncDecl).Body, "app")
			if len(inspector.contract.Constructors) != 1 || (inspector.contract.Constructors[0].Role == "Controller") != tc.marked {
				t.Fatal(inspector.contract.Constructors)
			}
		})
	}
}

func TestComponentConcreteDependencyRequiresExactProvider(t *testing.T) {
	inspector := controllerFixture(t, `func(*UserController)Constructor(*struct{Name string;UserID int})*UserController{return nil}
 func(*UserController)RegisterRoutes(r *api.Registry){r.POST(endpoint,func(a input.Body[DTO])httpx.Response[DTO]{return httpx.Response[DTO]{Body:DTO{Name:a.Name}}})}
 func NewDTO()*DTO{return nil}`)
	inspector.contract.Constructors = []contractRegistration{{Target: "example.com/typed.UserController.Constructor", Role: "Controller", object: true}, {Target: "example.com/typed.NewDTO"}}
	inspector.inspectControllerRoutes()
	for _, d := range inspector.contract.Diagnostics {
		if d.Code == "COMPONENT_DEPENDENCIES_INVALID" {
			return
		}
	}
	t.Fatalf("assignable concrete type incorrectly replaced an exact provider: %+v", inspector.contract.Diagnostics)
}

func TestRoleContractsRequireAvailableCoreTypes(t *testing.T) {
	inspector := controllerFixture(t, "")
	if err := os.WriteFile("controller.go", []byte("package typed\ntype Settings struct{}\nfunc NewSettings()*Settings{panic(\"must not run\")}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	sources, _, err := readLayoutSources()
	if err != nil {
		t.Fatal(err)
	}
	inspector.sources = sources
	inspector.contract.Constructors = []contractRegistration{{Target: "example.com/typed.NewSettings", Role: "Custom"}}
	inspector.inspectControllerRoutes()
	for _, diagnostic := range inspector.contract.Diagnostics {
		if diagnostic.Code == "CONTROLLER_TYPECHECK_UNRESOLVED" {
			return
		}
	}
	t.Fatal("role contract was accepted without loaded core types", inspector.contract.Diagnostics)
}
