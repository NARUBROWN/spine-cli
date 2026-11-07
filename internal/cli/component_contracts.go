package cli

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
	"strings"

	"golang.org/x/tools/go/packages"
)

// objectConstructorTarget는 명시적 타입 표식만 읽고 임의 객체 식을 실행하지 않습니다.
func (i *contractInspector) objectConstructorTarget(expression ast.Expr, file *ast.File, fset *token.FileSet) (string, bool) {
	for {
		p, ok := expression.(*ast.ParenExpr)
		if !ok {
			break
		}
		expression = p.X
	}
	var payload ast.Expr
	switch e := expression.(type) {
	case *ast.UnaryExpr:
		if e.Op == token.AND {
			if literal, ok := e.X.(*ast.CompositeLit); ok {
				payload = literal.Type
			}
		}
	case *ast.CallExpr:
		if name, ok := e.Fun.(*ast.Ident); ok && name.Name == "new" && name.Obj == nil && len(e.Args) == 1 {
			payload = e.Args[0]
		}
		if p, ok := e.Fun.(*ast.ParenExpr); ok && len(e.Args) == 1 {
			if star, ok := p.X.(*ast.StarExpr); ok {
				if nilName, ok := e.Args[0].(*ast.Ident); ok && nilName.Name == "nil" {
					payload = star.X
				}
			}
		}
	}
	if payload == nil {
		return "", false
	}
	imports := importPathsByAlias(file)
	pkg := modulePackage(i.module, filepath.ToSlash(filepath.Dir(fset.Position(file.Package).Filename)))
	return qualifiedExpression(payload, imports, pkg, fset) + ".Constructor", true
}

func typedComponentConstructor(provider contractRegistration, all map[string]*packages.Package) (types.Type, *types.Signature, string) {
	target := provider.Target
	if provider.object {
		target = strings.TrimSuffix(target, ".Constructor")
	}
	split := strings.LastIndex(target, ".")
	if split < 0 {
		return nil, nil, "unresolved component type: " + target
	}
	pkg := all[target[:split]]
	if pkg == nil || pkg.Types == nil {
		return nil, nil, "component package is unavailable: " + target
	}
	object := pkg.Types.Scope().Lookup(target[split+1:])
	if object == nil {
		return nil, nil, "component declaration is unavailable: " + target
	}
	if !provider.object {
		signature, ok := object.Type().(*types.Signature)
		if !ok || signature.Results().Len() != 1 {
			return nil, nil, ""
		}
		return types.Unalias(signature.Results().At(0).Type()), signature, ""
	}
	owner := types.NewPointer(types.Unalias(object.Type()))
	if _, ok := owner.Elem().Underlying().(*types.Struct); !ok {
		return nil, nil, "component prototype must point to a struct: " + target
	}
	selection := types.NewMethodSet(owner).Lookup(nil, "Constructor")
	if selection == nil {
		return nil, nil, "missing Constructor method: " + target
	}
	signature, ok := selection.Obj().Type().(*types.Signature)
	if !ok || signature.Variadic() || signature.Results().Len() != 1 || !types.Identical(types.Unalias(signature.Results().At(0).Type()), owner) {
		return nil, nil, "Constructor must be non-variadic and return exactly " + types.TypeString(owner, nil)
	}
	return owner, signature, ""
}
func componentTypeName(t types.Type) string {
	return strings.ReplaceAll(types.TypeString(t, func(p *types.Package) string { return p.Path() + ":" }), ":.", ":")
}

func validateTypedComponentGraph(i *contractInspector, outputs map[int]types.Type, signatures map[int]*types.Signature) {
	enabled := false
	for _, p := range i.contract.Constructors {
		enabled = enabled || p.object
	}
	if !enabled {
		return
	}
	resolve := func(t types.Type) []int {
		var exact, compatible []int
		for n, out := range outputs {
			if types.Identical(out, t) {
				exact = append(exact, n)
			} else if types.AssignableTo(out, t) {
				compatible = append(compatible, n)
			}
		}
		if len(exact) > 0 {
			return exact
		}
		if _, isInterface := t.Underlying().(*types.Interface); !isInterface {
			return exact
		}
		return compatible
	}
	graph := map[string][]string{}
	for n, out := range outputs {
		source := i.contract.Constructors[n].Source
		if matches := resolve(out); len(matches) != 1 {
			i.controllerDiagnostic("COMPONENT_DEPENDENCIES_INVALID", "Duplicate component providers for "+componentTypeName(out), source)
		}
		key := fmt.Sprint(n)
		graph[key] = nil
		for k := 0; k < signatures[n].Params().Len(); k++ {
			dependency := signatures[n].Params().At(k).Type()
			matches := resolve(dependency)
			if len(matches) != 1 {
				i.controllerDiagnostic("COMPONENT_DEPENDENCIES_INVALID", "Expected one provider for "+componentTypeName(dependency), source)
				continue
			}
			graph[key] = append(graph[key], fmt.Sprint(matches[0]))
		}
	}
	if err := acyclicGraph(graph, "component dependency"); err != nil {
		i.controllerDiagnostic("COMPONENT_DEPENDENCIES_INVALID", err.Error(), "")
	}
}
