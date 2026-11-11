package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const (
	runtimeProbeSchemaVersion = "spine.runtime-probe/v1alpha1"
	runtimeProbeMarker        = "SPINE_RUNTIME_PROBE="
)

type runtimeProbeReport struct {
	SchemaVersion string                     `json:"schema_version"`
	EvidenceMode  string                     `json:"evidence_mode"`
	Validated     bool                       `json:"validated"`
	StaticValid   bool                       `json:"static_contract_valid"`
	MatchesStatic bool                       `json:"matches_static"`
	Consumers     []runtimeProbeRegistration `json:"consumers"`
	WebSockets    []runtimeProbeRegistration `json:"websockets"`
	Diagnostics   []contractDiagnostic       `json:"diagnostics"`
	Limits        []string                   `json:"limits"`
}

type runtimeProbeRegistration struct {
	Name    string `json:"name"`
	Handler string `json:"handler"`
}

type runtimeProbePayload struct {
	Consumers  []runtimeProbeRegistration `json:"consumers"`
	WebSockets []runtimeProbeRegistration `json:"websockets"`
}

func runContractProbe(args []string, out, errOut io.Writer) int {
	jsonOutput := false
	for _, argument := range args {
		if argument == "--json" {
			jsonOutput = true
		} else {
			fmt.Fprintf(errOut, "unknown option: %s\n", argument)
			return 1
		}
	}
	static, err := inspectApplication()
	if err != nil {
		fmt.Fprintf(errOut, "static contract inspection failed: %v\n", err)
		return 1
	}
	payload, err := executeRuntimeProbe()
	if err != nil {
		fmt.Fprintf(errOut, "runtime contract probe failed: %v\n", err)
		return 1
	}
	report := compareRuntimeProbe(static, payload)
	if jsonOutput {
		encoder := json.NewEncoder(out)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
	} else {
		fmt.Fprintf(out, "Spine runtime contract probe (%s)\n", report.EvidenceMode)
		fmt.Fprintf(out, "  app.Validate: passed\n")
		fmt.Fprintf(out, "  static contract valid: %t\n", report.StaticValid)
		fmt.Fprintf(out, "  consumers: %d\n", len(report.Consumers))
		fmt.Fprintf(out, "  WebSockets: %d\n", len(report.WebSockets))
		for _, diagnostic := range report.Diagnostics {
			fmt.Fprintf(out, "  [%s] %s: %s\n", diagnostic.Severity, diagnostic.Code, diagnostic.Message)
		}
		for _, limit := range report.Limits {
			fmt.Fprintf(out, "  [not observable] %s\n", limit)
		}
	}
	if !report.StaticValid || !report.MatchesStatic {
		return 1
	}
	return 0
}

func executeRuntimeProbe() (runtimeProbePayload, error) {
	importPath, err := compositionImportPath()
	if err != nil {
		return runtimeProbePayload{}, err
	}
	hasDescription, err := coreHasRegistrationDescription()
	if err != nil {
		return runtimeProbePayload{}, err
	}
	source, err := format.Source([]byte(runtimeProbeSource(importPath, hasDescription)))
	if err != nil {
		return runtimeProbePayload{}, fmt.Errorf("could not format runtime probe: %w", err)
	}
	directory, err := os.MkdirTemp(".", ".spine-probe-")
	if err != nil {
		return runtimeProbePayload{}, err
	}
	defer os.RemoveAll(directory)
	path := filepath.Join(directory, "main.go")
	if err := os.WriteFile(path, source, 0o600); err != nil {
		return runtimeProbePayload{}, err
	}
	command := exec.Command("go", "run", "./"+filepath.Base(directory))
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return runtimeProbePayload{}, fmt.Errorf("composition execution or validation failed: %s", message)
	}
	marker := bytes.LastIndex(stdout.Bytes(), []byte(runtimeProbeMarker))
	if marker < 0 {
		return runtimeProbePayload{}, fmt.Errorf("probe process did not emit the runtime evidence marker")
	}
	encoded := bytes.TrimSpace(stdout.Bytes()[marker+len(runtimeProbeMarker):])
	var payload runtimeProbePayload
	if err := json.Unmarshal(encoded, &payload); err != nil {
		return runtimeProbePayload{}, fmt.Errorf("invalid runtime probe evidence: %w", err)
	}
	return payload, nil
}

func coreHasRegistrationDescription() (bool, error) {
	output, err := exec.Command("go", "list", "-json", spineImportPath).Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && len(exitErr.Stderr) > 0 {
			return false, fmt.Errorf("could not load Spine core for the runtime probe: %s", strings.TrimSpace(string(exitErr.Stderr)))
		}
		return false, fmt.Errorf("could not load Spine core for the runtime probe: %w", err)
	}
	var pkg struct {
		Dir     string
		GoFiles []string
	}
	if err := json.Unmarshal(output, &pkg); err != nil {
		return false, fmt.Errorf("could not read Spine package information: %w", err)
	}
	for _, name := range pkg.GoFiles {
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(pkg.Dir, name), nil, parser.SkipObjectResolution)
		if err != nil {
			return false, err
		}
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == "DescribeRegistrations" {
				return true, nil
			}
		}
	}
	return false, nil
}

func compositionImportPath() (string, error) {
	config, err := loadProjectConfig()
	if err != nil {
		return "", err
	}
	info, err := os.Stat(config.Composition)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("runtime probe requires composition to name an importable package directory, got %q", config.Composition)
	}
	relative, err := filepath.Rel(".", config.Composition)
	if err != nil {
		return "", err
	}
	if relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("runtime probe composition must be an importable package below the module root, got %q", config.Composition)
	}
	module, err := modulePath()
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(module, "/") + "/" + filepath.ToSlash(relative), nil
}

func runtimeProbeSource(compositionImport string, hasDescription bool) string {
	handlerExpression := `fmt.Sprintf("(*%s).%s", receiver, method)`
	if config, err := loadProjectConfig(); err == nil && config.Layout == layeredLayout {
		handlerExpression = `fmt.Sprintf("(*%s.%s).%s", packagePath, receiver, method)`
	}
	coreImport := spineImportPath
	readRegistrations := `registrations, err := spine.DescribeRegistrations(app)
	if err != nil {
		panic(err)
	}
	for _, item := range registrations.Consumers {
		handler := item.Handler
		result.Consumers = append(result.Consumers, registration{Name: item.Topic, Handler: handlerName(handler.PackagePath, handler.Receiver, handler.Method)})
	}
	for _, item := range registrations.WebSockets {
		handler := item.Handler
		result.WebSockets = append(result.WebSockets, registration{Name: item.Path, Handler: handlerName(handler.PackagePath, handler.Receiver, handler.Method)})
	}`
	if !hasDescription {
		coreImport = spineImportPath + "/core"
		readRegistrations = `legacyHandlerName := func(meta core.HandlerMeta) string {
		receiver := meta.ControllerType.Elem()
		return handlerName(receiver.PkgPath(), receiver.Name(), meta.Method.Name)
	}
	for _, item := range app.Consumers().Registrations() {
		result.Consumers = append(result.Consumers, registration{Name: item.Topic, Handler: legacyHandlerName(item.Meta)})
	}
	for _, item := range app.WebSocket().Registrations() {
		result.WebSockets = append(result.WebSockets, registration{Name: item.Path, Handler: legacyHandlerName(item.Meta)})
	}`
	}
	return fmt.Sprintf(`package main

import (
	"encoding/json"
	"fmt"

	application %q
	%q
)

type registration struct {
	Name string `+"`json:\"name\"`"+`
	Handler string `+"`json:\"handler\"`"+`
}

type payload struct {
	Consumers []registration `+"`json:\"consumers\"`"+`
	WebSockets []registration `+"`json:\"websockets\"`"+`
}

func handlerName(packagePath, receiver, method string) string {
	return %s
}

func main() {
	app, options, err := application.BuildApplication()
	if err != nil {
		panic(err)
	}
	if err := app.Validate(options); err != nil {
		panic(err)
	}
	result := payload{Consumers: []registration{}, WebSockets: []registration{}}
	%s
	encoded, err := json.Marshal(result)
	if err != nil {
		panic(err)
	}
	fmt.Printf(%q+"%%s\n", encoded)
}
`, compositionImport, coreImport, handlerExpression, readRegistrations, runtimeProbeMarker)
}

func compareRuntimeProbe(static applicationContract, payload runtimeProbePayload) runtimeProbeReport {
	report := runtimeProbeReport{
		SchemaVersion: runtimeProbeSchemaVersion,
		EvidenceMode:  "runtime-public-registry-partial",
		Validated:     true,
		StaticValid:   static.Complete && !contractHasErrors(static),
		MatchesStatic: true,
		Consumers:     sortedRuntimeRegistrations(payload.Consumers),
		WebSockets:    sortedRuntimeRegistrations(payload.WebSockets),
		Diagnostics:   append([]contractDiagnostic{}, static.Diagnostics...),
		Limits: []string{
			"HTTP routes, constructors, and interceptors are not exposed by the current spine.App API",
			"database and broker connectivity, listeners, and deployed behavior are not executed",
		},
	}
	parityDiagnostics := compareRuntimeRegistrations("CONSUMER", namedToRuntime(static.Consumers), report.Consumers)
	parityDiagnostics = append(parityDiagnostics, compareRuntimeRegistrations("WEBSOCKET", namedToRuntime(static.WebSockets), report.WebSockets)...)
	report.Diagnostics = append(report.Diagnostics, parityDiagnostics...)
	for _, diagnostic := range parityDiagnostics {
		if diagnostic.Severity == "error" {
			report.MatchesStatic = false
		}
	}
	return report
}

func namedToRuntime(registrations []namedRegistration) []runtimeProbeRegistration {
	result := make([]runtimeProbeRegistration, 0, len(registrations))
	for _, registration := range registrations {
		result = append(result, runtimeProbeRegistration{Name: registration.Name, Handler: registration.Handler})
	}
	return sortedRuntimeRegistrations(result)
}

func sortedRuntimeRegistrations(registrations []runtimeProbeRegistration) []runtimeProbeRegistration {
	result := append([]runtimeProbeRegistration{}, registrations...)
	sort.Slice(result, func(i, j int) bool {
		if result[i].Name == result[j].Name {
			return result[i].Handler < result[j].Handler
		}
		return result[i].Name < result[j].Name
	})
	return result
}

func compareRuntimeRegistrations(prefix string, expected, actual []runtimeProbeRegistration) []contractDiagnostic {
	expectedCounts := map[runtimeProbeRegistration]int{}
	actualCounts := map[runtimeProbeRegistration]int{}
	for _, registration := range expected {
		expectedCounts[registration]++
	}
	for _, registration := range actual {
		actualCounts[registration]++
	}
	var diagnostics []contractDiagnostic
	for registration, count := range expectedCounts {
		if actualCounts[registration] < count {
			diagnostics = append(diagnostics, contractDiagnostic{Severity: "error", Code: prefix + "_RUNTIME_REGISTRATION_MISSING", Message: fmt.Sprintf("static contract includes %q -> %s but runtime registry does not", registration.Name, registration.Handler)})
		}
	}
	for registration, count := range actualCounts {
		if expectedCounts[registration] < count {
			diagnostics = append(diagnostics, contractDiagnostic{Severity: "error", Code: prefix + "_STATIC_REGISTRATION_MISSING", Message: fmt.Sprintf("runtime registry includes %q -> %s but static contract does not", registration.Name, registration.Handler)})
		}
	}
	sort.Slice(diagnostics, func(i, j int) bool {
		if diagnostics[i].Code == diagnostics[j].Code {
			return diagnostics[i].Message < diagnostics[j].Message
		}
		return diagnostics[i].Code < diagnostics[j].Code
	})
	return diagnostics
}
