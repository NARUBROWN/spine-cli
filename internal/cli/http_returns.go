package cli

import (
	"fmt"
	"go/ast"
)

type httpReturnShape struct {
	kind    string
	nilable bool
}

// 기본 반환 핸들러는 오류를 먼저 처리한 뒤 첫 번째 nil이 아닌 성공 값을 사용합니다.
func (catalog *httpBindingCatalog) returns(source *layoutSource, fields *ast.FieldList, handler string) []contractDiagnostic {
	if fields == nil {
		return nil
	}
	shapes := make([]httpReturnShape, len(fields.List))
	for index, field := range fields.List {
		shapes[index] = catalog.returnShape(source, field.Type, 0, map[string]bool{})
		if shapes[index].kind == "error" && !shapes[index].nilable {
			return nil
		}
	}
	var diagnostics []contractDiagnostic
	for index, field := range fields.List {
		shape := shapes[index]
		if shape.kind == "error" {
			continue
		}
		if shape.kind == "response" {
			if !shape.nilable {
				break
			}
			continue
		}
		typ := expressionText(source.Fset, field.Type)
		code := "HTTP_RETURN_TYPE_UNSUPPORTED"
		hint := fmt.Sprintf("Return httpx.Response[%s] and set Body to the value; use httpx.ResponseOptions for status, headers and cookies. Binary and Redirect are also supported.", typ)
		if shape.kind == "unknown" {
			code = "HTTP_RETURN_TYPE_UNRESOLVED"
			hint = "Static inspection cannot prove the runtime return type. Use an explicit httpx.Response[T], httpx.Binary, httpx.Redirect or error return."
		}
		diagnostics = append(diagnostics, contractDiagnostic{Severity: "error", Code: code, Message: fmt.Sprintf("handler %s return type %s has no verified default HTTP return handler", handler, typ), Source: relativeSource(source.Fset.Position(field.Pos())), Hint: hint})
	}
	return diagnostics
}

func (catalog *httpBindingCatalog) returnShape(source *layoutSource, expression ast.Expr, pointers int, visiting map[string]bool) httpReturnShape {
	if parenthesized, ok := expression.(*ast.ParenExpr); ok {
		return catalog.returnShape(source, parenthesized.X, pointers, visiting)
	}
	if pointer, ok := expression.(*ast.StarExpr); ok {
		return catalog.returnShape(source, pointer.X, pointers+1, visiting)
	}
	key := canonicalType(source, expression, catalog.module)
	if identifier, ok := expression.(*ast.Ident); ok {
		local := modulePackage(catalog.module, source.Package) + ":" + identifier.Name
		if source.ImportPath != "" {
			local = source.ImportPath + ":" + identifier.Name
		}
		if _, exists := catalog.types[local]; exists {
			key = local
		}
	}
	// 공개 반환 타입은 의존 패키지의 선언을 읽었어도 고유한 런타임 계약으로 판정합니다.
	if key == "error" && pointers == 0 {
		return httpReturnShape{kind: "error", nilable: true}
	}
	if key == spineImportPath+"/pkg/httperr:HTTPError" {
		if pointers == 1 {
			return httpReturnShape{kind: "error", nilable: true}
		}
		return httpReturnShape{kind: "unsupported"}
	}
	if key == spineImportPath+"/pkg/httpx:Binary" || key == spineImportPath+"/pkg/httpx:Redirect" {
		if pointers <= 1 {
			return httpReturnShape{kind: "response", nilable: pointers == 1}
		}
		return httpReturnShape{kind: "unsupported"}
	}
	if declaration, ok := catalog.types[key]; ok {
		if visiting[key] || declaration.spec.TypeParams != nil {
			return httpReturnShape{kind: "unknown"}
		}
		visiting[key] = true
		defer delete(visiting, key)
		if declaration.spec.Assign.IsValid() {
			return catalog.returnShape(declaration.source, declaration.spec.Type, pointers, visiting)
		}
		if pointers <= 1 && catalog.hasErrorMethod(key, pointers == 1) {
			return httpReturnShape{kind: "error", nilable: pointers > 0 || catalog.returnNilable(declaration.source, declaration.spec.Type, map[string]bool{})}
		}
		_, valueMethod := catalog.errorMethods[key]
		_, pointerMethod := catalog.errorMethods["*"+key]
		if valueMethod || pointerMethod {
			return httpReturnShape{kind: "unsupported"}
		}

		if pointers == 0 && catalog.isErrorInterface(declaration.source, declaration.spec.Type, visiting) {
			return httpReturnShape{kind: "error", nilable: true}
		}
		if _, ok := declaration.spec.Type.(*ast.InterfaceType); ok {
			if pointers > 0 {
				return httpReturnShape{kind: "unsupported"}
			}
			return httpReturnShape{kind: "unknown", nilable: true}
		}
		if pointers <= 1 {
			promotion := catalog.promotedError(declaration.source, declaration.spec.Type, pointers == 1, map[string]bool{})
			if promotion == "error" {
				return httpReturnShape{kind: "error", nilable: pointers > 0}
			}
			if promotion == "unknown" {
				return httpReturnShape{kind: "unknown"}
			}
		}
		shape := catalog.returnShape(declaration.source, declaration.spec.Type, pointers, visiting)
		if shape.kind == "unknown" {
			return shape
		}
		// 새로 정의한 타입은 원래 httpx 타입의 런타임 식별자를 유지하지 않습니다.
		return httpReturnShape{kind: "unsupported", nilable: pointers > 0}
	}
	if instance, ok := expression.(*ast.IndexExpr); ok && canonicalType(source, instance.X, catalog.module) == spineImportPath+"/pkg/httpx:Response" {
		if pointers > 1 {
			return httpReturnShape{kind: "unsupported"}
		}
		// StringReturnHandler는 Response[string]만 직접 변환할 수 있습니다.
		body := catalog.stringReturnBody(source, instance.Index, map[string]bool{})
		if body == "defined-string" {
			return httpReturnShape{kind: "unsupported"}
		}
		if body == "unknown" {
			return httpReturnShape{kind: "unknown"}
		}
		return httpReturnShape{kind: "response", nilable: pointers == 1}
	}
	switch node := expression.(type) {
	case *ast.InterfaceType:
		if pointers == 0 && catalog.isErrorInterface(source, node, visiting) {
			return httpReturnShape{kind: "error", nilable: true}
		}
		return httpReturnShape{kind: "unknown", nilable: true}
	case *ast.Ident:
		if key == "any" {
			return httpReturnShape{kind: "unknown", nilable: true}
		}
		if key == node.Name {
			return httpReturnShape{kind: "unsupported"}
		}
	case *ast.StructType:
		if pointers <= 1 {
			promotion := catalog.promotedError(source, node, pointers == 1, map[string]bool{})
			if promotion == "error" {
				return httpReturnShape{kind: "error", nilable: pointers > 0}
			}
			if promotion == "unknown" {
				return httpReturnShape{kind: "unknown"}
			}
		}
		return httpReturnShape{kind: "unsupported"}
	case *ast.ArrayType, *ast.MapType, *ast.FuncType, *ast.ChanType:
		return httpReturnShape{kind: "unsupported", nilable: true}
	}
	return httpReturnShape{kind: "unknown"}
}

func (catalog *httpBindingCatalog) stringReturnBody(source *layoutSource, expression ast.Expr, visiting map[string]bool) string {
	key := canonicalType(source, expression, catalog.module)
	if declaration, ok := catalog.types[key]; ok {
		if visiting[key] || declaration.spec.TypeParams != nil {
			return "unknown"
		}
		visiting[key] = true
		defer delete(visiting, key)
		kind := catalog.stringReturnBody(declaration.source, declaration.spec.Type, visiting)
		if !declaration.spec.Assign.IsValid() && (kind == "string" || kind == "defined-string") {
			return "defined-string"
		}
		return kind
	}
	switch node := expression.(type) {
	case *ast.ParenExpr:
		return catalog.stringReturnBody(source, node.X, visiting)
	case *ast.Ident:
		if node.Name == "string" && node.Obj == nil {
			return "string"
		}
		if key == node.Name {
			return "other"
		}
		return "unknown"
	case *ast.SelectorExpr:
		if key == "time:Time" || key == "time:Duration" || key == "encoding/json:RawMessage" || key == "net:IP" || key == "net:IPMask" || key == "net/netip:Addr" || key == "net/netip:Prefix" {
			return "other"
		}
		return "unknown"
	case *ast.IndexExpr, *ast.IndexListExpr:
		return "unknown"
	default:
		return "other"
	}
}

func (catalog *httpBindingCatalog) hasErrorMethod(key string, pointer bool) bool {
	return catalog.errorMethods[key] || (pointer && catalog.errorMethods["*"+key])
}

func (catalog *httpBindingCatalog) returnNilable(source *layoutSource, expression ast.Expr, visiting map[string]bool) bool {
	key := canonicalType(source, expression, catalog.module)
	if declaration, ok := catalog.types[key]; ok {
		if visiting[key] {
			return true
		}
		visiting[key] = true
		defer delete(visiting, key)
		return catalog.returnNilable(declaration.source, declaration.spec.Type, visiting)
	}
	switch node := expression.(type) {
	case *ast.ParenExpr:
		return catalog.returnNilable(source, node.X, visiting)
	case *ast.StructType:
		return false
	case *ast.ArrayType:
		return node.Len == nil
	case *ast.Ident:
		return key == "error" || key == "any" || key != node.Name
	default:
		return true
	}
}

func (catalog *httpBindingCatalog) isErrorInterface(source *layoutSource, expression ast.Expr, visiting map[string]bool) bool {
	if identifier, ok := expression.(*ast.Ident); ok && identifier.Name == "error" && identifier.Obj == nil {
		return true
	}
	if declaration, ok := catalog.types[canonicalType(source, expression, catalog.module)]; ok {
		key := canonicalType(source, expression, catalog.module)
		if visiting[key] {
			return false
		}
		visiting[key] = true
		defer delete(visiting, key)
		return catalog.isErrorInterface(declaration.source, declaration.spec.Type, visiting)
	}
	iface, ok := expression.(*ast.InterfaceType)
	if !ok {
		return false
	}
	for _, field := range iface.Methods.List {
		if len(field.Names) == 0 && catalog.isErrorInterface(source, field.Type, visiting) {
			return true
		}
		function, ok := field.Type.(*ast.FuncType)
		if !ok || len(field.Names) != 1 || field.Names[0].Name != "Error" {
			continue
		}
		results := fieldTypes(source.Fset, function.Results)
		if len(fieldTypes(source.Fset, function.Params)) == 0 && len(results) == 1 && results[0] == "string" {
			return true
		}
	}
	return false
}

// 익명 필드가 승격한 Error 메서드도 기본 오류 반환 핸들러가 처리할 수 있습니다.
func (catalog *httpBindingCatalog) promotedError(source *layoutSource, expression ast.Expr, pointer bool, visiting map[string]bool) string {
	key := canonicalType(source, expression, catalog.module)
	if declaration, ok := catalog.types[key]; ok {
		if visiting[key] {
			return "unknown"
		}
		visiting[key] = true
		defer delete(visiting, key)
		return catalog.promotedError(declaration.source, declaration.spec.Type, pointer, visiting)
	}
	structure, ok := expression.(*ast.StructType)
	if !ok {
		return ""
	}
	result := ""
	for _, field := range structure.Fields.List {
		if len(field.Names) != 0 {
			continue
		}
		pointers := 0
		if pointer {
			if _, alreadyPointer := field.Type.(*ast.StarExpr); !alreadyPointer {
				pointers = 1
			}
		}
		shape := catalog.returnShape(source, field.Type, pointers, visiting)
		if shape.kind == "error" {
			if result == "error" || result == "unknown" {
				return "unknown"
			}
			result = "error"
		}
		if shape.kind == "unknown" {
			result = "unknown"
		}
	}
	return result
}

func (catalog *httpBindingCatalog) errorReceiverKey(source *layoutSource, expression ast.Expr, visiting map[string]bool) string {
	if pointer, ok := expression.(*ast.StarExpr); ok {
		return "*" + catalog.errorReceiverKey(source, pointer.X, visiting)
	}
	if paren, ok := expression.(*ast.ParenExpr); ok {
		return catalog.errorReceiverKey(source, paren.X, visiting)
	}
	key := canonicalType(source, expression, catalog.module)
	if declaration, ok := catalog.types[key]; ok && declaration.spec.Assign.IsValid() && !visiting[key] {
		visiting[key] = true
		defer delete(visiting, key)
		return catalog.errorReceiverKey(declaration.source, declaration.spec.Type, visiting)
	}
	return key
}
