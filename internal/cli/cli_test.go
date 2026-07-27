package cli

import (
	"bytes"
	"go/format"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunHelp(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Run([]string{"help"}, &out, &errOut); code != 0 {
		t.Fatalf("code = %d", code)
	}
	if !strings.Contains(out.String(), "spine new") {
		t.Fatal("help did not include new command")
	}
}

func TestGenerateResource(t *testing.T) {
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })

	var out, errOut bytes.Buffer
	if code := runGenerate([]string{"resource", "receipt", "--no-register"}, &out, &errOut); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, errOut.String())
	}
	for _, path := range []string{
		filepath.Join("src", "receipt", "module.go"),
		filepath.Join("src", "receipt", "receipt_repository.go"),
		filepath.Join("src", "receipt", "receipt_service.go"),
		filepath.Join("src", "receipt", "receipt_controller.go"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected generated file %s: %v", path, err)
		}
	}
}

func TestGenerateLayeredComponents(t *testing.T) {
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })

	for _, kind := range []string{"controller", "service", "repository"} {
		var out, errOut bytes.Buffer
		if code := runGenerate([]string{kind, "user"}, &out, &errOut); code != 0 {
			t.Fatalf("%s: code = %d, stderr = %s", kind, code, errOut.String())
		}
		path := filepath.Join("src", kind, "user_"+kind+".go")
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected generated file %s: %v", path, err)
		}
	}
}

func TestGeneratedTemplatesAreValidGo(t *testing.T) {
	name, err := parseResourceName("blog-posts")
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{
		moduleTemplate(name), repositoryTemplate(name), serviceTemplate(name), controllerTemplate(name),
		standaloneRepositoryTemplate(name), standaloneServiceTemplate(name), standaloneControllerTemplate(name),
	} {
		if _, err := format.Source([]byte(source)); err != nil {
			t.Fatalf("invalid generated source: %v", err)
		}
	}
}

func TestParseResourceName(t *testing.T) {
	got, err := parseResourceName("blog_posts")
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != "BlogPosts" || got.Package != "blog_posts" || got.Route != "blog-posts" {
		t.Fatalf("unexpected name: %#v", got)
	}
}

func TestGenerateResourceRegistersInMain(t *testing.T) {
	inTempDir(t)
	if err := os.WriteFile("go.mod", []byte("module example.com/demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("spine.toml", []byte("module = \"example.com/demo\"\nentrypoint = \".\"\nport = 8080\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	main := `package main

import (
    "github.com/NARUBROWN/spine"
)

func main() {
    app := spine.New()
    _ = app
}

`
	if err := os.WriteFile("main.go", []byte(main), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if code := runGenerate([]string{"resource", "receipt", "--register"}, &out, &errOut); code != 0 {
		t.Fatalf("code=%d: %s", code, errOut.String())
	}
	got, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `receipt "example.com/demo/src/receipt"`) || !strings.Contains(string(got), "receipt.Register(app)") {
		t.Fatalf("resource was not registered:\n%s", got)
	}
	if code := runGenerate([]string{"resource", "receipt", "--register", "--force"}, &out, &errOut); code != 0 {
		t.Fatalf("idempotent registration failed: %s", errOut.String())
	}
	got, _ = os.ReadFile("main.go")
	if strings.Count(string(got), "receipt.Register(app)") != 1 {
		t.Fatal("resource registration was duplicated")
	}
}

func TestGenerateDryRunDoesNotWrite(t *testing.T) {
	inTempDir(t)
	var out, errOut bytes.Buffer
	if code := runGenerate([]string{"service", "billing", "--dry-run", "--json"}, &out, &errOut); code != 0 {
		t.Fatalf("code=%d: %s", code, errOut.String())
	}
	if _, err := os.Stat(filepath.Join("src", "service", "billing_service.go")); !os.IsNotExist(err) {
		t.Fatalf("dry run created a file: %v", err)
	}
	if !strings.Contains(out.String(), `"dry_run":true`) {
		t.Fatalf("expected JSON dry_run output: %s", out.String())
	}
}

func TestNewCreatesSpineTomlAndCurrentSpineVersion(t *testing.T) {
	inTempDir(t)
	var out, errOut bytes.Buffer
	if code := runNew([]string{"todo", "--module", "example.com/todo"}, &out, &errOut); code != 0 {
		t.Fatalf("code=%d: %s", code, errOut.String())
	}
	config, err := os.ReadFile(filepath.Join("todo", "spine.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(config), `module = "example.com/todo"`) || !strings.Contains(string(config), "port = 8080") {
		t.Fatalf("unexpected config: %s", config)
	}
	mod, err := os.ReadFile(filepath.Join("todo", "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mod), "github.com/NARUBROWN/spine v0.5.1") {
		t.Fatalf("unexpected go.mod: %s", mod)
	}
}

func TestLoadProjectConfig(t *testing.T) {
	inTempDir(t)
	if err := os.WriteFile("spine.toml", []byte("module = \"example.com/app\"\nentrypoint = \"./cmd/api\"\nport = 3000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	config, err := loadProjectConfig()
	if err != nil || config.Module != "example.com/app" || config.Entrypoint != "./cmd/api" || config.Port != 3000 {
		t.Fatalf("config = %#v, %v", config, err)
	}
}

func TestCompletionAndProjectName(t *testing.T) {
	inTempDir(t)
	if err := os.WriteFile("go.mod", []byte("module example.com/my-api\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	name, err := projectName()
	if err != nil || name != "my-api" {
		t.Fatalf("projectName = %q, %v", name, err)
	}
	var out, errOut bytes.Buffer
	if code := runCompletion([]string{"powershell"}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "Register-ArgumentCompleter") {
		t.Fatalf("completion = %d, %s", code, errOut.String())
	}
}

func inTempDir(t *testing.T) {
	t.Helper()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
}
