package cli

import (
	"os"
	"strings"
	"testing"
)

func TestBodyAnonymousPointerEmbeddingsMatchCore(t *testing.T) {
	for _, version := range []string{"v0.7.2-preview.3", "v0.7.5-preview.1"} {
		for _, tc := range []struct {
			name, declarations, marker string
			invalid                    bool
		}{
			{"pointer", `type Base struct{Name string};type Request struct{*Base}`, "Body", false},
			{"nested-pointer", `type Base struct{Name string};type Middle struct{*Base};type Request struct{*Middle}`, "Body", false},
			{"struct-alias", `type Base struct{Name string};type Alias = Base;type Request struct{*Alias}`, "Body", false},
			{"pointer-alias", `type Base struct{Name string};type Alias = *Base;type Request struct{Alias}`, "Body", false},
			{"defined-struct", `type Base struct{Name string};type Defined Base;type Request struct{*Defined}`, "Body", false},
			{"recursive-pointer", `type Request struct{*Request;Name string}`, "Body", false},
			{"mixed-query", "type Base struct{Value string `query:\"value\"`};type Request struct{*Base}", "Body", true},
			{"mixed-header-alias", "type Base struct{Value string `header:\"X-Value\"`};type Alias = *Base;type Request struct{Alias}", "Body", true},
			{"mixed-path-nested", "type Base struct{Value string `path:\"value\"`};type Middle struct{*Base};type Request struct{*Middle}", "Body", true},
			{"query-still-rejected", `type Base struct{Name string};type Request struct{*Base}`, "Query", true},
			{"header-still-rejected", `type Base struct{Name string};type Request struct{*Base}`, "Header", true},
			{"path-still-rejected", `type Base struct{Name string};type Request struct{*Base}`, "Path", true},
		} {
			t.Run(version+"/"+tc.name, func(t *testing.T) {
				body := tc.declarations + `
func(*UserController)RegisterRoutes(r *api.Registry){
 r.POST("/body",func(input.` + tc.marker + `[Request])error{return nil})
}`
				inspector := previewStableHTTPFixture(t, body, version)
				inspector.inspectControllerRoutes()
				inspector.resolveHandlerSignatures()
				if contractHasErrors(*inspector.contract) != tc.invalid {
					t.Fatalf("Body embedding support differs from core: %+v", inspector.contract.Diagnostics)
				}
				if tc.invalid {
					found := false
					for _, diagnostic := range inspector.contract.Diagnostics {
						found = found || diagnostic.Code == "HTTP_INPUT_CONTRACT_INVALID"
					}
					if !found {
						t.Fatalf("expected an input contract error: %+v", inspector.contract.Diagnostics)
					}
					return
				}
				if len(inspector.contract.HTTP) != 1 || len(inspector.contract.HTTP[0].Parameters) != 1 || inspector.contract.HTTP[0].Parameters[0].Binding != "body" {
					t.Fatalf("valid embedded Body was not resolved: %+v", inspector.contract.HTTP)
				}
			})
		}
	}
}

func TestBodyAnonymousPointerEmbeddingResolvesExternalAliasesWithoutExecution(t *testing.T) {
	inspector := previewStableHTTPFixture(t, `
type LocalAlias = shared.Pointer
type Request struct{LocalAlias}
func(*UserController)RegisterRoutes(r *api.Registry){r.POST("/body",func(input.Body[Request])error{return nil})}
`, "v0.7.5-preview.1")
	module, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	writeE2EFile(t, "go.mod", string(module)+"\nrequire example.com/shared v0.0.0\nreplace example.com/shared => ./shared\n")
	if err := os.MkdirAll("shared", 0755); err != nil {
		t.Fatal(err)
	}
	writeE2EFile(t, "shared/go.mod", "module example.com/shared\n\ngo 1.25.5\n")
	writeE2EFile(t, "shared/request.go", `package shared
type Base struct{Name string}
type Pointer = *Base
func init(){panic("static inspection must not run dependency init")}
`)
	controller, err := os.ReadFile("controller.go")
	if err != nil {
		t.Fatal(err)
	}
	writeE2EFile(t, "controller.go", strings.Replace(string(controller), "import (", "import (\nshared \"example.com/shared\"", 1))
	inspector.sources, _, err = readLayoutSources()
	if err != nil {
		t.Fatal(err)
	}
	inspector.inspectControllerRoutes()
	inspector.resolveHandlerSignatures()
	if contractHasErrors(*inspector.contract) || len(inspector.contract.HTTP) != 1 || inspector.contract.HTTP[0].Parameters[0].Binding != "body" {
		t.Fatalf("external embedded pointer alias was not resolved: %+v", inspector.contract.Diagnostics)
	}
}
