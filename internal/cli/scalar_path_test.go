package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestScalarPathContractAndStaticOpenAPI(t *testing.T) {
	for _, tc := range []struct{ name, payload, declaration string }{{"int64", "int64", ""}, {"alias", "UserID", "type UserID = int64"}, {"byte", "byte", ""}} {
		t.Run(tc.name, func(t *testing.T) {
			inspector := controllerFixture(t, tc.declaration+`
func(c *UserController)RegisterRoutes(r *api.Registry){r.GET("/users/:id",func(input.Path[`+tc.payload+`])error{return nil})}`)
			inspector.contract.HTTPAdapter.SelectedModules[0].Replace = &selectedModule{Path: "./core"}
			inspector.inspectControllerRoutes()
			inspector.resolveHandlerSignatures()
			if contractHasErrors(*inspector.contract) {
				t.Fatalf("diagnostics: %+v", inspector.contract.Diagnostics)
			}
			if len(inspector.contract.HTTP) != 1 {
				t.Fatalf("routes: %+v", inspector.contract.HTTP)
			}
			param := inspector.contract.HTTP[0].Parameters[0]
			if param.Binding != "path" || len(param.Keys) != 1 || param.Keys[0] != "id" || !param.scalarPath {
				t.Fatalf("%+v", param)
			}
			doc, err := buildOpenAPIDocument(*inspector.contract)
			if err != nil {
				t.Fatal(err)
			}
			operation := doc["paths"].(map[string]any)["/users/{id}"].(map[string]any)["get"].(map[string]any)
			entry := operation["parameters"].([]any)[0].(map[string]any)
			schema := entry["schema"].(map[string]any)
			if entry["name"] != "id" || entry["required"] != true || schema["type"] != "integer" {
				t.Fatalf("%+v", entry)
			}
			if tc.name == "byte" && (schema["minimum"] != json.Number("0") || schema["maximum"] != json.Number("255")) {
				t.Fatalf("%+v", schema)
			}
			if tc.name != "byte" && (schema["minimum"] != json.Number("-9223372036854775808") || schema["maximum"] != json.Number("9223372036854775807")) {
				t.Fatalf("%+v", schema)
			}
		})
	}
}
func TestScalarPathRejectsAmbiguousAndUnsupported(t *testing.T) {
	for _, tc := range []struct{ name, body, code string }{
		{"no key", `func(c *UserController)RegisterRoutes(r *api.Registry){r.GET("/users",func(input.Path[int])error{return nil})}`, "HTTP_PATH_SCALAR_AMBIGUOUS"},
		{"many keys", `func(c *UserController)RegisterRoutes(r *api.Registry){r.GET("/:org/:id",func(input.Path[int])error{return nil})}`, "HTTP_PATH_SCALAR_AMBIGUOUS"},
		{"many inputs", `func(c *UserController)RegisterRoutes(r *api.Registry){r.GET("/:id",func(input.Path[int],input.Path[int64])error{return nil})}`, "HTTP_PATH_SCALAR_AMBIGUOUS"},
		{"duplicate dto", `func(c *UserController)RegisterRoutes(r *api.Registry){r.GET("/:id",func(input.Path[int],input.Path[PathDTO])error{return nil})}`, "HTTP_INPUT_KEY_DUPLICATED"},
		{"defined type", `type ID int;func(c *UserController)RegisterRoutes(r *api.Registry){r.GET("/:id",func(input.Path[ID])error{return nil})}`, "HTTP_INPUT_CONTRACT_INVALID"},
		{"slice", `func(c *UserController)RegisterRoutes(r *api.Registry){r.GET("/:id",func(input.Path[[]int])error{return nil})}`, "HTTP_INPUT_CONTRACT_INVALID"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inspector := controllerFixture(t, tc.body)
			inspector.inspectControllerRoutes()
			inspector.resolveHandlerSignatures()
			for _, d := range inspector.contract.Diagnostics {
				if d.Code == tc.code {
					return
				}
			}
			t.Fatalf("missing %s: %+v", tc.code, inspector.contract.Diagnostics)
		})
	}
}
func TestScalarPathPlatformIntSchemaDoesNotAssumeArchitecture(t *testing.T) {
	for _, name := range []string{"int", "uint", "uintptr"} {
		s := scalarPathSchema(name)
		if s["x-spine-platform-range-unresolved"] != true || s["maximum"] != nil {
			t.Fatalf("%s: %+v", name, s)
		}
	}
	if strings.Contains(scalarPathSchema("uint64")["x-spine-type-range-text"].(string), "-") {
		t.Fatal("unexpected negative range")
	}
}

func TestScalarPathRejectsKnownUnsupportedCore(t *testing.T) {
	inspector := controllerFixture(t, `func(c *UserController)RegisterRoutes(r *api.Registry){r.GET("/:id",func(input.Path[int64])error{return nil})}`)
	inspector.inspectControllerRoutes()
	inspector.resolveHandlerSignatures()
	for _, d := range inspector.contract.Diagnostics {
		if d.Code == "HTTP_PATH_SCALAR_CORE_UNSUPPORTED" {
			return
		}
	}
	t.Fatalf("%+v", inspector.contract.Diagnostics)
}

func TestScalarPathCandidateVersionRequiresNoWarning(t *testing.T) {
	inspector := controllerFixture(t, `func(c *UserController)RegisterRoutes(r *api.Registry){r.GET("/:id",func(input.Path[int64])error{return nil})}`)
	inspector.contract.HTTPAdapter.SelectedModules[0].Version = "v0.7.0-preview.8"
	inspector.inspectControllerRoutes()
	inspector.resolveHandlerSignatures()
	for _, d := range inspector.contract.Diagnostics {
		if d.Code == "HTTP_PATH_SCALAR_CORE_UNVERIFIED" || d.Code == "HTTP_PATH_SCALAR_CORE_UNSUPPORTED" {
			t.Fatalf("%+v", d)
		}
	}
	if contractHasErrors(*inspector.contract) {
		t.Fatalf("%+v", inspector.contract.Diagnostics)
	}
}

func TestScalarPathUntagedCoreIsUnverified(t *testing.T) {
	inspector := controllerFixture(t, `func(c *UserController)RegisterRoutes(r *api.Registry){r.GET("/:id",func(input.Path[int64])error{return nil})}`)
	inspector.contract.HTTPAdapter.SelectedModules[0].Version = "v0.7.0-preview.7.0.20261108010000-abcdefabcdef"
	inspector.inspectControllerRoutes()
	inspector.resolveHandlerSignatures()
	for _, d := range inspector.contract.Diagnostics {
		if d.Code == "HTTP_PATH_SCALAR_CORE_UNVERIFIED" && d.Severity == "warning" {
			return
		}
	}
	t.Fatalf("%+v", inspector.contract.Diagnostics)
}
