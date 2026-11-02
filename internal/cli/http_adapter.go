package cli

import (
	"context"
	"go/ast"
	"go/token"
	"os"
	"strings"

	"golang.org/x/mod/semver"
)

type httpAdapterSelection struct {
	Enabled              *bool            `json:"enabled"`
	Provider             string           `json:"provider,omitempty"`
	Selection            string           `json:"selection"`
	CanonicalFactory     string           `json:"canonical_factory,omitempty"`
	ExpectedContract     string           `json:"expected_contract,omitempty"`
	SelectedModules      []selectedModule `json:"selected_modules"`
	RequiredCapabilities []string         `json:"required_capabilities"`
	Resolution           string           `json:"resolution"`
	Source               string           `json:"source,omitempty"`
}

func (i *contractInspector) inspectHTTPAdapter(config projectConfig, file *ast.File, fset *token.FileSet, function *ast.FuncDecl, index int) {
	assignment := function.Body.List[index].(*ast.AssignStmt)
	selection := adapterSelection(assignment.Rhs[0], importPathsByAlias(file))
	if applicationVariableReassigned(function.Body, assignment) || returnsDifferentApplication(function.Body, assignment.Lhs[0].(*ast.Ident)) {
		selection = "unresolved"
	}
	a := httpAdapterSelection{Selection: selection, Resolution: "static-source", Source: relativeSource(fset.Position(assignment.Pos())), SelectedModules: []selectedModule{}, RequiredCapabilities: []string{}}
	a.Enabled = returnedHTTPEnabled(function, file)
	add := func(code, message string) {
		i.contract.Diagnostics = append(i.contract.Diagnostics, contractDiagnostic{Severity: "error", Code: code, Message: message, Source: a.Source, Hint: "Check the composition factory and run go mod tidy before inspecting the contract again."})
		a.Resolution = "unresolved"
	}
	root, err := os.Getwd()
	if err == nil {
		a.SelectedModules, err = selectedModules(context.Background(), root, true)
	}
	if err != nil {
		a.SelectedModules = []selectedModule{}
		add("MODULE_GRAPH_UNAVAILABLE", err.Error())
	} else if selectedModuleVersion(a.SelectedModules, spineImportPath) == "" {
		add("MODULE_GRAPH_UNAVAILABLE", "the selected module graph does not contain Spine")
	}
	legacy := false
	for _, module := range a.SelectedModules {
		if module.Path == spineImportPath && module.Replace != nil && selection == "missing" && (a.Enabled == nil || *a.Enabled) {
			selection = "unresolved"
			a.Selection = selection
		}
		if module.Path == spineImportPath && module.Replace == nil {
			// 내장 Echo가 포함된 안정 버전만 암묵적 선택으로 인정합니다.
			legacy = module.Version == "v0.5.2" || module.Version == "v0.5.1" || module.Version == "v0.5.0"
		}
	}
	if selection == "missing" && legacy {
		a.Provider, a.Selection = "echo", "legacy-implicit"
	} else {
		a.ExpectedContract = httpAdapterContract
		coreVersion := selectedModuleVersion(a.SelectedModules, spineImportPath)
		if semver.IsValid(coreVersion) && semver.Compare(coreVersion, "v0.6.0") < 0 {
			a.ExpectedContract = "spine.http-adapter/v1"
		}
		if _, removed := importedCall(assignment.Rhs[0], importPathsByAlias(file), spineImportPath, "NewWithHTTPAdapter"); removed && a.ExpectedContract == "spine.http-adapter/v2" {
			add("HTTP_ADAPTER_API_REMOVED", "Spine v0.6.0 removed NewWithHTTPAdapter; use spine.New(spine.WithHTTPAdapter(platform.NewFactory()))")
		}
		if selection == "echo" {
			a.Provider, a.Selection, a.CanonicalFactory = "echo", "explicit", echoPlatformPath+".NewFactory"
			if selectedModuleVersion(a.SelectedModules, echoPlatformPath) == "" && err == nil {
				add("MODULE_GRAPH_UNAVAILABLE", "the selected module graph does not contain the Echo platform")
			}
		} else if selection == "unresolved" {
			add("HTTP_ADAPTER_UNRESOLVED", "the HTTP adapter option or factory cannot be resolved statically")
		}
		if a.Enabled == nil {
			add("HTTP_CONFIGURATION_UNRESOLVED", "the returned boot.Options.HTTP configuration cannot be resolved statically")
		} else if *a.Enabled && selection == "missing" {
			add("HTTP_ADAPTER_MISSING", "HTTP is enabled but the application has no HTTP adapter factory")
		}
	}
	if config.Platform != "" && a.Provider != "" && config.Platform != a.Provider {
		add("HTTP_PLATFORM_MISMATCH", "spine.toml platform differs from the composition factory")
	}
	if len(i.contract.WebSockets) > 0 && (a.Enabled == nil || *a.Enabled) {
		a.RequiredCapabilities = append(a.RequiredCapabilities, "websocket-upgrade")
	}
	for _, item := range i.contract.Configuration {
		if strings.HasPrefix(item.Path, "http_adapter.native_hooks") && (a.Enabled == nil || *a.Enabled) {
			if a.ExpectedContract == "spine.http-adapter/v2" {
				add("HTTP_NATIVE_HOOK_API_REMOVED", "Spine v0.6.0 removed App.Transport; configure Echo through platform.WithEcho(func(*echo.Echo))")
			} else {
				a.RequiredCapabilities = append(a.RequiredCapabilities, "native-hooks")
			}
			break
		}
	}
	i.contract.HTTPAdapter = a
}

// 성공 경로에서 반환하는 Options를 분석합니다. 오류 반환용 빈 Options는 제외합니다.
func returnedHTTPEnabled(function *ast.FuncDecl, file *ast.File) *bool {
	var result *bool
	unknown, found := false, false
	ast.Inspect(function.Body, func(node ast.Node) bool {
		if _, ok := node.(*ast.FuncLit); ok {
			return false
		}
		statement, ok := node.(*ast.ReturnStmt)
		if !ok || len(statement.Results) != 3 {
			return true
		}
		if app, ok := statement.Results[0].(*ast.Ident); ok && app.Name == "nil" {
			return true
		}
		found = true
		expression := statement.Results[1]
		if identifier, ok := expression.(*ast.Ident); ok && identifier.Obj != nil {
			if assignment, ok := identifier.Obj.Decl.(*ast.AssignStmt); ok && len(assignment.Rhs) == 1 {
				expression = assignment.Rhs[0]
			}
			if optionsVariableMutated(function.Body, identifier) {
				unknown = true
			}
		}
		literal := unwrapCompositeLiteral(expression)
		if literal == nil || !isBootOptionsType(literal.Type, importPathsByAlias(file)) {
			unknown = true
			return true
		}
		enabled := false
		for _, element := range literal.Elts {
			pair, ok := element.(*ast.KeyValueExpr)
			if !ok {
				unknown = true
				continue
			}
			key, ok := pair.Key.(*ast.Ident)
			if !ok || key.Name != "HTTP" {
				continue
			}
			if value, ok := pair.Value.(*ast.Ident); ok && value.Name == "nil" {
				continue
			}
			if value := unwrapCompositeLiteral(pair.Value); value != nil {
				enabled = true
			} else {
				unknown = true
			}
		}
		if result != nil && *result != enabled {
			unknown = true
		}
		result = &enabled
		return true
	})
	if unknown || !found {
		return nil
	}
	return result
}

// Options 변수에 대한 재할당, 필드 변경과 외부 전달은 보수적으로 미해결 처리합니다.
func optionsVariableMutated(root ast.Node, identifier *ast.Ident) bool {
	mutated := false
	ast.Inspect(root, func(node ast.Node) bool {
		switch statement := node.(type) {
		case *ast.AssignStmt:
			if statement == identifier.Obj.Decl {
				return true
			}
			for _, target := range statement.Lhs {
				ast.Inspect(target, func(n ast.Node) bool {
					if name, ok := n.(*ast.Ident); ok && name.Obj == identifier.Obj {
						mutated = true
					}
					return true
				})
			}
		case *ast.UnaryExpr:
			if statement.Op == token.AND {
				ast.Inspect(statement.X, func(n ast.Node) bool {
					if name, ok := n.(*ast.Ident); ok && name.Obj == identifier.Obj {
						mutated = true
					}
					return true
				})
			}
		case *ast.CallExpr:
			for _, argument := range statement.Args {
				ast.Inspect(argument, func(n ast.Node) bool {
					if name, ok := n.(*ast.Ident); ok && name.Obj == identifier.Obj {
						mutated = true
					}
					return true
				})
			}
		}
		return true
	})
	return mutated
}

func applicationVariableReassigned(root ast.Node, initial *ast.AssignStmt) bool {
	app := initial.Lhs[0].(*ast.Ident)
	reassigned := false
	ast.Inspect(root, func(node ast.Node) bool {
		if assignment, ok := node.(*ast.AssignStmt); ok && assignment != initial {
			for _, target := range assignment.Lhs {
				if identifier, ok := target.(*ast.Ident); ok && identifier.Obj == app.Obj {
					reassigned = true
				}
			}
		}
		return true
	})
	return reassigned
}

func returnsDifferentApplication(root ast.Node, app *ast.Ident) bool {
	different := false
	ast.Inspect(root, func(node ast.Node) bool {
		if _, ok := node.(*ast.FuncLit); ok {
			return false
		}
		statement, ok := node.(*ast.ReturnStmt)
		if !ok || len(statement.Results) != 3 {
			return true
		}
		identifier, ok := statement.Results[0].(*ast.Ident)
		if ok && identifier.Name == "nil" {
			return true
		}
		if !ok || identifier.Obj != app.Obj {
			different = true
		}
		return true
	})
	return different
}
