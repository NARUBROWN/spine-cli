package cli

import (
	"go/ast"
	"go/parser"
	"reflect"
	"strconv"
	"strings"
)

func (catalog *apiSchemas) xmlSchema(typeName, sourcePath string) map[string]any {
	expr, err := parser.ParseExpr(typeName)
	source := catalog.source(sourcePath)
	if err != nil || source == nil {
		return unresolvedAPISchema(typeName)
	}
	return catalog.xmlExpression(expr, source)
}

func (catalog *apiSchemas) xmlExpression(expr ast.Expr, source *layoutSource) map[string]any {
	key := canonicalType(source, expr, catalog.module)
	if declaration, exists := catalog.declarations[key]; exists {
		if catalog.bindings.xmlDecoders[key] || declaration.spec.TypeParams != nil {
			return unresolvedAPISchema(key)
		}
		id := "XML." + catalog.ids[key]
		if _, exists := catalog.schemas[id]; !exists {
			catalog.schemas[id] = map[string]any{}
			catalog.schemas[id] = catalog.xmlExpression(declaration.spec.Type, declaration.source)
		}
		return map[string]any{"$ref": "#/components/schemas/" + id}
	}
	switch node := expr.(type) {
	case *ast.ParenExpr:
		return catalog.xmlExpression(node.X, source)
	case *ast.StarExpr:
		return catalog.xmlExpression(node.X, source)
	case *ast.ArrayType:
		if node.Len == nil && (canonicalType(source, node.Elt, catalog.module) == "byte" || canonicalType(source, node.Elt, catalog.module) == "uint8") {
			return map[string]any{"type": "string"}
		}
		return map[string]any{"type": "array", "items": catalog.xmlExpression(node.Elt, source)}
	case *ast.StructType:
		return catalog.xmlObject(node, source)
	case *ast.MapType:
		return unresolvedAPISchema(expressionText(source.Fset, expr))
	default:
		return catalog.expression(expr, source)
	}
}

func (catalog *apiSchemas) xmlObject(node *ast.StructType, source *layoutSource) map[string]any {
	properties := map[string]any{}
	schema := map[string]any{"type": "object", "properties": properties}
	var unresolved []string
	for _, field := range node.Fields.List {
		if len(field.Names) == 0 {
			unresolved = append(unresolved, embeddedHTTPFieldName(field.Type))
			continue
		}
		var tags reflect.StructTag
		if field.Tag != nil {
			raw, err := strconv.Unquote(field.Tag.Value)
			if err != nil {
				continue
			}
			tags = reflect.StructTag(raw)
		}
		tag := tags.Get("xml")
		if tag == "-" {
			continue
		}
		name, options, _ := strings.Cut(tag, ",")
		namespace, local, namespaced := strings.Cut(name, " ")
		if namespaced {
			name = local
		}
		attribute, complex := false, namespaced || strings.Contains(name, ">")
		for _, option := range strings.Split(options, ",") {
			switch option {
			case "attr":
				attribute = true
			case "", "omitempty":
			default:
				complex = true
			}
		}
		for _, identifier := range field.Names {
			if !identifier.IsExported() {
				continue
			}
			if identifier.Name == "XMLName" && canonicalType(source, field.Type, catalog.module) == "encoding/xml:Name" {
				if name != "" {
					root := map[string]any{"name": name}
					if namespaced {
						root["namespace"] = namespace
					}
					schema["xml"] = root
				}
				continue
			}
			if complex {
				unresolved = append(unresolved, identifier.Name)
				continue
			}
			fieldName := name
			if fieldName == "" {
				fieldName = identifier.Name
			}
			property := catalog.xmlExpression(field.Type, source)
			if attribute {
				property["xml"] = map[string]any{"attribute": true}
			}
			properties[fieldName] = property
		}
	}
	if len(unresolved) > 0 {
		schema["x-spine-unresolved-xml-fields"] = unresolved
	}
	return schema
}
