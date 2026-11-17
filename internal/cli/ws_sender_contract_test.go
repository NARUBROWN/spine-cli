package cli

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"testing"
)

func TestWebSocketSenderAutomaticDependencyRequiresNewContract(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		want         bool
	}{
		{"legacy", `package ws;type Sender interface{Send(int,[]byte)error}`, false},
		{"application", `package ws;import "context";type Sender interface{Send(context.Context,string,int,[]byte)error}`, true},
		{"different method", `package ws;type Sender interface{Close()error}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			set := token.NewFileSet()
			file, err := parser.ParseFile(set, "sender.go", tc.source, 0)
			if err != nil {
				t.Fatal(err)
			}
			config := types.Config{Importer: importer.Default()}
			pkg, err := config.Check(spineImportPath+"/pkg/ws", set, []*ast.File{file}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if got := isApplicationProvidedWSDependency(pkg.Scope().Lookup("Sender").Type()); got != tc.want {
				t.Fatalf("auto dependency=%v want=%v", got, tc.want)
			}
		})
	}
}
