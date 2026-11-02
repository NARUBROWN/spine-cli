package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestLayeredCompletionShowsActualPathsReferencesAndRegistration(t *testing.T) {
	layeredProject(t)
	var out, errOut bytes.Buffer
	if code := runGenerate([]string{"repository", "warehouse/catalog"}, &out, &errOut); code != 0 {
		t.Fatal(errOut.String())
	}
	for _, fragment := range []string{"Created src/repository/warehouse/catalog_repository.go", "Type reference: src/repository/warehouse:CatalogRepository", "Registration (constructor): src/repository/warehouse:NewCatalogRepository -> internal/application/application.go"} {
		if !strings.Contains(out.String(), fragment) {
			t.Fatalf("completion lacks %q: %s", fragment, out.String())
		}
	}
	out.Reset()
	errOut.Reset()
	if code := runGenerate([]string{"repository", "warehouse/catalog", "--json"}, &out, &errOut); code != 0 {
		t.Fatal(errOut.String())
	}
	var result struct {
		Types         []generatedTypeInfo         `json:"types"`
		Registrations []generatedRegistrationInfo `json:"registrations"`
		Changes       []plannedChange             `json:"changes"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Types) != 1 || result.Types[0].Reference != "src/repository/warehouse:CatalogRepository" || len(result.Registrations) != 1 {
		t.Fatalf("JSON details missing: %s", out.String())
	}
	for _, change := range result.Changes {
		if change.Operation != "unchanged" {
			t.Fatalf("idempotent request changed source: %+v", result.Changes)
		}
	}
	out.Reset()
	if code := runGenerate([]string{"controller", "api/product", "--no-register"}, &out, &errOut); code != 0 {
		t.Fatal(errOut.String())
	}
	if strings.Contains(out.String(), "Registration (") {
		t.Fatalf("no-register claimed registration: %s", out.String())
	}
	if !strings.Contains(out.String(), "Type reference: src/controller/api:ProductController") {
		t.Fatal(out.String())
	}
	controller, _ := os.ReadFile("src/controller/api/product_controller.go")
	writeE2EFile(t, "src/controller/api/product_controller.go", string(controller)+"\nfunc (*ProductController) Create() error { return nil }\n")
	out.Reset()
	if code := runGenerate([]string{"route", "create-product", "--method", "POST", "--path", "/products", "--handler", "src/controller/api:ProductController.Create"}, &out, &errOut); code != 0 {
		t.Fatal(errOut.String())
	}
	if !strings.Contains(out.String(), "Route: POST /products -> src/controller/api:ProductController.Create") || !strings.Contains(out.String(), "Updated src/route/route.go") {
		t.Fatal(out.String())
	}
}

func TestRoleHelpExplainsPathsExamplesAndSupportedOptions(t *testing.T) {
	for _, kind := range []string{"domain", "dto", "repository", "service", "controller", "route", "resource", "consumer", "websocket"} {
		var out, errOut bytes.Buffer
		if handled, code := generationHelp("generate", []string{kind, "--help"}, &out, &errOut); !handled || code != 0 {
			t.Fatal(errOut.String())
		}
		if !strings.Contains(out.String(), "spine g "+kind+" ") || !strings.Contains(out.String(), "예:") {
			t.Fatal(out.String())
		}
		if (kind == "domain" || kind == "dto") && strings.Contains(out.String(), "--register") {
			t.Fatalf("data role advertises DI option: %s", out.String())
		}
		if kind == "route" && (!strings.Contains(out.String(), "httpx.Response[T]") || !strings.Contains(out.String(), "src/route/route.go")) {
			t.Fatal(out.String())
		}
	}
}
