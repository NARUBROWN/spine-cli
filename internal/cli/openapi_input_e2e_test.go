package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStaticOpenAPIInputDTOAndMultipartAgainstRuntime(t *testing.T) {
	requireValidatorToolchain(t)
	binary := buildUpdateCLI(t)
	workspace := t.TempDir()
	applyAssemblyPlan(t, binary, workspace, "new", "sample", "--module", "example.com/sample", "--validator", "playground", "--layout", "legacy")
	project := filepath.Join(workspace, "sample")

	application := filepath.Join(project, "internal/application/application.go")
	source, err := os.ReadFile(application)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(source), "app.Constructor(", "app.Constructor(NewInputProbe)\napp.Route(\"POST\", \"/probe/:id\", (*InputProbe).Post)\napp.Route(\"POST\", \"/json\", (*InputProbe).JSON)\napp.Constructor(", 1)
	if updated == string(source) {
		t.Fatal("registration missing")
	}
	writeE2EFile(t, application, updated)
	writeE2EFile(t, filepath.Join(project, "internal/application/input_probe.go"), `package application
import ("mime/multipart"; "os"; clock "time"; "github.com/NARUBROWN/spine/pkg/input")
func init() { _ = os.WriteFile("ran-user-code", []byte("yes"), 0600) }
type PathInput struct { ID int64 `+"`path:\"id\"`"+` }
type QueryInput struct { Limit *int `+"`query:\"limit\" validate:\"omitempty,min=1\"`"+` }
type HeaderInput struct { Trace string `+"`header:\"X-Trace-ID\" validate:\"required\"`"+` }
type UploadInput struct { File *multipart.FileHeader `+"`form:\"file\" json:\"-\" validate:\"required\"`"+` }
type Date = clock.Time
type JSONInput struct { Created clock.Time `+"`json:\"created\"`"+`; Modified Date `+"`json:\"modified\"`"+` }
type InputProbe struct{}
func NewInputProbe() *InputProbe { return &InputProbe{} }
func (*InputProbe) Post(path *PathInput, query *QueryInput, header *HeaderInput, body input.Body[UploadInput]) error { return nil }
func (*InputProbe) JSON(body *JSONInput) error { return nil }
`)
	var docs []map[string]any
	for _, mode := range []string{"static", "runtime"} {
		result := runE2E(binary, project, nil, "contract", "export", "--format", "openapi", "--source", mode)
		assertE2ESuccess(t, result, mode)
		_, err := os.Stat(filepath.Join(project, "ran-user-code"))
		if mode == "static" && !os.IsNotExist(err) {
			t.Fatalf("static export ran application init: %v", err)
		}
		if mode == "runtime" && err != nil {
			t.Fatalf("runtime fixture did not run: %v", err)
		}
		var doc map[string]any
		if err := json.Unmarshal([]byte(result.stdout), &doc); err != nil {
			t.Fatal(err)
		}
		docs = append(docs, doc)
	}
	for i, doc := range docs {
		jsonOperation := doc["paths"].(map[string]any)["/json"].(map[string]any)["post"].(map[string]any)
		jsonBody := jsonOperation["requestBody"].(map[string]any)
		jsonContent := jsonBody["content"].(map[string]any)
		if jsonContent["application/json"] == nil || jsonBody["x-spine-unresolved-content"] != nil {
			t.Errorf("mode %d lost JSON body: %#v", i, jsonBody)
		}
		operation := doc["paths"].(map[string]any)["/probe/{id}"].(map[string]any)["post"].(map[string]any)
		params := operation["parameters"].([]any)
		names := map[string]bool{}
		for _, p := range params {
			m := p.(map[string]any)
			names[m["in"].(string)+":"+m["name"].(string)] = true
		}
		if i == 0 {
			for _, p := range params {
				m := p.(map[string]any)
				key := m["in"].(string) + ":" + m["name"].(string)
				schema := m["schema"].(map[string]any)
				if key == "path:id" && (schema["type"] != "integer" || schema["format"] != "int64" || m["required"] != true) {
					t.Errorf("bad path schema: %#v", m)
				}
				if key == "query:limit" && (schema["anyOf"] == nil || m["x-spine-validation-unresolved"] != true) {
					t.Errorf("missing nullable or unresolved query validation: %#v", m)
				}
				if key == "header:X-Trace-Id" && m["x-spine-validation-unresolved"] != true {
					t.Errorf("missing unresolved header validation: %#v", m)
				}
			}
		}
		for _, name := range []string{"path:id", "query:limit", "header:X-Trace-Id"} {
			if !names[name] {
				t.Errorf("mode %d missing %s: %#v", i, name, params)
			}
		}
		body := operation["requestBody"].(map[string]any)
		content := body["content"].(map[string]any)
		raw, _ := json.Marshal(doc)
		if !strings.Contains(string(raw), `"format":"binary"`) {
			t.Errorf("mode %d missing binary file schema", i)
		}
		if content["multipart/form-data"] == nil || content["application/json"] != nil {
			t.Errorf("mode %d wrong content %#v", i, content)
		}
	}
}
