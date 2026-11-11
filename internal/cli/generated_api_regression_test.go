package cli

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func assertCurrentGeneratedAPIs(t *testing.T, files map[string]string) {
	t.Helper()
	for path, source := range files {
		if !strings.HasSuffix(path, ".go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, source, 0)
		if err != nil {
			t.Fatal(err)
		}
		imports := map[string]string{}
		for _, spec := range file.Imports {
			imported, _ := strconv.Unquote(spec.Path.Value)
			alias := filepath.Base(imported)
			if spec.Name != nil {
				alias = spec.Name.Name
			}
			imports[alias] = imported
			switch imported {
			case spineImportPath + "/pkg/path", spineImportPath + "/pkg/query", spineImportPath + "/pkg/header", spineImportPath + "/pkg/multipart", spineImportPath + "/pkg/route":
				t.Errorf("%s imports a deprecated application API: %s", path, imported)
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if base, ok := selector.X.(*ast.Ident); ok && imports[base.Name] == spineImportPath+"/core" {
				switch selector.Sel.Name {
				case "ControllerContext", "ExecutionContext", "HttpRequestContext", "HTTPRequest":
					t.Errorf("%s references deprecated core.%s", path, selector.Sel.Name)
				}
			}
			return true
		})
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if selector.Sel.Name == "Route" {
				t.Errorf("%s generates deprecated App.Route:\n%s", path, source)
			}
			if selector.Sel.Name == "Constructor" {
				for _, arg := range call.Args {
					if literal, ok := arg.(*ast.UnaryExpr); ok && literal.Op == token.AND {
						t.Errorf("%s passes an unmarked object to App.Constructor", path)
					}
				}
			}
			return true
		})
	}
}

func TestSupportedCoreScaffoldsDoNotUseDeprecatedAPIs(t *testing.T) {
	for _, version := range []string{"v0.7.2-preview.3", "v0.7.5-preview.1"} {
		for _, layout := range []string{layeredLayout, "legacy"} {
			t.Run(version+"/"+layout, func(t *testing.T) {
				options, err := parseNewOptions([]string{"sample", "--module", "example.com/sample", "--platform", "nethttp", "--layout", layout})
				if err != nil {
					t.Fatal(err)
				}
				options.Modules = []selectedModule{{Path: spineImportPath, Version: version}, {Path: nethttpPlatformPath, Version: "v0.1.0-preview.2"}}
				files, err := newProjectFiles(options)
				if err != nil {
					t.Fatal(err)
				}
				assertCurrentGeneratedAPIs(t, files)
			})
		}
	}
}

func TestLegacyLayoutGeneratorsUseCurrentAPIsOnSupportedCores(t *testing.T) {
	for _, version := range []string{"v0.7.2-preview.3", "v0.7.5-preview.1"} {
		for _, kind := range []string{"resource", "slice", "controller"} {
			t.Run(version+"/"+kind, func(t *testing.T) {
				inTempDir(t)
				options, err := parseNewOptions([]string{"sample", "--module", "example.com/sample", "--platform", "nethttp", "--layout", "legacy"})
				if err != nil {
					t.Fatal(err)
				}
				options.Modules = []selectedModule{{Path: spineImportPath, Version: version}, {Path: nethttpPlatformPath, Version: "v0.1.0-preview.2"}}
				files, err := newProjectFiles(options)
				if err != nil {
					t.Fatal(err)
				}
				for path, source := range files {
					if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
						t.Fatal(err)
					}
					writeE2EFile(t, path, source)
				}
				if err := os.MkdirAll("src", 0755); err != nil {
					t.Fatal(err)
				}
				args := []string{kind, "product", "--no-register", "--json"}
				if kind == "slice" {
					args = append(args, "--storage", "memory", "--field", "name:string")
				}
				var out, errOut bytes.Buffer
				if code := runGenerate(args, &out, &errOut); code != 0 {
					t.Fatalf("generation failed: code=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
				}
				generated := map[string]string{}
				if err := filepath.WalkDir(".", func(path string, entry os.DirEntry, err error) error {
					if err != nil {
						return err
					}
					if entry.IsDir() || !strings.HasSuffix(path, ".go") {
						return nil
					}
					source, err := os.ReadFile(path)
					if err == nil {
						generated[path] = string(source)
					}
					return err
				}); err != nil {
					t.Fatal(err)
				}
				assertCurrentGeneratedAPIs(t, generated)
			})
		}
	}
}
