package cli

import (
	"go/parser"
	"testing"
)

func TestDynamicTransportContractRejectsUnknownAndDuplicateOptions(t *testing.T) {
	root := t.TempDir()
	path := providerModule("sample-http")
	writeTestDescriptor(t, root, &platformDescriptor{SchemaVersion: platformDescriptorSchema, Kind: "http", Provider: "sample-http", ContractVersion: "spine.http-adapter/v3", Package: "transport", Factory: "Attach", MinimumGo: "1.25.5"})
	modules := []selectedModule{{Path: path, Version: "v0.1.0", Dir: root}}
	imports := map[string]string{"spine": spineImportPath, "custom": path}
	for _, scenario := range []struct{ expr, want string }{
		{"spine.New(custom.Attach())", "sample-http"},
		{"spine.New(custom.Attach(), custom.Attach())", "unresolved"},
		{"spine.New(custom.Attach(), unknown())", "unresolved"},
		{"spine.New(custom.Attach(unknown()))", "unresolved"},
		{"spine.New(custom.Other())", "unresolved"},
	} {
		expr, err := parser.ParseExpr(scenario.expr)
		if err != nil {
			t.Fatal(err)
		}
		if got := dynamicAdapterSelection(expr, imports, modules); got != scenario.want {
			t.Fatalf("%s: got %s, want %s", scenario.expr, got, scenario.want)
		}
	}
}
