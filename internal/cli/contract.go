package cli

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const contractSchemaVersion = "spine.contract/v1alpha1"

type applicationContract struct {
	SchemaVersion string                 `json:"schema_version"`
	AnalysisMode  string                 `json:"analysis_mode"`
	Complete      bool                   `json:"analysis_complete"`
	Module        string                 `json:"module"`
	Entrypoint    string                 `json:"entrypoint"`
	Composition   string                 `json:"composition"`
	Constructors  []contractRegistration `json:"constructors"`
	HTTP          []httpRegistration     `json:"http"`
	Interceptors  []contractRegistration `json:"interceptors"`
	Consumers     []namedRegistration    `json:"consumers"`
	WebSockets    []namedRegistration    `json:"websockets"`
	Resources     []resourceRegistration `json:"resources"`
	Configuration []contractSetting      `json:"configuration"`
	Diagnostics   []contractDiagnostic   `json:"diagnostics"`
}

type contractRegistration struct {
	Target       string   `json:"target"`
	Scope        string   `json:"scope,omitempty"`
	Dependencies []string `json:"dependencies,omitempty"`
	Source       string   `json:"source"`
}

type httpRegistration struct {
	Method       string             `json:"method"`
	Path         string             `json:"path"`
	Handler      string             `json:"handler"`
	Interceptors []string           `json:"interceptors,omitempty"`
	Parameters   []handlerParameter `json:"parameters,omitempty"`
	Returns      []string           `json:"returns,omitempty"`
	Source       string             `json:"source"`
}

type namedRegistration struct {
	Name       string             `json:"name"`
	Handler    string             `json:"handler"`
	Parameters []handlerParameter `json:"parameters,omitempty"`
	Returns    []string           `json:"returns,omitempty"`
	Source     string             `json:"source"`
}

type handlerParameter struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Binding string `json:"binding"`
	Key     string `json:"key,omitempty"`
}

type resourceRegistration struct {
	Package string `json:"package"`
	Import  string `json:"import"`
	Source  string `json:"source"`
}

type contractSetting struct {
	Path   string `json:"path"`
	Value  string `json:"value"`
	Source string `json:"source"`
}

type contractDiagnostic struct {
	Severity    string       `json:"severity"`
	Code        string       `json:"code"`
	Message     string       `json:"message"`
	Source      string       `json:"source,omitempty"`
	Retryable   bool         `json:"retryable"`
	Hint        string       `json:"hint,omitempty"`
	NextActions []nextAction `json:"next_actions,omitempty"`
}

type contractInspector struct {
	contract  *applicationContract
	module    string
	seen      map[string]bool
	qualified bool
	sources   layoutSources
}

func inspectApplication() (applicationContract, error) {
	config, err := loadProjectConfig()
	if err != nil {
		return applicationContract{}, err
	}
	module, err := modulePath()
	if err != nil {
		return applicationContract{}, err
	}
	var inspector *contractInspector
	if config.Layout == layeredLayout {
		sources, _, err := readLayoutSources()
		if err != nil {
			return applicationContract{}, err
		}
		inspector, err = inspectLayeredRegistrations(config, module, sources)
		if err != nil {
			return applicationContract{}, err
		}
	} else {
		_, file, fset, function, _, appName, err := findCompositionRoot(config.Composition)
		if err != nil {
			return applicationContract{}, err
		}
		inspector = newContractInspector(config, module, nil)
		inspector.inspectFile(file, fset, function.Body, appName)
		inspector.addDuplicateDiagnostics()
	}
	inspector.resolveConstructorDependencies()
	inspector.resolveHandlerSignatures()
	inspector.sort()
	inspector.contract.Complete = !contractHasErrors(*inspector.contract)
	return *inspector.contract, nil
}

func newContractInspector(config projectConfig, module string, sources layoutSources) *contractInspector {
	contract := applicationContract{
		SchemaVersion: contractSchemaVersion,
		AnalysisMode:  "static-source",
		Complete:      true,
		Module:        module,
		Entrypoint:    config.Entrypoint,
		Composition:   config.Composition,
		Constructors:  []contractRegistration{},
		HTTP:          []httpRegistration{},
		Interceptors:  []contractRegistration{},
		Consumers:     []namedRegistration{},
		WebSockets:    []namedRegistration{},
		Resources:     []resourceRegistration{},
		Configuration: []contractSetting{},
		Diagnostics:   []contractDiagnostic{},
	}
	return &contractInspector{contract: &contract, module: module, seen: map[string]bool{}, qualified: config.Layout == layeredLayout, sources: sources}
}

func (i *contractInspector) inspectFile(file *ast.File, fset *token.FileSet, root ast.Node, appName string) {
	imports := importPathsByAlias(file)
	parents := astParentMap(root)
	conditions := map[*ast.IfStmt]bool{}
	skipped := map[ast.Node]bool{}
	ast.Inspect(root, func(node ast.Node) bool {
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
			if containsAppContractCall(literal.Body, appName) {
				i.contract.Diagnostics = append(i.contract.Diagnostics, contractDiagnostic{Severity: "error", Code: "DEFERRED_REGISTRATION_UNRESOLVED", Message: "registration inside a function literal is not known to execute during composition", Source: relativeSource(fset.Position(literal.Pos()))})
			}
			return false
		}
		if literal, ok := node.(*ast.CompositeLit); ok && isBootOptionsType(literal.Type, imports) {
			i.inspectOptionsLiteral(literal, fset, "boot.Options")
			return false
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		source := relativeSource(fset.Position(call.Pos()))
		if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
			if alias, ok := selector.X.(*ast.Ident); ok && alias.Obj == nil && imports[alias.Name] == "os" {
				switch selector.Sel.Name {
				case "Setenv", "Unsetenv", "Clearenv":
					i.contract.Diagnostics = append(i.contract.Diagnostics, contractDiagnostic{Severity: "error", Code: "ENVIRONMENT_MUTATION_UNRESOLVED", Message: "composition mutates environment inputs, so static inspection cannot prove the active registrations", Source: source})
				}
			}
		}
		registrationCall := false
		if selector, ok := directAppCall(call, appName); ok {
			switch selector {
			case "Constructor":
				registrationCall = true
				for _, argument := range call.Args {
					i.contract.Constructors = append(i.contract.Constructors, contractRegistration{Target: i.registrationExpression(argument, file, fset), Source: source})
				}
				if packageName, importPath, ok := directResourceConstructor(call, imports, i.module); ok {
					i.addResource(packageName, importPath, source)
				}
			case "Route":
				registrationCall = true
				before := len(i.contract.HTTP)
				i.inspectHTTPCall(call, fset, source)
				if len(i.contract.HTTP) > before {
					i.contract.HTTP[len(i.contract.HTTP)-1].Handler = i.registrationExpression(call.Args[2], file, fset)
				}
			case "Interceptor", "InterceptorFor":
				registrationCall = true
				arguments := call.Args
				scope := "all"
				if selector == "InterceptorFor" && len(arguments) > 0 {
					scope = expressionText(fset, arguments[0])
					arguments = arguments[1:]
				}
				for _, argument := range arguments {
					i.contract.Interceptors = append(i.contract.Interceptors, contractRegistration{Target: expressionText(fset, argument), Scope: scope, Source: source})
				}
			}
		}
		if registry, ok := registryCall(call, appName); ok {
			registrationCall = true
			i.inspectRegistryCall(registry, call, file, fset, source, imports)
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "Register" || len(call.Args) != 1 {
			if registrationCall && conditionallyExecuted(call, root, parents, conditions) {
				i.addConditionalRegistrationDiagnostic(source)
			}
			if !registrationCall && hasIdentifierArgument(call, appName) {
				i.contract.Diagnostics = append(i.contract.Diagnostics, contractDiagnostic{Severity: "error", Code: "REGISTRATION_HELPER_UNRESOLVED", Message: "a helper receives the Spine app but static inspection cannot prove which registrations it performs", Source: source})
			}
			return true
		}
		pkg, packageOK := selector.X.(*ast.Ident)
		app, appOK := call.Args[0].(*ast.Ident)
		if !packageOK || !appOK || app.Name != appName {
			if registrationCall && conditionallyExecuted(call, root, parents, conditions) {
				i.addConditionalRegistrationDiagnostic(source)
			}
			return true
		}
		importPath := imports[pkg.Name]
		if importPath == "" || !strings.HasPrefix(importPath, i.module+"/") {
			return true
		}
		registrationCall = true
		i.inspectResource(pkg.Name, importPath, source)
		if conditionallyExecuted(call, root, parents, conditions) {
			i.addConditionalRegistrationDiagnostic(source)
		}
		return true
	})
}

func normalizeImportedExpression(expression string, imports map[string]string, module string) string {
	for alias, importPath := range imports {
		if strings.HasPrefix(importPath, module+"/") {
			expression = strings.ReplaceAll(expression, alias+".", "")
		}
	}
	return expression
}

func directResourceConstructor(call *ast.CallExpr, imports map[string]string, module string) (string, string, bool) {
	if len(call.Args) != 3 {
		return "", "", false
	}
	var packageName, importPath string
	for index, suffix := range []string{"Repository", "Service", "Controller"} {
		selector, ok := call.Args[index].(*ast.SelectorExpr)
		if !ok {
			return "", "", false
		}
		pkg, packageOK := selector.X.(*ast.Ident)
		if !packageOK || !strings.HasPrefix(imports[pkg.Name], module+"/src/") || !strings.HasPrefix(selector.Sel.Name, "New") || !strings.HasSuffix(selector.Sel.Name, suffix) {
			return "", "", false
		}
		if index == 0 {
			packageName, importPath = pkg.Name, imports[pkg.Name]
		} else if pkg.Name != packageName {
			return "", "", false
		}
	}
	return packageName, importPath, true
}

func (i *contractInspector) addConditionalRegistrationDiagnostic(source string) {
	i.contract.Diagnostics = append(i.contract.Diagnostics, contractDiagnostic{Severity: "error", Code: "REGISTRATION_CONDITIONAL", Message: "registration is conditionally executed, so static inspection cannot prove the active contract", Source: source})
}

func (i *contractInspector) inspectOptionsLiteral(literal *ast.CompositeLit, fset *token.FileSet, prefix string) {
	for _, element := range literal.Elts {
		pair, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := pair.Key.(*ast.Ident)
		if !ok {
			continue
		}
		path := prefix + "." + key.Name
		source := relativeSource(fset.Position(pair.Pos()))
		if value := unwrapCompositeLiteral(pair.Value); value != nil && isOptionsLiteral(value) {
			i.contract.Configuration = append(i.contract.Configuration, contractSetting{Path: path, Value: "configured", Source: source})
			i.inspectOptionsLiteral(value, fset, path)
			continue
		}
		text := expressionText(fset, pair.Value)
		i.contract.Configuration = append(i.contract.Configuration, contractSetting{Path: path, Value: text, Source: source})
		switch {
		case key.Name == "AllowInsecureTransport" && text == "true":
			i.contract.Diagnostics = append(i.contract.Diagnostics, contractDiagnostic{Severity: "warning", Code: "INSECURE_TRANSPORT_ENABLED", Message: path + " explicitly allows an insecure broker transport", Source: source})
		case key.Name == "DisableRecover" && text == "true":
			i.contract.Diagnostics = append(i.contract.Diagnostics, contractDiagnostic{Severity: "warning", Code: "HTTP_RECOVERY_DISABLED", Message: path + " disables the default panic recovery boundary", Source: source})
		case key.Name == "AllowedOrigins" && strings.Contains(text, `"*"`):
			i.contract.Diagnostics = append(i.contract.Diagnostics, contractDiagnostic{Severity: "warning", Code: "WEBSOCKET_WILDCARD_ORIGIN", Message: path + " allows every WebSocket origin", Source: source})
		}
	}
}

func isOptionsLiteral(literal *ast.CompositeLit) bool {
	switch value := literal.Type.(type) {
	case *ast.Ident:
		return strings.HasSuffix(value.Name, "Options")
	case *ast.SelectorExpr:
		return strings.HasSuffix(value.Sel.Name, "Options")
	default:
		return false
	}
}

func unwrapCompositeLiteral(expression ast.Expr) *ast.CompositeLit {
	switch value := expression.(type) {
	case *ast.CompositeLit:
		return value
	case *ast.UnaryExpr:
		literal, _ := value.X.(*ast.CompositeLit)
		return literal
	default:
		return nil
	}
}

func isBootOptionsType(expression ast.Expr, imports map[string]string) bool {
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Options" {
		return false
	}
	alias, ok := selector.X.(*ast.Ident)
	return ok && imports[alias.Name] == "github.com/NARUBROWN/spine/pkg/boot"
}

func (i *contractInspector) inspectHTTPCall(call *ast.CallExpr, fset *token.FileSet, source string) {
	if len(call.Args) < 3 {
		i.contract.Diagnostics = append(i.contract.Diagnostics, contractDiagnostic{Severity: "error", Code: "HTTP_ROUTE_ARGUMENTS_INVALID", Message: "app.Route requires method, path, and handler arguments", Source: source})
		return
	}
	method, methodOK := stringLiteral(call.Args[0])
	path, pathOK := stringLiteral(call.Args[1])
	if !methodOK || !pathOK {
		i.contract.Diagnostics = append(i.contract.Diagnostics, contractDiagnostic{Severity: "error", Code: "HTTP_ROUTE_DYNAMIC", Message: "static inspection cannot resolve a non-literal HTTP method or path", Source: source})
	}
	route := httpRegistration{Method: strings.ToUpper(method), Path: path, Handler: expressionText(fset, call.Args[2]), Source: source}
	for _, option := range call.Args[3:] {
		optionCall, ok := option.(*ast.CallExpr)
		if !ok {
			i.contract.Diagnostics = append(i.contract.Diagnostics, contractDiagnostic{Severity: "error", Code: "HTTP_ROUTE_OPTION_UNRESOLVED", Message: "static inspection cannot resolve a non-call route option", Source: source})
			continue
		}
		selector, ok := optionCall.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "WithInterceptors" {
			i.contract.Diagnostics = append(i.contract.Diagnostics, contractDiagnostic{Severity: "error", Code: "HTTP_ROUTE_OPTION_UNRESOLVED", Message: "static inspection cannot resolve route option " + expressionText(fset, option), Source: source})
			continue
		}
		for _, interceptor := range optionCall.Args {
			route.Interceptors = append(route.Interceptors, expressionText(fset, interceptor))
		}
	}
	i.contract.HTTP = append(i.contract.HTTP, route)
}

func (i *contractInspector) resolveConstructorDependencies() {
	if i.qualified {
		i.resolveLayeredConstructors()
		return
	}
	type signature struct {
		dependencies []string
		source       string
	}
	signatures := map[string][]signature{}
	_ = filepath.WalkDir(".", func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() && (entry.Name() == ".git" || entry.Name() == "bin" || entry.Name() == "vendor" || strings.HasPrefix(entry.Name(), ".spine")) {
			return filepath.SkipDir
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return nil
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Recv != nil {
				continue
			}
			var dependencies []string
			if function.Type.Params != nil {
				for _, field := range function.Type.Params.List {
					dependency := expressionText(fset, field.Type)
					count := len(field.Names)
					if count == 0 {
						count = 1
					}
					for index := 0; index < count; index++ {
						dependencies = append(dependencies, dependency)
					}
				}
			}
			signatures[function.Name.Name] = append(signatures[function.Name.Name], signature{dependencies: dependencies, source: relativeSource(fset.Position(function.Pos()))})
		}
		return nil
	})
	for index := range i.contract.Constructors {
		constructor := &i.contract.Constructors[index]
		matches := signatures[constructor.Target]
		if len(matches) == 1 {
			constructor.Dependencies = matches[0].dependencies
		} else if len(matches) == 0 {
			i.contract.Diagnostics = append(i.contract.Diagnostics, contractDiagnostic{Severity: "error", Code: "CONSTRUCTOR_SIGNATURE_UNRESOLVED", Message: fmt.Sprintf("could not resolve the DI signature of %s from project source", constructor.Target), Source: constructor.Source})
		} else if len(matches) > 1 {
			i.contract.Diagnostics = append(i.contract.Diagnostics, contractDiagnostic{Severity: "error", Code: "CONSTRUCTOR_SIGNATURE_AMBIGUOUS", Message: fmt.Sprintf("multiple functions named %s prevent static DI signature resolution", constructor.Target), Source: constructor.Source})
		}
	}
}

type staticHandlerSignature struct {
	parameters    []handlerParameter
	returns       []string
	source        string
	unimplemented bool
}

func (i *contractInspector) resolveHandlerSignatures() {
	signatures := map[string][]staticHandlerSignature{}
	collect := func(path string, file *ast.File, fset *token.FileSet) {
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Recv == nil || len(function.Recv.List) == 0 {
				continue
			}
			key := methodExpressionKey(function)
			if key == "" {
				continue
			}
			if i.qualified {
				prefix := modulePackage(i.module, filepath.ToSlash(filepath.Dir(path))) + "."
				if strings.HasPrefix(key, "(*") {
					key = "(*" + prefix + strings.TrimPrefix(key, "(*")
				} else {
					key = "(" + prefix + strings.TrimPrefix(key, "(")
				}
			}
			signature := staticHandlerSignature{
				parameters:    fieldParameters(fset, function.Type.Params),
				returns:       fieldTypes(fset, function.Type.Results),
				source:        relativeSource(fset.Position(function.Pos())),
				unimplemented: containsUnimplementedHandlerError(function),
			}
			if i.qualified {
				signature.parameters = layeredHandlerParameters(file, fset, function.Type.Params)
			}
			signatures[key] = append(signatures[key], signature)
			// 값 receiver의 메서드도 Spine이 요구하는 포인터 메서드 집합에 포함됩니다.
			if i.qualified && !strings.HasPrefix(key, "(*") {
				pointerKey := "(*" + strings.TrimPrefix(key, "(")
				signatures[pointerKey] = append(signatures[pointerKey], signature)
			}
		}
	}
	if i.qualified {
		for _, source := range i.sources.sortedFiles() {
			collect(source.Path, source.File, source.Fset)
		}
	} else {
		_ = filepath.WalkDir(".", func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if entry.IsDir() && (entry.Name() == ".git" || entry.Name() == "bin" || entry.Name() == "vendor" || strings.HasPrefix(entry.Name(), ".spine")) {
				return filepath.SkipDir
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			fset := token.NewFileSet()
			file, parseErr := parser.ParseFile(fset, path, nil, 0)
			if parseErr == nil {
				collect(path, file, fset)
			}
			return nil
		})
	}

	for index := range i.contract.HTTP {
		route := &i.contract.HTTP[index]
		matches := signatures[route.Handler]
		if len(matches) == 1 {
			route.Parameters = bindHTTPParameters(matches[0].parameters, route.Path)
			route.Returns = matches[0].returns
			pathKeys := contractPathKeys(route.Path)
			boundPathCount := 0
			for _, parameter := range route.Parameters {
				if parameter.Binding == "path" {
					boundPathCount++
				}
			}
			if boundPathCount != len(pathKeys) {
				i.contract.Diagnostics = append(i.contract.Diagnostics, contractDiagnostic{Severity: "error", Code: "HTTP_PATH_BINDING_MISMATCH", Message: fmt.Sprintf("route %s %s declares %d path keys but the handler binds %d", route.Method, route.Path, len(pathKeys), boundPathCount), Source: route.Source})
			}
		} else if len(matches) == 0 {
			i.contract.Diagnostics = append(i.contract.Diagnostics, contractDiagnostic{Severity: "error", Code: "HTTP_HANDLER_SIGNATURE_UNRESOLVED", Message: "could not resolve handler signature " + route.Handler + " from project source", Source: route.Source})
		} else {
			i.contract.Diagnostics = append(i.contract.Diagnostics, contractDiagnostic{Severity: "error", Code: "HTTP_HANDLER_SIGNATURE_AMBIGUOUS", Message: "multiple methods match " + route.Handler, Source: route.Source})
		}
	}
	for index := range i.contract.Consumers {
		registration := &i.contract.Consumers[index]
		matches := signatures[registration.Handler]
		if len(matches) == 1 {
			registration.Parameters = bindMessageParameters(matches[0].parameters, "consumer")
			registration.Returns = matches[0].returns
			if matches[0].unimplemented {
				i.contract.Diagnostics = append(i.contract.Diagnostics, contractDiagnostic{Severity: "error", Code: "CONSUMER_HANDLER_NOT_IMPLEMENTED", Message: fmt.Sprintf("consumer %q still uses the generated fail-closed handler", registration.Name), Source: matches[0].source})
			}
		} else if len(matches) == 0 {
			i.contract.Diagnostics = append(i.contract.Diagnostics, contractDiagnostic{Severity: "error", Code: "CONSUMER_HANDLER_SIGNATURE_UNRESOLVED", Message: "could not resolve handler signature " + registration.Handler + " from project source", Source: registration.Source})
		} else {
			i.contract.Diagnostics = append(i.contract.Diagnostics, contractDiagnostic{Severity: "error", Code: "CONSUMER_HANDLER_SIGNATURE_AMBIGUOUS", Message: "multiple methods match " + registration.Handler, Source: registration.Source})
		}
	}
	for index := range i.contract.WebSockets {
		registration := &i.contract.WebSockets[index]
		matches := signatures[registration.Handler]
		if len(matches) == 1 {
			registration.Parameters = bindMessageParameters(matches[0].parameters, "websocket")
			registration.Returns = matches[0].returns
			if matches[0].unimplemented {
				i.contract.Diagnostics = append(i.contract.Diagnostics, contractDiagnostic{Severity: "error", Code: "WEBSOCKET_HANDLER_NOT_IMPLEMENTED", Message: fmt.Sprintf("WebSocket %q still uses the generated fail-closed handler", registration.Name), Source: matches[0].source})
			}
		} else if len(matches) == 0 {
			i.contract.Diagnostics = append(i.contract.Diagnostics, contractDiagnostic{Severity: "error", Code: "WEBSOCKET_HANDLER_SIGNATURE_UNRESOLVED", Message: "could not resolve handler signature " + registration.Handler + " from project source", Source: registration.Source})
		} else {
			i.contract.Diagnostics = append(i.contract.Diagnostics, contractDiagnostic{Severity: "error", Code: "WEBSOCKET_HANDLER_SIGNATURE_AMBIGUOUS", Message: "multiple methods match " + registration.Handler, Source: registration.Source})
		}
	}
}

func containsUnimplementedHandlerError(function *ast.FuncDecl) bool {
	found := false
	ast.Inspect(function.Body, func(node ast.Node) bool {
		literal, ok := node.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		value, err := strconv.Unquote(literal.Value)
		if err == nil && strings.HasSuffix(value, " handler is not implemented") {
			found = true
			return false
		}
		return true
	})
	return found
}

func methodExpressionKey(function *ast.FuncDecl) string {
	receiver := function.Recv.List[0].Type
	switch value := receiver.(type) {
	case *ast.StarExpr:
		identifier, ok := value.X.(*ast.Ident)
		if !ok {
			return ""
		}
		return "(*" + identifier.Name + ")." + function.Name.Name
	case *ast.Ident:
		return "(" + value.Name + ")." + function.Name.Name
	default:
		return ""
	}
}

func fieldParameters(fset *token.FileSet, fields *ast.FieldList) []handlerParameter {
	if fields == nil {
		return nil
	}
	var result []handlerParameter
	argumentIndex := 0
	for _, field := range fields.List {
		typeName := expressionText(fset, field.Type)
		names := field.Names
		if len(names) == 0 {
			names = []*ast.Ident{{Name: fmt.Sprintf("arg%d", argumentIndex)}}
		}
		for _, name := range names {
			result = append(result, handlerParameter{Name: name.Name, Type: typeName})
			argumentIndex++
		}
	}
	return result
}

func fieldTypes(fset *token.FileSet, fields *ast.FieldList) []string {
	if fields == nil {
		return nil
	}
	var result []string
	for _, field := range fields.List {
		count := len(field.Names)
		if count == 0 {
			count = 1
		}
		for index := 0; index < count; index++ {
			result = append(result, expressionText(fset, field.Type))
		}
	}
	return result
}

func bindHTTPParameters(parameters []handlerParameter, path string) []handlerParameter {
	result := append([]handlerParameter(nil), parameters...)
	keys := contractPathKeys(path)
	pathIndex := 0
	for index := range result {
		parameter := &result[index]
		switch {
		case parameter.Type == "context.Context" || parameter.Type == "spine.Ctx" || strings.HasSuffix(parameter.Type, "ExecutionContext"):
			parameter.Binding = "context"
		case strings.HasPrefix(parameter.Type, "path."):
			parameter.Binding = "path"
			if pathIndex < len(keys) {
				parameter.Key = keys[pathIndex]
				pathIndex++
			}
		case strings.HasPrefix(parameter.Type, "query."):
			parameter.Binding = "query"
		case strings.HasPrefix(parameter.Type, "header."):
			parameter.Binding = "header"
		case strings.HasPrefix(parameter.Type, "*"):
			parameter.Binding = "body"
		default:
			parameter.Binding = "value"
		}
	}
	return result
}

func bindMessageParameters(parameters []handlerParameter, transport string) []handlerParameter {
	result := append([]handlerParameter(nil), parameters...)
	payloadAssigned := false
	for index := range result {
		parameter := &result[index]
		switch {
		case parameter.Type == "context.Context" || strings.HasSuffix(parameter.Type, "ExecutionContext"):
			parameter.Binding = "context"
		case transport == "websocket" && strings.HasSuffix(parameter.Type, "ConnectionID"):
			parameter.Binding = "connection"
		case transport == "consumer" && parameter.Type == "string" && !payloadAssigned:
			parameter.Binding = "event-name"
			payloadAssigned = true
		default:
			parameter.Binding = "payload"
		}
	}
	return result
}

func contractPathKeys(path string) []string {
	var keys []string
	for _, segment := range splitContractPath(path) {
		if strings.HasPrefix(segment, ":") && len(segment) > 1 {
			keys = append(keys, segment[1:])
		}
	}
	return keys
}

func (i *contractInspector) inspectRegistryCall(registry string, call *ast.CallExpr, file *ast.File, fset *token.FileSet, source string, imports map[string]string) {
	if len(call.Args) < 2 {
		i.contract.Diagnostics = append(i.contract.Diagnostics, contractDiagnostic{Severity: "error", Code: strings.ToUpper(registry) + "_REGISTRATION_ARGUMENTS_INVALID", Message: registry + " registration requires a name and handler", Source: source})
		return
	}
	name, ok := registrationString(call.Args[0], imports)
	if !ok {
		i.contract.Diagnostics = append(i.contract.Diagnostics, contractDiagnostic{Severity: "error", Code: strings.ToUpper(registry) + "_REGISTRATION_DYNAMIC", Message: "static inspection cannot resolve this registration name; only string literals, concatenation, and direct os.Getenv calls are supported", Source: source})
	}
	registration := namedRegistration{Name: name, Handler: i.registrationExpression(call.Args[1], file, fset), Source: source}
	if registry == "consumer" {
		i.contract.Consumers = append(i.contract.Consumers, registration)
	} else {
		i.contract.WebSockets = append(i.contract.WebSockets, registration)
	}
}

// registrationString은 코드를 실행하지 않고 문자열과 명시적인 환경변수 입력만 해석합니다.
// 지역 변수와 임의 함수 호출은 현재 값이 같아 보이더라도 추측하지 않습니다.
func registrationString(expression ast.Expr, imports map[string]string) (string, bool) {
	if value, ok := stringLiteral(expression); ok {
		return value, true
	}
	switch value := expression.(type) {
	case *ast.ParenExpr:
		return registrationString(value.X, imports)
	case *ast.BinaryExpr:
		if value.Op != token.ADD {
			return "", false
		}
		left, leftOK := registrationString(value.X, imports)
		right, rightOK := registrationString(value.Y, imports)
		return left + right, leftOK && rightOK
	case *ast.CallExpr:
		selector, ok := value.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "Getenv" || len(value.Args) != 1 {
			return "", false
		}
		alias, ok := selector.X.(*ast.Ident)
		if !ok || alias.Obj != nil || imports[alias.Name] != "os" {
			return "", false
		}
		key, ok := stringLiteral(value.Args[0])
		if !ok || key == "" {
			return "", false
		}
		return os.Getenv(key), true
	}
	return "", false
}

func registrationCondition(expression ast.Expr, imports map[string]string) (bool, bool) {
	if parentheses, ok := expression.(*ast.ParenExpr); ok {
		return registrationCondition(parentheses.X, imports)
	}
	comparison, ok := expression.(*ast.BinaryExpr)
	if !ok || (comparison.Op != token.EQL && comparison.Op != token.NEQ) {
		return false, false
	}
	left, leftOK := registrationString(comparison.X, imports)
	right, rightOK := registrationString(comparison.Y, imports)
	if !leftOK || !rightOK {
		return false, false
	}
	equal := left == right
	return equal == (comparison.Op == token.EQL), true
}

func (i *contractInspector) inspectResource(packageName, importPath, source string) {
	i.addResource(packageName, importPath, source)
	if i.seen[importPath] {
		return
	}
	i.seen[importPath] = true
	directory := filepath.FromSlash(strings.TrimPrefix(importPath, i.module+"/"))
	if i.qualified {
		i.inspectLayeredResource(filepath.ToSlash(directory), importPath, source)
		return
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		i.contract.Diagnostics = append(i.contract.Diagnostics, contractDiagnostic{Severity: "error", Code: "RESOURCE_SOURCE_UNREADABLE", Message: fmt.Sprintf("could not read resource %s: %v", importPath, err), Source: source})
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		fset := token.NewFileSet()
		file, parseErr := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if parseErr != nil {
			i.contract.Diagnostics = append(i.contract.Diagnostics, contractDiagnostic{Severity: "error", Code: "RESOURCE_SOURCE_INVALID", Message: parseErr.Error(), Source: path})
			continue
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Name.Name != "Register" || function.Body == nil || function.Type.Params == nil || len(function.Type.Params.List) == 0 || len(function.Type.Params.List[0].Names) == 0 {
				continue
			}
			i.inspectFile(file, fset, function.Body, function.Type.Params.List[0].Names[0].Name)
			break
		}
	}
}

func (i *contractInspector) addResource(packageName, importPath, source string) {
	if i.seen[importPath] {
		return
	}
	i.contract.Resources = append(i.contract.Resources, resourceRegistration{Package: packageName, Import: importPath, Source: source})
}

func (i *contractInspector) addDuplicateDiagnostics() {
	routes := map[string]string{}
	for index, route := range i.contract.HTTP {
		key := route.Method + " " + route.Path
		if previous, ok := routes[key]; ok && route.Method != "" && route.Path != "" {
			i.contract.Diagnostics = append(i.contract.Diagnostics, contractDiagnostic{Severity: "error", Code: "HTTP_ROUTE_DUPLICATE", Message: fmt.Sprintf("duplicate route %s (first registered at %s)", key, previous), Source: route.Source})
		} else {
			routes[key] = route.Source
		}
		if route.Method == "" || route.Path == "" {
			continue
		}
		for _, previous := range i.contract.HTTP[:index] {
			if previous.Method == route.Method && previous.Path != route.Path && routesOverlap(previous.Path, route.Path) {
				i.contract.Diagnostics = append(i.contract.Diagnostics, contractDiagnostic{Severity: "error", Code: "HTTP_ROUTE_AMBIGUOUS", Message: fmt.Sprintf("route %s %s overlaps %s registered at %s", route.Method, route.Path, previous.Path, previous.Source), Source: route.Source})
			}
		}
	}
	i.addNamedDuplicateDiagnostics("CONSUMER_DUPLICATE", "consumer", i.contract.Consumers)
	i.addNamedDuplicateDiagnostics("WEBSOCKET_DUPLICATE", "WebSocket", i.contract.WebSockets)
}

func (i *contractInspector) addNamedDuplicateDiagnostics(code, kind string, registrations []namedRegistration) {
	seen := map[string]string{}
	for _, registration := range registrations {
		if previous, ok := seen[registration.Name]; ok && registration.Name != "" {
			i.contract.Diagnostics = append(i.contract.Diagnostics, contractDiagnostic{Severity: "error", Code: code, Message: fmt.Sprintf("duplicate %s registration %q (first registered at %s)", kind, registration.Name, previous), Source: registration.Source})
		} else {
			seen[registration.Name] = registration.Source
		}
	}
}

func routesOverlap(left, right string) bool {
	leftSegments := splitContractPath(left)
	rightSegments := splitContractPath(right)
	if len(leftSegments) != len(rightSegments) {
		return false
	}
	for index := range leftSegments {
		leftParameter := strings.HasPrefix(leftSegments[index], ":")
		rightParameter := strings.HasPrefix(rightSegments[index], ":")
		if !leftParameter && !rightParameter && leftSegments[index] != rightSegments[index] {
			return false
		}
	}
	return true
}

func splitContractPath(path string) []string {
	path = strings.Trim(strings.TrimSpace(path), "/")
	if path == "" {
		return nil
	}
	return strings.Split(path, "/")
}

func (i *contractInspector) sort() {
	sort.Slice(i.contract.HTTP, func(a, b int) bool {
		left, right := i.contract.HTTP[a], i.contract.HTTP[b]
		return left.Method+" "+left.Path+left.Source < right.Method+" "+right.Path+right.Source
	})
	sort.Slice(i.contract.Resources, func(a, b int) bool { return i.contract.Resources[a].Import < i.contract.Resources[b].Import })
	sort.Slice(i.contract.Configuration, func(a, b int) bool {
		left, right := i.contract.Configuration[a], i.contract.Configuration[b]
		return left.Path+left.Value+left.Source < right.Path+right.Value+right.Source
	})
	sort.Slice(i.contract.Diagnostics, func(a, b int) bool {
		left, right := i.contract.Diagnostics[a], i.contract.Diagnostics[b]
		return left.Severity+left.Code+left.Source < right.Severity+right.Code+right.Source
	})
}

func directAppCall(call *ast.CallExpr, appName string) (string, bool) {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	receiver, ok := selector.X.(*ast.Ident)
	return selector.Sel.Name, ok && receiver.Name == appName
}

func astParentMap(root ast.Node) map[ast.Node]ast.Node {
	parents := map[ast.Node]ast.Node{}
	var stack []ast.Node
	ast.Inspect(root, func(node ast.Node) bool {
		if node == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		if len(stack) > 0 {
			parents[node] = stack[len(stack)-1]
		}
		stack = append(stack, node)
		return true
	})
	return parents
}

func conditionallyExecuted(node, root ast.Node, parents map[ast.Node]ast.Node, conditions map[*ast.IfStmt]bool) bool {
	child := node
	for parent := parents[child]; parent != nil && parent != root; parent = parents[child] {
		switch statement := parent.(type) {
		case *ast.IfStmt:
			if child != statement.Init {
				if _, resolved := conditions[statement]; !resolved {
					return true
				}
			}
		case *ast.ForStmt:
			if child != statement.Init {
				return true
			}
		case *ast.RangeStmt:
			if child == statement.Body {
				return true
			}
		case *ast.SwitchStmt:
			if child == statement.Body {
				return true
			}
		case *ast.TypeSwitchStmt:
			if child == statement.Body {
				return true
			}
		case *ast.SelectStmt:
			return true
		}
		child = parent
	}
	return false
}

func containsAppContractCall(root ast.Node, appName string) bool {
	found := false
	ast.Inspect(root, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if selector, direct := directAppCall(call, appName); direct {
			switch selector {
			case "Constructor", "Route", "Interceptor", "InterceptorFor":
				found = true
				return false
			}
		}
		if _, registry := registryCall(call, appName); registry || hasIdentifierArgument(call, appName) {
			found = true
			return false
		}
		return true
	})
	return found
}

func hasIdentifierArgument(call *ast.CallExpr, name string) bool {
	for _, argument := range call.Args {
		identifier, ok := argument.(*ast.Ident)
		if ok && identifier.Name == name {
			return true
		}
	}
	return false
}

func registryCall(call *ast.CallExpr, appName string) (string, bool) {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Register" {
		return "", false
	}
	receiverCall, ok := selector.X.(*ast.CallExpr)
	if !ok {
		return "", false
	}
	receiverSelector, ok := receiverCall.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	receiver, ok := receiverSelector.X.(*ast.Ident)
	if !ok || receiver.Name != appName {
		return "", false
	}
	switch receiverSelector.Sel.Name {
	case "Consumers":
		return "consumer", true
	case "WebSocket":
		return "websocket", true
	default:
		return "", false
	}
}

func importPathsByAlias(file *ast.File) map[string]string {
	result := map[string]string{}
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		alias := filepath.Base(path)
		if spec.Name != nil {
			alias = spec.Name.Name
		}
		result[alias] = path
	}
	return result
}

func stringLiteral(expression ast.Expr) (string, bool) {
	literal, ok := expression.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(literal.Value)
	return value, err == nil
}

func expressionText(fset *token.FileSet, expression ast.Expr) string {
	var result strings.Builder
	if err := format.Node(&result, fset, expression); err != nil {
		return "<unprintable>"
	}
	return result.String()
}

func relativeSource(position token.Position) string {
	path := position.Filename
	if relative, err := filepath.Rel(".", path); err == nil {
		path = relative
	}
	return fmt.Sprintf("%s:%d", filepath.ToSlash(path), position.Line)
}

func runInspect(args []string, out, errOut io.Writer) int {
	jsonOutput := false
	for _, argument := range args {
		if argument == "--json" {
			jsonOutput = true
		} else {
			fmt.Fprintf(errOut, "unknown option: %s\n", argument)
			return 1
		}
	}
	contract, err := inspectApplication()
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	if jsonOutput {
		encoder := json.NewEncoder(out)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(contract); err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
		return 0
	}
	fmt.Fprintf(out, "Spine application contract (%s, %s)\n", contract.SchemaVersion, contract.AnalysisMode)
	fmt.Fprintf(out, "  module: %s\n  composition: %s\n", contract.Module, contract.Composition)
	fmt.Fprintf(out, "  constructors: %d\n  HTTP routes: %d\n  interceptors: %d\n  consumers: %d\n  WebSockets: %d\n  resources: %d\n", len(contract.Constructors), len(contract.HTTP), len(contract.Interceptors), len(contract.Consumers), len(contract.WebSockets), len(contract.Resources))
	for _, route := range contract.HTTP {
		fmt.Fprintf(out, "  %s %-24s %s (%s)\n", route.Method, route.Path, route.Handler, route.Source)
		for _, parameter := range route.Parameters {
			fmt.Fprintf(out, "    %s %s -> %s %s\n", parameter.Name, parameter.Type, parameter.Binding, parameter.Key)
		}
		for _, interceptor := range route.Interceptors {
			fmt.Fprintf(out, "    route interceptor: %s\n", interceptor)
		}
	}
	for _, setting := range contract.Configuration {
		fmt.Fprintf(out, "  config %s = %s (%s)\n", setting.Path, setting.Value, setting.Source)
	}
	for _, diagnostic := range contract.Diagnostics {
		fmt.Fprintf(out, "  [%s] %s: %s (%s)\n", diagnostic.Severity, diagnostic.Code, diagnostic.Message, diagnostic.Source)
	}
	return 0
}

func contractHasErrors(contract applicationContract) bool {
	for _, diagnostic := range contract.Diagnostics {
		if diagnostic.Severity == "error" {
			return true
		}
	}
	return false
}
