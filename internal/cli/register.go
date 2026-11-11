package cli

import (
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const spineImportPath = "github.com/NARUBROWN/spine"

func modulePath() (string, error) {
	config, err := loadProjectConfig()
	if err == nil {
		moduleFromGoMod, goModErr := modulePathFromGoMod()
		if goModErr != nil {
			return "", goModErr
		}
		if config.Module != moduleFromGoMod {
			return "", fmt.Errorf("spine.toml module %q does not match go.mod module %q", config.Module, moduleFromGoMod)
		}
		return config.Module, nil
	}
	return modulePathFromGoMod()
}

func modulePathFromGoMod() (string, error) {
	contents, err := os.ReadFile("go.mod")
	if err != nil {
		return "", fmt.Errorf("resource registration requires go.mod in the current directory: %w", err)
	}
	for _, line := range strings.Split(string(contents), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "module" {
			return fields[1], nil
		}
	}
	return "", fmt.Errorf("could not find module name in go.mod")
}

func prepareComponentWiring(kind string, name resourceName, directory, packageName string) ([]fileChange, error) {
	module, err := modulePath()
	if err != nil {
		return nil, err
	}
	config, err := loadProjectConfig()
	if err != nil {
		return nil, err
	}
	path, file, fset, function, statementIndex, appName, err := findCompositionRoot(config.Composition)
	if err != nil {
		return nil, err
	}
	importPath := module + "/" + filepath.ToSlash(directory)
	alias := packageName
	constructorName := "New" + name.Type + strings.ToUpper(kind[:1]) + kind[1:]
	modernHTTP := kind == "controller" && requiresExplicitHTTPInputs(declaredSpineVersion())
	statements := []ast.Stmt{}
	factoryRegistered := hasComponentConstructor(function.Body, alias, constructorName, appName, nil)
	if modernHTTP && factoryRegistered {
		return nil, fmt.Errorf("migrate the existing %s registration to constructor.Controller before regenerating", constructorName)
	}
	registered := factoryRegistered
	if modernHTTP {
		registered = hasComponentConstructor(function.Body, alias, name.Type+"Controller", appName, importPathsByAlias(file))
	}
	if !registered {
		if err := rejectDuplicateConstructorNames(constructorName); err != nil {
			return nil, err
		}
		if err := addNamedImport(file, alias, importPath); err != nil {
			return nil, err
		}
		var argument ast.Expr = &ast.SelectorExpr{X: ast.NewIdent(alias), Sel: ast.NewIdent(constructorName)}
		if modernHTTP {
			markerAlias := availableImportAlias(file, "constructor", constructorImportPath)
			if err := addNamedImport(file, markerAlias, constructorImportPath); err != nil {
				return nil, err
			}
			typ := &ast.SelectorExpr{X: ast.NewIdent(alias), Sel: ast.NewIdent(name.Type + "Controller")}
			argument = &ast.CallExpr{Fun: &ast.SelectorExpr{X: ast.NewIdent(markerAlias), Sel: ast.NewIdent("Controller")}, Args: []ast.Expr{
				&ast.UnaryExpr{Op: token.AND, X: &ast.CompositeLit{Type: typ}},
			}}
		}
		statements = append(statements, &ast.ExprStmt{X: &ast.CallExpr{
			Fun:  &ast.SelectorExpr{X: ast.NewIdent(appName), Sel: ast.NewIdent("Constructor")},
			Args: []ast.Expr{argument},
		}})
	}
	changes := []fileChange{}
	if kind == "controller" && !modernHTTP {
		routeChange, routeErr := prepareLayeredControllerRoute(name, module, directory, packageName)
		if routeErr != nil {
			return nil, routeErr
		}
		changes = append(changes, routeChange)
		routeAlias := availableImportAlias(file, "route", module+"/src/route")
		if err := addNamedImport(file, routeAlias, module+"/src/route"); err != nil {
			return nil, err
		}
		if !hasRegisterCall(function.Body, routeAlias, appName) {
			bootAlias, ok := checkedRegistrationBootAlias(function, fset, file)
			if !ok {
				return nil, fmt.Errorf("composition root %s must return (spine.App, boot.Options, error) before automatic route registration; use --no-register until it is migrated", function.Name.Name)
			}
			statements = append(statements, checkedRegisterStatement(routeAlias, appName, bootAlias))
		}
	}
	oldLength := len(function.Body.List)
	function.Body.List = append(function.Body.List, make([]ast.Stmt, len(statements))...)
	copy(function.Body.List[statementIndex+1+len(statements):], function.Body.List[statementIndex+1:oldLength])
	copy(function.Body.List[statementIndex+1:], statements)
	var formatted strings.Builder
	if err := format.Node(&formatted, fset, file); err != nil {
		return nil, fmt.Errorf("could not format updated %s: %w", path, err)
	}
	changes = append(changes, fileChange{Path: path, Contents: formatted.String(), AllowOverwrite: true})
	return changes, nil
}

func prepareResourceModuleRegistration(name resourceName, module string) (fileChange, error) {
	importPath := module + "/src/" + name.Package
	registered, err := moduleRegistrationExists(name.Package, importPath)
	if err != nil {
		return fileChange{}, err
	}
	if !registered {
		constructors := []string{
			"New" + name.Type + "Repository",
			"New" + name.Type + "Service",
			"New" + name.Type + "Controller",
		}
		if err := rejectDuplicateConstructorNames(constructors...); err != nil {
			return fileChange{}, err
		}
	}
	return prepareModuleRegistration(name.Package, importPath)
}

func moduleRegistrationExists(alias, importPath string) (bool, error) {
	config, err := loadProjectConfig()
	if err != nil {
		return false, err
	}
	_, file, _, function, _, appName, err := findCompositionRoot(config.Composition)
	if err != nil {
		return false, err
	}
	return importPathsByAlias(file)[alias] == importPath && hasRegisterCall(function.Body, alias, appName), nil
}

func rejectDuplicateConstructorNames(names ...string) error {
	contract, err := inspectApplication()
	if err != nil {
		return fmt.Errorf("could not inspect existing constructor registrations: %w", err)
	}
	wanted := make(map[string]bool, len(names))
	for _, name := range names {
		wanted[name] = true
	}
	for _, constructor := range contract.Constructors {
		name := constructor.Target
		if separator := strings.LastIndex(name, "."); separator >= 0 {
			name = name[separator+1:]
		}
		if wanted[name] {
			return fmt.Errorf("constructor %s is already registered at %s; refusing duplicate registration", name, constructor.Source)
		}
	}
	return nil
}

func availableImportAlias(file *ast.File, preferred, importPath string) string {
	imports := importPathsByAlias(file)
	if existing := imports[preferred]; existing == "" || existing == importPath {
		return preferred
	}
	return "generatedRoute"
}

func checkedRegisterStatement(alias, appName, bootAlias string) ast.Stmt {
	call := &ast.CallExpr{Fun: &ast.SelectorExpr{X: ast.NewIdent(alias), Sel: ast.NewIdent("Register")}, Args: []ast.Expr{ast.NewIdent(appName)}}
	return &ast.IfStmt{
		Init: &ast.AssignStmt{Lhs: []ast.Expr{ast.NewIdent("err")}, Tok: token.DEFINE, Rhs: []ast.Expr{call}},
		Cond: &ast.BinaryExpr{X: ast.NewIdent("err"), Op: token.NEQ, Y: ast.NewIdent("nil")},
		Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{
			ast.NewIdent("nil"),
			&ast.CompositeLit{Type: &ast.SelectorExpr{X: ast.NewIdent(bootAlias), Sel: ast.NewIdent("Options")}},
			ast.NewIdent("err"),
		}}}},
	}
}

func prepareLayeredControllerRoute(name resourceName, module, directory, packageName string) (fileChange, error) {
	path := filepath.Join("src", "route", "route.go")
	importPath := module + "/" + filepath.ToSlash(directory)
	localController := filepath.Clean(directory) == filepath.Clean(filepath.Dir(path))
	contents, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		var source string
		if localController {
			source = fmt.Sprintf(`package route

import "github.com/NARUBROWN/spine"

func Register(app spine.App) error {
	app.Route("GET", "/%[1]s/:id", (*%[2]sController).Get)
	return nil
}
`, name.Route, name.Type)
		} else {
			source = fmt.Sprintf(`package route

import (
	%[1]s "%[2]s"

	"github.com/NARUBROWN/spine"
)

func Register(app spine.App) error {
	app.Route("GET", "/%[3]s/:id", (*%[1]s.%[4]sController).Get)
	return nil
}
`, packageName, importPath, name.Route, name.Type)
		}
		formatted, formatErr := format.Source([]byte(source))
		if formatErr != nil {
			return fileChange{}, formatErr
		}
		return fileChange{Path: path, Contents: string(formatted), AllowOverwrite: true}, nil
	}
	if err != nil {
		return fileChange{}, err
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, contents, parser.ParseComments)
	if err != nil {
		return fileChange{}, fmt.Errorf("could not parse %s: %w", path, err)
	}
	var register *ast.FuncDecl
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Recv == nil && function.Name.Name == "Register" && function.Body != nil {
			register = function
			break
		}
	}
	if register == nil || !validRouteRegisterSignature(register, fset, file) {
		return fileChange{}, fmt.Errorf("generated route file %s must declare Register(app spine.App) error", path)
	}
	alias := ""
	if !localController {
		alias = availableImportAlias(file, packageName, importPath)
		if err := addNamedImport(file, alias, importPath); err != nil {
			return fileChange{}, err
		}
	}
	appName := register.Type.Params.List[0].Names[0].Name
	handler, routeExists := findLayeredControllerRoute(register, appName, "GET", "/"+name.Route+"/:id")
	if routeExists && !isControllerGetHandler(handler, alias, name.Type+"Controller") {
		return fileChange{}, fmt.Errorf("route GET /%s/:id is already registered with a different handler in %s", name.Route, path)
	}
	if !routeExists {
		var controllerType ast.Expr = ast.NewIdent(name.Type + "Controller")
		if alias != "" {
			controllerType = &ast.SelectorExpr{X: ast.NewIdent(alias), Sel: ast.NewIdent(name.Type + "Controller")}
		}
		route := &ast.ExprStmt{X: &ast.CallExpr{Fun: &ast.SelectorExpr{X: ast.NewIdent(appName), Sel: ast.NewIdent("Route")}, Args: []ast.Expr{
			&ast.BasicLit{Kind: token.STRING, Value: strconv.Quote("GET")},
			&ast.BasicLit{Kind: token.STRING, Value: strconv.Quote("/" + name.Route + "/:id")},
			&ast.SelectorExpr{X: &ast.ParenExpr{X: &ast.StarExpr{X: controllerType}}, Sel: ast.NewIdent("Get")},
		}}}
		insert := len(register.Body.List)
		if insert > 0 {
			if _, ok := register.Body.List[insert-1].(*ast.ReturnStmt); ok {
				insert--
			}
		}
		register.Body.List = append(register.Body.List, nil)
		copy(register.Body.List[insert+1:], register.Body.List[insert:])
		register.Body.List[insert] = route
	}
	var formatted strings.Builder
	if err := format.Node(&formatted, fset, file); err != nil {
		return fileChange{}, fmt.Errorf("could not format updated %s: %w", path, err)
	}
	return fileChange{Path: path, Contents: formatted.String(), AllowOverwrite: true}, nil
}

func validRouteRegisterSignature(function *ast.FuncDecl, fset *token.FileSet, file *ast.File) bool {
	if function == nil || function.Recv != nil || function.Type.Params == nil || len(function.Type.Params.List) != 1 || len(function.Type.Params.List[0].Names) != 1 {
		return false
	}
	selector, ok := function.Type.Params.List[0].Type.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "App" {
		return false
	}
	alias, ok := selector.X.(*ast.Ident)
	if !ok || importPathsByAlias(file)[alias.Name] != spineImportPath {
		return false
	}
	results := fieldTypes(fset, function.Type.Results)
	return len(results) == 1 && results[0] == "error"
}

func findLayeredControllerRoute(function *ast.FuncDecl, appName, method, path string) (ast.Expr, bool) {
	var handler ast.Expr
	ast.Inspect(function.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) < 3 {
			return true
		}
		selector, direct := directAppCall(call, appName)
		if !direct || selector != "Route" {
			return true
		}
		registeredMethod, methodOK := stringLiteral(call.Args[0])
		registeredPath, pathOK := stringLiteral(call.Args[1])
		if methodOK && pathOK && strings.EqualFold(registeredMethod, method) && registeredPath == path {
			handler = call.Args[2]
			return false
		}
		return true
	})
	return handler, handler != nil
}

func isControllerGetHandler(expression ast.Expr, alias, controller string) bool {
	handler, ok := expression.(*ast.SelectorExpr)
	if !ok || handler.Sel.Name != "Get" {
		return false
	}
	parenthesized, ok := handler.X.(*ast.ParenExpr)
	if !ok {
		return false
	}
	pointer, ok := parenthesized.X.(*ast.StarExpr)
	if !ok {
		return false
	}
	if alias == "" {
		identifier, ok := pointer.X.(*ast.Ident)
		return ok && identifier.Name == controller
	}
	typeSelector, ok := pointer.X.(*ast.SelectorExpr)
	if !ok || typeSelector.Sel.Name != controller {
		return false
	}
	packageName, ok := typeSelector.X.(*ast.Ident)
	return ok && packageName.Name == alias
}

func hasComponentConstructor(root ast.Node, alias, constructorName, appName string, imports map[string]string) bool {
	found := false
	ast.Inspect(root, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := directAppCall(call, appName)
		if !ok || selector != "Constructor" {
			return true
		}
		for _, argument := range call.Args {
			if imports != nil {
				role, value, _ := componentRoleArgument(argument, imports)
				prototype, ok := value.(*ast.UnaryExpr)
				if role != "Controller" || !ok || prototype.Op != token.AND {
					continue
				}
				literal, ok := prototype.X.(*ast.CompositeLit)
				if !ok {
					continue
				}
				argument = literal.Type
			}
			constructor, selectorOK := argument.(*ast.SelectorExpr)
			if !selectorOK {
				continue
			}
			pkg, packageOK := constructor.X.(*ast.Ident)
			if packageOK && pkg.Name == alias && constructor.Sel.Name == constructorName {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

// prepareModuleRegistration은 로컬 모듈을 검사 가능한 composition root에 안전하게 연결합니다.
func prepareModuleRegistration(alias, importPath string) (fileChange, error) {
	module, err := modulePath()
	if err != nil {
		return fileChange{}, err
	}
	if !strings.HasPrefix(importPath, module+"/") {
		return fileChange{}, fmt.Errorf("registration import %q must belong to module %q", importPath, module)
	}
	config, err := loadProjectConfig()
	if err != nil {
		return fileChange{}, err
	}
	path, file, fset, function, statementIndex, appName, err := findCompositionRoot(config.Composition)
	if err != nil {
		return fileChange{}, err
	}
	if importPathsByAlias(file)[alias] == importPath && hasRegisterCall(function.Body, alias, appName) {
		contents, readErr := os.ReadFile(path)
		if readErr != nil {
			return fileChange{}, readErr
		}
		return fileChange{Path: path, Contents: string(contents), AllowOverwrite: true}, nil
	}
	if err := addNamedImport(file, alias, importPath); err != nil {
		return fileChange{}, err
	}
	bootAlias, ok := checkedRegistrationBootAlias(function, fset, file)
	if !ok {
		return fileChange{}, fmt.Errorf("composition root %s must return (spine.App, boot.Options, error) before automatic registration; use --no-register until it is migrated", function.Name.Name)
	}
	call := &ast.CallExpr{
		Fun:  &ast.SelectorExpr{X: ast.NewIdent(alias), Sel: ast.NewIdent("Register")},
		Args: []ast.Expr{ast.NewIdent(appName)},
	}
	registration := &ast.IfStmt{
		Init: &ast.AssignStmt{Lhs: []ast.Expr{ast.NewIdent("err")}, Tok: token.DEFINE, Rhs: []ast.Expr{call}},
		Cond: &ast.BinaryExpr{X: ast.NewIdent("err"), Op: token.NEQ, Y: ast.NewIdent("nil")},
		Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{
			ast.NewIdent("nil"),
			&ast.CompositeLit{Type: &ast.SelectorExpr{X: ast.NewIdent(bootAlias), Sel: ast.NewIdent("Options")}},
			ast.NewIdent("err"),
		}}}},
	}
	function.Body.List = append(function.Body.List, nil)
	copy(function.Body.List[statementIndex+2:], function.Body.List[statementIndex+1:])
	function.Body.List[statementIndex+1] = registration
	var formatted strings.Builder
	if err := format.Node(&formatted, fset, file); err != nil {
		return fileChange{}, fmt.Errorf("could not format updated %s: %w", path, err)
	}
	return fileChange{Path: path, Contents: formatted.String(), AllowOverwrite: true}, nil
}

func findCompositionRoot(composition string) (string, *ast.File, *token.FileSet, *ast.FuncDecl, int, string, error) {
	info, err := os.Stat(composition)
	if err != nil {
		return "", nil, nil, nil, 0, "", fmt.Errorf("composition path %q is not readable: %w", composition, err)
	}
	var paths []string
	if !info.IsDir() {
		paths = []string{composition}
	} else {
		entries, readErr := os.ReadDir(composition)
		if readErr != nil {
			return "", nil, nil, nil, 0, "", readErr
		}
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".go") && !strings.HasSuffix(entry.Name(), "_test.go") {
				paths = append(paths, filepath.Join(composition, entry.Name()))
			}
		}
		sort.Strings(paths)
	}
	for _, path := range paths {
		fset := token.NewFileSet()
		file, parseErr := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if parseErr != nil {
			return "", nil, nil, nil, 0, "", fmt.Errorf("could not parse %s: %w", path, parseErr)
		}

		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			for index, statement := range function.Body.List {
				appName, ok := applicationAssignment(statement, file)
				if ok {
					return path, file, fset, function, index, appName, nil
				}
			}
		}
	}
	return "", nil, nil, nil, 0, "", fmt.Errorf("could not find a supported top-level Spine application assignment in composition path %q", composition)
}

func checkedRegistrationBootAlias(function *ast.FuncDecl, fset *token.FileSet, file *ast.File) (string, bool) {
	if function == nil {
		return "", false
	}
	results := fieldTypes(fset, function.Type.Results)
	if len(results) != 3 || results[2] != "error" {
		return "", false
	}
	spineAlias, spineType, spineOK := strings.Cut(results[0], ".")
	bootAlias, bootType, bootOK := strings.Cut(results[1], ".")
	imports := importPathsByAlias(file)
	if !spineOK || spineType != "App" || imports[spineAlias] != spineImportPath {
		return "", false
	}
	if !bootOK || bootType != "Options" || imports[bootAlias] != "github.com/NARUBROWN/spine/pkg/boot" {
		return "", false
	}
	return bootAlias, true
}

func importAliases(file *ast.File, importPath string) map[string]bool {
	aliases := map[string]bool{}
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil || path != importPath {
			continue
		}
		alias := filepath.Base(path)
		if spec.Name != nil {
			alias = spec.Name.Name
		}
		aliases[alias] = true
	}
	return aliases
}

func hasRegisterCall(root ast.Node, packageName, appName string) bool {
	found := false
	ast.Inspect(root, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) != 1 {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, packageOK := selector.X.(*ast.Ident)
		app, appOK := call.Args[0].(*ast.Ident)
		if packageOK && appOK && pkg.Name == packageName && selector.Sel.Name == "Register" && app.Name == appName {
			found = true
			return false
		}
		return true
	})
	return found
}

func addNamedImport(file *ast.File, alias, importPath string) error {
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			return fmt.Errorf("invalid import path %s", spec.Path.Value)
		}
		name := filepath.Base(path)
		if spec.Name != nil {
			name = spec.Name.Name
		}
		if path == importPath {
			if name != alias {
				return fmt.Errorf("resource import %q already uses alias %q, expected %q", importPath, name, alias)
			}
			return nil
		}
		if name == alias {
			return fmt.Errorf("import alias %q is already used by %q", alias, path)
		}
	}
	spec := &ast.ImportSpec{Name: ast.NewIdent(alias), Path: &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(importPath)}}
	for _, declaration := range file.Decls {
		imports, ok := declaration.(*ast.GenDecl)
		if !ok || imports.Tok != token.IMPORT {
			continue
		}
		if !imports.Lparen.IsValid() {
			imports.Lparen = imports.Pos()
		}
		imports.Specs = append(imports.Specs, spec)
		file.Imports = append(file.Imports, spec)
		return nil
	}
	declaration := &ast.GenDecl{Tok: token.IMPORT, Specs: []ast.Spec{spec}}
	file.Decls = append([]ast.Decl{declaration}, file.Decls...)
	file.Imports = append(file.Imports, spec)
	return nil
}
