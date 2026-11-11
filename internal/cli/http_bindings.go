package cli

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"

	"golang.org/x/mod/semver"
)

type httpTypeDeclaration struct {
	source *layoutSource
	spec   *ast.TypeSpec
}

type httpTypeUse struct {
	path, expression string
}

func httpTypeUseAt(fset *token.FileSet, expression ast.Expr) httpTypeUse {
	path := fset.Position(expression.Pos()).Filename
	if absolute, err := filepath.Abs(path); err == nil {
		path = absolute
	}
	return httpTypeUse{path, expressionText(fset, expression)}
}

func (i *contractInspector) configureHTTPBindings(catalog *httpBindingCatalog) {
	for key, declaration := range i.controllerTypes {
		catalog.types[key] = declaration
	}
	catalog.instances = i.controllerInstances
}

type httpBindingCatalog struct {
	instances                map[httpTypeUse]types.Type
	module                   string
	types                    map[string]httpTypeDeclaration
	errorMethods             map[string]bool
	jsonDecoders             map[string]bool
	xmlDecoders              map[string]bool
	structured               bool
	explicitInputs           bool
	controllerContextRemoved bool
}

type httpBindingShape struct {
	payload       string
	payloadSource string
	fields        []handlerInputField
	binding       string
	kind          string
	source        string
	keys          []string
	scalarPath    bool
	legacy        bool
	problem       string
}

func requiresExplicitHTTPInputs(version string) bool {
	return semver.IsValid(version) && semver.Compare(version, "v0.7.2-preview.2") >= 0
}

// HTTP 검증과 route 생성은 동일한 소스 기반 resolver 지원 규칙을 사용합니다.
func newHTTPBindingCatalog(module string, sources layoutSources, coreVersion string) *httpBindingCatalog {
	catalog := &httpBindingCatalog{
		explicitInputs: requiresExplicitHTTPInputs(coreVersion),
		structured:     semver.IsValid(coreVersion) && semver.Compare(coreVersion, "v0.7.0-preview.1") >= 0,
		module:         module,
		types:          map[string]httpTypeDeclaration{},
		errorMethods:   map[string]bool{},
		jsonDecoders:   map[string]bool{},
		xmlDecoders:    map[string]bool{},
	}
	for _, source := range sources.sortedFiles() {
		for _, declaration := range source.File.Decls {
			group, ok := declaration.(*ast.GenDecl)
			if !ok || group.Tok != token.TYPE {
				continue
			}
			for _, specification := range group.Specs {
				spec := specification.(*ast.TypeSpec)
				key := modulePackage(module, source.Package) + ":" + spec.Name.Name
				catalog.types[key] = httpTypeDeclaration{source, spec}
			}
		}
	}
	for _, source := range sources.sortedFiles() {
		for _, node := range source.File.Decls {
			function, ok := node.(*ast.FuncDecl)
			if !ok || function.Recv == nil || len(function.Recv.List) != 1 {
				continue
			}
			if function.Name.Name == "UnmarshalJSON" {
				params, results := fieldTypes(source.Fset, function.Type.Params), fieldTypes(source.Fset, function.Type.Results)
				if len(params) == 1 && (params[0] == "[]byte" || params[0] == "[]uint8") && len(results) == 1 && results[0] == "error" {
					catalog.jsonDecoders[catalog.dtoIdentity(source, function.Recv.List[0].Type)] = true
				}
			}
			switch function.Name.Name {
			case "UnmarshalXML", "UnmarshalXMLAttr", "UnmarshalText":
				catalog.xmlDecoders[catalog.dtoIdentity(source, function.Recv.List[0].Type)] = true
			}
			if function.Name.Name != "Error" || len(fieldTypes(source.Fset, function.Type.Params)) != 0 {
				continue
			}
			results := function.Type.Results
			valid := results != nil && len(results.List) == 1 && len(fieldTypes(source.Fset, results)) == 1 && catalog.stringReturnBody(source, results.List[0].Type, map[string]bool{}) == "string"
			catalog.errorMethods[catalog.errorReceiverKey(source, function.Recv.List[0].Type, map[string]bool{})] = valid
		}
	}
	return catalog
}

func (catalog *httpBindingCatalog) parameters(source *layoutSource, fields *ast.FieldList, path, handler string) ([]handlerParameter, []contractDiagnostic) {
	parameters := layeredHandlerParameters(source.File, source.Fset, fields)
	var diagnostics []contractDiagnostic
	if fields == nil {
		return parameters, diagnostics
	}
	pathKeys, pathIndex, index := contractPathKeys(path), 0, 0
	scalarPathCount, otherPath := 0, false
	for _, field := range fields.List {
		shape := catalog.shape(source, field.Type, map[string]bool{})
		count := len(field.Names)
		if count == 0 {
			count = 1
		}
		for range count {
			parameter := &parameters[index]
			parameter.Binding = shape.binding
			parameter.Payload, parameter.payloadSource, parameter.InputFields = shape.payload, shape.payloadSource, shape.fields
			parameter.SourceDTO = shape.source != "" && shape.kind == "pointer"
			parameter.Keys = append([]string(nil), shape.keys...)
			parameter.scalarPath = shape.scalarPath
			if shape.scalarPath {
				scalarPathCount++
				if pathIndex < len(pathKeys) {
					parameter.Keys = []string{pathKeys[pathIndex]}
					parameter.InputFields = []handlerInputField{{Name: pathKeys[pathIndex], Type: shape.payload, source: shape.payloadSource}}
				}
				pathIndex++
			} else if shape.binding == "path" {
				otherPath = true
			}
			parameter.Deprecated = catalog.structured && (shape.legacy || shape.binding == "multipart")
			if shape.binding == "path" && !parameter.SourceDTO && !shape.scalarPath {
				if pathIndex < len(pathKeys) {
					parameter.Key = pathKeys[pathIndex]
				}
				pathIndex++
			}
			if shape.binding == "" {
				code := "HTTP_PARAMETER_BINDING_UNSUPPORTED"
				hint := "Use a supported context/path/query/header parameter or a pointer to a DTO struct."
				if shape.kind == "struct" && !catalog.explicitInputs {
					hint = "Use *" + parameter.Type + " for a DTO body; default HTTP resolvers do not accept a struct value."
				}
				if shape.kind == "unknown" || shape.kind == "interface" {
					code = "HTTP_PARAMETER_BINDING_UNRESOLVED"
					hint = "Static inspection cannot prove support for this type or a custom argument resolver; use an explicit supported binding."
				}
				if shape.problem != "" {
					code = "HTTP_INPUT_CONTRACT_INVALID"
					hint = shape.problem + "."
					if !catalog.explicitInputs {
						hint += " Use separate struct pointers with path, query or header tags."
					}
				}
				if catalog.explicitInputs && shape.kind != "unknown" && shape.kind != "interface" && shape.problem == "" {
					hint = "Use input.Body[T], input.Path[T], input.Query[T] or input.Header[T] for the input source."
				}
				parameter.Binding = "unresolved"
				diagnostics = append(diagnostics, contractDiagnostic{
					Severity: "error", Code: code,
					Message: fmt.Sprintf("handler %s parameter %d (%s %s) has no verified default HTTP resolver", handler, index, parameter.Name, parameter.Type),
					Source:  relativeSource(source.Fset.Position(field.Pos())), Hint: hint,
				})
			}
			index++
		}
	}
	if scalarPathCount > 0 && !otherPath && scalarPathCount != len(pathKeys) {
		diagnostics = append(diagnostics, contractDiagnostic{Severity: "error", Code: "HTTP_PATH_SCALAR_AMBIGUOUS", Message: fmt.Sprintf("Handler %s declares %d scalar path arguments for %d route keys.", handler, scalarPathCount, len(pathKeys)), Source: relativeSource(source.Fset.Position(fields.Pos())), Hint: "Declare one input.Path scalar for each route key in route order."})
	}
	if catalog.structured {
		diagnostics = append(diagnostics, catalog.inputParameterDiagnostics(source, fields, parameters, handler)...)
	}
	return parameters, diagnostics
}

func (catalog *httpBindingCatalog) shape(source *layoutSource, expression ast.Expr, visiting map[string]bool) httpBindingShape {
	key := canonicalType(source, expression, catalog.module)
	if catalog.controllerContextRemoved && key == spineImportPath+"/core:ControllerContext" {
		return httpBindingShape{kind: "unsupported", problem: "The selected core no longer exports core.ControllerContext. Use spine.Ctx from github.com/NARUBROWN/spine/pkg/spine"}
	}
	if known, ok := defaultHTTPBinding(key); ok {
		return known
	}
	if declaration, ok := catalog.types[key]; ok {
		if visiting[key] || declaration.spec.TypeParams != nil {
			return httpBindingShape{kind: "unknown"}
		}
		visiting[key] = true
		defer delete(visiting, key)
		shape := catalog.shape(declaration.source, declaration.spec.Type, visiting)
		if declaration.spec.Assign.IsValid() {
			return shape
		}
		// 새로 정의한 타입은 path.Int 같은 기본 wrapper와 런타임 타입이 다릅니다.
		if shape.kind == "struct" {
			shape.binding, shape.legacy = "", false
			return shape
		}
		if shape.kind == "interface" && shape.binding == "context" {
			return shape
		}
		shape.binding = ""
		return shape
	}
	switch node := expression.(type) {
	case *ast.IndexExpr:
		if marker, ok := catalog.markerShape(source, node, visiting); ok {
			return marker
		}
		return httpBindingShape{kind: "unknown"}
	case *ast.ParenExpr:
		return catalog.shape(source, node.X, visiting)
	case *ast.StarExpr:
		shape := catalog.shape(source, node.X, visiting)
		if shape.kind == "struct" {
			if catalog.explicitInputs {
				return httpBindingShape{kind: "unsupported", problem: "Unwrapped DTO pointers are unsupported; use input.Body[T], input.Query[T], input.Header[T] or input.Path[T]"}
			}
			if catalog.structured && shape.legacy {
				return httpBindingShape{kind: "unsupported", problem: "Deprecated path/query/header wrappers must be values"}
			}
			shape.binding, shape.kind = "body", "pointer"
			if shape.source != "" {
				shape.binding = shape.source
			}
			shape.legacy = false
			return shape
		}
		if shape.problem != "" {
			return shape
		}
		return httpBindingShape{kind: "unknown"}
	case *ast.StructType:
		if catalog.structured {
			return catalog.inputStruct(source, node, visiting)
		}
		return httpBindingShape{kind: "struct"}
	case *ast.InterfaceType:
		if catalog.controllerContextInterface(source, node, visiting) {
			return httpBindingShape{binding: "context", kind: "interface"}
		}
		return httpBindingShape{kind: "interface"}
	case *ast.Ident:
		if node.Name == "any" {
			return httpBindingShape{binding: "context", kind: "interface"}
		}
		if canonicalType(source, node, catalog.module) == node.Name {
			return httpBindingShape{kind: "scalar"}
		}
		return httpBindingShape{kind: "unknown"}
	case *ast.SelectorExpr, *ast.IndexListExpr:
		return httpBindingShape{kind: "unknown"}
	default:
		return httpBindingShape{kind: "unsupported"}
	}
}

func (catalog *httpBindingCatalog) inspectSelectedCore(modules []selectedModule) {
	for _, module := range modules {
		if module.Path != spineImportPath {
			continue
		}
		root := module.Dir
		if module.Replace != nil && module.Replace.Dir != "" {
			root = module.Replace.Dir
		}
		if root == "" {
			return
		}
		dir := filepath.Join(root, "core")
		pkg, err := build.Default.ImportDir(dir, 0)
		if err != nil {
			return
		}
		for _, name := range pkg.GoFiles {
			file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, name), nil, 0)
			if err != nil || file.Scope.Lookup("ControllerContext") != nil {
				return
			}
		}
		catalog.controllerContextRemoved = true
		return
	}
}

func (catalog *httpBindingCatalog) controllerContextInterface(source *layoutSource, iface *ast.InterfaceType, visiting map[string]bool) bool {
	for _, field := range iface.Methods.List {
		if len(field.Names) == 0 {
			shape := catalog.shape(source, field.Type, visiting)
			if shape.kind != "interface" || shape.binding != "context" {
				return false
			}
			continue
		}
		method, ok := field.Type.(*ast.FuncType)
		if !ok || len(field.Names) != 1 || field.Names[0].Name != "Get" {
			return false
		}
		inputs, outputs := fieldTypes(source.Fset, method.Params), fieldTypes(source.Fset, method.Results)
		if len(inputs) != 1 || inputs[0] != "string" || len(outputs) != 2 || (outputs[0] != "any" && outputs[0] != "interface{}") || outputs[1] != "bool" {
			return false
		}
	}
	return true
}

func defaultHTTPBinding(key string) (httpBindingShape, bool) {
	switch key {
	case "context:Context":
		return httpBindingShape{binding: "context", kind: "standard-context"}, true
	case spineImportPath + "/core:ControllerContext", spineImportPath + "/pkg/spine:Ctx":
		return httpBindingShape{binding: "context", kind: "interface"}, true
	case spineImportPath + "/pkg/path:Int", spineImportPath + "/pkg/path:String", spineImportPath + "/pkg/path:Boolean":
		return httpBindingShape{binding: "path", kind: "struct", legacy: true}, true
	case spineImportPath + "/pkg/query:Values", spineImportPath + "/pkg/query:Pagination":
		return httpBindingShape{binding: "query", kind: "struct", legacy: true}, true
	case spineImportPath + "/pkg/header:Values":
		return httpBindingShape{binding: "header", kind: "struct", legacy: true}, true
	case spineImportPath + "/pkg/multipart:UploadedFiles":
		return httpBindingShape{binding: "multipart", kind: "struct"}, true
	}
	return httpBindingShape{}, false
}
