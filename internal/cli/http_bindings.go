package cli

import (
	"fmt"
	"go/ast"
	"go/token"
)

type httpTypeDeclaration struct {
	source *layoutSource
	spec   *ast.TypeSpec
}

type httpBindingCatalog struct {
	module string
	types  map[string]httpTypeDeclaration
}

type httpBindingShape struct {
	binding string
	kind    string
}

// HTTP 검증과 route 생성은 동일한 소스 기반 resolver 지원 규칙을 사용합니다.
func newHTTPBindingCatalog(module string, sources layoutSources) *httpBindingCatalog {
	catalog := &httpBindingCatalog{module: module, types: map[string]httpTypeDeclaration{}}
	for _, source := range sources.sortedFiles() {
		for _, declaration := range source.File.Decls {
			group, ok := declaration.(*ast.GenDecl)
			if !ok || group.Tok != token.TYPE {
				continue
			}
			for _, specification := range group.Specs {
				spec := specification.(*ast.TypeSpec)
				key := modulePackage(module, source.Package) + ":" + spec.Name.Name
				catalog.types[key] = httpTypeDeclaration{source, spec}
			}
		}
	}
	return catalog
}

func (catalog *httpBindingCatalog) parameters(source *layoutSource, fields *ast.FieldList, path, handler string) ([]handlerParameter, []contractDiagnostic) {
	parameters := layeredHandlerParameters(source.File, source.Fset, fields)
	var diagnostics []contractDiagnostic
	if fields == nil {
		return parameters, diagnostics
	}
	pathKeys, pathIndex, index := contractPathKeys(path), 0, 0
	for _, field := range fields.List {
		shape := catalog.shape(source, field.Type, map[string]bool{})
		count := len(field.Names)
		if count == 0 {
			count = 1
		}
		for range count {
			parameter := &parameters[index]
			parameter.Binding = shape.binding
			if shape.binding == "path" {
				if pathIndex < len(pathKeys) {
					parameter.Key = pathKeys[pathIndex]
				}
				pathIndex++
			}
			if shape.binding == "" {
				code := "HTTP_PARAMETER_BINDING_UNSUPPORTED"
				hint := "Use a supported context/path/query/header parameter or a pointer to a DTO struct."
				if shape.kind == "struct" {
					hint = "Use *" + parameter.Type + " for a DTO body; default HTTP resolvers do not accept a struct value."
				}
				if shape.kind == "unknown" || shape.kind == "interface" {
					code = "HTTP_PARAMETER_BINDING_UNRESOLVED"
					hint = "Static inspection cannot prove support for this type or a custom argument resolver; use an explicit supported binding."
				}
				parameter.Binding = "unresolved"
				diagnostics = append(diagnostics, contractDiagnostic{
					Severity: "error", Code: code,
					Message: fmt.Sprintf("handler %s parameter %d (%s %s) has no verified default HTTP resolver", handler, index, parameter.Name, parameter.Type),
					Source:  relativeSource(source.Fset.Position(field.Pos())), Hint: hint,
				})
			}
			index++
		}
	}
	return parameters, diagnostics
}

func (catalog *httpBindingCatalog) shape(source *layoutSource, expression ast.Expr, visiting map[string]bool) httpBindingShape {
	key := canonicalType(source, expression, catalog.module)
	if known, ok := defaultHTTPBinding(key); ok {
		return known
	}
	if declaration, ok := catalog.types[key]; ok {
		if visiting[key] || declaration.spec.TypeParams != nil {
			return httpBindingShape{kind: "unknown"}
		}
		visiting[key] = true
		defer delete(visiting, key)
		shape := catalog.shape(declaration.source, declaration.spec.Type, visiting)
		if declaration.spec.Assign.IsValid() {
			return shape
		}
		// 새로 정의한 타입은 path.Int 같은 기본 wrapper와 런타임 타입이 다릅니다.
		if shape.kind == "struct" {
			return httpBindingShape{kind: "struct"}
		}
		if shape.kind == "interface" && shape.binding == "context" {
			return shape
		}
		return httpBindingShape{kind: shape.kind}
	}
	switch node := expression.(type) {
	case *ast.ParenExpr:
		return catalog.shape(source, node.X, visiting)
	case *ast.StarExpr:
		if catalog.shape(source, node.X, visiting).kind == "struct" {
			return httpBindingShape{binding: "body", kind: "pointer"}
		}
		return httpBindingShape{kind: "unknown"}
	case *ast.StructType:
		return httpBindingShape{kind: "struct"}
	case *ast.InterfaceType:
		if catalog.controllerContextInterface(source, node, visiting) {
			return httpBindingShape{binding: "context", kind: "interface"}
		}
		return httpBindingShape{kind: "interface"}
	case *ast.Ident:
		if node.Name == "any" {
			return httpBindingShape{binding: "context", kind: "interface"}
		}
		if canonicalType(source, node, catalog.module) == node.Name {
			return httpBindingShape{kind: "scalar"}
		}
		return httpBindingShape{kind: "unknown"}
	case *ast.SelectorExpr, *ast.IndexExpr, *ast.IndexListExpr:
		return httpBindingShape{kind: "unknown"}
	default:
		return httpBindingShape{kind: "unsupported"}
	}
}

func (catalog *httpBindingCatalog) controllerContextInterface(source *layoutSource, iface *ast.InterfaceType, visiting map[string]bool) bool {
	for _, field := range iface.Methods.List {
		if len(field.Names) == 0 {
			shape := catalog.shape(source, field.Type, visiting)
			if shape.kind != "interface" || shape.binding != "context" {
				return false
			}
			continue
		}
		method, ok := field.Type.(*ast.FuncType)
		if !ok || len(field.Names) != 1 || field.Names[0].Name != "Get" {
			return false
		}
		inputs, outputs := fieldTypes(source.Fset, method.Params), fieldTypes(source.Fset, method.Results)
		if len(inputs) != 1 || inputs[0] != "string" || len(outputs) != 2 || (outputs[0] != "any" && outputs[0] != "interface{}") || outputs[1] != "bool" {
			return false
		}
	}
	return true
}

func defaultHTTPBinding(key string) (httpBindingShape, bool) {
	switch key {
	case "context:Context":
		return httpBindingShape{binding: "context", kind: "standard-context"}, true
	case spineImportPath + "/core:ControllerContext", spineImportPath + "/pkg/spine:Ctx":
		return httpBindingShape{binding: "context", kind: "interface"}, true
	case spineImportPath + "/pkg/path:Int", spineImportPath + "/pkg/path:String", spineImportPath + "/pkg/path:Boolean":
		return httpBindingShape{binding: "path", kind: "struct"}, true
	case spineImportPath + "/pkg/query:Values", spineImportPath + "/pkg/query:Pagination":
		return httpBindingShape{binding: "query", kind: "struct"}, true
	case spineImportPath + "/pkg/header:Values":
		return httpBindingShape{binding: "header", kind: "struct"}, true
	case spineImportPath + "/pkg/multipart:UploadedFiles":
		return httpBindingShape{binding: "multipart", kind: "struct"}, true
	}
	return httpBindingShape{}, false
}
