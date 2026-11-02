package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewSkipInstallIsOfflineAndGuardsMode(t *testing.T) {
	inTempDir(t)
	t.Setenv("PATH", t.TempDir())
	var out, errOut bytes.Buffer
	args := []string{"new", "inventory", "--module", "example.com/inventory", "--skip-install"}
	if code := Run(append(append([]string{}, args...), "--dry-run", "--json"), &out, &errOut); code != 0 {
		t.Fatalf("offline plan: %d %s", code, errOut.String())
	}
	var plan struct {
		Result struct {
			BaseHash       string              `json:"base_hash"`
			Stages         []installationStage `json:"stages"`
			InstallSkipped bool                `json:"install_skipped"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if !plan.Result.InstallSkipped || len(plan.Result.Stages) != 6 || plan.Result.Stages[1].Status != "skipped" {
		t.Fatalf("installation was misreported: %s", out.String())
	}
	entries, _ := os.ReadDir(".")
	if len(entries) != 0 {
		t.Fatalf("dry-run wrote project files: %v", entries)
	}
	out.Reset()
	errOut.Reset()
	if code := Run(append(append([]string{}, args...), "--if-match", plan.Result.BaseHash), &out, &errOut); code != 0 {
		t.Fatalf("offline apply: %d %s", code, errOut.String())
	}
	if _, err := os.Stat("inventory/go.sum"); !os.IsNotExist(err) {
		t.Fatalf("skip-install produced go.sum: %v", err)
	}
	source, _ := os.ReadFile("inventory/internal/application/application.go")
	if !bytes.Contains(source, []byte("platform.New()")) {
		t.Fatal("missing platform selection")
	}
}

func TestNewFailuresHaveSamePlainAndJSONCodes(t *testing.T) {
	inTempDir(t)
	if err := os.Mkdir("existing", 0o755); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []struct {
		args []string
		code int
	}{
		{[]string{"new", "--skip-install"}, 2},
		{[]string{"new", "sample", "--platform", "unknown"}, 2},
		{[]string{"new", "sample", "--force"}, 2},
		{[]string{"new", "existing", "--skip-install"}, 4},
		{[]string{"new", "sample", "--skip-install", "--if-match", "wrong"}, 4},
		{[]string{"--root", "missing", "new", "sample"}, 3},
		{[]string{"new", "sample", "--root", "missing"}, 3},
		{[]string{"--root", "first", "--root", "second", "new", "sample"}, 2},
		{[]string{"new", "sample", "--root"}, 2},
	} {
		for _, jsonOutput := range []bool{false, true} {
			args := append([]string{}, scenario.args...)
			if jsonOutput {
				args = append(args, "--json")
			}
			var out, errOut bytes.Buffer
			if code := Run(args, &out, &errOut); code != scenario.code {
				t.Fatalf("%v: got %d want %d: %s %s", args, code, scenario.code, out.String(), errOut.String())
			}
		}
	}
}

func TestPlatformCompositionFormsAndAdapterDiagnostics(t *testing.T) {
	if err := prepareTestModuleCache(); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []struct{ name, expression, options, diagnostic string }{
		{"platform", "p.New()", "boot.Options{HTTP:&boot.HTTPOptions{}}", ""},
		{"option", "s.New(p.Option())", "boot.Options{HTTP:&boot.HTTPOptions{}}", ""},
		{"factory-option", "s.New(s.WithHTTPAdapter(p.NewFactory()))", "boot.Options{HTTP:&boot.HTTPOptions{}}", ""},
		{"platform-native", "p.New(p.WithEcho(func(*echo.Echo) {}))", "boot.Options{HTTP:&boot.HTTPOptions{}}", ""},
		{"option-native", "s.New(p.Option(p.WithEcho(func(*echo.Echo) {})))", "boot.Options{HTTP:&boot.HTTPOptions{}}", ""},
		{"factory-native", "s.New(s.WithHTTPAdapter(p.NewFactory(p.WithEcho(func(*echo.Echo) {}))))", "boot.Options{HTTP:&boot.HTTPOptions{}}", ""},
		{"unknown-platform-option", "p.New(customOption())", "boot.Options{HTTP:&boot.HTTPOptions{}}", "HTTP_ADAPTER_UNRESOLVED"},
		{"unknown-factory-option", "s.New(s.WithHTTPAdapter(p.NewFactory(customOption())))", "boot.Options{HTTP:&boot.HTTPOptions{}}", "HTTP_ADAPTER_UNRESOLVED"},
		{"expanded-options", "p.New(options...)", "boot.Options{HTTP:&boot.HTTPOptions{}}", "HTTP_ADAPTER_UNRESOLVED"},
		{"removed-factory", "s.NewWithHTTPAdapter(p.NewFactory())", "boot.Options{HTTP:&boot.HTTPOptions{}}", "HTTP_ADAPTER_API_REMOVED"},
		{"missing", "s.New()", "boot.Options{HTTP:&boot.HTTPOptions{}}", "HTTP_ADAPTER_MISSING"},
		{"unknown-factory", "s.NewWithHTTPAdapter(customFactory())", "boot.Options{HTTP:&boot.HTTPOptions{}}", "HTTP_ADAPTER_UNRESOLVED"},
		{"unknown-option", "s.New(customOption())", "boot.Options{HTTP:&boot.HTTPOptions{}}", "HTTP_ADAPTER_UNRESOLVED"},
		{"unknown-http", "p.New()", "dynamicOptions()", "HTTP_CONFIGURATION_UNRESOLVED"},
		{"worker", "s.New()", "boot.Options{}", ""},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			inTempDir(t)
			var out, errOut bytes.Buffer
			if code := runNew([]string{"todo", "--module", "example.com/todo", "--skip-install"}, &out, &errOut); code != 0 {
				t.Fatal(errOut.String())
			}
			if err := os.Chdir("todo"); err != nil {
				t.Fatal(err)
			}
			path := "internal/application/application.go"
			source := `package application
import (s "github.com/NARUBROWN/spine"; p "github.com/NARUBROWN/spine-echo-platform"; "github.com/NARUBROWN/spine/pkg/boot"; "github.com/labstack/echo/v5")
func BuildApplication() (s.App, boot.Options, error) { app := ` + scenario.expression + `; return app, ` + scenario.options + `, nil }
`
			writeE2EFile(t, path, source)
			contract, err := inspectApplication()
			if err != nil {
				t.Fatal(err)
			}
			codes := []string{}
			for _, diagnostic := range contract.Diagnostics {
				if diagnostic.Severity == "error" {
					codes = append(codes, diagnostic.Code)
				}
			}
			if scenario.diagnostic == "" && len(codes) != 0 {
				t.Fatalf("unexpected diagnostics: %+v", contract.Diagnostics)
			}
			if scenario.diagnostic != "" && !strings.Contains(strings.Join(codes, ","), scenario.diagnostic) {
				t.Fatalf("missing %s: %+v", scenario.diagnostic, contract.Diagnostics)
			}
			if scenario.name == "platform" {
				baseline, _ := json.Marshal(semanticView(contract))
				unaliased := strings.ReplaceAll(strings.Replace(source, `p "github.com/NARUBROWN/spine-echo-platform"`, `"github.com/NARUBROWN/spine-echo-platform"`, 1), "p.New()", "platform.New()")
				writeE2EFile(t, path, unaliased)
				implicit, err := inspectApplication()
				if err != nil {
					t.Fatal(err)
				}
				implicitJSON, _ := json.Marshal(semanticView(implicit))
				if !bytes.Equal(baseline, implicitJSON) {
					t.Fatalf("unaliased platform import changed semantics: %s", implicitJSON)
				}
				writeE2EFile(t, path, strings.Replace(source, "p.New()", "s.New(s.WithHTTPAdapter(p.NewFactory()))", 1))
				current, err := inspectApplication()
				if err != nil {
					t.Fatal(err)
				}
				actual, _ := json.Marshal(semanticView(current))
				if !bytes.Equal(baseline, actual) {
					t.Fatalf("equivalent forms changed semantics:\n%s\n%s", baseline, actual)
				}
			}
		})
	}
}

func TestSelectedModulesDoesNotChangeProjectFiles(t *testing.T) {
	if err := prepareTestModuleCache(); err != nil {
		t.Fatal(err)
	}
	inTempDir(t)
	writeE2EFile(t, "go.mod", "module example.com/legacy\n\ngo 1.25.5\n\nrequire github.com/NARUBROWN/spine v0.5.2\n")
	before, _ := os.ReadFile("go.mod")
	version, err := spineDependencyVersion("go.mod")
	if err != nil || version != "v0.5.2" {
		t.Fatalf("version=%s err=%v", version, err)
	}
	after, _ := os.ReadFile("go.mod")
	if !bytes.Equal(before, after) {
		t.Fatal("module inspection changed go.mod")
	}
	if _, err := os.Stat(filepath.Join(".", "go.sum")); !os.IsNotExist(err) {
		t.Fatalf("offline inspection wrote go.sum: %v", err)
	}
}

func TestHTTPOptionsMutationIsUnresolved(t *testing.T) {
	for _, body := range []string{
		`options := boot.Options{}; options.HTTP = &boot.HTTPOptions{}; return app, options, nil`,
		`options := boot.Options{HTTP:&boot.HTTPOptions{}}; configure(&options); return app, options, nil`,
		`options := boot.Options{}; alias := &options; alias.HTTP = &boot.HTTPOptions{}; return app, options, nil`,
	} {
		file, err := parser.ParseFile(token.NewFileSet(), "app.go", `package application; import "github.com/NARUBROWN/spine/pkg/boot"; func BuildApplication() { app := factory(); `+body+` }`, 0)
		if err != nil {
			t.Fatal(err)
		}
		function := file.Decls[len(file.Decls)-1].(*ast.FuncDecl)
		if enabled := returnedHTTPEnabled(function, file); enabled != nil {
			t.Fatalf("mutable options treated as known: %s", body)
		}
	}
}

func TestCompositionDoesNotRecognizeOtherOrShadowedPackages(t *testing.T) {
	for _, source := range []string{
		`package application; import p "example.com/other"; func Build() { app := p.New() }`,
		`package application; import p "github.com/NARUBROWN/spine-echo-platform"; func Build() { p := other(); app := p.New() }`,
	} {
		file, err := parser.ParseFile(token.NewFileSet(), "app.go", source, 0)
		if err != nil {
			t.Fatal(err)
		}
		function := file.Decls[len(file.Decls)-1].(*ast.FuncDecl)
		for _, statement := range function.Body.List {
			if _, ok := applicationAssignment(statement, file); ok {
				t.Fatalf("arbitrary factory recognized: %s", source)
			}
		}
	}
}

func TestOfflineModuleInspectorRejectsChangedSelection(t *testing.T) {
	inTempDir(t)
	writeE2EFile(t, "go.mod", "module example.com/flags\n\ngo 1.25.5\n")
	writeE2EFile(t, "alternate.mod", "module example.com/alternate\n\ngo 1.25.5\n")
	t.Setenv("GOFLAGS", "-modfile=alternate.mod")
	if _, err := spineDependencyVersion("go.mod"); err == nil || !strings.Contains(err.Error(), "GOFLAGS") {
		t.Fatalf("alternate module selection was ignored: %v", err)
	}
}

func TestPlatformGoHonorsSavedConfigurationAndClearsSavedFlags(t *testing.T) {
	inTempDir(t)
	configuration := filepath.Join(t.TempDir(), "go-env")
	cache := filepath.Join(t.TempDir(), "modules")
	contents := "GOMODCACHE=" + cache + "\nGOPROXY=https://proxy.example.test\nGOFLAGS=-modfile=missing.mod\n"
	writeE2EFile(t, configuration, contents)
	t.Setenv("GOENV", configuration)
	t.Setenv("GOMODCACHE", "")
	t.Setenv("GOPROXY", "")
	t.Setenv("GOFLAGS", "")
	output, err := platformGo(context.Background(), ".", false, "env", "-json", "GOMODCACHE", "GOPROXY", "GOFLAGS", "GOWORK", "GOTOOLCHAIN")
	if err != nil {
		t.Fatal(err)
	}
	var values map[string]string
	if err := json.Unmarshal(output, &values); err != nil {
		t.Fatal(err)
	}
	if values["GOMODCACHE"] != cache || values["GOPROXY"] != "https://proxy.example.test" || strings.TrimSpace(values["GOFLAGS"]) != "" || values["GOWORK"] != "off" || values["GOTOOLCHAIN"] != "local" {
		t.Fatalf("unexpected Go configuration: %s", output)
	}
	after, _ := os.ReadFile(configuration)
	if string(after) != contents {
		t.Fatal("saved Go configuration was modified")
	}
}

func TestDoctorAndContextExposeSelectedAdapter(t *testing.T) {
	layeredProject(t)
	report := inspectDoctorReport()
	if !report.OK || report.HTTPAdapter == nil || report.HTTPAdapter.Provider != "echo" || selectedModuleVersion(report.HTTPAdapter.SelectedModules, echoPlatformPath) != echoPlatformVersion {
		t.Fatalf("doctor lost adapter metadata: %+v", report)
	}
	var out, errOut bytes.Buffer
	if code := runContext([]string{"--json"}, &out, &errOut); code != 0 {
		t.Fatalf("context: %s", errOut.String())
	}
	var result projectContext
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Config.Platform != "echo" || result.Contract == nil || result.Contract.HTTPAdapter == nil || result.Contract.HTTPAdapter.CanonicalFactory != echoPlatformPath+".NewFactory" {
		t.Fatalf("context lost selected adapter: %s", out.String())
	}
}
