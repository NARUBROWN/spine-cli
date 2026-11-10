package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestHeaderMarkerContract(t *testing.T) {
	inspector := controllerFixture(t, "type Headers struct { RequestID string `header:\"X-Request-ID\"`; Token string; Ignored string `header:\"-\"` }; func(c *UserController)RegisterRoutes(r *api.Registry){r.GET(\"/users\",func(input.Header[Headers])error{return nil})}")
	inspector.contract.HTTPAdapter.SelectedModules = []selectedModule{{Path: spineImportPath, Version: "v0.7.2-preview.2"}}
	inspector.inspectControllerRoutes()
	inspector.resolveHandlerSignatures()
	if contractHasErrors(*inspector.contract) {
		t.Fatalf("diagnostics=%+v", inspector.contract.Diagnostics)
	}
	p := inspector.contract.HTTP[0].Parameters[0]
	if p.Binding != "header" || strings.Join(p.Keys, ",") != "X-Request-Id,Token" {
		t.Fatalf("parameter=%+v", p)
	}
}

func TestHeaderMarkerRejectsInvalidContract(t *testing.T) {
	for _, fields := range []string{
		"A string `header:\"X-ID\"`; B string `header:\"x-id\"`",
		"A string `header:\"bad name\"`",
		"A map[string]string",
		"A string `query:\"q\"`",
	} {
		t.Run(fields, func(t *testing.T) {
			inspector := controllerFixture(t, "type Headers struct { "+fields+" }; func(c *UserController)RegisterRoutes(r *api.Registry){r.GET(\"/users\",func(input.Header[Headers])error{return nil})}")
			inspector.inspectControllerRoutes()
			inspector.resolveHandlerSignatures()
			if !contractHasErrors(*inspector.contract) {
				t.Fatalf("accepted invalid header: %+v", inspector.contract.HTTP)
			}
		})
	}
}

func TestWrappedPathAndMultipartBodyContract(t *testing.T) {
	inspector := controllerFixture(t, "type Upload struct { Name string `json:\"name\" form:\"name\"`; File *FixtureFileHeader `json:\"-\" form:\"file\"` }; func(c *UserController)RegisterRoutes(r *api.Registry){r.POST(\"/users/:id\",func(input.Path[PathDTO],input.Body[Upload])error{return nil})}")
	inspector.contract.HTTPAdapter.SelectedModules = []selectedModule{{Path: spineImportPath, Version: "v0.7.2-preview.2"}}
	inspector.inspectControllerRoutes()
	inspector.resolveHandlerSignatures()
	if contractHasErrors(*inspector.contract) {
		t.Fatalf("diagnostics=%+v", inspector.contract.Diagnostics)
	}
	parameters := inspector.contract.HTTP[0].Parameters
	if parameters[0].Binding != "path" || strings.Join(parameters[0].Keys, ",") != "id" || parameters[1].Binding != "body" {
		t.Fatalf("parameters=%+v", parameters)
	}
	doc, err := buildOpenAPIDocument(*inspector.contract)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{`"multipart/form-data"`, `"format":"binary"`, `"name"`} {
		if !strings.Contains(string(data), fragment) {
			t.Fatalf("missing %s: %s", fragment, data)
		}
	}
}

func TestExplicitCoreRejectsUnwrappedDTOs(t *testing.T) {
	for _, parameter := range []string{"*DTO", "*PathDTO", "*QueryDTO", "*HeaderDTO", "*FormDTO", "PlainAlias"} {
		t.Run(parameter, func(t *testing.T) {
			inspector := controllerFixture(t, "type QueryDTO struct { Name string `query:\"name\"` }; type HeaderDTO struct { Name string `header:\"X-Name\"` }; type FormDTO struct { Name string `form:\"name\"` }; type PlainAlias = *DTO; func(c *UserController)RegisterRoutes(r *api.Registry){r.POST(\"/users\",func("+parameter+")error{return nil})}")
			inspector.contract.HTTPAdapter.SelectedModules = []selectedModule{{Path: spineImportPath, Version: "v0.7.2-preview.2"}}
			inspector.inspectControllerRoutes()
			inspector.resolveHandlerSignatures()
			if !contractHasErrors(*inspector.contract) {
				t.Fatalf("accepted unwrapped DTO: %+v", inspector.contract.HTTP)
			}
		})
	}
}
