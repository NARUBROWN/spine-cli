package cli

import (
	"crypto/sha256"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

type apiType struct {
	source *layoutSource
	spec   *ast.TypeSpec
}

// apiSchemas는 타입을 선언한 패키지와 import 범위를 기준으로 문서 참조를 만듭니다.
type apiSchemas struct {
	module       string
	sources      layoutSources
	declarations map[string]apiType
	byName       map[string][]string
	ids          map[string]string
	schemas      map[string]any
}

func newAPISchemas(module string) (*apiSchemas, error) {
	sources, _, err := readLayoutSources()
	if err != nil {
		return nil, err
	}
	result := &apiSchemas{module: module, sources: sources, declarations: map[string]apiType{}, byName: map[string][]string{}, ids: map[string]string{}, schemas: map[string]any{}}
	for _, source := range sources.sortedFiles() {
		for _, declaration := range source.File.Decls {
			group, ok := declaration.(*ast.GenDecl)
			if !ok || group.Tok != token.TYPE {
				continue
			}
			for _, specification := range group.Specs {
				spec := specification.(*ast.TypeSpec)
				key := modulePackage(module, source.Package) + ":" + spec.Name.Name
				if _, exists := result.declarations[key]; exists {
					return nil, fmt.Errorf("duplicate Go type declaration: %s", key)
				}
				result.declarations[key] = apiType{source, spec}
				result.byName[spec.Name.Name] = append(result.byName[spec.Name.Name], key)
			}
		}
	}
	used := map[string]string{}
	names := make([]string, 0, len(result.byName))
	for name := range result.byName {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		for _, key := range result.byName[name] {
			id := name
			if len(result.byName[name]) > 1 {
				id = stableDocumentID(name, key)
			}
			if old, exists := used[id]; exists && old != key {
				return nil, fmt.Errorf("API schema identifier collision between %s and %s", old, key)
			}
			used[id], result.ids[key] = key, id
		}
	}
	return result, nil
}

func stableDocumentID(base string, values ...string) string {
	digest := sha256.New()
	for _, value := range values {
		fmt.Fprintf(digest, "%d:%s\n", len(value), value)
	}
	return fmt.Sprintf("%s_%x", base, digest.Sum(nil)[:6])
}

func (catalog *apiSchemas) source(path string) *layoutSource {
	if filepath.IsAbs(path) {
		root, err := filepath.Abs(".")
		if err != nil {
			return nil
		}
		path, err = filepath.Rel(root, path)
		if err != nil {
			return nil
		}
	}
	return catalog.sources[filepath.ToSlash(filepath.Clean(path))]
}

func (catalog *apiSchemas) schema(typeName, sourcePath string) map[string]any {
	expression, err := parser.ParseExpr(typeName)
	if err != nil {
		return unresolvedAPISchema(typeName)
	}
	return catalog.expression(expression, catalog.source(sourcePath))
}

func unresolvedAPISchema(typeName string) map[string]any {
	return map[string]any{"x-spine-unresolved-type": typeName}
}

func (catalog *apiSchemas) named(key string) map[string]any {
	declaration, ok := catalog.declarations[key]
	if !ok || declaration.spec.TypeParams != nil {
		return unresolvedAPISchema(key)
	}
	id := catalog.ids[key]
	if _, built := catalog.schemas[id]; !built {
		// 자기 자신을 참조하는 타입도 같은 참조를 쓰도록 확장 전에 등록합니다.
		catalog.schemas[id] = map[string]any{}
		catalog.schemas[id] = catalog.expression(declaration.spec.Type, declaration.source)
	}
	return map[string]any{"$ref": "#/components/schemas/" + id}
}

func (catalog *apiSchemas) expression(expression ast.Expr, source *layoutSource) map[string]any {
	switch value := expression.(type) {
	case *ast.ParenExpr:
		return catalog.expression(value.X, source)
	case *ast.StarExpr:
		return catalog.expression(value.X, source)
	case *ast.ArrayType:
		if name, ok := value.Elt.(*ast.Ident); ok && value.Len == nil && (name.Name == "byte" || name.Name == "uint8") {
			return map[string]any{"type": "string", "contentEncoding": "base64"}
		}
		return map[string]any{"type": "array", "items": catalog.expression(value.Elt, source)}
	case *ast.MapType:
		return map[string]any{"type": "object", "additionalProperties": catalog.expression(value.Value, source)}
	case *ast.InterfaceType:
		return map[string]any{}
	case *ast.StructType:
		return catalog.structure(value, source)
	case *ast.Ident:
		switch value.Name {
		case "string":
			return map[string]any{"type": "string"}
		case "bool":
			return map[string]any{"type": "boolean"}
		case "int", "int8", "int16", "int32", "uint", "uint8", "uint16", "uint32", "byte", "rune":
			return map[string]any{"type": "integer", "format": "int32"}
		case "int64", "uint64", "uintptr":
			return map[string]any{"type": "integer", "format": "int64"}
		case "float32":
			return map[string]any{"type": "number", "format": "float"}
		case "float64":
			return map[string]any{"type": "number", "format": "double"}
		case "any":
			return map[string]any{}
		}
		if source != nil {
			return catalog.named(modulePackage(catalog.module, source.Package) + ":" + value.Name)
		}
		if keys := catalog.byName[value.Name]; len(keys) == 1 {
			return catalog.named(keys[0])
		}
	case *ast.SelectorExpr:
		if alias, ok := value.X.(*ast.Ident); ok && source != nil {
			path := importPathsByAlias(source.File)[alias.Name]
			if path == "time" && value.Sel.Name == "Time" {
				return map[string]any{"type": "string", "format": "date-time"}
			}
			if path == "time" && value.Sel.Name == "Duration" {
				return map[string]any{"type": "integer", "format": "int64"}
			}
			return catalog.named(path + ":" + value.Sel.Name)
		}
	}
	return unresolvedAPISchema(expressionText(token.NewFileSet(), expression))
}

func (catalog *apiSchemas) structure(structure *ast.StructType, source *layoutSource) map[string]any {
	properties := map[string]any{}
	required := []string{}
	for _, field := range structure.Fields.List {
		jsonName, omitEmpty, skip := jsonFieldName(field)
		if skip {
			continue
		}
		for _, name := range field.Names {
			if !ast.IsExported(name.Name) {
				continue
			}
			property := jsonName
			if property == "" {
				property = name.Name
			}
			properties[property] = catalog.expression(field.Type, source)
			if !omitEmpty {
				required = append(required, property)
			}
		}
	}
	sort.Strings(required)
	result := map[string]any{"type": "object", "properties": properties}
	if len(required) > 0 {
		result["required"] = required
	}
	return result
}

func jsonFieldName(field *ast.Field) (string, bool, bool) {
	if field.Tag == nil {
		return "", false, false
	}
	value, err := strconv.Unquote(field.Tag.Value)
	if err != nil {
		return "", false, false
	}
	parts := strings.Split(reflect.StructTag(value).Get("json"), ",")
	if parts[0] == "-" {
		return "", false, true
	}
	omitEmpty := false
	for _, option := range parts[1:] {
		omitEmpty = omitEmpty || option == "omitempty" || option == "omitzero"
	}
	return parts[0], omitEmpty, false
}

func (catalog *apiSchemas) response(returns []string, sourcePath string) (map[string]any, bool) {
	source := catalog.source(sourcePath)
	for _, text := range returns {
		expression, err := parser.ParseExpr(text)
		if err != nil {
			continue
		}
		generic, ok := expression.(*ast.IndexExpr)
		if !ok {
			continue
		}
		selector, ok := generic.X.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "Response" {
			continue
		}
		alias, ok := selector.X.(*ast.Ident)
		if !ok {
			continue
		}
		if source != nil && importPathsByAlias(source.File)[alias.Name] != spineImportPath+"/pkg/httpx" {
			continue
		}
		if source == nil && alias.Name != "httpx" {
			continue
		}
		return catalog.expression(generic.Index, source), true
	}
	return nil, false
}

func pathParameterSchema(typeName string) map[string]any {
	switch typeName {
	case "path.Int":
		return map[string]any{"type": "integer", "format": "int64"}
	case "path.Boolean":
		return map[string]any{"type": "boolean"}
	case "path.String":
		return map[string]any{"type": "string"}
	default:
		return unresolvedAPISchema(typeName)
	}
}
