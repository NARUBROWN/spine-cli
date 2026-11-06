package cli

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/constant"
	"go/format"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/tools/go/packages"
)

type controllerFunction struct {
	pkg         *packages.Package
	source      *layoutSource
	declaration *ast.FuncDecl
}

type controllerRouteInspector struct {
	inspector *contractInspector
	functions map[*types.Func]controllerFunction
	registry  types.Type
	active    map[*types.Func]bool
}

// inspectControllerRoutes는 타입 검사 결과로 DI 반환 타입의 컨트롤러 계약을 판별합니다.
// packages.Load는 코드를 실행하지 않으며 함수 본문과 생성자 호출도 실행하지 않습니다.
func (i *contractInspector) inspectControllerRoutes() {
	sources := i.sources
	if sources == nil {
		var err error
		sources, _, err = readLayoutSources()
		if err != nil {
			i.controllerDiagnostic("CONTROLLER_SOURCE_UNRESOLVED", err.Error(), "")
			return
		}
	}
	needed := controllerRoutesVersion(selectedModuleVersion(i.contract.HTTPAdapter.SelectedModules, spineImportPath))
	importedAPI := false
	overlay := map[string][]byte{}
	for _, source := range sources.sortedFiles() {
		for _, path := range importPathsByAlias(source.File) {
			if path == spineImportPath+"/pkg/api" {
				importedAPI = true
				needed = true
			}
		}
		for _, decl := range source.File.Decls {
			if method, ok := decl.(*ast.FuncDecl); ok && method.Recv != nil && method.Name.Name == "RegisterRoutes" {
				needed = true
			}
		}
		var buffer bytes.Buffer
		if err := format.Node(&buffer, source.Fset, source.File); err != nil {
			i.controllerDiagnostic("CONTROLLER_SOURCE_UNRESOLVED", err.Error(), source.Path)
			return
		}
		absolute, err := filepath.Abs(source.Path)
		if err != nil {
			return
		}
		overlay[absolute] = buffer.Bytes()
	}
	if !needed {
		return
	}
	config := &packages.Config{Mode: packages.NeedName | packages.NeedFiles | packages.NeedImports | packages.NeedDeps | packages.NeedTypes | packages.NeedSyntax | packages.NeedTypesInfo, Overlay: overlay, Env: os.Environ(), BuildFlags: []string{"-mod=readonly"}}
	loaded, err := packages.Load(config, "./...")
	if err != nil {
		i.controllerDiagnostic("CONTROLLER_TYPECHECK_UNRESOLVED", err.Error(), "")
		return
	}
	all := map[string]*packages.Package{}
	packages.Visit(loaded, func(pkg *packages.Package) bool { all[pkg.PkgPath] = pkg; return true }, nil)
	apiPackage := all[spineImportPath+"/pkg/api"]
	if apiPackage == nil || apiPackage.Types == nil {
		if !importedAPI {
			return
		}
		i.controllerDiagnostic("CONTROLLER_TYPECHECK_UNRESOLVED", "The selected core must provide pkg/api. Install project dependencies before controller inspection.", "")
		return
	}
	contractObject := apiPackage.Types.Scope().Lookup("Controller")
	registryObject := apiPackage.Types.Scope().Lookup("Registry")
	if contractObject == nil || registryObject == nil {
		i.controllerDiagnostic("CONTROLLER_TYPECHECK_UNRESOLVED", "The selected core does not provide api.Controller and api.Registry.", "")
		return
	}
	contract, ok := contractObject.Type().Underlying().(*types.Interface)
	if !ok {
		return
	}
	scan := &controllerRouteInspector{inspector: i, functions: map[*types.Func]controllerFunction{}, registry: types.NewPointer(types.Unalias(registryObject.Type())), active: map[*types.Func]bool{}}
	for _, pkg := range loaded {
		if !strings.HasPrefix(pkg.PkgPath, i.module) {
			continue
		}
		for _, problem := range pkg.Errors {
			i.controllerDiagnostic("CONTROLLER_TYPECHECK_UNRESOLVED", problem.Msg, problem.Pos)
		}
		for _, file := range pkg.Syntax {
			path := pkg.Fset.Position(file.Pos()).Filename
			relative, _ := filepath.Rel(".", path)
			if absolute, e := filepath.Abs("."); e == nil {
				relative, _ = filepath.Rel(absolute, path)
			}
			source := &layoutSource{Path: relative, File: file, Fset: pkg.Fset, Package: filepath.ToSlash(filepath.Dir(relative))}
			for _, decl := range file.Decls {
				function, ok := decl.(*ast.FuncDecl)
				if !ok {
					continue
				}
				object, ok := pkg.TypesInfo.Defs[function.Name].(*types.Func)
				if ok {
					scan.functions[object] = controllerFunction{pkg, source, function}
				}
			}
		}
	}
	seen := map[types.Type]bool{}
	for _, provider := range i.contract.Constructors {
		split := strings.LastIndex(provider.Target, ".")
		if split < 0 {
			continue
		}
		pkg := all[provider.Target[:split]]
		if pkg == nil || pkg.Types == nil {
			continue
		}
		object := pkg.Types.Scope().Lookup(provider.Target[split+1:])
		if object == nil {
			continue
		}
		signature, ok := object.Type().(*types.Signature)
		if !ok || signature.Results().Len() != 1 {
			continue
		}
		output := types.Unalias(signature.Results().At(0).Type())
		if _, abstract := output.Underlying().(*types.Interface); abstract && types.Implements(output, contract) {
			i.controllerDiagnostic("HTTP_CONTROLLER_PROVIDER_ABSTRACT", "Controller constructors must return a concrete controller type, not api.Controller.", provider.Source)
			continue
		}
		if _, abstract := output.Underlying().(*types.Interface); abstract || !types.Implements(output, contract) || seen[output] {
			continue
		}
		seen[output] = true
		selection := types.NewMethodSet(output).Lookup(nil, "RegisterRoutes")
		if selection == nil {
			continue
		}
		method, ok := selection.Obj().(*types.Func)
		if !ok {
			continue
		}
		scan.walk(method, types.TypeString(output, func(p *types.Package) string { return p.Path() }), nil)
	}
}

func (i *contractInspector) controllerDiagnostic(code, message, source string) {
	i.contract.Diagnostics = append(i.contract.Diagnostics, contractDiagnostic{Severity: "error", Code: code, Message: message, Source: source, Hint: "Use statically resolved Registry calls and typed callbacks; inspect installed project dependencies. User code is never executed to infer routes."})
}

func (s *controllerRouteInspector) walk(object *types.Func, owner string, forwarded []int) {
	function, ok := s.functions[object]
	if !ok {
		s.inspector.controllerDiagnostic("CONTROLLER_HELPER_UNRESOLVED", "Controller registration target has no inspectable project body: "+object.FullName(), "")
		return
	}
	if s.active[object] {
		s.inspector.controllerDiagnostic("CONTROLLER_REGISTRATION_RECURSIVE", "Recursive registration helper: "+object.FullName(), function.source.Path)
		return
	}
	s.active[object] = true
	defer delete(s.active, object)
	if function.declaration.Body == nil {
		return
	}
	signature := object.Type().(*types.Signature)
	tracked := map[types.Object]bool{}
	for index := 0; index < signature.Params().Len(); index++ {
		param := signature.Params().At(index)
		if types.Identical(param.Type(), s.registry) && (forwarded == nil || containsControllerIndex(forwarded, index)) {
			tracked[param] = true
		}
	}
	info := function.pkg.TypesInfo
	isRegistry := func(expression ast.Expr) bool {
		identifier, ok := expression.(*ast.Ident)
		if !ok {
			return false
		}
		return tracked[info.ObjectOf(identifier)]
	}
	parents := astParentMap(function.declaration.Body)
	conditions := map[*ast.IfStmt]bool{}
	skipped := map[ast.Node]bool{}
	imports := importPathsByAlias(function.source.File)
	ast.Inspect(function.declaration.Body, func(node ast.Node) bool {
		if skipped[node] {
			return false
		}
		if statement, ok := node.(*ast.IfStmt); ok && statement.Init == nil {
			if enabled, resolved := registrationCondition(statement.Cond, imports); resolved {
				conditions[statement] = enabled
				if enabled && statement.Else != nil {
					skipped[statement.Else] = true
				} else if !enabled {
					skipped[statement.Body] = true
				}
			}
		}
		if literal, ok := node.(*ast.FuncLit); ok {
			captures := false
			ast.Inspect(literal.Body, func(child ast.Node) bool {
				if id, ok := child.(*ast.Ident); ok && tracked[info.ObjectOf(id)] {
					captures = true
				}
				return true
			})
			if captures {
				s.inspector.controllerDiagnostic("CONTROLLER_REGISTRATION_DEFERRED_UNRESOLVED", "A function literal captures the registration Registry outside an endpoint declaration.", function.source.Path)
			}
			return false
		}
		if assignment, ok := node.(*ast.AssignStmt); ok {
			values := make([]bool, len(assignment.Lhs))
			for index, rhs := range assignment.Rhs {
				if index < len(values) {
					values[index] = isRegistry(rhs)
				}
			}
			for index, lhs := range assignment.Lhs {
				if identifier, ok := lhs.(*ast.Ident); ok {
					tracked[info.ObjectOf(identifier)] = values[index]
				}
			}
		}
		if declaration, ok := node.(*ast.ValueSpec); ok {
			for index, name := range declaration.Names {
				if index < len(declaration.Values) {
					tracked[info.ObjectOf(name)] = isRegistry(declaration.Values[index])
				}
			}
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		position := function.pkg.Fset.Position(call.Pos())
		if absolute, e := filepath.Abs("."); e == nil {
			if relative, e := filepath.Rel(absolute, position.Filename); e == nil {
				position.Filename = relative
			}
		}
		source := relativeSource(position)
		if selector, ok := call.Fun.(*ast.SelectorExpr); ok && info.TypeOf(selector.X) != nil && types.Identical(info.TypeOf(selector.X), s.registry) && !isRegistry(selector.X) {
			s.inspector.controllerDiagnostic("CONTROLLER_REGISTRY_VALUE_UNRESOLVED", "Registry receiver is not a statically tracked registration parameter.", source)
			return false
		}
		if selector, ok := call.Fun.(*ast.SelectorExpr); ok && isRegistry(selector.X) {
			method := selector.Sel.Name
			if method == "Error" {
				return false
			}
			switch method {
			case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
			default:
				s.inspector.controllerDiagnostic("CONTROLLER_REGISTRY_CALL_UNRESOLVED", "Unsupported Registry call: "+method, source)
				return false
			}
			if controllerCallConditional(call, function.declaration.Body, parents, conditions) {
				s.inspector.controllerDiagnostic("CONTROLLER_ROUTE_CONDITIONAL", "Controller route is conditionally registered.", source)
			}
			s.route(function, call, owner, method, source)
			return false
		}
		var forwarded []int
		for index, arg := range call.Args {
			if isRegistry(arg) {
				forwarded = append(forwarded, index)
			}
		}
		if len(forwarded) == 0 {
			return true
		}
		var target *types.Func
		switch callee := call.Fun.(type) {
		case *ast.Ident:
			target, _ = info.ObjectOf(callee).(*types.Func)
		case *ast.SelectorExpr:
			if selected := info.Selections[callee]; selected != nil {
				if selected.Kind() == types.MethodExpr {
					for index := range forwarded {
						forwarded[index]--
					}
				}
				if _, dynamic := selected.Recv().Underlying().(*types.Interface); !dynamic {
					target, _ = selected.Obj().(*types.Func)
				}
			} else {
				target, _ = info.ObjectOf(callee.Sel).(*types.Func)
			}
		}
		if target == nil {
			s.inspector.controllerDiagnostic("CONTROLLER_HELPER_UNRESOLVED", "Dynamic Registry forwarding target cannot be resolved.", source)
			return false
		}
		if controllerCallConditional(call, function.declaration.Body, parents, conditions) {
			s.inspector.controllerDiagnostic("CONTROLLER_ROUTE_CONDITIONAL", "Registration helper is conditionally called.", source)
		}
		s.walk(target, owner, forwarded)
		return false
	})
}

func containsControllerIndex(indices []int, value int) bool {
	for _, index := range indices {
		if index == value {
			return true
		}
	}
	return false
}

func (s *controllerRouteInspector) route(function controllerFunction, call *ast.CallExpr, owner, method, source string) {
	if len(call.Args) < 2 {
		s.inspector.controllerDiagnostic("CONTROLLER_ROUTE_ARGUMENTS_INVALID", "Registry route needs a path and handler.", source)
		return
	}
	pathValue := function.pkg.TypesInfo.Types[call.Args[0]].Value
	if pathValue == nil || pathValue.Kind() != constant.String {
		s.inspector.controllerDiagnostic("CONTROLLER_ROUTE_DYNAMIC", "Controller route path must be a compile-time string constant.", source)
		return
	}
	path := constant.StringVal(pathValue)
	if signature, ok := function.pkg.TypesInfo.TypeOf(call.Args[1]).(*types.Signature); ok {
		if err := checkControllerInputs(signature); err != nil {
			s.inspector.controllerDiagnostic("HTTP_INPUT_CONTRACT_INVALID", err.Error(), source)
			return
		}
	}
	var fields, results *ast.FieldList
	delegatedMethod := ""
	callbackSource := function.source
	switch handler := call.Args[1].(type) {
	case *ast.FuncLit:
		fields, results = handler.Type.Params, handler.Type.Results
		if len(handler.Body.List) == 1 {
			var expression ast.Expr
			switch statement := handler.Body.List[0].(type) {
			case *ast.ReturnStmt:
				if len(statement.Results) == 1 {
					expression = statement.Results[0]
				}
			case *ast.ExprStmt:
				expression = statement.X
			}
			if call, ok := expression.(*ast.CallExpr); ok {
				if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
					if target, ok := function.pkg.TypesInfo.ObjectOf(selector.Sel).(*types.Func); ok {
						delegatedMethod = target.FullName()
					}
				}
			}
		}
	default:
		var target *types.Func
		switch h := handler.(type) {
		case *ast.Ident:
			target, _ = function.pkg.TypesInfo.ObjectOf(h).(*types.Func)
		case *ast.SelectorExpr:
			target, _ = function.pkg.TypesInfo.ObjectOf(h.Sel).(*types.Func)
		}
		found, ok := s.functions[target]
		if !ok {
			s.inspector.controllerDiagnostic("CONTROLLER_CALLBACK_UNRESOLVED", "Callback value is dynamic or outside inspectable source.", source)
			return
		}
		fields, results, callbackSource = found.declaration.Type.Params, found.declaration.Type.Results, found.source
	}
	key := owner + ".RegisterRoutes[" + method + " " + path + "]"
	if s.inspector.controllerHandlers == nil {
		s.inspector.controllerHandlers = map[string]staticHandlerSignature{}
	}
	s.inspector.controllerHandlers[key] = staticHandlerSignature{httpSource: callbackSource, httpFields: fields, httpResults: results, schemaSource: callbackSource.Path, returns: fieldTypes(callbackSource.Fset, results), source: source}
	fake := &ast.CallExpr{Args: []ast.Expr{&ast.BasicLit{Kind: token.STRING, Value: fmt.Sprintf("%q", method)}, &ast.BasicLit{Kind: token.STRING, Value: fmt.Sprintf("%q", path)}, call.Args[1]}}
	fake.Args = append(fake.Args, call.Args[2:]...)
	s.inspector.inspectHTTPCall(fake, function.pkg.Fset, source)
	s.inspector.contract.HTTP[len(s.inspector.contract.HTTP)-1].Handler = key
	s.inspector.contract.HTTP[len(s.inspector.contract.HTTP)-1].delegatedMethod = delegatedMethod
}

func controllerCallConditional(node, root ast.Node, parents map[ast.Node]ast.Node, conditions map[*ast.IfStmt]bool) bool {
	for parent := parents[node]; parent != nil && parent != root; parent = parents[parent] {
		if _, asynchronous := parent.(*ast.GoStmt); asynchronous {
			return true
		}
	}
	return conditionallyExecuted(node, root, parents, conditions)
}
