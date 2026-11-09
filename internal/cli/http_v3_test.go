package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHTTPContractReadsSelectedSource(t *testing.T) {
	for _, version := range []string{"v2", "v3"} {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, "contract/http"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "contract/http/contract.go"), []byte("package httpcontract\nconst ContractVersion = \"spine.http-adapter/"+version+"\""), 0644); err != nil {
			t.Fatal(err)
		}
		for _, replaced := range []bool{false, true} {
			module := selectedModule{Path: spineImportPath, Version: "v0.7.0-preview.2.0.20261106020000-abcdefabcdef", Dir: root}
			if replaced {
				module.Dir = "missing"
				module.Replace = &selectedModule{Dir: root}
			}
			if got := selectedHTTPContract([]selectedModule{module}, httpAdapterContract); got != "spine.http-adapter/"+version {
				t.Fatalf("%s replaced=%v: %s", version, replaced, got)
			}
		}
	}
}

func TestNetHTTPGenerationAndStaticSelection(t *testing.T) {
	for _, layout := range []string{layeredLayout, "legacy"} {
		options, err := parseNewOptions([]string{"sample", "--platform", "nethttp", "--layout", layout})
		if err != nil {
			t.Fatal(err)
		}
		files, err := newProjectFiles(options)
		if err != nil {
			t.Fatal(err)
		}
		source := files["internal/application/application.go"]
		if !strings.Contains(source, "nethttpplatform.Option()") || !strings.Contains(source, "HTTP: &boot.HTTPOptions{}") || strings.Contains(source, "echoplatform") {
			t.Fatal(source)
		}
		file, err := parser.ParseFile(token.NewFileSet(), "application.go", source, 0)
		if err != nil {
			t.Fatal(err)
		}
		var expression ast.Expr
		ast.Inspect(file, func(n ast.Node) bool {
			if stmt, ok := n.(*ast.AssignStmt); ok && len(stmt.Rhs) == 1 {
				if _, ok := importedCall(stmt.Rhs[0], importPathsByAlias(file), spineImportPath, "New"); ok {
					expression = stmt.Rhs[0]
				}
			}
			return true
		})
		if got := adapterSelection(expression, importPathsByAlias(file)); got != "nethttp" {
			t.Fatal(got)
		}
		if selectedModuleVersion(currentAssemblyModules(options), nethttpPlatformPath) != "preview" {
			t.Fatal("net/http not dynamically resolved")
		}
	}
}
