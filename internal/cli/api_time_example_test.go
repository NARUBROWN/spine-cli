package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"testing"
)

func TestStaticTimeExamples(t *testing.T) {
	const input = "package dto\nimport (clk \"time\"; other \"example.com/other\")\ntype Payload struct {\n" +
		" Created clk.Time `json:\"created\" example:\"2024-01-02T03:04:05Z\"`\n" +
		" Updated *clk.Time `json:\"updated\" example:\"2024-02-29T03:04:05.123456789+09:00\"`\n" +
		" Quoted clk.Time `json:\"quoted,string\" example:\"2024-01-02T03:04:05Z\"`\n" +
		" Invalid clk.Time `json:\"invalid\" example:\"2024-02-30T03:04:05Z\"`\n" +
		" Other other.Time `json:\"other\" example:\"2024-01-02T03:04:05Z\"`\n}"
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "payload.go", input, 0)
	if err != nil {
		t.Fatal(err)
	}
	source := &layoutSource{File: file, Fset: fset}
	spec := file.Decls[1].(*ast.GenDecl).Specs[0].(*ast.TypeSpec)
	catalog := &apiSchemas{}
	properties := catalog.structure(spec.Type.(*ast.StructType), source)["properties"].(map[string]any)
	for name, want := range map[string]string{"created": "2024-01-02T03:04:05Z", "updated": "2024-02-29T03:04:05.123456789+09:00", "quoted": "2024-01-02T03:04:05Z"} {
		schema := properties[name].(map[string]any)
		if schema["type"] != "string" || schema["format"] != "date-time" || !reflect.DeepEqual(schema["examples"], []any{want}) {
			t.Fatalf("%s: %#v", name, schema)
		}
	}
	for _, name := range []string{"invalid", "other"} {
		schema := properties[name].(map[string]any)
		if schema["x-spine-unresolved-example"] != true || schema["examples"] != nil {
			t.Fatalf("%s: %#v", name, schema)
		}
	}
	expression, err := parser.ParseExpr("(*clk.Time)")
	if err != nil {
		t.Fatal(err)
	}
	if value, ok := staticFieldExample(expression, "2024-01-02T03:04:05Z", source); !ok || value != "2024-01-02T03:04:05Z" {
		t.Fatalf("pointer expression: %v %v", value, ok)
	}
}
