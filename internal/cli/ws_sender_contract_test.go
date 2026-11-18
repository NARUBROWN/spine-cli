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

func TestWebSocketMethodExpressionRejectsSpineContext(t *testing.T) {
	for _, tc := range []struct {
		name, path, typeName string
		wantError            bool
	}{
		{"standard", "context", "Context", false},
		{"http", spineImportPath + "/pkg/spine", "Ctx", true},
		{"frame", spineImportPath + "/pkg/spine", "WebSocketCtx", false},
		{"event", spineImportPath + "/pkg/spine", "EventCtx", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pkg := types.NewPackage(tc.path, "context")
			parameter := types.NewNamed(types.NewTypeName(token.NoPos, pkg, tc.typeName, nil), types.NewInterfaceType(nil, nil).Complete(), nil)
			method := types.NewSignatureType(nil, nil, nil, types.NewTuple(types.NewVar(token.NoPos, nil, "ctx", parameter)), types.NewTuple(), false)
			inspector := &contractInspector{contract: &applicationContract{}, controllerMethods: map[string]*types.Signature{"Socket.Handle": method}}
			inspector.checkWebSocketSignature(&namedRegistration{Handler: "Socket.Handle"}, staticHandlerSignature{}, nil)
			rejected := false
			for _, d := range inspector.contract.Diagnostics {
				rejected = rejected || d.Code == "WEBSOCKET_PARAMETER_UNSUPPORTED"
			}
			if rejected != tc.wantError {
				t.Fatal(inspector.contract.Diagnostics)
			}
		})
	}
}
