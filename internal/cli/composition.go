package cli

import "go/ast"

// 패키지 별칭과 지역 변수의 가림을 확인해 알려진 앱 생성식만 인식합니다.
func importedCall(expression ast.Expr, imports map[string]string, path, method string) (*ast.CallExpr, bool) {
	call, ok := expression.(*ast.CallExpr)
	if !ok {
		return nil, false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != method {
		return nil, false
	}
	alias, ok := selector.X.(*ast.Ident)
	return call, ok && alias.Obj == nil && imports[alias.Name] == path
}

func applicationAssignment(statement ast.Stmt, file *ast.File) (string, bool) {
	assignment, ok := statement.(*ast.AssignStmt)
	if !ok || len(assignment.Lhs) != 1 || len(assignment.Rhs) != 1 {
		return "", false
	}
	identifier, ok := assignment.Lhs[0].(*ast.Ident)
	if !ok || identifier.Name == "_" {
		return "", false
	}
	imports := importPathsByAlias(file)
	for _, candidate := range [][2]string{{spineImportPath, "New"}, {spineImportPath, "NewWithHTTPAdapter"}, {echoPlatformPath, "New"}} {
		if _, ok := importedCall(assignment.Rhs[0], imports, candidate[0], candidate[1]); ok {
			return identifier.Name, true
		}
	}
	return "", false
}

func adapterSelection(expression ast.Expr, imports map[string]string) string {
	if call, ok := importedCall(expression, imports, echoPlatformPath, "New"); ok {
		if len(call.Args) == 0 {
			return "echo"
		}
		return "unresolved"
	}
	if call, ok := importedCall(expression, imports, spineImportPath, "NewWithHTTPAdapter"); ok {
		if len(call.Args) == 1 && echoFactory(call.Args[0], imports) {
			return "echo"
		}
		return "unresolved"
	}
	call, ok := importedCall(expression, imports, spineImportPath, "New")
	if !ok {
		return "unresolved"
	}
	selection := "missing"
	for _, argument := range call.Args {
		if option, ok := importedCall(argument, imports, echoPlatformPath, "Option"); ok && len(option.Args) == 0 {
			selection = "echo"
			continue
		}
		if option, ok := importedCall(argument, imports, spineImportPath, "WithHTTPAdapter"); ok && len(option.Args) == 1 && echoFactory(option.Args[0], imports) {
			selection = "echo"
			continue
		}
		// 모르는 옵션이 factory를 바꿀 가능성을 배제하지 않습니다.
		return "unresolved"
	}
	return selection
}

func echoFactory(expression ast.Expr, imports map[string]string) bool {
	call, ok := importedCall(expression, imports, echoPlatformPath, "NewFactory")
	return ok && len(call.Args) == 0
}
