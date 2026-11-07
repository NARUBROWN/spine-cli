package cli

import "go/ast"

// 선택한 모듈의 메타데이터가 선언한 인자 없는 option만 정적으로 인정합니다.
func dynamicPlatformOption(expression ast.Expr, imports map[string]string, modules []selectedModule, kind string) (*platformDescriptor, string) {
	call, ok := expression.(*ast.CallExpr)
	if !ok || call.Ellipsis.IsValid() || len(call.Args) != 0 {
		return nil, ""
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil, ""
	}
	ident, ok := selector.X.(*ast.Ident)
	if !ok || ident.Obj != nil {
		return nil, ""
	}
	path := imports[ident.Name]
	provider := providerFromModule(path, kind)
	if provider == "" || selectedModuleVersion(modules, path) == "" {
		return nil, ""
	}
	d, err := staticPlatformDescriptor(kind, provider, path, modules)
	if err != nil || selector.Sel.Name != d.Factory {
		return nil, ""
	}
	return d, path
}

func dynamicAdapterSelection(expression ast.Expr, imports map[string]string, modules []selectedModule) string {
	call, ok := importedCall(expression, imports, spineImportPath, "New")
	if !ok || call.Ellipsis.IsValid() {
		return "unresolved"
	}
	selected := "missing"
	for _, arg := range call.Args {
		if d, _ := dynamicPlatformOption(arg, imports, modules, "http"); d != nil {
			if selected != "missing" {
				return "unresolved"
			}
			selected = d.Provider
			continue
		}
		if d, _ := dynamicPlatformOption(arg, imports, modules, "websocket"); d != nil {
			continue
		}
		known := false
		for _, method := range []string{"WithEventAdapter", "WithValidator", "WithWebSocketAdapter"} {
			if option, ok := importedCall(arg, imports, spineImportPath, method); ok && !option.Ellipsis.IsValid() && ((method == "WithEventAdapter" && len(option.Args) >= 2) || (method != "WithEventAdapter" && len(option.Args) == 1)) {
				known = true
				break
			}
		}
		if !known {
			return "unresolved"
		}
	}
	return selected
}

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
		if !call.Ellipsis.IsValid() && echoOptions(call.Args, imports) {
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
	if !ok || call.Ellipsis.IsValid() {
		return "unresolved"
	}
	selection := "missing"
	for _, argument := range call.Args {
		if option, ok := importedCall(argument, imports, nethttpPlatformPath, "Option"); ok && len(option.Args) == 0 && !option.Ellipsis.IsValid() {
			selection = "nethttp"
			continue
		}
		if option, ok := importedCall(argument, imports, spineImportPath, "WithHTTPAdapter"); ok && len(option.Args) == 1 && !option.Ellipsis.IsValid() {
			if factory, ok := importedCall(option.Args[0], imports, nethttpPlatformPath, "NewFactory"); ok && len(factory.Args) == 0 && !factory.Ellipsis.IsValid() {
				selection = "nethttp"
				continue
			}
		}
		if option, ok := importedCall(argument, imports, echoPlatformPath, "Option"); ok && !option.Ellipsis.IsValid() && echoOptions(option.Args, imports) {
			selection = "echo"
			continue
		}
		if option, ok := importedCall(argument, imports, spineImportPath, "WithHTTPAdapter"); ok && len(option.Args) == 1 && echoFactory(option.Args[0], imports) {
			selection = "echo"
			continue
		}
		if option, ok := importedCall(argument, imports, spineImportPath, "WithEventAdapter"); ok && len(option.Args) >= 2 && !option.Ellipsis.IsValid() {
			continue
		}
		if option, ok := importedCall(argument, imports, spineImportPath, "WithValidator"); ok && len(option.Args) == 1 && !option.Ellipsis.IsValid() {
			continue
		}
		if option, ok := importedCall(argument, imports, gorillaPlatformPath, "Option"); ok && len(option.Args) == 0 && !option.Ellipsis.IsValid() {
			continue
		}
		if option, ok := importedCall(argument, imports, spineImportPath, "WithWebSocketAdapter"); ok && len(option.Args) == 1 && !option.Ellipsis.IsValid() {
			continue
		}
		// 모르는 옵션이 factory를 바꿀 가능성을 배제하지 않습니다.
		return "unresolved"
	}
	return selection
}

func echoFactory(expression ast.Expr, imports map[string]string) bool {
	call, ok := importedCall(expression, imports, echoPlatformPath, "NewFactory")
	return ok && !call.Ellipsis.IsValid() && echoOptions(call.Args, imports)
}

// WithEcho는 Echo 설정만 추가하므로 알려진 옵션의 인자나 콜백은 실행하지 않습니다.
// 임의 옵션과 가변 인자 목록은 기존과 같이 미해결로 남깁니다.
func echoOptions(arguments []ast.Expr, imports map[string]string) bool {
	for _, argument := range arguments {
		option, ok := importedCall(argument, imports, echoPlatformPath, "WithEcho")
		if !ok || len(option.Args) != 1 || option.Ellipsis.IsValid() {
			return false
		}
	}
	return true
}
