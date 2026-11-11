package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMessageGeneratorsUseSelectedCoreContext(t *testing.T) {
	for _, layout := range []string{layeredLayout, "legacy"} {
		for _, hasPublic := range []bool{false, true} {
			t.Run(layout+"/"+map[bool]string{false: "released", true: "public-context"}[hasPublic], func(t *testing.T) {
				inTempDir(t)
				core := filepath.Join(t.TempDir(), "core")
				if err := os.MkdirAll(filepath.Join(core, "pkg", "spine"), 0755); err != nil {
					t.Fatal(err)
				}
				writeE2EFile(t, filepath.Join(core, "go.mod"), "module "+spineImportPath+"\ngo 1.25.0\n")
				if hasPublic {
					writeE2EFile(t, filepath.Join(core, "pkg", "spine", "types.go"), "package spine\ntype Ctx interface { Get(string)(any,bool) }\n")
				}
				writeE2EFile(t, "go.mod", "module example.com/messages\ngo 1.25.0\nrequire "+spineImportPath+" v0.7.5-preview.1\nreplace "+spineImportPath+" => "+filepath.ToSlash(core)+"\n")
				writeE2EFile(t, "spine.toml", "module = \"example.com/messages\"\nentrypoint = \".\"\ncomposition = \".\"\nlayout = \""+layout+"\"\nport = 8080\n")
				public, err := messageContext()
				if err != nil || public != hasPublic {
					t.Fatalf("public=%v err=%v", public, err)
				}
				for _, kind := range []string{"consumer", "websocket"} {
					name, _ := parseResourceName("audit")
					if layout == layeredLayout {
						planner := &layeredPlanner{Sources: layoutSources{}, Changes: map[string]fileChange{}}
						if err := planner.addMessageModule(layeredRequest{Kind: kind, Name: generationName{Name: name}, Topic: "audit.created", Path: "/ws/audit", Fields: []sliceField{{GoName: "Value", GoType: "string", JSONName: "value"}}}); err != nil {
							t.Fatal(err)
						}
						source := planner.Changes["src/"+kind+"/audit/audit_handler.go"].Contents
						assertMessageContext(t, source, hasPublic)
					} else {
						files := messageModuleFiles(kind, name, "audit", nil, public)
						assertMessageContext(t, files[filepath.Join("src", kind, "audit", "audit_handler.go")], hasPublic)
					}
				}
			})
		}
	}
}
func assertMessageContext(t *testing.T, source string, public bool) {
	t.Helper()
	want := "ctx context.Context"
	if public {
		want = "ctx spine.Ctx"
	}
	if !strings.Contains(source, want) || strings.Contains(source, "%!") {
		t.Fatalf("unexpected handler context:\n%s", source)
	}
	if public && strings.Contains(source, "\"context\"") {
		t.Fatalf("public handler uses a standard context import:\n%s", source)
	}
}
