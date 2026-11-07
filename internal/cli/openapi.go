package cli

import (
	"bytes"
	"crypto/sha256"
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
	source := ""
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
		case "--source":
			if index+1 >= len(args) {
				fmt.Fprintln(errOut, "--source requires runtime or static")
				return 1
			}
			index++
			source = args[index]
		default:
			fmt.Fprintf(errOut, "unknown option: %s\n", args[index])
			return 1
		}
	}
	if formatName != "openapi" && formatName != "asyncapi" {
		fmt.Fprintln(errOut, "contract export supports --format openapi or --format asyncapi")
		return 1
	}
	if source == "" {
		source = "static"
	}
	if source != "runtime" && source != "static" || formatName == "asyncapi" && source != "static" {
		fmt.Fprintln(errOut, "--source supports runtime or static for OpenAPI; AsyncAPI uses static")
		return 1
	}
	if source == "runtime" {
		contents, err := executeRuntimeOpenAPI()
		if err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
		var formatted bytes.Buffer
		if err := json.Indent(&formatted, contents, "", "  "); err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
		return writeAPIExport(formatted.Bytes(), formatName, outputPath, force, out, errOut)
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
	return writeAPIExport(contents, formatName, outputPath, force, out, errOut)
}

func writeAPIExport(contents []byte, formatName, outputPath string, force bool, out, errOut io.Writer) int {
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
		if len(route.Tags) > 0 {
			operation["tags"] = route.Tags
		}
		if route.Summary != "" {
			operation["summary"] = route.Summary
		}
		if route.Description != "" {
			operation["description"] = route.Description
		}
		var parameters []any
		var unresolved []string
		for _, parameter := range route.Parameters {
			if parameter.SourceDTO && len(parameter.InputFields) > 0 {
				for _, field := range parameter.InputFields {
					schema := catalog.schema(field.Type, field.source)
					if field.description != "" {
						schema["description"] = field.description
					}
					if field.hasExample {
						schema["examples"] = []any{field.example}
					}
					if field.invalidExample {
						schema["x-spine-unresolved-example"] = true
					}
					item := map[string]any{"name": field.Name, "in": parameter.Binding, "required": parameter.Binding == "path", "schema": schema}
					if parameter.Binding != "path" {
						item["x-spine-validation-unresolved"] = true
					}
					parameters = append(parameters, item)
				}
				continue
			}
			switch parameter.Binding {
			case "path":
				if parameter.SourceDTO {
					for _, key := range parameter.Keys {
						parameters = append(parameters, map[string]any{"name": key, "in": "path", "required": true, "schema": unresolvedAPISchema(parameter.Type + "." + key)})
					}
					continue
				}
				parameters = append(parameters, map[string]any{
					"name": parameter.Key, "in": "path", "required": true, "schema": pathParameterSchema(parameter.Type),
				})
			case "body":
				payload, source := parameter.Type, route.schemaSource
				if parameter.Payload != "" {
					payload, source = parameter.Payload, parameter.payloadSource
				}
				operation["requestBody"] = map[string]any{
					"content": map[string]any{"application/json": map[string]any{"schema": catalog.schema(payload, source)}},
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
	if strings.Contains(route.Handler, ".RegisterRoutes[") {
		digest := sha256.Sum256([]byte(route.Method + " " + route.Path))
		return fmt.Sprintf("%s_%x", strings.ToLower(route.Method), digest[:8])
	}
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
