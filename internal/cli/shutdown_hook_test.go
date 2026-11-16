package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

func TestShutdownHookDoesNotChangeAdapterSelection(t *testing.T) {
	for _, tc := range []struct {
		name, option string
		valid        bool
	}{
		{"callback", `s.WithShutdownHook(func(context.Context) error { panic("must never execute") })`, true},
		{"dependency callback", `s.WithShutdownHook(cleanup)`, true},
		{"multiple hooks", `s.WithShutdownHook(first), s.WithShutdownHook(second)`, true},
		{"missing argument", `s.WithShutdownHook()`, false},
		{"extra argument", `s.WithShutdownHook(first, second)`, false},
		{"expanded argument", `s.WithShutdownHook(callbacks...)`, false},
		{"different package", `other.WithShutdownHook(cleanup)`, false},
		{"unknown option", `customOption()`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			imports := map[string]string{"s": spineImportPath, "p": nethttpPlatformPath, "other": "example.com/other"}
			expression, err := parser.ParseExpr("s.New(p.Option()," + tc.option + ")")
			if err != nil {
				t.Fatal(err)
			}
			modules := []selectedModule{{Path: nethttpPlatformPath, Version: "v0.1.0-preview.2"}}
			for name, selection := range map[string]string{"builtin": adapterSelection(expression, imports), "descriptor": dynamicAdapterSelection(expression, imports, modules)} {
				expected := "unresolved"
				if tc.valid {
					expected = "nethttp"
				}
				if selection != expected {
					t.Fatalf("%s: got %s, want %s", name, selection, expected)
				}
			}
		})
	}
	t.Run("shadowed import", func(t *testing.T) {
		file, err := parser.ParseFile(token.NewFileSet(), "app.go", `package app
import s "github.com/NARUBROWN/spine"
func build(){ s:=other; _=s.WithShutdownHook(cleanup) }`, 0)
		if err != nil {
			t.Fatal(err)
		}
		function := file.Decls[1].(*ast.FuncDecl)
		call := function.Body.List[1].(*ast.AssignStmt).Rhs[0]
		if knownShutdownHook(call, importPathsByAlias(file)) {
			t.Fatal("shadowed package accepted")
		}
	})
}
