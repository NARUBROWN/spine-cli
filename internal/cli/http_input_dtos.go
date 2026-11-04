package cli

import (
	"fmt"
	"go/ast"
	"net/http"
	"reflect"
	"strconv"
	"strings"
)

type httpInputField struct {
	name string
	tags reflect.StructTag
}

// 태그와 키만 정적으로 확인합니다. validator 규칙과 사용자 decoder는 실행하지 않습니다.
func (catalog *httpBindingCatalog) inputStruct(source *layoutSource, node *ast.StructType, visiting map[string]bool) httpBindingShape {
	fields, known := catalog.inputFields(source, node, visiting)
	if !known {
		return httpBindingShape{kind: "unknown"}
	}
	selected, declared := "", ""
	for _, field := range fields {
		for _, name := range []string{"path", "query", "header"} {
			value, exists := field.tags.Lookup(name)
			if exists && declared == "" {
				declared = name
			}
			if value == "" || value == "-" {
				continue
			}
			if selected != "" && selected != name {
				return httpBindingShape{kind: "unsupported", problem: "A DTO mixes input sources"}
			}
			selected = name
		}
	}
	if selected == "" {
		selected = declared
	}
	shape := httpBindingShape{kind: "struct", source: selected}
	if selected == "" {
		return shape
	}
	seen := map[string]bool{}
	for _, field := range fields {
		for _, name := range []string{"path", "query", "header", "form", "param"} {
			if value := field.tags.Get(name); name != selected && value != "" && value != "-" {
				return httpBindingShape{kind: "unsupported", problem: "A DTO mixes input sources"}
			}
		}
		key := field.tags.Get(selected)
		if key == "-" {
			continue
		}
		if key == "" || strings.ContainsAny(key, ",\r\n\t") {
			return httpBindingShape{kind: "unsupported", problem: fmt.Sprintf("Field %s needs an explicit %s tag or '-'", field.name, selected)}
		}
		if selected == "header" {
			key = http.CanonicalHeaderKey(key)
		}
		if seen[key] {
			return httpBindingShape{kind: "unsupported", problem: fmt.Sprintf("A DTO repeats %s key %q", selected, key)}
		}
		seen[key] = true
		shape.keys = append(shape.keys, key)
	}
	return shape
}

func (catalog *httpBindingCatalog) inputFields(source *layoutSource, node *ast.StructType, visiting map[string]bool) ([]httpInputField, bool) {
	var fields []httpInputField
	for _, field := range node.Fields.List {
		tags := reflect.StructTag("")
		if field.Tag != nil {
			raw, err := strconv.Unquote(field.Tag.Value)
			if err != nil {
				return nil, false
			}
			tags = reflect.StructTag(raw)
		}
		if len(field.Names) > 0 {
			for _, name := range field.Names {
				if ast.IsExported(name.Name) {
					fields = append(fields, httpInputField{name.Name, tags})
				}
			}
			continue
		}
		name := embeddedHTTPFieldName(field.Type)
		if !ast.IsExported(name) {
			continue
		}
		tagged := false
		for _, source := range []string{"path", "query", "header"} {
			_, exists := tags.Lookup(source)
			tagged = tagged || exists
		}
		if tagged {
			fields = append(fields, httpInputField{name, tags})
			continue
		}
		if key := canonicalType(source, field.Type, catalog.module); key != "" {
			declaration, ok := catalog.types[key]
			if ok && !visiting[key] {
				if embedded, ok := declaration.spec.Type.(*ast.StructType); ok {
					visiting[key] = true
					flattened, known := catalog.inputFields(declaration.source, embedded, visiting)
					delete(visiting, key)
					if !known {
						return nil, false
					}
					fields = append(fields, flattened...)
					continue
				}
			}
		}
		// 외부 또는 재귀 임베딩의 입력 출처를 추측하지 않습니다.
		return nil, false
	}
	return fields, true
}

func embeddedHTTPFieldName(expression ast.Expr) string {
	switch node := expression.(type) {
	case *ast.Ident:
		return node.Name
	case *ast.SelectorExpr:
		return node.Sel.Name
	case *ast.StarExpr:
		return embeddedHTTPFieldName(node.X)
	}
	return ""
}

func (catalog *httpBindingCatalog) dtoIdentity(source *layoutSource, expression ast.Expr) string {
	switch node := expression.(type) {
	case *ast.StarExpr:
		return catalog.dtoIdentity(source, node.X)
	case *ast.ParenExpr:
		return catalog.dtoIdentity(source, node.X)
	}
	key := canonicalType(source, expression, catalog.module)
	if declaration, ok := catalog.types[key]; ok && declaration.spec.Assign.IsValid() {
		return catalog.dtoIdentity(declaration.source, declaration.spec.Type)
	}
	return key
}

func (catalog *httpBindingCatalog) inputParameterDiagnostics(source *layoutSource, fields *ast.FieldList, parameters []handlerParameter, handler string) []contractDiagnostic {
	var diagnostics []contractDiagnostic
	add := func(code, message string) {
		diagnostics = append(diagnostics, contractDiagnostic{Severity: "error", Code: code, Message: "handler " + handler + ": " + message, Source: relativeSource(source.Fset.Position(fields.Pos())), Hint: "Use one DTO per input source, unique keys and at most one body DTO. Migrate deprecated inputs together."})
	}
	legacy, typed, legacyPath, typedPath, body := false, false, false, false, false
	seenTypes, seenKeys := map[string]bool{}, map[string]bool{}
	index := 0
	for _, field := range fields.List {
		count := len(field.Names)
		if count == 0 {
			count = 1
		}
		for range count {
			parameter := parameters[index]
			index++
			legacy, typed = legacy || parameter.Deprecated, typed || parameter.SourceDTO
			legacyPath = legacyPath || parameter.Deprecated && parameter.Binding == "path"
			typedPath = typedPath || parameter.SourceDTO && parameter.Binding == "path"
			if parameter.Deprecated {
				diagnostics = append(diagnostics, contractDiagnostic{Severity: "warning", Code: "HTTP_INPUT_API_DEPRECATED", Message: fmt.Sprintf("handler %s parameter %d uses a deprecated %s input", handler, index-1, parameter.Binding), Source: relativeSource(source.Fset.Position(field.Pos())), Hint: "Migrate path, query and header inputs in this handler to separate tagged struct pointers."})
			}
			if parameter.Binding == "body" || parameter.SourceDTO {
				identity := catalog.dtoIdentity(source, field.Type)
				if seenTypes[identity] {
					add("HTTP_INPUT_DTO_DUPLICATED", "the same DTO type appears more than once")
				}
				seenTypes[identity] = true
			}
			if parameter.Binding == "body" {
				if body {
					add("HTTP_BODY_PARAMETERS_DUPLICATED", "multiple body DTOs are declared")
				}
				body = true
			}
			for _, key := range parameter.Keys {
				identity := parameter.Binding + "\x00" + key
				if seenKeys[identity] {
					add("HTTP_INPUT_KEY_DUPLICATED", fmt.Sprintf("%s key %q appears more than once", parameter.Binding, key))
				}
				seenKeys[identity] = true
			}
		}
	}
	if legacyPath && typedPath {
		add("HTTP_PATH_INPUT_MIXED", "deprecated path wrappers and path DTOs are mixed")
	} else if legacy && typed {
		add("HTTP_INPUT_API_MIXED", "deprecated inputs and source DTOs are mixed")
	}
	return diagnostics
}

func validateHTTPPathBindings(parameters []handlerParameter, path string) error {
	declared := contractPathKeys(path)
	bound := []string{}
	for _, parameter := range parameters {
		if parameter.Binding != "path" {
			continue
		}
		if parameter.SourceDTO {
			bound = append(bound, parameter.Keys...)
		} else {
			bound = append(bound, parameter.Key)
		}
	}
	if len(bound) != len(declared) {
		return fmt.Errorf("declares %d path keys but the handler binds %d", len(declared), len(bound))
	}
	seen := map[string]bool{}
	for _, key := range declared {
		seen[key] = true
	}
	for _, key := range bound {
		if !seen[key] {
			return fmt.Errorf("path key %q is absent from the route or bound more than once", key)
		}
		delete(seen, key)
	}
	return nil
}
