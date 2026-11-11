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
	base, _ := genericType(expr)
	key := canonicalType(source, base, catalog.module)
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
		if name := catalog.duplicateBodyKey(source, node, false); name != "" {
			return fmt.Sprintf("Body DTO repeats JSON key %q", name)
		}
		if name := catalog.duplicateBodyKey(source, node, true); name != "" {
			return fmt.Sprintf("Body DTO repeats form key %q", name)
		}
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

// duplicateBodyKey는 코어 본문 바인더와 같은 깊이 및 태그 우선순위로 필드를 비교합니다.
func (catalog *httpBindingCatalog) duplicateBodyKey(source *layoutSource, root *ast.StructType, form bool) string {
	type candidate struct {
		depth  int
		tagged bool
	}
	selected := map[string]candidate{}
	conflicts := map[string]bool{}
	var walk func(*layoutSource, *ast.StructType, int, map[*ast.StructType]bool)
	walk = func(source *layoutSource, structure *ast.StructType, depth int, seen map[*ast.StructType]bool) {
		if seen[structure] {
			return
		}
		seen[structure] = true
		defer delete(seen, structure)
		for _, field := range structure.Fields.List {
			tags := reflect.StructTag("")
			if field.Tag != nil {
				value, err := strconv.Unquote(field.Tag.Value)
				if err != nil {
					continue
				}
				tags = reflect.StructTag(value)
			}
			tag := tags.Get("json")
			if form {
				tag = tags.Get("form")
			}
			if tag == "-" {
				continue
			}
			name, _, _ := strings.Cut(tag, ",")
			if form {
				name = tag
			}
			fieldType := field.Type
			pointer, indirect := fieldType.(*ast.StarExpr)
			if indirect {
				fieldType = pointer.X
			}
			embedded := len(field.Names) == 0
			if name == "" && (!form && embedded || form && !indirect) {
				fieldSource, nested, ok := catalog.markerPayload(source, fieldType, map[string]bool{})
				if ok {
					if !form || (embedded && ast.IsExported(embeddedHTTPFieldName(field.Type))) || (!embedded && field.Names[0].IsExported()) {
						walk(fieldSource, nested, depth+1, seen)
					}
					continue
				}
			}
			if form && name == "" {
				continue
			}
			names := field.Names
			if embedded {
				names = []*ast.Ident{ast.NewIdent(embeddedHTTPFieldName(field.Type))}
			}
			for _, fieldName := range names {
				if !fieldName.IsExported() {
					continue
				}
				key := name
				if key == "" {
					key = fieldName.Name
				}
				next := candidate{depth: depth, tagged: name != ""}
				previous, exists := selected[key]
				switch {
				case !exists || !form && (next.depth < previous.depth || next.depth == previous.depth && next.tagged && !previous.tagged):
					selected[key], conflicts[key] = next, false
				case form || next == previous:
					conflicts[key] = true
				}
			}
		}
	}
	walk(source, root, 0, map[*ast.StructType]bool{})
	first := ""
	for name, conflict := range conflicts {
		if conflict && (first == "" || name < first) {
			first = name
		}
	}
	return first
}
