package cli

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"strings"
	"testing"
)

func previewStableHTTPFixture(t *testing.T, body, version string) *contractInspector {
	t.Helper()
	inspector := controllerFixture(t, body)
	module, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	writeE2EFile(t, "go.mod", strings.ReplaceAll(string(module), "v0.7.0-preview.5", version))
	inspector.contract.HTTPAdapter.SelectedModules[0].Version = version
	return inspector
}

func TestPreviewStableControllerResponseCountMatchesSelectedCore(t *testing.T) {
	for _, version := range []string{"v0.7.2-preview.3", "v0.7.5-preview.1", "v0.7.5-rc.1", "v0.7.5"} {
		for _, tc := range []struct {
			name, declarations, results, values string
			multiple                            bool
		}{
			{"single", "", "httpx.Response[string]", `httpx.Response[string]{}`, false},
			{"response_error", "", "(httpx.Response[string],error)", `httpx.Response[string]{},nil`, false},
			{"error_response", "", "(error,*httpx.Binary)", `nil,nil`, false},
			{"two_values", "", "(httpx.Response[string],httpx.Response[int])", `httpx.Response[string]{},httpx.Response[int]{}`, true},
			{"two_pointers", "", "(*httpx.Binary,*httpx.Redirect,error)", `nil,nil,nil`, true},
			{"grouped_results", "", "(first,second httpx.Response[string])", `httpx.Response[string]{},httpx.Response[string]{}`, true},
			{"response_alias", "type AuditResult = httpx.Response[string]", "(AuditResult,*httpx.Redirect)", `AuditResult{},nil`, true},
		} {
			t.Run(version+"/"+tc.name, func(t *testing.T) {
				body := tc.declarations + `
func(*UserController)RegisterRoutes(r *api.Registry){r.GET("/response",func()` + tc.results + `{return ` + tc.values + `})}`
				inspector := previewStableHTTPFixture(t, body, version)
				writeE2EFile(t, "core/pkg/httpx/httpx.go", "package httpx\ntype Response[T any]struct{Body T}\ntype Binary struct{}\ntype Redirect struct{}\n")
				inspector.inspectControllerRoutes()
				inspector.resolveHandlerSignatures()
				wantError := tc.multiple && version != "v0.7.2-preview.3"
				found := false
				for _, diagnostic := range inspector.contract.Diagnostics {
					found = found || diagnostic.Code == "HTTP_RETURN_UNSUPPORTED" && strings.Contains(diagnostic.Message, "at most one HTTP response")
				}
				if found != wantError || !wantError && contractHasErrors(*inspector.contract) {
					t.Fatalf("선택 코어의 응답 개수 계약과 다릅니다: %+v", inspector.contract.Diagnostics)
				}
			})
		}
	}
}

func TestPreviewStableControllerXMLPathsMatchCore(t *testing.T) {
	for _, tc := range []struct {
		name, declarations string
		conflict           bool
	}{
		{"duplicate", "type XMLBody struct { A string `json:\"a\" xml:\"same\"`; B string `json:\"b\" xml:\"same\"` }", true},
		{"parent_child", "type XMLBody struct { A string `json:\"a\" xml:\"same\"`; B string `json:\"b\" xml:\"same>child\"` }", true},
		{"shared_parent", "type XMLBody struct { A string `xml:\"same>a\"`; B string `xml:\"same>b\"` }", false},
		{"attribute_element", "type XMLBody struct { A string `xml:\"same,attr\"`; B string `xml:\"same\"` }", false},
		{"duplicate_attributes", "type XMLBody struct { A string `json:\"a\" xml:\"same,attr\"`; B string `json:\"b\" xml:\"same,attr\"` }", true},
		{"different_namespaces", "type XMLBody struct { A string `xml:\"urn:a same\"`; B string `xml:\"urn:b same\"` }", false},
		{"same_namespace", "type XMLBody struct { A string `xml:\"urn:a same\"`; B string `xml:\"urn:a same\"` }", true},
		{"qualified_unqualified", "type XMLBody struct { A string `xml:\"same\"`; B string `xml:\"urn:b same\"` }", false},
		{"namespace_parent", "type XMLBody struct { A string `xml:\"same\"`; B string `xml:\"urn:b same>child\"` }", true},
		{"nested_pointer", "type Child struct { A string `xml:\"same\"`; B string `xml:\"same>child\"` }; type XMLBody struct { Child *Child }", true},
		{"nested_slice", "type Child struct { A string `xml:\"same\"`; B string `xml:\"same>child\"` }; type XMLBody struct { Children []Child }", true},
		{"recursive", "type XMLBody struct { Next *XMLBody }", false},
		{"ignored", "type Child struct { A string `xml:\"same\"`; B string `xml:\"same>child\"` }; type XMLBody struct { Child Child `xml:\"-\"` }", false},
		{"embedded_shallow_wins", "type XMLBase struct { X string `json:\"x\" xml:\"same\"` }; type XMLBody struct { XMLBase; Y string `json:\"y\" xml:\"same\"` }", false},
		{"embedded_shallow_first", "type XMLBase struct { X string `json:\"x\" xml:\"same\"` }; type XMLBody struct { Y string `json:\"y\" xml:\"same\"`; *XMLBase }", false},
		{"embedded_same_depth", "type XMLBase struct { X string `xml:\"same\"` }; type XMLOther struct { Y string `xml:\"same>child\"` }; type XMLBody struct { XMLBase; XMLOther }", true},
		{"xmlname_defaults", "type Child struct { XMLName auditxml.Name `xml:\"child\"` }; type XMLBody struct { First Child; Second Child }", true},
		{"generic_alias", "type Envelope[T any] struct { A T `xml:\"same\"`; B T `xml:\"same>child\"` };type XMLBody = Envelope[string]", true},
		{"json_decoder_only", "type XMLBody struct { A string `xml:\"same\"`; B string `xml:\"same>child\"` };func(*XMLBody)UnmarshalJSON([]byte)error{panic(\"do not run\")}", true},
		{"xml_decoder", "type XMLBody struct { A string `xml:\"same\"`; B string `xml:\"same>child\"` };func(*XMLBody)UnmarshalXML(*auditxml.Decoder,auditxml.StartElement)error{panic(\"do not run\")}", false},
		{"text_decoder", "type XMLBody struct { A string `xml:\"same\"`; B string `xml:\"same>child\"` };func(*XMLBody)UnmarshalText([]byte)error{panic(\"do not run\")}", false},
		{"attr_decoder_is_not_element_decoder", "type XMLBody struct { A string `xml:\"same\"`; B string `xml:\"same>child\"` };func(*XMLBody)UnmarshalXMLAttr(auditxml.Attr)error{panic(\"do not run\")}", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := "package audit\nimport auditxml \"encoding/xml\"\ntype XMLNameAlias = auditxml.Name\n" + tc.declarations
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, "xml.go", source, 0)
			if err != nil {
				t.Fatal(err)
			}
			config := types.Config{Importer: importer.Default()}
			pkg, err := config.Check("example.com/xml-audit", fset, []*ast.File{file}, nil)
			if err != nil {
				t.Fatal(err)
			}
			err = checkControllerXMLBody(pkg.Scope().Lookup("XMLBody").Type())
			if (err != nil) != tc.conflict {
				t.Fatalf("코어와 XML 경로 충돌 판정이 다릅니다: %v", err)
			}
		})
	}
}

func TestPreviewXMLConstraintMatchesSelectedCore(t *testing.T) {
	for _, version := range []string{"v0.7.2-preview.3", "v0.7.5-preview.1"} {
		t.Run(version, func(t *testing.T) {
			body := "type Body struct { A string `json:\"a\" xml:\"same\"`; B string `json:\"b\" xml:\"same>child\"` };" +
				`func(*UserController)RegisterRoutes(r *api.Registry){r.POST("/xml",func(input.Body[Body])error{return nil})}`
			inspector := previewStableHTTPFixture(t, body, version)
			inspector.inspectControllerRoutes()
			inspector.resolveHandlerSignatures()
			found := false
			for _, diagnostic := range inspector.contract.Diagnostics {
				found = found || diagnostic.Code == "HTTP_INPUT_CONTRACT_INVALID" && strings.Contains(diagnostic.Message, "conflicting XML fields")
			}
			if found != (version == "v0.7.5-preview.1") || version == "v0.7.2-preview.3" && contractHasErrors(*inspector.contract) {
				t.Fatalf("선택 코어와 XML 충돌 검사가 다릅니다: %+v", inspector.contract.Diagnostics)
			}
		})
	}
}
