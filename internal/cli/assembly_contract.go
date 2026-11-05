package cli

import (
	"fmt"
	"go/ast"
	"go/token"
	"sort"

	"golang.org/x/mod/semver"
)

type eventAdapterSelection struct {
	Name             string `json:"name"`
	Provider         string `json:"provider,omitempty"`
	CanonicalFactory string `json:"canonical_factory,omitempty"`
	Contract         string `json:"contract"`
	Resolution       string `json:"resolution"`
	Configuration    string `json:"configuration"`
	Source           string `json:"source,omitempty"`
}

type validatorSelection struct {
	Provider         string `json:"provider"`
	CanonicalFactory string `json:"canonical_factory,omitempty"`
	Contract         string `json:"contract,omitempty"`
	Resolution       string `json:"resolution"`
	Source           string `json:"source,omitempty"`
}

// factory의 생성식과 등록만 추적하며 설정 helper와 검증기, 브로커를 실행하지 않습니다.
func (i *contractInspector) inspectAssembly(config projectConfig, file *ast.File, fset *token.FileSet, function *ast.FuncDecl, index int) {
	assignment := function.Body.List[index].(*ast.AssignStmt)
	imports := importPathsByAlias(file)
	call, ok := importedCall(assignment.Rhs[0], imports, spineImportPath, "New")
	i.contract.EventAdapters = []eventAdapterSelection{}
	i.contract.Validator = validatorSelection{Provider: "none", Resolution: "static-source"}
	if !ok {
		call = &ast.CallExpr{}
	}
	add := func(code, message string, node ast.Node) {
		i.contract.Diagnostics = append(i.contract.Diagnostics, contractDiagnostic{Severity: "error", Code: code, Message: message, Source: relativeSource(fset.Position(node.Pos()))})
	}
	names := map[string]bool{}
	registeredValidator := false
	changed := applicationVariableReassigned(function.Body, assignment) || returnsDifferentApplication(function.Body, assignment.Lhs[0].(*ast.Ident))
	for _, arg := range call.Args {
		if option, ok := importedCall(arg, imports, spineImportPath, "WithEventAdapter"); ok {
			item := eventAdapterSelection{Contract: "github.com/NARUBROWN/spine/contract/event", Resolution: "unresolved", Configuration: "runtime-values-not-evaluated", Source: relativeSource(fset.Position(arg.Pos()))}
			valid := len(option.Args) >= 2 && !option.Ellipsis.IsValid() && !changed
			if valid {
				item.Name, valid = stringLiteral(option.Args[0])
				valid = valid && item.Name != ""
			}
			if valid && names[item.Name] {
				add("EVENT_ADAPTER_DUPLICATE", "duplicate event adapter name: "+item.Name, arg)
				valid = false
			}
			names[item.Name] = true
			if valid {
				expression := assemblyValue(option.Args[1], function.Body, option.Pos())
				for _, candidate := range []struct{ path, provider string }{{kafkaPlatformPath, "kafka"}, {rabbitPlatformPath, "rabbitmq"}, {natsPlatformPath, "nats"}} {
					if factory, ok := importedCall(expression, imports, candidate.path, "NewFactory"); ok && len(factory.Args) == 1 && !factory.Ellipsis.IsValid() {
						item.Provider, item.CanonicalFactory = candidate.provider, candidate.path+".NewFactory"
					}
				}
				valid = item.Provider != "" && knownEventOptions(option.Args[2:], imports)
			}
			if valid {
				item.Resolution = "static-source"
			} else {
				add("EVENT_ADAPTER_UNRESOLVED", "event adapter name, factory or options cannot be resolved statically", arg)
			}
			if item.Provider != "" && selectedModuleVersion(i.contract.HTTPAdapter.SelectedModules, providerModule(item.Provider)) == "" {
				add("MODULE_GRAPH_UNAVAILABLE", "the selected graph does not contain the event platform", arg)
			}
			i.contract.EventAdapters = append(i.contract.EventAdapters, item)
			continue
		}
		if option, ok := importedCall(arg, imports, spineImportPath, "WithValidator"); ok {
			item := validatorSelection{Provider: "unresolved", Contract: "github.com/NARUBROWN/spine/contract/validator", Resolution: "unresolved", Source: relativeSource(fset.Position(arg.Pos()))}
			valid := len(option.Args) == 1 && !option.Ellipsis.IsValid() && !changed
			if valid {
				factory, known := importedCall(assemblyValue(option.Args[0], function.Body, option.Pos()), imports, validatorPlatformPath, "New")
				valid = known && len(factory.Args) == 0 && !factory.Ellipsis.IsValid()
			}
			if valid {
				item.Provider, item.CanonicalFactory, item.Resolution = "playground", validatorPlatformPath+".New", "static-source"
			} else {
				add("VALIDATOR_UNRESOLVED", "validator factory or options cannot be resolved statically", arg)
			}
			if registeredValidator {
				add("VALIDATOR_DUPLICATE", "multiple validators are registered", arg)
			}
			registeredValidator = true
			if valid && selectedModuleVersion(i.contract.HTTPAdapter.SelectedModules, validatorPlatformPath) == "" {
				add("MODULE_GRAPH_UNAVAILABLE", "the selected graph does not contain the validator implementation", arg)
			}
			i.contract.Validator = item
			continue
		}
	}
	if len(i.contract.EventAdapters) > 0 || registeredValidator {
		version := selectedModuleVersion(i.contract.HTTPAdapter.SelectedModules, spineImportPath)
		if !semver.IsValid(version) || semver.Compare(version, assemblySpineVersion) < 0 {
			add("ASSEMBLY_CORE_INCOMPATIBLE", "event and validator registration require a core version providing the public contracts; v0.6.0 does not provide these APIs", assignment)
		}
	}
	for _, spec := range config.Events {
		found := false
		for _, item := range i.contract.EventAdapters {
			found = found || item.Name == spec.Name && item.Provider == spec.Provider
		}
		if !found {
			add("EVENT_PLATFORM_MISMATCH", fmt.Sprintf("spine.toml event platform %s:%s differs from composition", spec.Name, spec.Provider), assignment)
		}
	}
	if config.Validator != "" && config.Validator != i.contract.Validator.Provider {
		add("VALIDATOR_MISMATCH", "spine.toml validator differs from composition", assignment)
	}
	i.inspectWebSocketAdapter(config, imports, fset, function, assignment, call, changed)
	sort.Slice(i.contract.EventAdapters, func(a, b int) bool { return i.contract.EventAdapters[a].Name < i.contract.EventAdapters[b].Name })
}

func providerModule(provider string) string {
	switch provider {
	case "kafka":
		return kafkaPlatformPath
	case "rabbitmq":
		return rabbitPlatformPath
	case "nats":
		return natsPlatformPath
	}
	return ""
}

// 직접 대입한 지역 변수만 추적하며 재대입과 조건 분기, 외부 함수를 평가하지 않습니다.
func assemblyValue(expression ast.Expr, body *ast.BlockStmt, before token.Pos) ast.Expr {
	seen := map[*ast.Object]bool{}
	for {
		identifier, ok := expression.(*ast.Ident)
		if !ok || identifier.Obj == nil || seen[identifier.Obj] {
			return expression
		}
		seen[identifier.Obj] = true
		declaration, ok := identifier.Obj.Decl.(*ast.AssignStmt)
		if !ok || len(declaration.Lhs) == 0 || len(declaration.Rhs) != 1 || declaration.Pos() >= before {
			return nil
		}
		first, ok := declaration.Lhs[0].(*ast.Ident)
		if !ok || first.Obj != identifier.Obj {
			return nil
		}
		topLevel := false
		for _, statement := range body.List {
			topLevel = topLevel || statement == declaration
		}
		if !topLevel {
			return nil
		}
		changed := false
		ast.Inspect(body, func(node ast.Node) bool {
			if assignment, ok := node.(*ast.AssignStmt); ok && assignment != declaration {
				for _, target := range assignment.Lhs {
					ast.Inspect(target, func(n ast.Node) bool {
						if id, ok := n.(*ast.Ident); ok && id.Obj == identifier.Obj {
							changed = true
						}
						return true
					})
				}
			}
			if unary, ok := node.(*ast.UnaryExpr); ok && unary.Op == token.AND {
				if id, ok := unary.X.(*ast.Ident); ok && id.Obj == identifier.Obj {
					changed = true
				}
			}
			return true
		})
		if changed {
			return nil
		}
		expression = declaration.Rhs[0]
	}
}

func knownEventOptions(args []ast.Expr, imports map[string]string) bool {
	for _, arg := range args {
		known := false
		for _, name := range []string{"WithEventConsumerRetry", "WithEventSettlementTimeout"} {
			if call, ok := importedCall(arg, imports, spineImportPath, name); ok && len(call.Args) == 1 && !call.Ellipsis.IsValid() {
				known = true
			}
		}
		if !known {
			return false
		}
	}
	return true
}
