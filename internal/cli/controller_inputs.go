package cli

import (
	"fmt"
	"go/ast"
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
	case "Body":
		binding = "body"
	default:
		return httpBindingShape{}, false
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
		if seen[name] {
			return httpBindingShape{kind: "unsupported", problem: "Duplicate input field " + name}, true
		}
		seen[name] = true
		shape.keys = append(shape.keys, name)
		shape.fields = append(shape.fields, handlerInputField{Name: name, Type: expressionText(field.source.Fset, field.expression), source: field.source.Path})
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
