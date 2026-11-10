package cli

import (
	"fmt"
	"go/ast"
	"net/http"
	"strings"
	"unicode"
)

func (catalog *httpBindingCatalog) markerShape(source *layoutSource, node *ast.IndexExpr, visiting map[string]bool) (httpBindingShape, bool) {
	key := canonicalType(source, node.X, catalog.module)
	prefix := spineImportPath + "/pkg/input:"
	if !strings.HasPrefix(key, prefix) {
		return httpBindingShape{}, false
	}
	marker := strings.TrimPrefix(key, prefix)
	binding := ""
	switch marker {
	case "Path":
		binding = "path"
	case "Query":
		binding = "query"
	case "Header":
		binding = "header"
	case "Body":
		binding = "body"
	default:
		return httpBindingShape{}, false
	}
	if binding == "path" {
		if scalar, ok := catalog.scalarPathPayload(source, node.Index, map[string]bool{}); ok {
			return httpBindingShape{binding: "path", kind: "pointer", source: "path", scalarPath: true, payload: scalar, payloadSource: source.Path}, true
		}
	}
	payloadSource, structure, ok := catalog.markerPayload(source, node.Index, map[string]bool{})
	if !ok {
		return httpBindingShape{kind: "unsupported", problem: "input." + marker + " requires a concrete struct payload"}, true
	}
	fields, known := catalog.inputFields(payloadSource, structure, visiting)
	if !known {
		return httpBindingShape{kind: "unknown", problem: "Cannot resolve embedded input fields"}, true
	}
	shape := httpBindingShape{binding: binding, kind: "pointer", payload: expressionText(source.Fset, node.Index), payloadSource: source.Path}
	if binding != "body" {
		shape.source = binding
	}
	seen := map[string]bool{}
	for _, field := range fields {
		for _, other := range []string{"path", "query", "header", "form", "param"} {
			if other != binding && (binding != "body" || other != "form") {
				if value, exists := field.tags.Lookup(other); exists && value != "-" && (value != "" || binding == "body") {
					return httpBindingShape{kind: "unsupported", problem: "A DTO mixes input sources"}, true
				}
			}
		}
		if binding == "body" {
			continue
		}
		name := field.tags.Get(binding)
		if name == "-" {
			continue
		}
		if name == "" {
			name = controllerLowerCamel(field.name)
		}
		if strings.ContainsAny(name, ",\r\n\t") {
			return httpBindingShape{kind: "unsupported", problem: fmt.Sprintf("Invalid %s field %q", binding, name)}, true
		}
		if binding == "header" {
			for _, r := range name {
				if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", r)) {
					return httpBindingShape{kind: "unsupported", problem: fmt.Sprintf("Invalid header field %q", name)}, true
				}
			}
			name = http.CanonicalHeaderKey(name)
		}
		if seen[name] {
			return httpBindingShape{kind: "unsupported", problem: "Duplicate input field " + name}, true
		}
		seen[name] = true
		shape.keys = append(shape.keys, name)
		descriptor := handlerInputField{Name: name, Type: expressionText(field.source.Fset, field.expression), source: field.source.Path, description: field.tags.Get("description")}
		if example, ok := field.tags.Lookup("example"); ok {
			descriptor.example, descriptor.hasExample = staticFieldExample(field.expression, example, field.source)
			descriptor.invalidExample = !descriptor.hasExample
		}
		shape.fields = append(shape.fields, descriptor)
	}
	return shape, true
}

func (catalog *httpBindingCatalog) markerPayload(source *layoutSource, expression ast.Expr, visiting map[string]bool) (*layoutSource, *ast.StructType, bool) {
	if value, ok := expression.(*ast.ParenExpr); ok {
		return catalog.markerPayload(source, value.X, visiting)
	}
	if structure, ok := expression.(*ast.StructType); ok {
		return source, structure, true
	}
	key := canonicalType(source, expression, catalog.module)
	declaration, ok := catalog.types[key]
	if !ok || visiting[key] {
		return nil, nil, false
	}
	visiting[key] = true
	defer delete(visiting, key)
	return catalog.markerPayload(declaration.source, declaration.spec.Type, visiting)
}

func controllerLowerCamel(name string) string {
	runes := []rune(name)
	var words []string
	start := 0
	for index := 1; index < len(runes); index++ {
		if unicode.IsUpper(runes[index]) && (unicode.IsLower(runes[index-1]) || unicode.IsDigit(runes[index-1]) || unicode.IsUpper(runes[index-1]) && index+1 < len(runes) && unicode.IsLower(runes[index+1])) {
			words = append(words, strings.ToLower(string(runes[start:index])))
			start = index
		}
	}
	words = append(words, strings.ToLower(string(runes[start:])))
	for index := 1; index < len(words); index++ {
		chars := []rune(words[index])
		if len(chars) > 0 {
			chars[0] = unicode.ToUpper(chars[0])
		}
		words[index] = string(chars)
	}
	return strings.Join(words, "")
}

// scalarPathPayload는 기본 타입과 타입 별칭만 해석하고 새 정의 타입은 거부합니다.
func (catalog *httpBindingCatalog) scalarPathPayload(source *layoutSource, expression ast.Expr, visiting map[string]bool) (string, bool) {
	if paren, ok := expression.(*ast.ParenExpr); ok {
		return catalog.scalarPathPayload(source, paren.X, visiting)
	}
	if ident, ok := expression.(*ast.Ident); ok {
		switch ident.Name {
		case "bool", "string", "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "uintptr", "float32", "float64", "byte", "rune":
			if _, shadowed := catalog.types[canonicalType(source, expression, catalog.module)]; !shadowed {
				if ident.Name == "byte" {
					return "uint8", true
				}
				if ident.Name == "rune" {
					return "int32", true
				}
				return ident.Name, true
			}
		}
	}
	key := canonicalType(source, expression, catalog.module)
	declaration, ok := catalog.types[key]
	if !ok || !declaration.spec.Assign.IsValid() || visiting[key] {
		return "", false
	}
	visiting[key] = true
	defer delete(visiting, key)
	return catalog.scalarPathPayload(declaration.source, declaration.spec.Type, visiting)
}
