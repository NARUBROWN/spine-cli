package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
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
 import repo "example.com/test/src/repository"
 func Build(){app:=unknown();app.RepositoryConstructor(&repo.UserRepository{});app.ServiceConstructor(new(LocalService));app.ControllerConstructor((*LocalController)(nil));app.Constructor(&Custom{})}`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "internal/application/application.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	inspector := newContractInspector(projectConfig{Layout: layeredLayout}, "example.com/test", nil)
	inspector.inspectFile(file, fset, file.Decls[1].(*ast.FuncDecl).Body, "app")
	roles := []string{"Repository", "Service", "Controller", "Custom"}
	if len(inspector.contract.Constructors) != len(roles) {
		t.Fatal(inspector.contract.Constructors)
	}
	for n, p := range inspector.contract.Constructors {
		if p.Role != roles[n] || !p.object || !strings.HasSuffix(p.Target, ".Constructor") {
			t.Fatalf("%+v", p)
		}
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
