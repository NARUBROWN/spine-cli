package cli

import (
	"go/ast"
	"go/parser"
	"reflect"
	"strconv"
	"strings"
)

// staticFormContent은 알 수 있는 폼 필드만 출력하고 검증 규칙은 추측하지 않습니다.
func (catalog *apiSchemas) staticFormContent(typeName, sourcePath string) (map[string]any, bool, bool) {
	source := catalog.source(sourcePath)
	expression, err := parser.ParseExpr(typeName)
	if err != nil || source == nil {
		return nil, false, true
	}
	if pointer, ok := expression.(*ast.StarExpr); ok {
		expression = pointer.X
	}
	bindings := newHTTPBindingCatalog(catalog.module, catalog.sources, "")
	declaring, structure, ok := bindings.markerPayload(source, expression, map[string]bool{})
	if !ok {
		return nil, false, true
	}
	properties := map[string]any{}
	hasFile, uncertain := false, false
	var collect func(*layoutSource, *ast.StructType, map[string]bool)
	collect = func(src *layoutSource, node *ast.StructType, seen map[string]bool) {
		for _, f := range node.Fields.List {
			tag := reflect.StructTag("")
			if f.Tag != nil {
				raw, err := strconv.Unquote(f.Tag.Value)
				if err != nil {
					uncertain = true
					continue
				}
				tag = reflect.StructTag(raw)
			}
			name := tag.Get("form")
			if name == "-" {
				continue
			}
			exported := false
			for _, n := range f.Names {
				exported = exported || ast.IsExported(n.Name)
			}
			if len(f.Names) == 0 {
				exported = ast.IsExported(embeddedHTTPFieldName(f.Type))
			}
			if !exported {
				continue
			}
			if name != "" {
				if len(f.Names) > 1 {
					uncertain = true
					continue
				}
				if _, exists := properties[name]; exists {
					uncertain = true
					continue
				}
				schema, file, known := catalog.formFieldSchema(f.Type, src, map[string]bool{})
				uncertain = uncertain || !known
				hasFile = hasFile || file
				if description := tag.Get("description"); description != "" {
					schema["description"] = description
				}
				properties[name] = schema
				continue
			}
			if _, pointer := f.Type.(*ast.StarExpr); pointer {
				continue
			}
			key := canonicalType(src, f.Type, catalog.module)
			if declaration, ok := bindings.types[key]; ok {
				if seen[key] {
					continue
				}
				seen[key] = true
				nestedSrc, nested, ok := bindings.markerPayload(declaration.source, declaration.spec.Type, map[string]bool{})
				if ok {
					collect(nestedSrc, nested, seen)
				}
				delete(seen, key)
			} else if _, external := f.Type.(*ast.SelectorExpr); external {
				uncertain = true
			} else if nested, ok := f.Type.(*ast.StructType); ok {
				collect(src, nested, seen)
			}
		}
	}
	collect(declaring, structure, map[string]bool{})
	if uncertain {
		return nil, false, true
	}
	if len(properties) == 0 {
		return nil, false, false
	}
	form := map[string]any{"type": "object", "properties": properties, "x-spine-validation-unresolved": true}
	media := map[string]any{"multipart/form-data": map[string]any{"schema": form}}
	if !hasFile {
		media["application/x-www-form-urlencoded"] = map[string]any{"schema": form}
		media["application/json"] = map[string]any{"schema": catalog.schema(typeName, sourcePath)}
	}
	return media, true, false
}

func (catalog *apiSchemas) formFieldSchema(expr ast.Expr, source *layoutSource, seen map[string]bool) (map[string]any, bool, bool) {
	file := func(e ast.Expr, src *layoutSource) bool {
		sel, ok := e.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		alias, ok := sel.X.(*ast.Ident)
		return ok && sel.Sel.Name == "FileHeader" && importPathsByAlias(src.File)[alias.Name] == "mime/multipart"
	}
	if p, ok := expr.(*ast.StarExpr); ok {
		target := p.X
		targetSource := source
		key := canonicalType(source, target, catalog.module)
		if decl, exists := catalog.declarations[key]; exists && decl.spec.Assign.IsValid() {
			targetSource, target = decl.source, decl.spec.Type
		}
		if file(target, targetSource) {
			return map[string]any{"type": "string", "format": "binary"}, true, true
		}
	}
	if a, ok := expr.(*ast.ArrayType); ok && a.Len == nil {
		e := a.Elt
		targetSource := source
		if p, ok := e.(*ast.StarExpr); ok {
			e = p.X
		}
		key := canonicalType(source, e, catalog.module)
		if decl, exists := catalog.declarations[key]; exists && decl.spec.Assign.IsValid() {
			targetSource, e = decl.source, decl.spec.Type
			if p, ok := e.(*ast.StarExpr); ok {
				e = p.X
			}
		}
		if file(e, targetSource) {
			return map[string]any{"type": "array", "items": map[string]any{"type": "string", "format": "binary"}}, true, true
		}
	}
	if catalog.formCodecUnresolved(expr, source) {
		return unresolvedAPISchema(expressionText(source.Fset, expr)), false, false
	}
	key := canonicalType(source, expr, catalog.module)
	if declaration, ok := catalog.declarations[key]; ok && declaration.spec.Assign.IsValid() && !seen[key] {
		seen[key] = true
		return catalog.formFieldSchema(declaration.spec.Type, declaration.source, seen)
	}
	schema := catalog.expression(expr, source)
	if catalog.formSchemaUnresolved(schema, map[string]bool{}) {
		return schema, false, false
	}
	if _, ok := expr.(*ast.StarExpr); ok {
		schema = map[string]any{"anyOf": []any{schema, map[string]any{"type": "null"}}}
	}
	return schema, false, true
}

func nullableInputSchema(schema map[string]any, typeName, binding string) map[string]any {
	if binding != "path" && strings.HasPrefix(strings.TrimSpace(typeName), "*") {
		return map[string]any{"anyOf": []any{schema, map[string]any{"type": "null"}}}
	}
	return schema
}

// 사용자 정의 디코더는 타입의 기본 형태만으로 wire 형식을 확정할 수 없습니다.
func (catalog *apiSchemas) formCodecUnresolved(expr ast.Expr, source *layoutSource) bool {
	switch node := expr.(type) {
	case *ast.ParenExpr:
		return catalog.formCodecUnresolved(node.X, source)
	case *ast.StarExpr:
		return catalog.formCodecUnresolved(node.X, source)
	case *ast.ArrayType:
		return catalog.formCodecUnresolved(node.Elt, source)
	}
	key := canonicalType(source, expr, catalog.module)
	decl, exists := catalog.declarations[key]
	if !exists {
		return false
	}
	for _, file := range catalog.sources.sortedFiles() {
		if file.Package != decl.source.Package {
			continue
		}
		for _, node := range file.File.Decls {
			fn, ok := node.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || len(fn.Recv.List) != 1 || (fn.Name.Name != "UnmarshalText" && fn.Name.Name != "UnmarshalJSON") {
				continue
			}
			receiver := fn.Recv.List[0].Type
			if ptr, ok := receiver.(*ast.StarExpr); ok {
				receiver = ptr.X
			}
			if canonicalType(file, receiver, catalog.module) == key {
				return true
			}
		}
	}
	return false
}

func (catalog *apiSchemas) formSchemaUnresolved(value any, seen map[string]bool) bool {
	switch node := value.(type) {
	case map[string]any:
		if _, unknown := node["x-spine-unresolved-type"]; unknown {
			return true
		}
		if ref, ok := node["$ref"].(string); ok && strings.HasPrefix(ref, "#/components/schemas/") {
			id := strings.TrimPrefix(ref, "#/components/schemas/")
			if !seen[id] {
				seen[id] = true
				if catalog.formSchemaUnresolved(catalog.schemas[id], seen) {
					return true
				}
			}
		}
		for _, child := range node {
			if catalog.formSchemaUnresolved(child, seen) {
				return true
			}
		}
	case []any:
		for _, child := range node {
			if catalog.formSchemaUnresolved(child, seen) {
				return true
			}
		}
	}
	return false
}
