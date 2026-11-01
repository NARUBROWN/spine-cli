package cli

import (
	"encoding/json"
	"fmt"
	"io"
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
	catalog, err := newAPISchemas(contract.Module)
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
					"name": parameter.Key, "in": "path", "required": true, "schema": pathParameterSchema(parameter.Type),
				})
			case "body":
				operation["requestBody"] = map[string]any{
					"content": map[string]any{"application/json": map[string]any{"schema": catalog.schema(parameter.Type, route.schemaSource)}},
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
		if responseSchema, ok := catalog.response(route.Returns, route.schemaSource); ok {
			responses := operation["responses"].(map[string]any)
			responses["default"] = map[string]any{
				"description": "Response status is determined by httpx.ResponseOptions at runtime.",
				"content":     map[string]any{"application/json": map[string]any{"schema": responseSchema}},
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
		"components":       map[string]any{"schemas": catalog.schemas},
		"x-spine-contract": map[string]any{"schema_version": contract.SchemaVersion, "analysis_mode": contract.AnalysisMode},
	}, nil
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
