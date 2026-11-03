package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func httpBindingProject(t *testing.T, layout, imports, declarations, parameters, path string) {
	if err := prepareTestModuleCache(); err != nil {
		t.Fatal(err)
	}
	t.Helper()
	inTempDir(t)
	var out, errOut bytes.Buffer
	if code := runNew([]string{"todo", "--module", "example.com/todo", "--layout", layout, "--skip-install"}, &out, &errOut); code != 0 {
		t.Fatal(errOut.String())
	}
	if err := os.Chdir("todo"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll("handlers", 0o755); err != nil {
		t.Fatal(err)
	}
	writeE2EFile(t, "handlers/controller.go", "package handlers\n"+imports+"\n"+declarations+"\ntype Controller struct{}\nfunc NewController() *Controller { return &Controller{} }\nfunc (*Controller) Get("+parameters+") error { return nil }\n")
	source, _ := os.ReadFile("internal/application/application.go")
	source = bytes.Replace(source, []byte("import ("), []byte("import (\nprobe \"example.com/todo/handlers\""), 1)
	source = bytes.Replace(source, []byte("app := spine.New(echoplatform.Option())"), []byte("app := spine.New(echoplatform.Option())\napp.Constructor(probe.NewController)\napp.Route(\"POST\", \""+path+"\", (*probe.Controller).Get)"), 1)
	writeE2EFile(t, "internal/application/application.go", string(source))
}

func TestHTTPBindingsMatchDefaultResolvers(t *testing.T) {
	cases := []struct {
		name, imports, declarations, parameters, path, code string
	}{
		{"value DTO", "", "type Request struct { Quantity int64 }", "req Request", "/probe", "HTTP_PARAMETER_BINDING_UNSUPPORTED"},
		{"pointer DTO", "", "type Request struct { Quantity int64 }", "req *Request", "/probe", ""},
		{"alias DTO", "", "type Request struct{}; type Alias = Request", "req Alias", "/probe", "HTTP_PARAMETER_BINDING_UNSUPPORTED"},
		{"alias pointer DTO", "", "type Request struct{}; type Alias = *Request", "req Alias", "/probe", ""},
		{"aliased imports", `import (ctx "context"; p "github.com/NARUBROWN/spine/pkg/path"; q "github.com/NARUBROWN/spine/pkg/query"; h "github.com/NARUBROWN/spine/pkg/header"; c "github.com/NARUBROWN/spine/core")`, "type Request struct{}", "ctx ctx.Context, id p.Int, query q.Values, headers h.Values, view c.ControllerContext, req *Request", "/probe/:id", ""},
		{"alias wrapper", `import p "github.com/NARUBROWN/spine/pkg/path"`, "type ID = p.Int", "id ID", "/probe/:id", ""},
		{"pointer wrapper body", `import p "github.com/NARUBROWN/spine/pkg/path"`, "", "req *p.Int", "/probe", ""},
		{"defined wrapper", `import p "github.com/NARUBROWN/spine/pkg/path"`, "type ID p.Int", "id ID", "/probe", "HTTP_PARAMETER_BINDING_UNSUPPORTED"},
		{"scalar", "", "", "quantity int64", "/probe", "HTTP_PARAMETER_BINDING_UNSUPPORTED"},
		{"custom unknown type", `import custom "example.net/custom"`, "", "req custom.Request", "/probe", "HTTP_PARAMETER_BINDING_UNRESOLVED"},
		{"custom extended context", `import c "github.com/NARUBROWN/spine/core"`, "type Extended interface { c.ControllerContext; Extra() string }", "ctx Extended", "/probe", "HTTP_PARAMETER_BINDING_UNRESOLVED"},
		{"context view", "", "type View interface { Get(string) (any, bool) }", "ctx View", "/probe", ""},
		{"empty context view", "", "", "ctx any", "/probe", ""},
		{"pagination and upload", `import (q "github.com/NARUBROWN/spine/pkg/query"; m "github.com/NARUBROWN/spine/pkg/multipart")`, "", "page q.Pagination, files m.UploadedFiles", "/probe", ""},
	}
	for _, layout := range []string{layeredLayout, "legacy"} {
		for _, test := range cases {
			t.Run(layout+"/"+test.name, func(t *testing.T) {
				httpBindingProject(t, layout, test.imports, test.declarations, test.parameters, test.path)
				contract, err := inspectApplication()
				if err != nil {
					t.Fatal(err)
				}
				if test.code == "" {
					if contractHasErrors(contract) || !contract.Complete {
						t.Fatalf("supported parameter rejected: %#v", contract.Diagnostics)
					}
					return
				}
				found := false
				for _, diagnostic := range contract.Diagnostics {
					if diagnostic.Code == test.code {
						found = strings.Contains(diagnostic.Message, "parameter 0") && strings.Contains(diagnostic.Message, "Get") && diagnostic.Hint != "" && strings.Contains(diagnostic.Source, "handlers/controller.go")
					}
				}
				if !found || contract.Complete {
					t.Fatalf("missing actionable resolver diagnostic: %#v", contract)
				}
				for _, args := range [][]string{{"contract", "check", "--json"}, {"verify", "--json"}} {
					var out, errOut bytes.Buffer
					if code := Run(args, &out, &errOut); code != 5 || !strings.Contains(out.String(), test.code) {
						t.Fatalf("gate accepted unsupported binding: %v code=%d %s %s", args, code, out.String(), errOut.String())
					}
				}
			})
		}
	}
}

func TestCrossPackageDTOBindingsResolveTypesInTheirOwnFiles(t *testing.T) {
	layeredProject(t)
	layeredGenerate(t, "dto", "inventory/request", "--field", "quantity:int64")
	layeredGenerate(t, "controller", "inventory")
	path := "src/controller/inventory_controller.go"
	source, _ := os.ReadFile(path)
	text := strings.Replace(string(source), "package controller", "package controller\nimport dto \"example.com/todo/src/dto/inventory\"", 1)
	text += "\nfunc (*InventoryController) Create(req dto.Request) error { return nil }\n"
	writeE2EFile(t, path, text)
	args := []string{"route", "inventory", "--method", "POST", "--path", "/products", "--handler", "src/controller:InventoryController.Create"}
	beforeRoute, _ := os.ReadFile(layeredRoutePath)
	beforeComposition, _ := os.ReadFile("internal/application/application.go")
	for _, dry := range []bool{true, false} {
		request := append([]string{}, args...)
		if dry {
			request = append(request, "--dry-run")
		}
		var out, errOut bytes.Buffer
		if code := runGenerate(request, &out, &errOut); code == 0 || !strings.Contains(errOut.String(), "HTTP_PARAMETER_BINDING_UNSUPPORTED") || !strings.Contains(errOut.String(), "*dto.Request") {
			t.Fatalf("route accepted value DTO: %d %s", code, errOut.String())
		}
	}
	afterRoute, _ := os.ReadFile(layeredRoutePath)
	afterComposition, _ := os.ReadFile("internal/application/application.go")
	if !bytes.Equal(beforeRoute, afterRoute) || !bytes.Equal(beforeComposition, afterComposition) {
		t.Fatal("rejected route changed project files")
	}
	writeE2EFile(t, path, strings.Replace(text, "req dto.Request", "req *dto.Request", 1))
	layeredGenerate(t, args...)
	contract, err := inspectApplication()
	if err != nil || contractHasErrors(contract) || !contract.Complete {
		t.Fatalf("fixed pointer DTO did not recover: %v %#v", err, contract.Diagnostics)
	}
}
