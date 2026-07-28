package cli

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

func runContractExport(args []string, out, errOut io.Writer) int {
	formatName := ""
	outputPath := ""
	force := false
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--format":
			if index+1 >= len(args) {
				fmt.Fprintln(errOut, "--format requires a value")
				return 1
			}
			index++
			formatName = args[index]
		case "--output":
			if index+1 >= len(args) {
				fmt.Fprintln(errOut, "--output requires a path")
				return 1
			}
			index++
			outputPath = args[index]
		case "--force":
			force = true
		default:
			fmt.Fprintf(errOut, "unknown option: %s\n", args[index])
			return 1
		}
	}
	if formatName != "openapi" && formatName != "asyncapi" {
		fmt.Fprintln(errOut, "contract export supports --format openapi or --format asyncapi")
		return 1
	}
	contract, err := inspectApplication()
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	if contractHasErrors(contract) {
		fmt.Fprintln(errOut, "refusing to export a contract with error diagnostics")
		return 1
	}
	var document map[string]any
	if formatName == "openapi" {
		document, err = buildOpenAPIDocument(contract)
	} else {
		document, err = buildAsyncAPIDocument(contract)
	}
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	contents, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	contents = append(contents, '\n')
	if outputPath == "" {
		_, _ = out.Write(contents)
		return 0
	}
	if err := applyFileChanges([]fileChange{{Path: outputPath, Contents: string(contents), AllowOverwrite: force}}, false); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	fmt.Fprintf(out, "Wrote %s contract to %s\n", formatName, outputPath)
	return 0
}

func buildOpenAPIDocument(contract applicationContract) (map[string]any, error) {
	schemas, err := inspectStructSchemas(".")
	if err != nil {
		return nil, err
	}
	paths := map[string]any{}
	for _, route := range contract.HTTP {
		openAPIPath := openAPIPath(route.Path)
		pathItem, _ := paths[openAPIPath].(map[string]any)
		if pathItem == nil {
			pathItem = map[string]any{}
			paths[openAPIPath] = pathItem
		}
		operation := map[string]any{
			"operationId":          operationID(route),
			"x-spine-handler":      route.Handler,
			"x-spine-source":       route.Source,
			"x-spine-analysis":     contract.AnalysisMode,
			"x-spine-return-types": append([]string(nil), route.Returns...),
			"responses": map[string]any{
				"default": map[string]any{"description": "Response status is determined by the Spine handler contract."},
			},
		}
		var parameters []any
		var unresolved []string
		for _, parameter := range route.Parameters {
			switch parameter.Binding {
			case "path":
				parameters = append(parameters, map[string]any{
					"name": parameter.Key, "in": "path", "required": true, "schema": openAPISchemaForType(parameter.Type),
				})
			case "body":
				operation["requestBody"] = map[string]any{
					"content": map[string]any{"application/json": map[string]any{"schema": openAPISchemaForType(parameter.Type)}},
				}
			case "query", "header", "value":
				unresolved = append(unresolved, parameter.Name+":"+parameter.Type+"@"+parameter.Binding)
			}
		}
		if len(parameters) > 0 {
			operation["parameters"] = parameters
		}
		if len(unresolved) > 0 {
			operation["x-spine-unresolved-parameters"] = unresolved
		}
		if responseType := spineResponseBodyType(route.Returns); responseType != "" {
			responses := operation["responses"].(map[string]any)
			responses["default"] = map[string]any{
				"description": "Response status is determined by httpx.ResponseOptions at runtime.",
				"content":     map[string]any{"application/json": map[string]any{"schema": openAPISchemaForType(responseType)}},
			}
		}
		pathItem[strings.ToLower(route.Method)] = operation
	}
	return map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title":       contract.Module,
			"version":     "generated",
			"description": "Generated from the Spine static execution contract. Runtime status selection and dynamic registration are not inferred.",
		},
		"paths":            paths,
		"components":       map[string]any{"schemas": schemas},
		"x-spine-contract": map[string]any{"schema_version": contract.SchemaVersion, "analysis_mode": contract.AnalysisMode},
	}, nil
}

func inspectStructSchemas(root string) (map[string]any, error) {
	schemas := map[string]any{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && (entry.Name() == ".git" || entry.Name() == "bin" || entry.Name() == "vendor" || strings.HasPrefix(entry.Name(), ".spine")) {
			return filepath.SkipDir
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		for _, declaration := range file.Decls {
			group, ok := declaration.(*ast.GenDecl)
			if !ok || group.Tok != token.TYPE {
				continue
			}
			for _, specification := range group.Specs {
				typeSpec, ok := specification.(*ast.TypeSpec)
				if !ok {
					continue
				}
				structure, ok := typeSpec.Type.(*ast.StructType)
				if !ok {
					continue
				}
				schema := schemaForStruct(fset, structure)
				properties, _ := schema["properties"].(map[string]any)
				if len(properties) > 0 {
					schemas[typeSpec.Name.Name] = schema
				}
			}
		}
		return nil
	})
	return schemas, err
}

func schemaForStruct(fset *token.FileSet, structure *ast.StructType) map[string]any {
	properties := map[string]any{}
	var required []string
	for _, field := range structure.Fields.List {
		if len(field.Names) == 0 {
			continue
		}
		jsonName, omitEmpty, skip := jsonFieldName(field)
		if skip {
			continue
		}
		for _, name := range field.Names {
			if !ast.IsExported(name.Name) {
				continue
			}
			propertyName := jsonName
			if propertyName == "" {
				propertyName = lowerFirst(name.Name)
			}
			properties[propertyName] = openAPISchemaForType(expressionText(fset, field.Type))
			if !omitEmpty {
				required = append(required, propertyName)
			}
		}
	}
	sort.Strings(required)
	schema := map[string]any{"type": "object", "properties": properties}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func jsonFieldName(field *ast.Field) (string, bool, bool) {
	if field.Tag == nil {
		return "", false, false
	}
	tagValue, err := strconv.Unquote(field.Tag.Value)
	if err != nil {
		return "", false, false
	}
	jsonTag := reflect.StructTag(tagValue).Get("json")
	parts := strings.Split(jsonTag, ",")
	if len(parts) > 0 && parts[0] == "-" {
		return "", false, true
	}
	omitEmpty := false
	for _, option := range parts[1:] {
		omitEmpty = omitEmpty || option == "omitempty"
	}
	return parts[0], omitEmpty, false
}

func openAPISchemaForType(typeName string) map[string]any {
	typeName = strings.TrimSpace(strings.TrimPrefix(typeName, "*"))
	if strings.HasPrefix(typeName, "[]") {
		return map[string]any{"type": "array", "items": openAPISchemaForType(typeName[2:])}
	}
	if strings.HasPrefix(typeName, "map[string]") {
		return map[string]any{"type": "object", "additionalProperties": openAPISchemaForType(strings.TrimPrefix(typeName, "map[string]"))}
	}
	switch typeName {
	case "string", "path.String":
		return map[string]any{"type": "string"}
	case "bool", "path.Boolean":
		return map[string]any{"type": "boolean"}
	case "int", "int32":
		return map[string]any{"type": "integer", "format": "int32"}
	case "int64", "path.Int":
		return map[string]any{"type": "integer", "format": "int64"}
	case "float32":
		return map[string]any{"type": "number", "format": "float"}
	case "float64":
		return map[string]any{"type": "number", "format": "double"}
	case "any", "interface{}":
		return map[string]any{}
	default:
		if strings.ContainsAny(typeName, "[]{}(), ") || typeName == "" {
			return map[string]any{"x-spine-go-type": typeName}
		}
		parts := strings.Split(typeName, ".")
		return map[string]any{"$ref": "#/components/schemas/" + parts[len(parts)-1]}
	}
}

func spineResponseBodyType(returns []string) string {
	for _, returnType := range returns {
		const prefix = "httpx.Response["
		if strings.HasPrefix(returnType, prefix) && strings.HasSuffix(returnType, "]") {
			return strings.TrimSuffix(strings.TrimPrefix(returnType, prefix), "]")
		}
	}
	return ""
}

func openAPIPath(path string) string {
	segments := splitContractPath(path)
	for index, segment := range segments {
		if strings.HasPrefix(segment, ":") {
			segments[index] = "{" + strings.TrimPrefix(segment, ":") + "}"
		}
	}
	return "/" + strings.Join(segments, "/")
}

func operationID(route httpRegistration) string {
	var result strings.Builder
	for _, character := range route.Handler {
		if unicode.IsLetter(character) || unicode.IsDigit(character) {
			result.WriteRune(character)
		} else if result.Len() > 0 && !strings.HasSuffix(result.String(), "_") {
			result.WriteByte('_')
		}
	}
	value := strings.Trim(result.String(), "_")
	if value == "" {
		value = strings.ToLower(route.Method) + "_" + strings.Trim(strings.ReplaceAll(route.Path, "/", "_"), "_")
	}
	return value
}

func lowerFirst(value string) string {
	characters := []rune(value)
	if len(characters) == 0 {
		return value
	}
	characters[0] = unicode.ToLower(characters[0])
	return string(characters)
}
