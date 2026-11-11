package cli

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/types"
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
	return catalog.inputMarker(source, strings.TrimPrefix(key, prefix), node.Index, visiting)
}

func (catalog *httpBindingCatalog) inputMarker(source *layoutSource, marker string, payload ast.Expr, visiting map[string]bool) (httpBindingShape, bool) {
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
		if scalar, ok := catalog.scalarPathPayload(source, payload, map[string]bool{}); ok {
			return httpBindingShape{binding: "path", kind: "pointer", source: "path", scalarPath: true, payload: scalar, payloadSource: source.Path}, true
		}
	}
	payloadSource, structure, ok := catalog.markerPayload(source, payload, map[string]bool{})
	if !ok {
		if catalog.unresolvedMarkerPayload(source, payload, map[string]bool{}) {
			return httpBindingShape{kind: "unknown"}, true
		}
		return httpBindingShape{kind: "unsupported", problem: "input." + marker + " requires a concrete struct payload"}, true
	}
	if binding == "body" {
		if problem := catalog.bodyProblem(source, payload, map[string]bool{}); problem != "" {
			return httpBindingShape{kind: "unsupported", problem: problem}, true
		}
		return httpBindingShape{binding: "body", kind: "pointer", payload: expressionText(source.Fset, payload), payloadSource: source.Path}, true
	}
	fields, known := catalog.inputFields(payloadSource, structure, visiting)
	if !known {
		return httpBindingShape{kind: "unknown", problem: "Cannot resolve embedded input fields"}, true
	}
	shape := httpBindingShape{binding: binding, kind: "pointer", payload: expressionText(source.Fset, payload), payloadSource: source.Path}
	shape.source = binding
	seen := map[string]bool{}
	concrete := catalog.instanceFields(source, payload, binding)
	for _, field := range fields {
		if problem := unsupportedInputTag(field.tags); problem != "" {
			return httpBindingShape{kind: "unsupported", problem: problem}, true
		}
		for _, other := range []string{"path", "query", "header", "form", "param"} {
			if other != binding {
				if value, exists := field.tags.Lookup(other); exists && value != "-" && value != "" {
					return httpBindingShape{kind: "unsupported", problem: "A DTO mixes input sources"}, true
				}
			}
		}
		name := field.tags.Get(binding)
		if name == "-" {
			continue
		}
		if name == "" {
			name = controllerLowerCamel(field.name)
		}
		if strings.Contains(name, ",") || strings.IndexFunc(name, unicode.IsControl) >= 0 {
			return httpBindingShape{kind: "unsupported", problem: fmt.Sprintf("Invalid %s field %q", binding, name)}, true
		}
		if binding == "header" {
			for _, r := range name {
				if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", r)) {
					return httpBindingShape{kind: "unsupported", problem: fmt.Sprintf("Invalid header field %q", name)}, true
				}
			}
			name = http.CanonicalHeaderKey(name)
			if list, ok := field.expression.(*ast.ArrayType); ok && list.Len == nil && !controllerHeaderListAllowed(name) {
				return httpBindingShape{kind: "unsupported", problem: fmt.Sprintf("Header %s does not support generic list binding", name)}, true
			}
		}
		if seen[name] {
			return httpBindingShape{kind: "unsupported", problem: "Duplicate input field " + name}, true
		}
		seen[name] = true
		shape.keys = append(shape.keys, name)
		descriptor := handlerInputField{Name: name, Type: expressionText(field.source.Fset, field.expression), source: field.source.Path, description: field.tags.Get("description")}
		exampleType := field.expression
		if instantiated := concrete[name]; instantiated != nil {
			descriptor.Type = types.TypeString(instantiated, func(pkg *types.Package) string { return pkg.Name() })
			if expression, err := parser.ParseExpr(descriptor.Type); err == nil {
				exampleType = expression
			}
		}
		if example, ok := field.tags.Lookup("example"); ok {
			descriptor.example, descriptor.hasExample = staticFieldExample(exampleType, example, field.source)
			descriptor.invalidExample = !descriptor.hasExample
		}
		shape.fields = append(shape.fields, descriptor)
	}
	return shape, true
}

// inputAlias uses the instantiated Go type to resolve aliases and reordered type arguments.
func (catalog *httpBindingCatalog) inputAlias(source *layoutSource, expression ast.Expr, visiting map[string]bool) (httpBindingShape, bool) {
	instance := catalog.instantiatedHTTPType(source, expression)
	if instance == nil || instance.Obj().Pkg() == nil || instance.Obj().Pkg().Path() != spineImportPath+"/pkg/input" || instance.TypeArgs().Len() != 1 {
		return httpBindingShape{}, false
	}
	argument, err := parser.ParseExpr(catalog.httpTypeText(source, instance.TypeArgs().At(0)))
	if err != nil {
		return httpBindingShape{}, false
	}
	return catalog.inputMarker(source, instance.Obj().Name(), argument, visiting)
}

func (catalog *httpBindingCatalog) httpTypeText(source *layoutSource, payload types.Type) string {
	local := source.ImportPath
	if local == "" {
		local = modulePackage(catalog.module, source.Package)
	}
	imports := importPathsByAlias(source.File)
	return types.TypeString(payload, func(pkg *types.Package) string {
		if pkg.Path() == local {
			return ""
		}
		for alias, path := range imports {
			if pkg.Path() == path {
				return alias
			}
		}
		return pkg.Name()
	})
}

// unresolvedMarkerPayload는 찾지 못한 외부 선언을 잘못된 입력 타입으로 단정하지 않습니다.
func (catalog *httpBindingCatalog) unresolvedMarkerPayload(source *layoutSource, expression ast.Expr, visiting map[string]bool) bool {
	if node, ok := expression.(*ast.ParenExpr); ok {
		return catalog.unresolvedMarkerPayload(source, node.X, visiting)
	}
	key := canonicalType(source, expression, catalog.module)
	if declaration, exists := catalog.types[key]; exists {
		if visiting[key] {
			return true
		}
		visiting[key] = true
		defer delete(visiting, key)
		return catalog.unresolvedMarkerPayload(declaration.source, declaration.spec.Type, visiting)
	}
	switch node := expression.(type) {
	case *ast.SelectorExpr, *ast.IndexExpr, *ast.IndexListExpr:
		return true
	case *ast.Ident:
		return types.Universe.Lookup(node.Name) == nil
	}
	return false
}

func (catalog *httpBindingCatalog) markerPayload(source *layoutSource, expression ast.Expr, visiting map[string]bool) (*layoutSource, *ast.StructType, bool) {
	if value, ok := expression.(*ast.ParenExpr); ok {
		return catalog.markerPayload(source, value.X, visiting)
	}
	if structure, ok := expression.(*ast.StructType); ok {
		return source, structure, true
	}
	base, arguments := genericType(expression)
	key := canonicalType(source, base, catalog.module)
	declaration, ok := catalog.types[key]
	if !ok || visiting[key] {
		return nil, nil, false
	}
	if arguments != 0 {
		if declaration.spec.TypeParams == nil {
			return nil, nil, false
		}
		count := 0
		for _, field := range declaration.spec.TypeParams.List {
			count += len(field.Names)
		}
		if count != arguments {
			return nil, nil, false
		}
	}
	visiting[key] = true
	defer delete(visiting, key)
	return catalog.markerPayload(declaration.source, declaration.spec.Type, visiting)
}

func genericType(expression ast.Expr) (ast.Expr, int) {
	switch node := expression.(type) {
	case *ast.IndexExpr:
		return node.X, 1
	case *ast.IndexListExpr:
		return node.X, len(node.Indices)
	default:
		return expression, 0
	}
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
