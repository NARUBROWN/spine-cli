package cli

import (
	"fmt"
	"go/ast"
	"go/token"
	"path/filepath"
	"strings"
)

func layeredHandlerParameters(file *ast.File, fset *token.FileSet, fields *ast.FieldList) []handlerParameter {
	parameters := fieldParameters(fset, fields)
	imports := importPathsByAlias(file)
	for index := range parameters {
		alias, name, ok := strings.Cut(parameters[index].Type, ".")
		if !ok {
			continue
		}
		path := imports[alias]
		canonical := ""
		switch path {
		case "context":
			canonical = "context"
		case spineImportPath:
			canonical = "spine"
		case spineImportPath + "/pkg/path", spineImportPath + "/pkg/query", spineImportPath + "/pkg/header":
			canonical = filepath.Base(path)
		}
		if canonical == "" {
			continue
		}
		parameters[index].Type = canonical + "." + name
	}
	return parameters
}

func modulePackage(module, directory string) string {
	if directory == "." || directory == "" {
		return module
	}
	return module + "/" + directory
}

func (i *contractInspector) registrationExpression(expression ast.Expr, file *ast.File, fset *token.FileSet) string {
	imports := importPathsByAlias(file)
	if !i.qualified {
		return normalizeImportedExpression(expressionText(fset, expression), imports, i.module)
	}
	directory := filepath.ToSlash(filepath.Dir(fset.Position(file.Package).Filename))
	return qualifiedExpression(expression, imports, modulePackage(i.module, directory), fset)
}

// 식별자 단위로 해석해 별칭이 다른 식별자의 일부와 겹쳐도 잘못 치환하지 않습니다.
func qualifiedExpression(expression ast.Expr, imports map[string]string, pkg string, fset *token.FileSet) string {
	switch e := expression.(type) {
	case *ast.Ident:
		return pkg + "." + e.Name
	case *ast.SelectorExpr:
		if alias, ok := e.X.(*ast.Ident); ok && imports[alias.Name] != "" {
			return imports[alias.Name] + "." + e.Sel.Name
		}
		return qualifiedExpression(e.X, imports, pkg, fset) + "." + e.Sel.Name
	case *ast.ParenExpr:
		return "(" + qualifiedExpression(e.X, imports, pkg, fset) + ")"
	case *ast.StarExpr:
		return "*" + qualifiedExpression(e.X, imports, pkg, fset)
	}
	return expressionText(fset, expression)
}

// resolveLayeredConstructors는 패키지가 다른 동명 생성자를 구분합니다.
func (i *contractInspector) resolveLayeredConstructors() {
	signatures := i.sources.constructorSignatures(i.module)
	for index := range i.contract.Constructors {
		ctor := &i.contract.Constructors[index]
		if ctor.object {
			continue
		}
		if matches := signatures[ctor.Target]; len(matches) == 1 && matches[0].Output != "" {
			ctor.Dependencies = matches[0].Dependencies
		} else if !ctor.typedResolved {
			i.contract.Diagnostics = append(i.contract.Diagnostics, contractDiagnostic{Severity: "error", Code: "CONSTRUCTOR_SIGNATURE_UNRESOLVED", Message: fmt.Sprintf("could not resolve %s from project source", ctor.Target), Source: ctor.Source})
		}
	}
}

// inspectLayeredRegistrations는 아직 적용하지 않은 소스에도 동일한 등록 해석 규칙을 적용합니다.
func inspectLayeredRegistrations(config projectConfig, module string, sources layoutSources) (*contractInspector, error) {
	source, function, index, appName, err := sources.composition(config.Composition)
	if err != nil {
		return nil, err
	}
	inspector := newContractInspector(config, module, sources)
	inspector.inspectFile(source.File, source.Fset, function.Body, appName)
	inspector.addDuplicateDiagnostics()
	inspector.inspectHTTPAdapter(config, source.File, source.Fset, function, index)
	return inspector, nil
}

func (i *contractInspector) inspectLayeredResource(directory, importPath, source string) {
	type registration struct {
		source   *layoutSource
		function *ast.FuncDecl
	}
	matches := []registration{}
	for _, file := range i.sources.packageFiles(directory) {
		for _, declaration := range file.File.Decls {
			if function, ok := declaration.(*ast.FuncDecl); ok && function.Name.Name == "Register" && function.Recv == nil {
				matches = append(matches, registration{file, function})
			}
		}
	}
	if len(matches) != 1 || !validRouteRegisterSignature(matches[0].function, matches[0].source.Fset, matches[0].source.File) || matches[0].function.Body == nil {
		i.contract.Diagnostics = append(i.contract.Diagnostics, contractDiagnostic{Severity: "error", Code: "RESOURCE_SOURCE_INVALID", Message: fmt.Sprintf("%s must declare exactly one Register(app spine.App) error function", importPath), Source: source})
		return
	}
	match := matches[0]
	i.inspectFile(match.source.File, match.source.Fset, match.function.Body, match.function.Type.Params.List[0].Names[0].Name)
}
