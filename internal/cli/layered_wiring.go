package cli

import (
	"fmt"
	"go/ast"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
)

type layeredDependency struct{ Package, Type, Pointer, Factory, Key, Expression, Field string }

func parseTypeReference(value string) (string, string, error) {
	dir, name, ok := strings.Cut(value, ":")
	if !ok || !token.IsIdentifier(name) || !ast.IsExported(name) {
		return "", "", fmt.Errorf("reference requires package/path:ExportedType: %s", value)
	}
	parsed, err := parseGenerationName(dir)
	if err != nil {
		return "", "", err
	}
	normalized := filepath.ToSlash(filepath.Join(parsed.Group, parsed.Name.Package))
	if normalized != dir {
		return "", "", fmt.Errorf("reference must use the actual normalized package path: %s", value)
	}
	if err = regularAncestors(filepath.FromSlash(dir)); err != nil {
		return "", "", err
	}
	return dir, name, nil
}

func (p *layeredPlanner) resolveDependency(ref string) (layeredDependency, error) {
	dir, name, err := parseTypeReference(ref)
	if err != nil {
		return layeredDependency{}, err
	}
	found := false
	candidates := []layeredDependency{}
	for _, source := range p.Sources.packageFiles(dir) {
		for _, declaration := range source.File.Decls {
			if general, ok := declaration.(*ast.GenDecl); ok && general.Tok == token.TYPE {
				for _, spec := range general.Specs {
					if spec.(*ast.TypeSpec).Name.Name == name {
						found = true
					}
				}
			}
		}
	}
	prefix := modulePackage(p.Module, dir) + "."
	expected := modulePackage(p.Module, dir) + ":" + name
	for key, signatures := range p.Sources.constructorSignatures(p.Module) {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		factory := strings.TrimPrefix(key, prefix)
		if (!token.IsIdentifier(factory) || !ast.IsExported(factory)) && factory != name+".Constructor" {
			continue
		}
		for _, signature := range signatures {
			pointer := ""
			if signature.Output == "*"+expected {
				pointer = "*"
			} else if signature.Output != expected {
				continue
			}
			candidates = append(candidates, layeredDependency{Package: dir, Type: name, Pointer: pointer, Factory: factory, Key: signature.Output})
		}
	}

	if !found {
		return layeredDependency{}, fmt.Errorf("dependency type %s was not found", ref)
	}
	if len(candidates) != 1 {
		return layeredDependency{}, fmt.Errorf("dependency %s must have exactly one exported constructor returning that type; found %d", ref, len(candidates))
	}
	return candidates[0], nil
}

func (p *layeredPlanner) composition() (*layoutSource, *ast.FuncDecl, int, string, error) {
	return p.Sources.composition(p.Config.Composition)
}

func (p *layeredPlanner) importInto(s *layoutSource, dir string) (string, error) {
	imports := importPathsByAlias(s.File)
	// 파일의 선언과 지역 변수도 import 별칭과 충돌하지 않도록 예약합니다.
	ast.Inspect(s.File, func(node ast.Node) bool {
		if id, ok := node.(*ast.Ident); ok {
			if _, used := imports[id.Name]; !used {
				imports[id.Name] = ""
			}
		}
		return true
	})
	alias := chooseAlias(imports, filepath.Base(dir), p.Module+"/"+dir)
	for _, spec := range s.File.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil || path != p.Module+"/"+dir || spec.Name == nil {
			continue
		}
		if spec.Name.Name == "." {
			return "", fmt.Errorf("cannot safely register through dot import %q; use a named import", path)
		}
		if spec.Name.Name == "_" {
			spec.Name = ast.NewIdent(alias)
		}
	}
	if err := addNamedImport(s.File, alias, p.Module+"/"+dir); err != nil {
		return "", err
	}
	return alias, nil
}

func (p *layeredPlanner) registerConstructor(dir, name string) error {
	s, f, index, appName, err := p.composition()
	if err != nil {
		return err
	}
	if _, ok := checkedRegistrationBootAlias(f, s.Fset, s.File); !ok {
		return fmt.Errorf("composition root must return (spine.App, boot.Options, error)")
	}
	inspector, err := inspectLayeredRegistrations(p.Config, p.Module, p.Sources)
	if err != nil {
		return err
	}
	for _, constructor := range inspector.contract.Constructors {
		if constructor.Target == modulePackage(p.Module, dir)+"."+name {
			return nil
		}
	}
	alias, err := p.importInto(s, dir)
	if err != nil {
		return err
	}
	registration := "Constructor"
	var argument ast.Expr = &ast.SelectorExpr{X: ast.NewIdent(alias), Sel: ast.NewIdent(name)}
	if strings.HasSuffix(name, ".Constructor") {
		typ := strings.TrimSuffix(name, ".Constructor")
		argument = &ast.UnaryExpr{Op: token.AND, X: &ast.CompositeLit{Type: &ast.SelectorExpr{X: ast.NewIdent(alias), Sel: ast.NewIdent(typ)}}}
		for _, role := range componentRoles() {
			if role.Constructor && (dir == role.Root || strings.HasPrefix(dir, role.Root+"/")) {
				registration = strings.ToUpper(role.Name[:1]) + role.Name[1:] + "Constructor"
				break
			}
		}
	}
	stmt := &ast.ExprStmt{X: &ast.CallExpr{Fun: &ast.SelectorExpr{X: ast.NewIdent(appName), Sel: ast.NewIdent(registration)}, Args: []ast.Expr{argument}}}
	f.Body.List = append(f.Body.List, nil)
	copy(f.Body.List[index+2:], f.Body.List[index+1:])
	f.Body.List[index+1] = stmt
	return p.update(s)
}

func (p *layeredPlanner) registerRouteModule() error {
	return p.registerModule(filepath.ToSlash(filepath.Dir(layeredRoutePath)))
}

func (p *layeredPlanner) registerModule(directory string) error {
	s, f, index, appName, err := p.composition()
	if err != nil {
		return err
	}
	bootAlias, ok := checkedRegistrationBootAlias(f, s.Fset, s.File)
	if !ok {
		return fmt.Errorf("composition root must return (spine.App, boot.Options, error)")
	}
	inspector, err := inspectLayeredRegistrations(p.Config, p.Module, p.Sources)
	if err != nil {
		return err
	}
	if inspector.seen[modulePackage(p.Module, directory)] {
		return nil
	}
	alias, err := p.importInto(s, directory)
	if err != nil {
		return err
	}
	f.Body.List = append(f.Body.List, nil)
	copy(f.Body.List[index+2:], f.Body.List[index+1:])
	f.Body.List[index+1] = checkedRegisterStatement(alias, appName, bootAlias)
	return p.update(s)
}
