package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

const defaultContractSnapshot = "spine.contract.json"

type semanticContract struct {
	SchemaVersion string   `json:"schema_version"`
	Module        string   `json:"module"`
	Entrypoint    string   `json:"entrypoint"`
	Composition   string   `json:"composition"`
	HTTPAdapter   string   `json:"http_adapter"`
	Constructors  []string `json:"constructors"`
	HTTP          []string `json:"http"`
	Interceptors  []string `json:"interceptors"`
	Consumers     []string `json:"consumers"`
	WebSockets    []string `json:"websockets"`
	Resources     []string `json:"resources"`
	Configuration []string `json:"configuration"`
}

func runContract(args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(errOut, "usage: spine contract <check|snapshot|diff|export|probe>")
		return 1
	}
	switch args[0] {
	case "check":
		return runContractCheck(args[1:], out, errOut)
	case "snapshot":
		return runContractSnapshot(args[1:], out, errOut)
	case "diff":
		return runContractDiff(args[1:], out, errOut)
	case "export":
		return runContractExport(args[1:], out, errOut)
	case "probe":
		return runContractProbe(args[1:], out, errOut)
	default:
		fmt.Fprintf(errOut, "unknown contract command: %s\n", args[0])
		return 1
	}
}

func runContractSnapshot(args []string, out, errOut io.Writer) int {
	path := defaultContractSnapshot
	force := false
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--output":
			if index+1 >= len(args) {
				fmt.Fprintln(errOut, "--output requires a path")
				return 1
			}
			index++
			path = args[index]
		case "--force":
			force = true
		default:
			fmt.Fprintf(errOut, "unknown option: %s\n", args[index])
			return 1
		}
	}
	contract, err := inspectApplication()
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	if contractHasErrors(contract) {
		fmt.Fprintln(errOut, "refusing to snapshot a contract with error diagnostics")
		return 1
	}
	contents, err := json.MarshalIndent(contract, "", "  ")
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	contents = append(contents, '\n')
	if err := applyFileChanges([]fileChange{{Path: path, Contents: string(contents), AllowOverwrite: force}}, false); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	fmt.Fprintf(out, "Wrote Spine application contract snapshot to %s\n", path)
	return 0
}

func runContractDiff(args []string, out, errOut io.Writer) int {
	path := defaultContractSnapshot
	jsonOutput := false
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--json":
			jsonOutput = true
		case "--against":
			if index+1 >= len(args) {
				fmt.Fprintln(errOut, "--against requires a path")
				return 1
			}
			index++
			path = args[index]
		default:
			fmt.Fprintf(errOut, "unknown option: %s\n", args[index])
			return 1
		}
	}
	expectedContents, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(errOut, "could not read contract snapshot %s: %v\n", path, err)
		return 1
	}
	var expected applicationContract
	if err := json.Unmarshal(expectedContents, &expected); err != nil {
		fmt.Fprintf(errOut, "invalid contract snapshot %s: %v\n", path, err)
		return 1
	}
	if expected.SchemaVersion != contractSchemaVersion {
		fmt.Fprintf(errOut, "unsupported contract snapshot schema %q; expected %q. Review the current contract and regenerate with spine contract snapshot --force\n", expected.SchemaVersion, contractSchemaVersion)
		return 1
	}
	current, err := inspectApplication()
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	if contractHasErrors(current) {
		fmt.Fprintln(errOut, "current contract contains error diagnostics")
		return 1
	}
	expectedSemantic := semanticView(expected)
	currentSemantic := semanticView(current)
	expectedJSON, _ := json.Marshal(expectedSemantic)
	currentJSON, _ := json.Marshal(currentSemantic)
	if string(expectedJSON) == string(currentJSON) {
		if jsonOutput {
			return encodeIndentedJSON(map[string]any{"schema_version": "spine.contract-diff/v1alpha1", "matches": true, "against": path, "removed": []string{}, "added": []string{}}, out, errOut)
		}
		fmt.Fprintf(out, "Spine application contract matches %s\n", path)
		return 0
	}
	removed, added := semanticChanges(expectedSemantic, currentSemantic)
	if jsonOutput {
		if code := encodeIndentedJSON(map[string]any{"schema_version": "spine.contract-diff/v1alpha1", "matches": false, "against": path, "removed": removed, "added": added}, out, errOut); code != 0 {
			return code
		}
		fmt.Fprintf(errOut, "Spine application contract differs from %s\n", path)
		return 1
	}
	fmt.Fprintf(errOut, "Spine application contract differs from %s\n", path)
	for _, item := range removed {
		fmt.Fprintf(errOut, "  - %s\n", item)
	}
	for _, item := range added {
		fmt.Fprintf(errOut, "  + %s\n", item)
	}
	return 1
}

func semanticView(contract applicationContract) semanticContract {
	result := semanticContract{
		SchemaVersion: contract.SchemaVersion,
		Module:        contract.Module,
		Entrypoint:    contract.Entrypoint,
		Composition:   contract.Composition,
		Constructors:  []string{}, HTTP: []string{}, Interceptors: []string{},
		Consumers: []string{}, WebSockets: []string{}, Resources: []string{}, Configuration: []string{},
	}
	adapter := contract.HTTPAdapter
	adapter.Source = ""
	adapter.Selection = ""
	for index := range adapter.SelectedModules {
		adapter.SelectedModules[index].Dir = ""
	}
	adapterJSON, _ := json.Marshal(adapter)
	result.HTTPAdapter = string(adapterJSON)
	for _, item := range contract.Constructors {
		result.Constructors = append(result.Constructors, item.Target+"("+strings.Join(item.Dependencies, ", ")+")")
	}
	for _, item := range contract.HTTP {
		result.HTTP = append(result.HTTP, item.Method+" "+item.Path+" -> "+item.Handler+signatureContractText(item.Parameters, item.Returns)+" ["+strings.Join(item.Interceptors, ", ")+"]")
	}
	for _, item := range contract.Interceptors {
		result.Interceptors = append(result.Interceptors, item.Scope+": "+item.Target)
	}
	for _, item := range contract.Consumers {
		result.Consumers = append(result.Consumers, item.Name+" -> "+item.Handler+signatureContractText(item.Parameters, item.Returns))
	}
	for _, item := range contract.WebSockets {
		result.WebSockets = append(result.WebSockets, item.Name+" -> "+item.Handler+signatureContractText(item.Parameters, item.Returns))
	}
	for _, item := range contract.Resources {
		result.Resources = append(result.Resources, item.Import)
	}
	for _, item := range contract.EventAdapters {
		item.Source = ""
		encoded, _ := json.Marshal(item)
		result.Configuration = append(result.Configuration, "event_adapter = "+string(encoded))
	}
	if contract.Validator.Provider != "" && contract.Validator.Provider != "none" {
		item := contract.Validator
		item.Source = ""
		encoded, _ := json.Marshal(item)
		result.Configuration = append(result.Configuration, "validator = "+string(encoded))
	}
	for _, item := range contract.Configuration {
		result.Configuration = append(result.Configuration, item.Path+" = "+item.Value)
	}
	sort.Strings(result.Constructors)
	sort.Strings(result.HTTP)
	sort.Strings(result.Interceptors)
	sort.Strings(result.Consumers)
	sort.Strings(result.WebSockets)
	sort.Strings(result.Resources)
	sort.Strings(result.Configuration)
	return result
}

func signatureContractText(parameters []handlerParameter, returns []string) string {
	var inputs []string
	for _, parameter := range parameters {
		input := parameter.Name + ":" + parameter.Type + "@" + parameter.Binding
		if parameter.Key != "" {
			input += "[" + parameter.Key + "]"
		}
		inputs = append(inputs, input)
	}
	return " (" + strings.Join(inputs, ",") + ") -> (" + strings.Join(returns, ",") + ")"
}

func semanticChanges(expected, current semanticContract) ([]string, []string) {
	expectedItems := flattenSemanticContract(expected)
	currentItems := flattenSemanticContract(current)
	expectedSet := map[string]bool{}
	currentSet := map[string]bool{}
	for _, item := range expectedItems {
		expectedSet[item] = true
	}
	for _, item := range currentItems {
		currentSet[item] = true
	}
	var removed, added []string
	for _, item := range expectedItems {
		if !currentSet[item] {
			removed = append(removed, item)
		}
	}
	for _, item := range currentItems {
		if !expectedSet[item] {
			added = append(added, item)
		}
	}
	sort.Strings(removed)
	sort.Strings(added)
	return removed, added
}

func flattenSemanticContract(contract semanticContract) []string {
	items := []string{
		"module: " + contract.Module,
		"entrypoint: " + contract.Entrypoint,
		"composition: " + contract.Composition,
		"http_adapter: " + contract.HTTPAdapter,
	}
	for _, item := range contract.Constructors {
		items = append(items, "constructor: "+item)
	}
	for _, item := range contract.HTTP {
		items = append(items, "http: "+item)
	}
	for _, item := range contract.Interceptors {
		items = append(items, "interceptor: "+item)
	}
	for _, item := range contract.Consumers {
		items = append(items, "consumer: "+item)
	}
	for _, item := range contract.WebSockets {
		items = append(items, "websocket: "+item)
	}
	for _, item := range contract.Resources {
		items = append(items, "resource: "+item)
	}
	for _, item := range contract.Configuration {
		items = append(items, "configuration: "+item)
	}
	return items
}

func runExplain(args []string, out, errOut io.Writer) int {
	jsonOutput := false
	filtered := make([]string, 0, len(args))
	for _, argument := range args {
		if argument == "--json" {
			jsonOutput = true
			continue
		}
		filtered = append(filtered, argument)
	}
	args = filtered
	if len(args) < 2 {
		fmt.Fprintln(errOut, "usage: spine explain <http|consumer|websocket> <name> [path] [--json]")
		return 1
	}
	contract, err := inspectApplication()
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	switch args[0] {
	case "http":
		if len(args) != 3 {
			fmt.Fprintln(errOut, "usage: spine explain http <method> <path>")
			return 1
		}
		method, path := strings.ToUpper(args[1]), args[2]
		for _, route := range contract.HTTP {
			if route.Method == method && route.Path == path {
				if jsonOutput {
					return encodeIndentedJSON(map[string]any{"kind": "http", "route": route, "execution_steps": httpExecutionSteps(contract, route), "evidence_boundary": "static-source inspection; dynamic registration and external infrastructure are not evaluated"}, out, errOut)
				}
				printHTTPExplanation(contract, route, out)
				return 0
			}
		}
		fmt.Fprintf(errOut, "HTTP route not found in static contract: %s %s\n", method, path)
		return 1
	case "consumer":
		if len(args) != 2 {
			fmt.Fprintln(errOut, "usage: spine explain consumer <topic>")
			return 1
		}
		return explainNamedRegistration("consumer", args[1], contract.Consumers, jsonOutput, out, errOut)
	case "websocket":
		if len(args) != 2 {
			fmt.Fprintln(errOut, "usage: spine explain websocket <path>")
			return 1
		}
		return explainNamedRegistration("WebSocket", args[1], contract.WebSockets, jsonOutput, out, errOut)
	default:
		fmt.Fprintf(errOut, "unknown execution contract type: %s\n", args[0])
		return 1
	}
}

func httpExecutionSteps(contract applicationContract, route httpRegistration) []string {
	steps := []string{"resolve request values from the handler signature"}
	for _, interceptor := range contract.Interceptors {
		if interceptor.Scope == "all" || strings.Contains(interceptor.Scope, "HTTP") || strings.Contains(interceptor.Scope, "All") {
			steps = append(steps, "PreHandle "+interceptor.Target)
		}
	}
	for _, target := range route.Interceptors {
		steps = append(steps, "PreHandle "+target)
	}
	return append(steps, "invoke "+route.Handler, "prepare and validate the response", "run successful interceptors' BeforeResponse in reverse order", "flush the prepared response", "run AfterCompletion cleanup/observation")
}

func printHTTPExplanation(contract applicationContract, route httpRegistration, out io.Writer) {
	fmt.Fprintf(out, "HTTP %s %s\n", route.Method, route.Path)
	fmt.Fprintf(out, "  handler: %s\n  registration: %s\n", route.Handler, route.Source)
	for _, parameter := range route.Parameters {
		key := ""
		if parameter.Key != "" {
			key = " key=" + parameter.Key
		}
		fmt.Fprintf(out, "  input: %s %s binding=%s%s\n", parameter.Name, parameter.Type, parameter.Binding, key)
	}
	if len(route.Returns) > 0 {
		fmt.Fprintf(out, "  returns: %s\n", strings.Join(route.Returns, ", "))
	}
	fmt.Fprintln(out, "  execution contract:")
	fmt.Fprintln(out, "    1. resolve request values from the handler signature")
	interceptors := make([]contractRegistration, 0, len(contract.Interceptors)+len(route.Interceptors))
	for _, interceptor := range contract.Interceptors {
		if interceptor.Scope == "all" || strings.Contains(interceptor.Scope, "HTTP") || strings.Contains(interceptor.Scope, "All") {
			interceptors = append(interceptors, interceptor)
		}
	}
	for _, target := range route.Interceptors {
		interceptors = append(interceptors, contractRegistration{Target: target, Scope: "route", Source: route.Source})
	}
	for index, interceptor := range interceptors {
		fmt.Fprintf(out, "    %d. PreHandle %s (%s)\n", index+2, interceptor.Target, interceptor.Source)
	}
	step := len(interceptors) + 2
	fmt.Fprintf(out, "    %d. invoke %s\n", step, route.Handler)
	fmt.Fprintf(out, "    %d. prepare and validate the response\n", step+1)
	fmt.Fprintf(out, "    %d. run successful interceptors' BeforeResponse in reverse order\n", step+2)
	fmt.Fprintf(out, "    %d. flush the prepared response\n", step+3)
	fmt.Fprintf(out, "    %d. run AfterCompletion cleanup/observation\n", step+4)
	fmt.Fprintln(out, "  evidence boundary: static-source inspection; dynamic registration and external infrastructure are not evaluated")
}

func explainNamedRegistration(kind, name string, registrations []namedRegistration, jsonOutput bool, out, errOut io.Writer) int {
	for _, registration := range registrations {
		if registration.Name == name {
			if jsonOutput {
				return encodeIndentedJSON(map[string]any{"kind": strings.ToLower(kind), "registration": registration, "evidence_boundary": "static-source inspection; runtime delivery and external infrastructure are not evaluated"}, out, errOut)
			}
			fmt.Fprintf(out, "%s %s\n  handler: %s\n  registration: %s\n", kind, name, registration.Handler, registration.Source)
			fmt.Fprintln(out, "  evidence boundary: static-source inspection; runtime delivery and external infrastructure are not evaluated")
			return 0
		}
	}
	fmt.Fprintf(errOut, "%s registration not found in static contract: %s\n", kind, name)
	return 1
}
