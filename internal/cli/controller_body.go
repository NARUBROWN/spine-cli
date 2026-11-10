package cli

import (
	"fmt"
	"go/ast"
	"reflect"
	"strconv"
	"strings"
)

func unsupportedInputTag(tags reflect.StructTag) string {
	for _, name := range []string{"paths", "headers", "queries", "querys", "params", "forms"} {
		if _, ok := tags.Lookup(name); ok {
			return fmt.Sprintf("Unsupported input tag %q", name)
		}
	}
	return ""
}

func (catalog *httpBindingCatalog) bodyProblem(source *layoutSource, expr ast.Expr, visiting map[string]bool) string {
	key := canonicalType(source, expr, catalog.module)
	if decl, exists := catalog.types[key]; exists {
		if visiting[key] || catalog.jsonDecoders[key] {
			return ""
		}
		visiting[key] = true
		defer delete(visiting, key)
		return catalog.bodyProblem(decl.source, decl.spec.Type, visiting)
	}
	switch node := expr.(type) {
	case *ast.ParenExpr:
		return catalog.bodyProblem(source, node.X, visiting)
	case *ast.StarExpr:
		return catalog.bodyProblem(source, node.X, visiting)
	case *ast.ArrayType:
		return catalog.bodyProblem(source, node.Elt, visiting)
	case *ast.MapType:
		return catalog.bodyProblem(source, node.Value, visiting)
	case *ast.StructType:
		forms, jsonNames := map[string]bool{}, map[string]bool{}
		for _, field := range node.Fields.List {
			tags := reflect.StructTag("")
			if field.Tag != nil {
				raw, err := strconv.Unquote(field.Tag.Value)
				if err != nil {
					continue
				}
				tags = reflect.StructTag(raw)
			}
			exported := field.Names
			if len(exported) == 0 {
				exported = []*ast.Ident{ast.NewIdent(embeddedHTTPFieldName(field.Type))}
			}
			hasExported := false
			for _, fieldName := range exported {
				if !fieldName.IsExported() {
					continue
				}
				hasExported = true
				if problem := unsupportedInputTag(tags); problem != "" {
					return problem
				}
				for _, tag := range []string{"path", "query", "header", "param"} {
					if value, ok := tags.Lookup(tag); ok && value != "-" {
						return fmt.Sprintf("Body DTO field %s contains %s input", fieldName.Name, tag)
					}
				}
				if name := tags.Get("form"); name != "" && name != "-" {
					if forms[name] {
						return fmt.Sprintf("Body DTO repeats form key %q", name)
					}
					forms[name] = true
				}
				if name, _, _ := strings.Cut(tags.Get("json"), ","); name != "" && name != "-" {
					if jsonNames[name] {
						return fmt.Sprintf("Body DTO repeats JSON key %q", name)
					}
					jsonNames[name] = true
				}
			}
			if !hasExported && len(field.Names) > 0 {
				continue
			}
			if problem := catalog.bodyProblem(source, field.Type, visiting); problem != "" {
				return problem
			}
		}
	}
	return ""
}
