package cli

import (
	"encoding/json"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func addAPIExportHandler(t *testing.T, name, imports, bodyType string) {
	t.Helper()
	layeredGenerate(t, "controller", name)
	path := "src/controller/" + name + "_controller.go"
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Replace(string(source), "package controller", "package controller\nimport (\"github.com/NARUBROWN/spine/pkg/httpx\"\n"+imports+"\n)", 1)
	text += fmt.Sprintf("\nfunc (*%sController) Get() httpx.Response[%s] { return httpx.Response[%s]{} }\n", strings.ToUpper(name[:1])+name[1:], bodyType, bodyType)
	formatted, err := format.Source([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	writeE2EFile(t, path, string(formatted))
	layeredGenerate(t, "route", name, "--method", "GET", "--path", "/"+name, "--handler", "src/controller:"+strings.ToUpper(name[:1])+name[1:]+"Controller.Get")
}

func exportedResponseSchema(t *testing.T, document map[string]any, path string) map[string]any {
	t.Helper()
	operation := document["paths"].(map[string]any)[path].(map[string]any)["get"].(map[string]any)
	return operation["responses"].(map[string]any)["default"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
}

func TestOpenAPIPreservesPackageIdentityForDuplicateDTOs(t *testing.T) {
	layeredProject(t)
	layeredGenerate(t, "dto", "a/response", "--field", "name:string")
	layeredGenerate(t, "dto", "b/response", "--field", "code:int64")
	addAPIExportHandler(t, "a", `dto "example.com/todo/src/dto/a"`, "dto.Response")
	addAPIExportHandler(t, "b", `dto "example.com/todo/src/dto/b"`, "dto.Response")
	contract, err := inspectApplication()
	if err != nil {
		t.Fatal(err)
	}
	document, err := buildOpenAPIDocument(contract)
	if err != nil {
		t.Fatal(err)
	}
	assertLocalAPIReferences(t, document)
	a := exportedResponseSchema(t, document, "/a")["$ref"].(string)
	b := exportedResponseSchema(t, document, "/b")["$ref"].(string)
	if a == b {
		t.Fatalf("different DTO packages share a schema: %s", a)
	}
	schemas := document["components"].(map[string]any)["schemas"].(map[string]any)
	for reference, field := range map[string]string{a: "name", b: "code"} {
		schema := schemas[strings.TrimPrefix(reference, "#/components/schemas/")].(map[string]any)
		if schema["properties"].(map[string]any)[field] == nil {
			t.Fatalf("wrong DTO schema for %s: %#v", reference, schema)
		}
	}
}

func TestOpenAPIHandlesTimeAndUnresolvedTypesWithoutDanglingReferences(t *testing.T) {
	layeredProject(t)
	addAPIExportHandler(t, "clock", `"time"`, "time.Time")
	addAPIExportHandler(t, "network", `"net"`, "net.IP")
	contract, err := inspectApplication()
	if err != nil {
		t.Fatal(err)
	}
	document, err := buildOpenAPIDocument(contract)
	if err != nil {
		t.Fatal(err)
	}
	clock := exportedResponseSchema(t, document, "/clock")
	if clock["type"] != "string" || clock["format"] != "date-time" {
		t.Fatalf("time.Time is not a JSON timestamp: %#v", clock)
	}
	unknown := exportedResponseSchema(t, document, "/network")
	if unknown["$ref"] != nil || unknown["x-spine-unresolved-type"] == nil {
		t.Fatalf("unknown type produced a dangling reference: %#v", unknown)
	}
}

func TestAPISchemaFieldNamesMatchEncodingJSON(t *testing.T) {
	layeredProject(t)
	layeredGenerate(t, "dto", "payload")
	source := "package dto\ntype Payload struct {\nOrderID int64\nAlias string `json:\"alias\"`\nOptional string `json:\",omitempty\"`\nHidden string `json:\"-\"`\n}\n"
	writeE2EFile(t, filepath.Join("src", "dto", "payload.go"), source)
	addAPIExportHandler(t, "payload", `dto "example.com/todo/src/dto"`, "dto.Payload")
	contract, err := inspectApplication()
	if err != nil {
		t.Fatal(err)
	}
	openAPI, err := buildOpenAPIDocument(contract)
	if err != nil {
		t.Fatal(err)
	}
	contract.Consumers = []namedRegistration{{Name: "payload.created", Handler: "Handle", Parameters: []handlerParameter{{Type: "Payload", Binding: "payload"}}}}
	asyncAPI, err := buildAsyncAPIDocument(contract)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(struct {
		OrderID  int64
		Alias    string `json:"alias"`
		Optional string `json:",omitempty"`
		Hidden   string `json:"-"`
	}{OrderID: 7, Alias: "ok"})
	var actual map[string]any
	if err := json.Unmarshal(encoded, &actual); err != nil {
		t.Fatal(err)
	}
	for _, document := range []map[string]any{openAPI, asyncAPI} {
		properties := document["components"].(map[string]any)["schemas"].(map[string]any)["Payload"].(map[string]any)["properties"].(map[string]any)
		for key := range actual {
			if properties[key] == nil {
				t.Fatalf("JSON field %s missing from schema: %#v", key, properties)
			}
		}
		if properties["orderID"] != nil || properties["Hidden"] != nil || properties["Optional"] == nil {
			t.Fatalf("JSON tags or default names changed: %#v", properties)
		}
	}
}

func TestAsyncAPIKeepsPunctuationCollisionsDistinct(t *testing.T) {
	layeredProject(t)
	contract := applicationContract{Module: "example.com/todo", Consumers: []namedRegistration{{Name: "order.created", Handler: "Handle"}, {Name: "order-created", Handler: "Handle"}}, WebSockets: []namedRegistration{{Name: "order.created", Handler: "Handle"}}}
	document, err := buildAsyncAPIDocument(contract)
	if err != nil {
		t.Fatal(err)
	}
	assertLocalAPIReferences(t, document)
	channels := document["channels"].(map[string]any)
	operations := document["operations"].(map[string]any)
	if len(channels) != 3 || len(operations) != 3 {
		t.Fatalf("registrations were overwritten: %d channels, %d operations", len(channels), len(operations))
	}
	for _, operation := range operations {
		reference := operation.(map[string]any)["channel"].(map[string]any)["$ref"].(string)
		if channels[strings.TrimPrefix(reference, "#/channels/")] == nil {
			t.Fatalf("dangling channel reference: %s", reference)
		}
	}
}

func assertLocalAPIReferences(t *testing.T, document map[string]any) {
	t.Helper()
	var visit func(any)
	visit = func(node any) {
		switch value := node.(type) {
		case map[string]any:
			if reference, ok := value["$ref"].(string); ok && strings.HasPrefix(reference, "#/") {
				var target any = document
				for _, part := range strings.Split(strings.TrimPrefix(reference, "#/"), "/") {
					object, ok := target.(map[string]any)
					if !ok {
						t.Fatalf("invalid document reference: %s", reference)
					}
					part = strings.NewReplacer("~1", "/", "~0", "~").Replace(part)
					target, ok = object[part]
					if !ok {
						t.Fatalf("dangling document reference: %s", reference)
					}
				}
			}
			for _, child := range value {
				visit(child)
			}
		case []any:
			for _, child := range value {
				visit(child)
			}
		}
	}
	visit(document)
}

func TestStaticOpenAPIFormNestedAliasesAndUnknown(t *testing.T) {
	layeredProject(t)
	writeE2EFile(t, "formtypes.go", `package todo
import m "mime/multipart"
import external "example.org/external"
type File = m.FileHeader
type Inner struct { Upload *File `+"`form:\"upload\"`"+` }
type Form struct { Inner Inner }
type Unknown struct { Other external.SomeType }
type Custom string
func (*Custom) UnmarshalText([]byte) error { return nil }
type Encoded struct { Value Custom `+"`form:\"value\"`"+` }
type ExternalFile struct { Upload []external.File `+"`form:\"file\"`"+` }
`)
	catalog, err := newAPISchemas("example.com/todo")
	if err != nil {
		t.Fatal(err)
	}
	content, known, unresolved := catalog.staticFormContent("*Form", "formtypes.go")
	if !known || unresolved {
		t.Fatalf("nested form unresolved: %#v", content)
	}
	media := content["multipart/form-data"].(map[string]any)
	schema := media["schema"].(map[string]any)
	upload := schema["properties"].(map[string]any)["upload"].(map[string]any)
	if upload["format"] != "binary" || content["application/json"] != nil {
		t.Fatalf("wrong file form: %#v", content)
	}
	_, known, unresolved = catalog.staticFormContent("*Unknown", "formtypes.go")
	if known || !unresolved {
		t.Fatal("unknown embedded fields were treated as resolved")
	}
	_, known, unresolved = catalog.staticFormContent("*Encoded", "formtypes.go")
	if known || !unresolved {
		t.Fatal("custom text decoder was treated as a known wire type")
	}
	_, known, unresolved = catalog.staticFormContent("*ExternalFile", "formtypes.go")
	if known || !unresolved {
		t.Fatal("unknown array element was treated as a known wire type")
	}
}
