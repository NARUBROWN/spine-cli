package cli

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func generationSafetyProject(t *testing.T, version string) {
	t.Helper()
	t.Chdir(t.TempDir())
	writeE2EFile(t, "go.mod", "module example.com/safety\ngo 1.25.5\nrequire "+spineImportPath+" "+version+"\n")
	writeE2EFile(t, "spine.toml", "module = \"example.com/safety\"\nentrypoint = \".\"\ncomposition = \"internal/application\"\nlayout = \"layered-v1\"\nplatform = \"echo\"\nport = 8080\n")
	if err := os.MkdirAll("src/controller", 0755); err != nil {
		t.Fatal(err)
	}
	writeE2EFile(t, "src/controller/existing.go", "package controller\n// User-owned source must remain unchanged.\n")
}

func generationSafetyFiles(t *testing.T) map[string]string {
	t.Helper()
	files := map[string]string{}
	if err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		contents, err := os.ReadFile(path)
		files[path] = string(contents)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return files
}

func TestRegistryUnsupportedMethodsFailBeforeGenerationWrites(t *testing.T) {
	for _, method := range []string{"TRACE", "CONNECT"} {
		t.Run(method, func(t *testing.T) {
			generationSafetyProject(t, "v0.7.5-preview.1")
			before := generationSafetyFiles(t)
			var out, errOut bytes.Buffer
			if code := runGenerate([]string{"route", "probe", "--method", method, "--path", "/probe", "--handler", "src/controller:ProbeController.Get"}, &out, &errOut); code == 0 || !strings.Contains(errOut.String(), "api.Registry") {
				t.Fatalf("unsupported Registry method must fail before source planning: %s", errOut.String())
			}
			if after := generationSafetyFiles(t); !reflect.DeepEqual(after, before) {
				t.Fatalf("rejected method changed the project: before=%v after=%v", before, after)
			}
		})
	}
}

func TestRouteMethodSupportPreservesLegacyRegistration(t *testing.T) {
	for _, version := range []string{"v0.6.5", "v0.7.5-preview.1"} {
		t.Run(version, func(t *testing.T) {
			generationSafetyProject(t, version)
			for _, method := range []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "TRACE", "CONNECT"} {
				_, err := parseLayeredRequest([]string{"route", "probe", "--method", method, "--path", "/probe", "--handler", "src/controller:ProbeController.Get"})
				unsupported := version == "v0.7.5-preview.1" && (method == "TRACE" || method == "CONNECT")
				if (err != nil) != unsupported {
					t.Fatalf("method support differs from registration API: core=%s method=%s err=%v", version, method, err)
				}
			}
		})
	}
}

func TestInvalidImportPathsFailBeforeGenerationWrites(t *testing.T) {
	for _, name := range []string{"관리/user", "café/user", "Équipe/user"} {
		t.Run(name, func(t *testing.T) {
			generationSafetyProject(t, "v0.7.5-preview.1")
			before := generationSafetyFiles(t)
			var out, errOut bytes.Buffer
			if code := runGenerate([]string{"controller", name}, &out, &errOut); code == 0 || !strings.Contains(errOut.String(), "invalid Go import path") {
				t.Fatalf("invalid import path was not rejected: %s", errOut.String())
			}
			if after := generationSafetyFiles(t); !reflect.DeepEqual(after, before) {
				t.Fatalf("rejected import path changed the project: before=%v after=%v", before, after)
			}
		})
	}
	for _, name := range []string{"user", "commerce/account/user", "user-api/user_record", "Équipe"} {
		if _, err := parseGenerationName(name); err != nil {
			t.Fatalf("valid nested name rejected: %q: %v", name, err)
		}
	}
	for _, kind := range []string{"consumer", "websocket"} {
		if _, err := parseLayeredRequest([]string{kind, "Équipe", "--topic", "audit", "--path", "/ws/audit", "--field", "value:string"}); err == nil || !strings.Contains(err.Error(), "invalid Go import path") {
			t.Fatalf("Unicode message package name accepted: %s: %v", kind, err)
		}
	}
}

func TestPathBindingValidationMatchesCoreCardinality(t *testing.T) {
	dto := func(keys ...string) handlerParameter {
		return handlerParameter{Binding: "path", SourceDTO: true, Keys: keys}
	}
	scalar := func(key string) handlerParameter {
		return handlerParameter{Binding: "path", SourceDTO: true, Keys: []string{key}, scalarPath: true}
	}
	legacy := func(key string) handlerParameter { return handlerParameter{Binding: "path", Key: key} }
	for _, tc := range []struct {
		name       string
		parameters []handlerParameter
		path       string
		valid      bool
	}{
		{"unused_route_keys", nil, "/orgs/:orgId/users/:userId", true},
		{"partial_path_dto", []handlerParameter{dto("userId")}, "/orgs/:orgId/users/:userId", true},
		{"full_path_dto", []handlerParameter{dto("orgId", "userId")}, "/orgs/:orgId/users/:userId", true},
		{"separate_path_dtos", []handlerParameter{dto("orgId"), dto("userId")}, "/orgs/:orgId/users/:userId", true},
		{"undeclared_dto_key", []handlerParameter{dto("tenantId")}, "/orgs/:orgId", false},
		{"duplicate_dto_key", []handlerParameter{dto("orgId"), dto("orgId")}, "/orgs/:orgId", false},
		{"missing_scalar", []handlerParameter{scalar("orgId")}, "/orgs/:orgId/users/:userId", false},
		{"exact_scalars", []handlerParameter{scalar("orgId"), scalar("userId")}, "/orgs/:orgId/users/:userId", true},
		{"surplus_scalars", []handlerParameter{scalar("orgId"), scalar("")}, "/orgs/:orgId", false},
		{"mixed_scalar_and_dto", []handlerParameter{scalar("orgId"), dto("userId")}, "/orgs/:orgId/users/:userId", false},
		{"partial_legacy", []handlerParameter{legacy("orgId")}, "/orgs/:orgId/users/:userId", true},
		{"surplus_legacy", []handlerParameter{legacy("orgId"), legacy("")}, "/orgs/:orgId", false},
		{"duplicate_route_key", nil, "/orgs/:id/users/:id", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateHTTPPathBindings(tc.parameters, tc.path); (err == nil) != tc.valid {
				t.Fatalf("path binding contract mismatch: valid=%v err=%v", tc.valid, err)
			}
		})
	}
}
