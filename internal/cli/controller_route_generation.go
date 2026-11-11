package cli

import (
	"fmt"
	"go/ast"
	"go/parser"
	"strings"
)

// addControllerRoute는 사용자 메서드를 재작성하지 않고 컨트롤러 안에 콜백을 추가합니다.
func (p *layeredPlanner) addControllerRoute(inspector *contractInspector, request layeredRequest, directory, typeName, method string, source *layoutSource, handler *ast.FuncDecl) error {
	for _, diagnostic := range inspector.contract.Diagnostics {
		if diagnostic.Severity == "error" {
			return fmt.Errorf("%s: %s", diagnostic.Code, diagnostic.Message)
		}
	}
	for _, existing := range inspector.contract.HTTP {
		if strings.EqualFold(existing.Method, request.Method) && routesOverlap(existing.Path, request.Path) {
			expected := "(*" + modulePackage(p.Module, directory) + "." + typeName + ")." + method
			if existing.Path == request.Path && existing.delegatedMethod == expected {
				return nil
			}
			return fmt.Errorf("route %s %s overlaps registered route %s", request.Method, request.Path, existing.Path)
		}
	}
	registerSource, register := source, (*ast.FuncDecl)(nil)
	for _, file := range p.Sources.packageFiles(directory) {
		for _, decl := range file.File.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if ok && fn.Recv != nil && fn.Name.Name == "RegisterRoutes" && strings.TrimPrefix(expressionText(file.Fset, fn.Recv.List[0].Type), "*") == typeName {
				if register != nil {
					return fmt.Errorf("duplicate RegisterRoutes for %s", typeName)
				}
				register, registerSource = fn, file
			}
		}
	}
	apiAlias := availableImportAlias(registerSource.File, "api", spineImportPath+"/pkg/api")
	if err := addNamedImport(registerSource.File, apiAlias, spineImportPath+"/pkg/api"); err != nil {
		return err
	}
	if register == nil {
		parsed, err := parser.ParseFile(registerSource.Fset, "generated_controller_routes.go", "package "+registerSource.File.Name.Name+"\nfunc(c *"+typeName+")RegisterRoutes(r *"+apiAlias+".Registry){}", 0)
		if err != nil {
			return err
		}
		register = parsed.Decls[0].(*ast.FuncDecl)
		registerSource.File.Decls = append(registerSource.File.Decls, register)
	}
	if len(register.Recv.List[0].Names) != 1 || register.Type.Params == nil || len(register.Type.Params.List) != 1 || len(register.Type.Params.List[0].Names) != 1 || register.Body == nil {
		return fmt.Errorf("RegisterRoutes requires a named receiver and one named Registry parameter")
	}
	receiver := register.Recv.List[0].Names[0].Name
	registry := register.Type.Params.List[0].Names[0].Name
	// 서로 다른 파일의 import 별칭은 타입 표현식을 원래 파일 기준으로 변환합니다.

	catalog := newHTTPBindingCatalog(p.Module, p.Sources, controllerRoutesMinimum)
	inspector.configureHTTPBindings(catalog)
	var declarations, arguments []string
	for _, field := range handler.Type.Params.List {
		count := len(field.Names)
		if count == 0 {
			count = 1
		}
		for range count {
			parameterType, err := controllerTypeExpression(field.Type, source, registerSource)
			if err != nil {
				return err
			}
			arg := fmt.Sprintf("arg%d", len(arguments))
			argument := arg
			shape := catalog.shape(source, field.Type, map[string]bool{})
			if pointer, ok := field.Type.(*ast.StarExpr); ok && shape.kind == "pointer" {
				marker := "Body"
				if shape.source == "path" {
					marker = "Path"
				} else if shape.source == "query" {
					marker = "Query"
				} else if shape.source != "" {
					marker = ""
				}
				if marker != "" {
					alias := availableImportAlias(registerSource.File, "input", spineImportPath+"/pkg/input")
					if err := addNamedImport(registerSource.File, alias, spineImportPath+"/pkg/input"); err != nil {
						return err
					}
					payload, err := controllerTypeExpression(pointer.X, source, registerSource)
					if err != nil {
						return err
					}
					parameterType = alias + "." + marker + "[" + payload + "]"
					converted, err := controllerTypeExpression(field.Type, source, registerSource)
					if err != nil {
						return err
					}
					argument = "(" + converted + ")(" + arg + ")"
				}
			}
			declarations = append(declarations, arg+" "+parameterType)
			arguments = append(arguments, argument)
		}
	}
	results := fieldTypes(source.Fset, handler.Type.Results)
	for index, text := range results {
		expression, err := parser.ParseExpr(text)
		if err != nil {
			return err
		}
		results[index], err = controllerTypeExpression(expression, source, registerSource)
		if err != nil {
			return err
		}
	}
	resultText := ""
	if len(results) == 1 {
		resultText = " " + results[0]
	} else if len(results) > 1 {
		resultText = " (" + strings.Join(results, ",") + ")"
	}
	body := receiver + "." + method + "(" + strings.Join(arguments, ",") + ")"
	if len(results) > 0 {
		body = "return " + body
	}
	expression, err := parser.ParseExpr(registry + "." + strings.ToUpper(request.Method) + "(" + fmt.Sprintf("%q", request.Path) + ",func(" + strings.Join(declarations, ",") + ")" + resultText + "{" + body + "})")
	if err != nil {
		return err
	}
	register.Body.List = append(register.Body.List, &ast.ExprStmt{X: expression})
	return p.update(registerSource)
}

func controllerTypeExpression(expression ast.Expr, source, target *layoutSource) (string, error) {
	clone, err := parser.ParseExpr(expressionText(source.Fset, expression))
	if err != nil {
		return "", err
	}
	imports := importPathsByAlias(source.File)
	ast.Inspect(clone, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		alias, ok := selector.X.(*ast.Ident)
		if !ok {
			return true
		}
		path := imports[alias.Name]
		if path == "" {
			return true
		}
		chosen := availableImportAlias(target.File, alias.Name, path)
		if problem := addNamedImport(target.File, chosen, path); problem != nil {
			err = problem
			return false
		}
		alias.Name = chosen
		return true
	})
	return expressionText(target.Fset, clone), err
}
