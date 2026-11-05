package cli

import (
	"go/ast"
	"go/token"

	"golang.org/x/mod/semver"
)

const (
	currentAssemblyRecipe        = "platform-contracts-v0.7.0-preview.2"
	preview1AssemblyRecipe       = "platform-contracts-v0.7.0-preview.1"
	stableAssemblyRecipe         = "platform-contracts-v0.6.5"
	legacyWebSocketRecipe        = "platform-contracts-20261103"
	currentAssemblySpineVersion  = "v0.7.0-preview.2"
	preview1AssemblySpineVersion = "v0.7.0-preview.1"
	previewPlatformVersion       = "v0.2.0-preview.1"
	releasedPlatformVersion      = "v0.1.0"
	webSocketSpineVersion        = "v0.6.1-0.20261103040400-9b1321410211"
	gorillaPlatformPath          = "github.com/NARUBROWN/spine-gorilla-platform"
	gorillaPlatformVersion       = "v0.0.0-20261103040512-36ad41dff88f"
)

// 이전 WS 개발 레시피의 공개 버전을 그대로 보존합니다.
func legacyWebSocketModules(options newProjectOptions) []selectedModule {
	modules := assemblyModules(options.Platform, options.Events, options.Validator)
	modules[0].Version = webSocketSpineVersion
	if options.WebSocketPlatform == "gorilla" {
		modules = append(modules, selectedModule{Path: gorillaPlatformPath, Version: gorillaPlatformVersion})
	}
	return modules
}

func stableAssemblyModules(options newProjectOptions) []selectedModule {
	modules := legacyWebSocketModules(options)
	for index := range modules {
		switch modules[index].Path {
		case spineImportPath:
			modules[index].Version = "v0.6.5"
		case kafkaPlatformPath, rabbitPlatformPath, validatorPlatformPath, gorillaPlatformPath:
			modules[index].Version = releasedPlatformVersion
		}
	}
	return modules
}

func currentAssemblyModules(options newProjectOptions) []selectedModule {
	return previewAssemblyModules(options, currentAssemblySpineVersion)
}

func isPreviewAssemblyRecipe(recipe string) bool {
	return recipe == currentAssemblyRecipe || recipe == preview1AssemblyRecipe
}

func previewAssemblyModules(options newProjectOptions, coreVersion string) []selectedModule {
	modules := stableAssemblyModules(options)
	for index := range modules {
		switch modules[index].Path {
		case spineImportPath:
			modules[index].Version = coreVersion
		case echoPlatformPath, kafkaPlatformPath, rabbitPlatformPath, validatorPlatformPath, gorillaPlatformPath:
			modules[index].Version = previewPlatformVersion
		}
	}
	return modules
}

type webSocketAdapterSelection struct {
	Provider         string `json:"provider"`
	CanonicalFactory string `json:"canonical_factory,omitempty"`
	Contract         string `json:"contract,omitempty"`
	Resolution       string `json:"resolution"`
	Source           string `json:"source,omitempty"`
}

// WS의 선택은 HTTP factory와 독립적으로 검사하며 어떤 옵션도 실행하지 않습니다.
func (i *contractInspector) inspectWebSocketAdapter(config projectConfig, imports map[string]string, fset *token.FileSet, function *ast.FuncDecl, assignment *ast.AssignStmt, call *ast.CallExpr, changed bool) {
	selection := webSocketAdapterSelection{Provider: "none", Resolution: "static-source"}
	add := func(code, message string, node ast.Node) {
		i.contract.Diagnostics = append(i.contract.Diagnostics, contractDiagnostic{Severity: "error", Code: code, Message: message, Source: relativeSource(fset.Position(node.Pos()))})
		selection.Resolution = "unresolved"
	}
	registered := false
	unsafeOptions := changed || call.Ellipsis.IsValid()
	for _, arg := range call.Args {
		direct, directOK := importedCall(arg, imports, spineImportPath, "WithWebSocketAdapter")
		option, optionOK := importedCall(arg, imports, gorillaPlatformPath, "Option")
		if !directOK && !optionOK {
			known := false
			for _, owner := range [][2]string{{spineImportPath, "WithHTTPAdapter"}, {spineImportPath, "WithEventAdapter"}, {spineImportPath, "WithValidator"}, {echoPlatformPath, "Option"}} {
				if _, ok := importedCall(arg, imports, owner[0], owner[1]); ok {
					known = true
				}
			}
			unsafeOptions = unsafeOptions || !known
			continue
		}
		if registered {
			add("WEBSOCKET_ADAPTER_DUPLICATE", "multiple WebSocket adapters are registered", arg)
		}
		registered = true
		selection.Provider = "unresolved"
		selection.Contract = "github.com/NARUBROWN/spine/contract/websocket"
		selection.Source = relativeSource(fset.Position(arg.Pos()))
		valid := optionOK && len(option.Args) == 0 && !option.Ellipsis.IsValid()
		if directOK && len(direct.Args) == 1 && !direct.Ellipsis.IsValid() {
			factory, ok := importedCall(assemblyValue(direct.Args[0], function.Body, direct.Pos()), imports, gorillaPlatformPath, "NewFactory")
			valid = ok && len(factory.Args) == 0 && !factory.Ellipsis.IsValid()
		}
		if valid {
			selection.Provider = "gorilla"
			selection.CanonicalFactory = gorillaPlatformPath + ".NewFactory"
		} else {
			add("WEBSOCKET_ADAPTER_UNRESOLVED", "WebSocket factory or options cannot be resolved statically", arg)
		}
	}
	if unsafeOptions {
		selection.Provider = "unresolved"
		add("WEBSOCKET_ADAPTER_UNRESOLVED", "application options may change the WebSocket adapter", assignment)
	}
	version := selectedModuleVersion(i.contract.HTTPAdapter.SelectedModules, spineImportPath)
	separated := semver.IsValid(version) && semver.Compare(version, webSocketSpineVersion) >= 0
	if registered {
		if !separated {
			add("WEBSOCKET_CORE_INCOMPATIBLE", "selected Spine version does not provide the standalone WebSocket adapter API", assignment)
		}
		if selection.Provider == "gorilla" && selectedModuleVersion(i.contract.HTTPAdapter.SelectedModules, gorillaPlatformPath) == "" {
			add("MODULE_GRAPH_UNAVAILABLE", "the selected graph does not contain the Gorilla platform", assignment)
		}
	} else if len(i.contract.WebSockets) > 0 && !unsafeOptions {
		if separated {
			add("WEBSOCKET_ADAPTER_MISSING", "WebSocket routes require an explicit WebSocket platform", assignment)
		} else if semver.IsValid(version) {
			selection.Provider, selection.Resolution = "gorilla", "legacy-implicit"
		} else {
			add("WEBSOCKET_ADAPTER_UNRESOLVED", "the core version for WebSocket selection is unknown", assignment)
		}
	}
	if config.WebSocketPlatform != "" && config.WebSocketPlatform != selection.Provider {
		add("WEBSOCKET_PLATFORM_MISMATCH", "spine.toml websocket_platform differs from composition", assignment)
	}
	i.contract.WebSocketAdapter = selection
}
