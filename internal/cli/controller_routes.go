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
	hasComponents := false
	for _, provider := range i.contract.Constructors {
		if provider.object || provider.Role != "" {
			needed = true
			hasComponents = true
		}
	}
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
		if !importedAPI && !hasComponents {
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
	outputs := map[int]types.Type{}
	signatures := map[int]*types.Signature{}
	for index := range i.contract.Constructors {
		provider := &i.contract.Constructors[index]
		output, signature, problem := typedComponentConstructor(*provider, all)
		if problem != "" {
			code := "CONSTRUCTOR_SIGNATURE_UNRESOLVED"
			if provider.object || provider.Role != "" {
				code = "COMPONENT_CONSTRUCTOR_INVALID"
			}
			i.controllerDiagnostic(code, problem, provider.Source)
			continue
		}
		if output == nil {
			continue
		}
		outputs[index] = output
		signatures[index] = signature
		if provider.object || provider.Role != "" {
			provider.Dependencies = []string{}
			for n := 0; n < signature.Params().Len(); n++ {
				provider.Dependencies = append(provider.Dependencies, componentTypeName(signature.Params().At(n).Type()))
			}
			provider.Output = componentTypeName(output)
			required := types.Type(nil)
			switch provider.Role {
			case "Controller":
				required = contract
			case "Interceptor":
				core := all[spineImportPath+"/core"]
				if core == nil || core.Types == nil {
					i.controllerDiagnostic("COMPONENT_CONTRACT_INVALID", "The core package is not loaded, so core.Interceptor cannot be resolved.", provider.Source)
					continue
				}
				obj := core.Types.Scope().Lookup("Interceptor")
				if obj == nil {
					i.controllerDiagnostic("COMPONENT_CONTRACT_INVALID", "The selected core does not provide core.Interceptor.", provider.Source)
					continue
				}
				required = obj.Type()
			}
			if required != nil {
				requiredInterface, ok := required.Underlying().(*types.Interface)
				if !ok {
					i.controllerDiagnostic("COMPONENT_CONTRACT_INVALID", "The selected core's "+provider.Role+" contract is not an interface.", provider.Source)
					continue
				}
				if !types.Implements(output, requiredInterface) {
					i.controllerDiagnostic("COMPONENT_CONTRACT_INVALID", "Component does not implement its "+provider.Role+" contract: "+provider.Output, provider.Source)
					continue
				}
			}
		}

		if provider.Role != "" && provider.Role != "Controller" {
			continue
		}
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
		scan.walk(method, types.TypeString(output, func(p *types.Package) string { return p.Path() }), nil, nil)
	}
	validateTypedComponentGraph(i, outputs, signatures)
}

func (i *contractInspector) controllerDiagnostic(code, message, source string) {
	hint := "Use statically resolved Registry calls and typed callbacks; inspect installed project dependencies. User code is never executed to infer routes."
	if strings.HasPrefix(code, "COMPONENT_") {
		hint = "Implement Constructor(dependencies...) *Self, satisfy the selected role contract and register unique dependencies. User code is never executed for inspection."
	} else if strings.HasPrefix(code, "CONSTRUCTOR_") {
		hint = "Register a statically resolved constructor function with one return value. User code is never executed for inspection."
	}
	i.contract.Diagnostics = append(i.contract.Diagnostics, contractDiagnostic{Severity: "error", Code: code, Message: message, Source: source, Hint: hint})
}

func (s *controllerRouteInspector) walk(object *types.Func, owner string, forwarded []int, forwardedGroups map[int]staticGroup) {
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
	groups := map[types.Object]staticGroup{}
	for index := 0; index < signature.Params().Len(); index++ {
		param := signature.Params().At(index)
		if group, ok := forwardedGroups[index]; ok {
			groups[param] = cloneStaticGroup(group)
		}
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
	var groupExpression func(ast.Expr) (staticGroup, bool, bool)
	groupExpression = func(expr ast.Expr) (staticGroup, bool, bool) {
		if id, ok := expr.(*ast.Ident); ok {
			group, found := groups[info.ObjectOf(id)]
			return cloneStaticGroup(group), found, found
		}
		call, ok := expr.(*ast.CallExpr)
		if !ok {
			return staticGroup{}, false, false
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Group" {
			return staticGroup{}, false, false
		}
		parent := staticGroup{}
		recognized := isRegistry(sel.X)
		if !recognized {
			var valid bool
			parent, recognized, valid = groupExpression(sel.X)
			if recognized && !valid {
				return staticGroup{}, true, false
			}
		}
		if !recognized {
			return staticGroup{}, false, false
		}
		if controllerCallConditional(call, function.declaration.Body, parents, conditions) {
			s.inspector.controllerDiagnostic("CONTROLLER_ROUTE_CONDITIONAL", "Group is conditionally created.", function.source.Path)
		}
		if len(call.Args) == 0 {
			s.inspector.controllerDiagnostic("CONTROLLER_GROUP_DYNAMIC", "Group path is missing.", function.source.Path)
			return staticGroup{}, true, false
		}
		v := info.Types[call.Args[0]].Value
		if v == nil || v.Kind() != constant.String {
			s.inspector.controllerDiagnostic("CONTROLLER_GROUP_DYNAMIC", "Group path must be a compile-time string constant.", function.source.Path)
			return staticGroup{}, true, false
		}
		path := constant.StringVal(v)
		if path == "" || !strings.HasPrefix(path, "/") || strings.Contains(path, "//") {
			s.inspector.controllerDiagnostic("CONTROLLER_GROUP_DYNAMIC", "Group path must be a valid absolute path.", function.source.Path)
			return staticGroup{}, true, false
		}
		parent = cloneStaticGroup(parent)
		if path != "/" {
			parent.path = strings.TrimSuffix(parent.path, "/") + path
		} else if parent.path == "" {
			parent.path = "/"
		}
		if !s.routeOptions(function, call.Args[1:], &parent) {
			return staticGroup{}, true, false
		}
		return parent, true, true
	}
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
				if id, ok := child.(*ast.Ident); ok && (tracked[info.ObjectOf(id)] || hasStaticGroup(groups, info.ObjectOf(id))) {
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
			for index, rhs := range assignment.Rhs {
				if index >= len(assignment.Lhs) {
					continue
				}
				id, ok := assignment.Lhs[index].(*ast.Ident)
				if !ok {
					continue
				}
				obj := info.ObjectOf(id)
				if other, ok := rhs.(*ast.Ident); ok {
					group, found := groups[info.ObjectOf(other)]
					delete(groups, obj)
					if found {
						groups[obj] = cloneStaticGroup(group)
					}
					continue
				}
				delete(groups, obj)
				if group, recognized, valid := groupExpression(rhs); recognized && valid {
					groups[obj] = group
				}
			}
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
					if alias, ok := declaration.Values[index].(*ast.Ident); ok {
						if group, found := groups[info.ObjectOf(alias)]; found {
							groups[info.ObjectOf(name)] = cloneStaticGroup(group)
						}
					}
					if group, recognized, valid := groupExpression(declaration.Values[index]); recognized && valid {
						groups[info.ObjectOf(name)] = group
					}
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
		if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
			typ := info.TypeOf(selector.X)
			if pointer, ok := typ.(*types.Pointer); ok {
				if named, ok := types.Unalias(pointer.Elem()).(*types.Named); ok && named.Obj().Name() == "Group" && named.Obj().Pkg() != nil && named.Obj().Pkg().Path() == spineImportPath+"/internal/apiregistry" {
					if id, ok := selector.X.(*ast.Ident); !ok || !hasStaticGroup(groups, info.ObjectOf(id)) {
						s.inspector.controllerDiagnostic("CONTROLLER_GROUP_VALUE_UNRESOLVED", "Group receiver is not statically tracked.", source)
						return false
					}
				}
			}
		}
		if selector, ok := call.Fun.(*ast.SelectorExpr); ok && info.TypeOf(selector.X) != nil && types.Identical(info.TypeOf(selector.X), s.registry) && !isRegistry(selector.X) {
			s.inspector.controllerDiagnostic("CONTROLLER_REGISTRY_VALUE_UNRESOLVED", "Registry receiver is not a statically tracked registration parameter.", source)
			return false
		}
		if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
			if id, ok := selector.X.(*ast.Ident); ok {
				if group, found := groups[info.ObjectOf(id)]; found {
					method := selector.Sel.Name
					if method == "Group" {
						return false
					}
					switch method {
					case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
					default:
						s.inspector.controllerDiagnostic("CONTROLLER_GROUP_CALL_UNRESOLVED", "Unsupported group call: "+method, source)
						return false
					}
					if controllerCallConditional(call, function.declaration.Body, parents, conditions) {
						s.inspector.controllerDiagnostic("CONTROLLER_ROUTE_CONDITIONAL", "Controller route is conditionally registered.", source)
					}
					s.route(function, call, owner, method, source, &group)
					return false
				}
			}
		}
		if selector, ok := call.Fun.(*ast.SelectorExpr); ok && isRegistry(selector.X) {
			method := selector.Sel.Name
			if method == "Group" {
				return false
			}
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
			s.route(function, call, owner, method, source, nil)
			return false
		}
		forwardedGroups := map[int]staticGroup{}
		for index, arg := range call.Args {
			group, recognized, valid := groupExpression(arg)
			if recognized {
				if !valid {
					return false
				}
				forwardedGroups[index] = group
			} else if isGroupType(info.TypeOf(arg)) {
				s.inspector.controllerDiagnostic("CONTROLLER_GROUP_VALUE_UNRESOLVED", "Group argument is not statically tracked.", source)
				return false
			}
		}
		forwarded := []int{}
		for index, arg := range call.Args {
			if isRegistry(arg) {
				forwarded = append(forwarded, index)
			}
		}
		if len(forwarded) == 0 && len(forwardedGroups) == 0 {
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
					adjusted := map[int]staticGroup{}
					for index, group := range forwardedGroups {
						adjusted[index-1] = group
					}
					forwardedGroups = adjusted
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
		s.walk(target, owner, forwarded, forwardedGroups)
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

func (s *controllerRouteInspector) route(function controllerFunction, call *ast.CallExpr, owner, method, source string, group *staticGroup) {
	if len(call.Args) < 2 {
		s.inspector.controllerDiagnostic("CONTROLLER_ROUTE_ARGUMENTS_INVALID", "Route needs a path and handler.", source)
		return
	}
	handlerIndex := 1
	optionExpressions := call.Args[2:]
	if group != nil && len(call.Args) >= 3 {
		if legacy, ok := call.Args[1].(*ast.CallExpr); ok {
			if selector, ok := legacy.Fun.(*ast.SelectorExpr); ok && selector.Sel.Name == "Options" && isAPISymbol(function.pkg.TypesInfo.ObjectOf(selector.Sel)) {
				handlerIndex = 2
				optionExpressions = legacy.Args
			}
		}
	}
	options := staticGroup{}
	if group != nil {
		options = cloneStaticGroup(*group)
	}
	if !s.routeOptions(function, optionExpressions, &options) {
		return
	}
	pathValue := function.pkg.TypesInfo.Types[call.Args[0]].Value
	if pathValue == nil || pathValue.Kind() != constant.String {
		s.inspector.controllerDiagnostic("CONTROLLER_ROUTE_DYNAMIC", "Controller route path must be a compile-time string constant.", source)
		return
	}
	path := constant.StringVal(pathValue)
	if group != nil {
		if path == "/" {
			path = group.path
		} else {
			path = strings.TrimSuffix(group.path, "/") + path
		}
	}
	if signature, ok := function.pkg.TypesInfo.TypeOf(call.Args[handlerIndex]).(*types.Signature); ok {
		if err := checkControllerInputs(signature); err != nil {
			s.inspector.controllerDiagnostic("HTTP_INPUT_CONTRACT_INVALID", err.Error(), source)
			return
		}
	}
	var fields, results *ast.FieldList
	delegatedMethod := ""
	callbackSource := function.source
	switch handler := call.Args[handlerIndex].(type) {
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
	fake := &ast.CallExpr{Args: []ast.Expr{&ast.BasicLit{Kind: token.STRING, Value: fmt.Sprintf("%q", method)}, &ast.BasicLit{Kind: token.STRING, Value: fmt.Sprintf("%q", path)}, call.Args[handlerIndex]}}
	s.inspector.inspectHTTPCall(fake, function.pkg.Fset, source)
	r := &s.inspector.contract.HTTP[len(s.inspector.contract.HTTP)-1]
	r.Tags = options.tags
	r.Interceptors = append([]string(nil), options.interceptors...)
	r.InterceptorSources = append([]string(nil), options.interceptorSources...)
	r.Summary = options.summary
	r.Description = options.description
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

// staticGroup is source-derived only; dynamic values are diagnosed rather than evaluated.
type staticGroup struct {
	path                 string
	tags                 []string
	interceptors         []string
	interceptorSources   []string
	summary, description string
}

func (s *controllerRouteInspector) routeOptions(f controllerFunction, exprs []ast.Expr, group *staticGroup) bool {
	for _, expr := range exprs {
		call, ok := expr.(*ast.CallExpr)
		if !ok {
			s.inspector.controllerDiagnostic("CONTROLLER_OPTIONS_UNRESOLVED", "Option must be statically declared.", f.source.Path)
			return false
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			s.inspector.controllerDiagnostic("CONTROLLER_OPTIONS_UNRESOLVED", "Option must be statically declared.", f.source.Path)
			return false
		}
		name := sel.Sel.Name
		object := f.pkg.TypesInfo.ObjectOf(sel.Sel)
		apiSymbol := isAPISymbol(object)
		routeInterceptor := name == "WithInterceptors" && isRouteSymbol(object)
		if !apiSymbol && !routeInterceptor {
			s.inspector.controllerDiagnostic("CONTROLLER_OPTIONS_UNRESOLVED", "Option is not from the Spine API package.", f.source.Path)
			return false
		}
		if name == "Interceptors" || routeInterceptor {
			if call.Ellipsis.IsValid() {
				s.inspector.controllerDiagnostic("CONTROLLER_OPTIONS_UNRESOLVED", "Expanded interceptor lists are not statically resolved.", f.source.Path)
				return false
			}
			for _, arg := range call.Args {
				if !staticInterceptorExpression(arg) {
					s.inspector.controllerDiagnostic("CONTROLLER_OPTIONS_UNRESOLVED", "Interceptor must be a statically identifiable value.", f.source.Path)
					return false
				}
				group.interceptors = append(group.interceptors, expressionText(f.pkg.Fset, arg))
				position := f.pkg.Fset.Position(arg.Pos())
				if absolute, err := filepath.Abs("."); err == nil {
					if relative, err := filepath.Rel(absolute, position.Filename); err == nil {
						position.Filename = relative
					}
				}
				group.interceptorSources = append(group.interceptorSources, relativeSource(position))
			}
			continue
		}
		if name != "Tags" && name != "Summary" && name != "Description" {
			s.inspector.controllerDiagnostic("CONTROLLER_OPTIONS_UNRESOLVED", "Unsupported route option: "+name, f.source.Path)
			return false
		}
		for _, arg := range call.Args {
			v := f.pkg.TypesInfo.Types[arg].Value
			if v == nil || v.Kind() != constant.String {
				s.inspector.controllerDiagnostic("CONTROLLER_OPTIONS_UNRESOLVED", "Option requires constant text.", f.source.Path)
				return false
			}
			value := constant.StringVal(v)
			switch name {
			case "Tags":
				if !containsString(group.tags, value) {
					group.tags = append(group.tags, value)
				}
			case "Summary":
				group.summary = value
			case "Description":
				group.description = value
			}
		}
	}
	return true
}
func containsString(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

func hasStaticGroup(groups map[types.Object]staticGroup, obj types.Object) bool {
	_, ok := groups[obj]
	return ok
}

func isAPISymbol(obj types.Object) bool {
	return obj != nil && obj.Pkg() != nil && obj.Pkg().Path() == spineImportPath+"/pkg/api"
}

func isRouteSymbol(obj types.Object) bool {
	return obj != nil && obj.Pkg() != nil && obj.Pkg().Path() == spineImportPath+"/pkg/route"
}

func cloneStaticGroup(group staticGroup) staticGroup {
	group.tags = append([]string(nil), group.tags...)
	group.interceptors = append([]string(nil), group.interceptors...)
	group.interceptorSources = append([]string(nil), group.interceptorSources...)
	return group
}
func staticInterceptorExpression(expr ast.Expr) bool {
	switch value := expr.(type) {
	case *ast.Ident:
		return value.Name != "nil"
	case *ast.SelectorExpr:
		_, ok := value.X.(*ast.Ident)
		return ok
	case *ast.ParenExpr:
		return staticInterceptorExpression(value.X)
	case *ast.UnaryExpr:
		return value.Op == token.AND && staticInterceptorExpression(value.X)
	case *ast.CompositeLit:
		safe := true
		ast.Inspect(value, func(node ast.Node) bool {
			switch node.(type) {
			case *ast.CallExpr, *ast.FuncLit, *ast.IndexExpr, *ast.SliceExpr:
				safe = false
				return false
			}
			return true
		})
		return safe
	}
	return false
}

func isGroupType(value types.Type) bool {
	if value == nil {
		return false
	}
	pointer, ok := types.Unalias(value).(*types.Pointer)
	if !ok {
		return false
	}
	named, ok := types.Unalias(pointer.Elem()).(*types.Named)
	if !ok || named.Obj().Pkg() == nil || named.Obj().Name() != "Group" {
		return false
	}
	path := named.Obj().Pkg().Path()
	return path == spineImportPath+"/internal/apiregistry" || path == spineImportPath+"/pkg/api"
}
