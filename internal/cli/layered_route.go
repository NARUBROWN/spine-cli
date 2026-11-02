package cli

import (
	"fmt"
	"go/ast"
	"go/token"
	"strings"
)

func (p *layeredPlanner) addRoute(r layeredRequest) error {
	pkg, handler, ok := strings.Cut(r.Handler, ":")
	if !ok {
		return fmt.Errorf("handler requires package/path:Type.Method")
	}
	typ, method, ok := strings.Cut(handler, ".")
	if !ok || !token.IsIdentifier(method) || !ast.IsExported(method) {
		return fmt.Errorf("handler requires package/path:Type.Method")
	}
	dir, name, err := parseTypeReference(pkg + ":" + typ)
	if err != nil {
		return err
	}
	found := 0
	for _, s := range p.Sources {
		if s.Package != dir {
			continue
		}
		for _, decl := range s.File.Decls {
			f, ok := decl.(*ast.FuncDecl)
			if !ok || f.Recv == nil || f.Name.Name != method {
				continue
			}
			if strings.TrimPrefix(expressionText(s.Fset, f.Recv.List[0].Type), "*") != name {
				continue
			}
			found++
			if f.Type.TypeParams != nil {
				return fmt.Errorf("generic handlers are unsupported")
			}
			catalog := newHTTPBindingCatalog(p.Module, p.Sources)
			params, diagnostics := catalog.parameters(s, f.Type.Params, r.Path, r.Handler)
			diagnostics = append(diagnostics, catalog.returns(s, f.Type.Results, r.Handler)...)
			if len(diagnostics) > 0 {
				return fmt.Errorf("%s: %s (%s). %s", diagnostics[0].Code, diagnostics[0].Message, diagnostics[0].Source, diagnostics[0].Hint)
			}
			bound := 0
			for _, param := range params {
				if param.Binding == "path" {
					bound++
				}
				if strings.HasPrefix(param.Type, "...") {
					return fmt.Errorf("variadic handlers are unsupported")
				}
			}
			if bound != len(contractPathKeys(r.Path)) {
				return fmt.Errorf("HTTP path binding mismatch for %s", r.Handler)
			}
		}
	}
	if found != 1 {
		return fmt.Errorf("handler %s must resolve to exactly one method; found %d", r.Handler, found)
	}
	dependency, err := p.resolveDependency(dir + ":" + name)
	if err != nil {
		return err
	}
	if dependency.Pointer != "*" {
		return fmt.Errorf("route receiver requires a constructor returning *%s", name)
	}
	if err = p.registerConstructor(dir, dependency.Factory); err != nil {
		return err
	}
	path := layeredRoutePath
	s := p.Sources[path]
	if s == nil {
		if err = p.put(path, "package route\nimport \"github.com/NARUBROWN/spine\"\nfunc Register(app spine.App) error { return nil }\n", false); err != nil {
			return err
		}
		s = p.Sources[path]
	}
	var register *ast.FuncDecl
	for _, decl := range s.File.Decls {
		if f, ok := decl.(*ast.FuncDecl); ok && f.Name.Name == "Register" {
			register = f
		}
	}
	if register == nil || !validRouteRegisterSignature(register, s.Fset, s.File) {
		return fmt.Errorf("generated route file must declare Register(app spine.App) error")
	}
	if err := p.registerRouteModule(); err != nil {
		return err
	}
	inspector, err := inspectLayeredRegistrations(p.Config, p.Module, p.Sources)
	if err != nil {
		return err
	}
	expected := "(*" + modulePackage(p.Module, dir) + "." + name + ")." + method
	for _, existing := range inspector.contract.HTTP {
		if !strings.EqualFold(existing.Method, r.Method) {
			continue
		}
		if existing.Path == r.Path {
			if existing.Handler != expected {
				return fmt.Errorf("route %s %s is already registered with a different handler", r.Method, r.Path)
			}
			return nil
		}
		if routesOverlap(existing.Path, r.Path) {
			return fmt.Errorf("route %s %s overlaps registered route %s", r.Method, r.Path, existing.Path)
		}
	}
	alias, err := p.importInto(s, dir)
	if err != nil {
		return err
	}
	appName := register.Type.Params.List[0].Names[0].Name
	stmt := &ast.ExprStmt{X: &ast.CallExpr{Fun: &ast.SelectorExpr{X: ast.NewIdent(appName), Sel: ast.NewIdent("Route")}, Args: []ast.Expr{&ast.BasicLit{Kind: token.STRING, Value: fmt.Sprintf("%q", r.Method)}, &ast.BasicLit{Kind: token.STRING, Value: fmt.Sprintf("%q", r.Path)}, &ast.SelectorExpr{X: &ast.ParenExpr{X: &ast.StarExpr{X: &ast.SelectorExpr{X: ast.NewIdent(alias), Sel: ast.NewIdent(name)}}}, Sel: ast.NewIdent(method)}}}}
	register.Body.List = append([]ast.Stmt{stmt}, register.Body.List...)
	if err = p.update(s); err != nil {
		return err
	}
	return p.registerRouteModule()
}
