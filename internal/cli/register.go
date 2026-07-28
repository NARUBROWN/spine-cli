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

// prepareResourceRegistration은 메모리에서 소스 변경을 준비합니다.
// 호출자가 생성 파일과 함께 반영하므로 등록 실패가 불완전한 resource를 남기지 않습니다.
func prepareResourceRegistration(name resourceName) (fileChange, error) {
	module, err := modulePath()
	if err != nil {
		return fileChange{}, err
	}
	return prepareModuleRegistration(name.Package, module+"/src/"+name.Package)
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
	if importPathsByAlias(file)[alias] == importPath && hasRegisterCall(file, alias, appName) {
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
		spineAliases := importAliases(file, spineImportPath)
		if len(spineAliases) == 0 {
			continue
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			for index, statement := range function.Body.List {
				appName, ok := spineNewAssignment(statement, spineAliases)
				if ok {
					return path, file, fset, function, index, appName, nil
				}
			}
		}
	}
	return "", nil, nil, nil, 0, "", fmt.Errorf("could not find a top-level app := spine.New() assignment in composition path %q", composition)
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

func spineNewAssignment(statement ast.Stmt, aliases map[string]bool) (string, bool) {
	assignment, ok := statement.(*ast.AssignStmt)
	if !ok || len(assignment.Lhs) != 1 || len(assignment.Rhs) != 1 {
		return "", false
	}
	identifier, ok := assignment.Lhs[0].(*ast.Ident)
	if !ok {
		return "", false
	}
	call, ok := assignment.Rhs[0].(*ast.CallExpr)
	if !ok {
		return "", false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "New" {
		return "", false
	}
	packageName, ok := selector.X.(*ast.Ident)
	return identifier.Name, ok && aliases[packageName.Name]
}

func hasRegisterCall(file *ast.File, packageName, appName string) bool {
	found := false
	ast.Inspect(file, func(node ast.Node) bool {
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
