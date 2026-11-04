package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestReleaseRecipeDoesNotChangeLegacyPins(t *testing.T) {
	options := newProjectOptions{Platform: "echo", WebSocketPlatform: "gorilla", Events: []eventPlatformSpec{{Name: "events", Provider: "kafka"}}, Validator: "none"}
	preview := currentAssemblyModules(options)
	if selectedModuleVersion(preview, spineImportPath) != "v0.7.0-preview.1" || selectedModuleVersion(preview, echoPlatformPath) != "v0.2.0-preview.1" || selectedModuleVersion(preview, gorillaPlatformPath) != "v0.2.0-preview.1" {
		t.Fatalf("preview recipe: %+v", preview)
	}
	stable := stableAssemblyModules(options)
	if selectedModuleVersion(stable, spineImportPath) != "v0.6.5" || selectedModuleVersion(stable, gorillaPlatformPath) != "v0.1.0" || selectedModuleVersion(stable, kafkaPlatformPath) != "v0.1.0" {
		t.Fatalf("release recipe: %+v", stable)
	}
	legacy := legacyWebSocketModules(options)
	if selectedModuleVersion(legacy, spineImportPath) != "v0.6.1-0.20261103040400-9b1321410211" || selectedModuleVersion(legacy, gorillaPlatformPath) != "v0.0.0-20261103040512-36ad41dff88f" {
		t.Fatalf("legacy recipe changed: %+v", legacy)
	}
	older := assemblyModules(options.Platform, options.Events, options.Validator)
	if selectedModuleVersion(older, spineImportPath) != "v0.6.1-0.20261103010611-6e60b34204d3" {
		t.Fatalf("initial public recipe changed: %+v", older)
	}
}

func TestWebSocketSelectionPlanAndDiagnostics(t *testing.T) {
	if err := prepareTestModuleCache(); err != nil {
		t.Fatal(err)
	}
	inTempDir(t)
	for _, args := range [][]string{{"--websocket-platform", "unknown"}, {"--websocket-platform"}, {"--platform", "none", "--event-platform", "kafka", "--websocket-platform", "gorilla"}} {
		var out, errOut bytes.Buffer
		if code := Run(append([]string{"new", "invalid", "--skip-install", "--json"}, args...), &out, &errOut); code != 2 {
			t.Fatalf("%v: %d %s", args, code, out.String())
		}
	}
	var out, errOut bytes.Buffer
	if code := runNew([]string{"sample", "--module", "example.com/sample", "--skip-install", "--websocket-platform", "gorilla"}, &out, &errOut); code != 0 {
		t.Fatal(errOut.String())
	}
	if err := os.Chdir("sample"); err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile("internal/application/application.go")
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := inspectApplication()
	if err != nil || !baseline.Complete || baseline.WebSocketAdapter.Provider != "gorilla" || baseline.HTTPAdapter.Provider != "echo" {
		t.Fatalf("baseline: %v %+v", err, baseline.Diagnostics)
	}
	config, err := loadProjectConfig()
	if err != nil || config.WebSocketPlatform != "gorilla" || config.Recipe != currentAssemblyRecipe {
		t.Fatalf("config: %+v %v", config, err)
	}
	for _, tt := range []struct{ before, after, code string }{
		{"gorillaplatform.Option(),", "gorillaplatform.Option(), gorillaplatform.Option(),", "WEBSOCKET_ADAPTER_DUPLICATE"},
		{"gorillaplatform.Option()", "spine.WithWebSocketAdapter(dynamicFactory())", "WEBSOCKET_ADAPTER_UNRESOLVED"},
		{"gorillaplatform.Option(),", "", "WEBSOCKET_PLATFORM_MISMATCH"},
		{"gorillaplatform.Option(),", "gorillaplatform.Option(), unknownOption(),", "WEBSOCKET_ADAPTER_UNRESOLVED"},
	} {
		updated := strings.Replace(string(source), tt.before, tt.after, 1)
		if updated == string(source) {
			t.Fatal("fixture replacement was not applied", tt.before)
		}
		writeE2EFile(t, "internal/application/application.go", updated)
		actual, err := inspectApplication()
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, d := range actual.Diagnostics {
			found = found || d.Code == tt.code
		}
		if !found || actual.Complete {
			t.Fatalf("%s: %+v", tt.code, actual.Diagnostics)
		}
	}
	direct := strings.Replace(string(source), "gorillaplatform.Option()", "spine.WithWebSocketAdapter(gorillaplatform.NewFactory())", 1)
	writeE2EFile(t, "internal/application/application.go", direct)
	actual, err := inspectApplication()
	if err != nil || !actual.Complete || actual.WebSocketAdapter.Provider != "gorilla" {
		t.Fatalf("direct factory: %v %+v", err, actual.Diagnostics)
	}
	before, _ := json.Marshal(semanticView(baseline))
	changed := baseline
	changed.WebSocketAdapter.Provider = "none"
	after, _ := json.Marshal(semanticView(changed))
	if bytes.Equal(before, after) {
		t.Fatal("WebSocket selection was omitted from semantic snapshots")
	}
}

func TestWebSocketPlanCannotReuseDifferentSelection(t *testing.T) {
	inTempDir(t)
	t.Setenv("PATH", t.TempDir())
	var out, errOut bytes.Buffer
	if code := Run([]string{"new", "sample", "--skip-install", "--dry-run", "--json"}, &out, &errOut); code != 0 {
		t.Fatal(code, errOut.String())
	}
	var plan struct {
		Result struct {
			Hash string `json:"base_hash"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errOut.Reset()
	if code := Run([]string{"new", "sample", "--skip-install", "--websocket-platform", "gorilla", "--if-match", plan.Result.Hash, "--json"}, &out, &errOut); code != 4 {
		t.Fatalf("changed selection reused reviewed plan: %d %s", code, out.String())
	}
	if _, err := os.Stat("sample"); !os.IsNotExist(err) {
		t.Fatal("failed plan published project")
	}
}
