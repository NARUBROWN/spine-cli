package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestStructuredHTTPInputContracts(t *testing.T) {
	for _, layout := range []string{layeredLayout, "legacy"} {
		for _, test := range []struct{ name, imports, declarations, parameters, path, code string }{
			{"separate inputs", "", "type Params struct { ID int `path:\"id\"` }; type Query struct { Tags []string `query:\"tag\"` }; type Headers struct { Host string `header:\"Host\"` }; type Body struct { Name string `json:\"name\"` }", "p *Params, q *Query, h *Headers, b *Body", "/probe/:id", ""},
			{"named path keys", "", "type Params struct { Item int `path:\"item\"`; ID int `path:\"id\"` }", "p *Params", "/probe/:id/:item", ""},
			{"embedded query", "", "type Base struct { Page int `query:\"page\"` }; type Query struct { Base; Name string `query:\"name\"` }", "q *Query", "/probe", ""},
			{"excluded query", "", "type Query struct { Hidden int `query:\"-\"` }", "q *Query", "/probe", ""},
			{"unexported field", "", "type Query struct { Name string `query:\"name\"`; hidden int }", "q *Query", "/probe", ""},
			{"mixed sources", "", "type Query struct { Name string `query:\"name\"`; ID int `path:\"id\"` }", "q *Query", "/probe", "HTTP_INPUT_CONTRACT_INVALID"},
			{"missing tag", "", "type Query struct { Name string `query:\"name\"`; Page int }", "q *Query", "/probe", "HTTP_INPUT_CONTRACT_INVALID"},
			{"duplicate fields", "", "type Query struct { First string `query:\"name\"`; Second string `query:\"name\"` }", "q *Query", "/probe", "HTTP_INPUT_CONTRACT_INVALID"},
			{"header case collision", "", "type Headers struct { First string `header:\"X-ID\"`; Second string `header:\"x-id\"` }", "h *Headers", "/probe", "HTTP_INPUT_CONTRACT_INVALID"},
			{"duplicate arguments", "", "type Query struct { Name string `query:\"name\"` }", "q *Query, again *Query", "/probe", "HTTP_INPUT_DTO_DUPLICATED"},
			{"aliased duplicate", "", "type Query struct { Name string `query:\"name\"` }; type Alias = Query", "q *Query, again *Alias", "/probe", "HTTP_INPUT_DTO_DUPLICATED"},
			{"duplicate source keys", "", "type Query struct { Name string `query:\"name\"` }; type Other struct { Name string `query:\"name\"` }", "q *Query, other *Other", "/probe", "HTTP_INPUT_KEY_DUPLICATED"},
			{"multiple bodies", "", "type First struct {}; type Second struct {}", "a *First, b *Second", "/probe", "HTTP_BODY_PARAMETERS_DUPLICATED"},
			{"legacy query mixed", `import q "github.com/NARUBROWN/spine/pkg/query"`, "type Query struct { Name string `query:\"name\"` }", "old q.Values, new *Query", "/probe", "HTTP_INPUT_API_MIXED"},
			{"legacy pagination mixed", `import q "github.com/NARUBROWN/spine/pkg/query"`, "type Query struct { Name string `query:\"name\"` }", "old q.Pagination, new *Query", "/probe", "HTTP_INPUT_API_MIXED"},
			{"legacy path mixed", `import p "github.com/NARUBROWN/spine/pkg/path"`, "type Params struct { Name string `path:\"name\"` }", "old p.Int, new *Params", "/probe/:id/:name", "HTTP_PATH_INPUT_MIXED"},
			{"wrong named path", "", "type Params struct { ID int `path:\"other\"` }", "p *Params", "/probe/:id", "HTTP_PATH_BINDING_MISMATCH"},
		} {
			t.Run(layout+"/"+test.name, func(t *testing.T) {
				httpBindingProject(t, layout, test.imports, test.declarations, test.parameters, test.path)
				contract, err := inspectApplication()
				if err != nil {
					t.Fatal(err)
				}
				if test.code == "" {
					if contractHasErrors(contract) || !contract.Complete {
						t.Fatalf("valid input contract rejected: %+v", contract.Diagnostics)
					}
					return
				}
				found := false
				for _, diagnostic := range contract.Diagnostics {
					found = found || diagnostic.Code == test.code
				}
				if !found || contract.Complete {
					t.Fatalf("missing %s: %+v", test.code, contract.Diagnostics)
				}
			})
		}
	}
}

func TestStructuredInputChecksPreserveStableWrapperPointers(t *testing.T) {
	httpBindingProject(t, "legacy", `import p "github.com/NARUBROWN/spine/pkg/path"`, "", "body *p.Int", "/probe")
	mod, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	mod = bytes.ReplaceAll(mod, []byte(currentAssemblySpineVersion), []byte("v0.6.5"))
	mod = bytes.ReplaceAll(mod, []byte(previewPlatformVersion), []byte("v0.1.0"))
	writeE2EFile(t, "go.mod", string(mod))
	contract, err := inspectApplication()
	if err != nil || contractHasErrors(contract) || !contract.Complete {
		t.Fatalf("stable input behavior changed: %v %+v", err, contract.Diagnostics)
	}
}

func TestStructuredPathDTOCanBeRegisteredAndExported(t *testing.T) {
	layeredProject(t)
	layeredGenerate(t, "controller", "inventory")
	path := "src/controller/inventory_controller.go"
	source, _ := os.ReadFile(path)
	source = append(source, []byte("\ntype InventoryPath struct { ID int `path:\"id\"`; Item string `path:\"item\"` }\ntype InventoryQuery struct { Name string `query:\"filter\"` }\nfunc (*InventoryController) Find(p *InventoryPath, q *InventoryQuery) error { return nil }\n")...)
	writeE2EFile(t, path, string(source))
	layeredGenerate(t, "route", "inventory", "--method", "GET", "--path", "/inventory/:item/:id", "--handler", "src/controller:InventoryController.Find")
	var out, errOut bytes.Buffer
	if code := Run([]string{"contract", "export", "--format", "openapi"}, &out, &errOut); code != 0 {
		t.Fatalf("static path export: %d %s", code, errOut.String())
	}
	for _, key := range []string{"id", "item"} {
		if !strings.Contains(out.String(), `"name": "`+key+`"`) {
			t.Fatalf("missing named path field %s: %s", key, out.String())
		}
	}
	out.Reset()
	errOut.Reset()
	if code := runExplain([]string{"http", "GET", "/inventory/:item/:id"}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "keys=id,item") || !strings.Contains(out.String(), "keys=filter") {
		t.Fatalf("source DTO keys missing from explanation: %d %s %s", code, out.String(), errOut.String())
	}
	if code := runContract([]string{"snapshot"}, &out, &errOut); code != 0 {
		t.Fatal(errOut.String())
	}
	writeE2EFile(t, path, strings.Replace(string(source), `query:"filter"`, `query:"search"`, 1))
	out.Reset()
	errOut.Reset()
	if code := runContract([]string{"diff", "--json"}, &out, &errOut); code == 0 || !strings.Contains(out.String(), "filter") || !strings.Contains(out.String(), "search") {
		t.Fatalf("source DTO key change missing from semantic diff: %d %s %s", code, out.String(), errOut.String())
	}
}

func TestUploadedFilesDeprecationPreservesBodyCompatibility(t *testing.T) {
	for _, parameters := range []string{"upload m.UploadedFiles", "body input.Body[Upload], upload m.UploadedFiles", "upload m.UploadedFiles, body input.Body[Upload]"} {
		t.Run(parameters, func(t *testing.T) {
			httpBindingProject(t, "legacy", `import (m "github.com/NARUBROWN/spine/pkg/multipart"; "github.com/NARUBROWN/spine/pkg/input")`, "type Upload struct { Name string `form:\"name\"` }; type UploadBody = input.Body[Upload]", parameters, "/upload")
			contract, err := inspectApplication()
			if err != nil {
				t.Fatal(err)
			}
			if contractHasErrors(contract) {
				t.Fatalf("compatibility: %+v", contract.Diagnostics)
			}
			found := false
			for _, diagnostic := range contract.Diagnostics {
				if diagnostic.Code == "HTTP_INPUT_API_DEPRECATED" && strings.Contains(diagnostic.Hint, "input.Body") {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing deprecation warning: %+v", contract.Diagnostics)
			}
		})
	}
}
