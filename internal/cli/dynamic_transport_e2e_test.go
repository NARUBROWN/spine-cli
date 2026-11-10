package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 로컬 프록시의 새 이름으로 생성, 계약, 빌드와 update를 확인합니다.
func TestDynamicHTTPAndWebSocketComposition(t *testing.T) {
	targets, err := resolvePreviewModules(context.Background(), []selectedModule{{Path: spineImportPath, Version: "preview"}, {Path: nethttpPlatformPath, Version: "preview"}, {Path: gorillaPlatformPath, Version: "preview"}})
	if err != nil {
		t.Fatal(err)
	}
	sources := []moduleProxySource{}
	for _, item := range []struct{ kind, provider, original, contract, constructor string }{
		{"http", "sample-http", nethttpPlatformPath, "spine.http-adapter/v3", "AttachHTTP"},
		{"websocket", "sample-socket", gorillaPlatformPath, "spine.websocket-adapter/v1", "AttachSocket"},
	} {
		root := t.TempDir()
		path := providerModule(item.provider)
		goMod := fmt.Sprintf("module %s\n\ngo 1.26.0\n\nrequire (\n %s %s\n %s %s\n)\n", path, spineImportPath, selectedModuleVersion(targets, spineImportPath), item.original, selectedModuleVersion(targets, item.original))
		writeE2EFile(t, filepath.Join(root, "go.mod"), goMod)
		writeE2EFile(t, filepath.Join(root, "option.go"), fmt.Sprintf("package transport\nimport (\"github.com/NARUBROWN/spine\"; original %q)\nfunc %s() spine.Option { return original.Option() }\n", item.original, item.constructor))
		d := &platformDescriptor{SchemaVersion: platformDescriptorSchema, Kind: item.kind, Provider: item.provider, ContractVersion: item.contract, Package: "transport", Factory: item.constructor, MinimumGo: "1.26.0"}
		writeTestDescriptor(t, root, d)
		sources = append(sources, moduleProxySource{path, "v0.1.0", root})
	}
	isolatedDynamicPlatformProxy(t, sources...)
	binary := buildUpdateCLI(t)
	for _, layout := range []string{"legacy", layeredLayout} {
		t.Run(layout, func(t *testing.T) {
			workspace := t.TempDir()
			args := []string{"new", "sample", "--module", "example.com/sample", "--channel", "preview", "--layout", layout, "--platform", "sample-http", "--websocket-platform", "sample-socket"}
			applyAssemblyPlan(t, binary, workspace, args...)
			project := filepath.Join(workspace, "sample")
			check := runE2E(binary, project, nil, "contract", "check", "--warnings-as-errors", "--json")
			assertE2ESuccess(t, check, "dynamic transport contract")
			for _, want := range []string{`"provider": "sample-http"`, `"provider": "sample-socket"`, "spine-sample-http-platform.AttachHTTP", "spine-sample-socket-platform.AttachSocket"} {
				if !strings.Contains(check.stdout, want) {
					t.Fatalf("missing %s: %s", want, check.stdout)
				}
			}
			addGeneratedTest(t, project, "dynamic_http_runtime_test.go", `package application
import (
 "context"
 "net/http"
 "net/http/httptest"
 "testing"
 "github.com/NARUBROWN/spine"
)
func TestDynamicHTTPHealth(t *testing.T) {
 app, options, err := BuildApplication(); if err != nil { t.Fatal(err) }
 prepared, err := app.Prepare(context.Background(), options); if err != nil { t.Fatal(err) }
 defer prepared.Close(context.Background())
 handler, err := spine.NetHTTPHandler(prepared); if err != nil { t.Fatal(err) }
 server := httptest.NewServer(handler); defer server.Close()
 response, err := http.Get(server.URL + "/health"); if err != nil { t.Fatal(err) }
 defer response.Body.Close()
 if response.StatusCode != http.StatusOK { t.Fatalf("health status: %d", response.StatusCode) }
}`)
			wsFixture, err := os.ReadFile(filepath.Join("testdata", "assembly_websocket_test.go.txt"))
			if err != nil {
				t.Fatal(err)
			}
			addGeneratedTest(t, project, "dynamic_websocket_runtime_test.go", strings.ReplaceAll(string(wsFixture), "{{VALIDATOR}}", "false"))
			generatedGo(t, project, "mod", "tidy")
			assertE2ESuccess(t, runE2E(binary, project, nil, "verify", "--json"), "dynamic transport verification")
			generatedGo(t, project, "build", "./...")
			before, _ := os.ReadFile(filepath.Join(project, "internal/application/application.go"))
			applyAssemblyPlan(t, binary, project, "update")
			after, _ := os.ReadFile(filepath.Join(project, "internal/application/application.go"))
			if string(before) != string(after) {
				t.Fatal("update changed composition")
			}
			var value map[string]any
			if err := json.Unmarshal([]byte(check.stdout), &value); err != nil {
				t.Fatal(err)
			}
		})
	}
}
