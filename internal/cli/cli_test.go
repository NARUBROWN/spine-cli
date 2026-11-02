package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/format"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestRunHelp(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Run([]string{"help"}, &out, &errOut); code != 0 {
		t.Fatalf("code = %d", code)
	}
	if !strings.Contains(out.String(), "spine-cli new") {
		t.Fatal("help did not include new command")
	}
}

func TestRunVersionUsesProgramName(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Run([]string{"version"}, &out, &errOut); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, errOut.String())
	}
	if out.String() != "spine-cli 0.4.0\n" {
		t.Fatalf("unexpected version output: %q", out.String())
	}
}

func TestJSONProtocolWrapsSuccessAndStructuredFailure(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Run([]string{"schema", "--json"}, &out, &errOut); code != 0 {
		t.Fatalf("schema code=%d: %s", code, errOut.String())
	}
	var success protocolResult
	if err := json.Unmarshal(out.Bytes(), &success); err != nil {
		t.Fatalf("decode protocol success: %v\n%s", err, out.String())
	}
	if !success.OK || success.SchemaVersion != protocolSchemaVersion || success.Command != "schema" || success.Result == nil {
		t.Fatalf("unexpected success envelope: %#v", success)
	}

	out.Reset()
	errOut.Reset()
	if code := Run([]string{"generate", "--json"}, &out, &errOut); code != 2 {
		t.Fatalf("usage code=%d, stdout=%s, stderr=%s", code, out.String(), errOut.String())
	}
	var failure protocolResult
	if err := json.Unmarshal(out.Bytes(), &failure); err != nil {
		t.Fatalf("decode protocol failure: %v\n%s", err, out.String())
	}
	if failure.OK || failure.Error == nil || failure.Error.Code != "USAGE_ERROR" || failure.ExitCode != 2 || !failure.Error.Retryable {
		t.Fatalf("unexpected failure envelope: %#v", failure)
	}
}

func TestCommandSchemaDescribesAgentSideEffects(t *testing.T) {
	report := currentCommandSchema()
	commands := map[string]commandDefinition{}
	for _, command := range report.Commands {
		commands[command.Name] = command
	}
	generate := commands["generate"]
	if !generate.Effects.WritesFiles || !generate.SupportsJSON || !generate.SupportsDryRun {
		t.Fatalf("generate side effects are incomplete: %#v", generate)
	}
	if _, ok := generate.Options["if-match"]; !ok {
		t.Fatalf("generate optimistic concurrency option missing: %#v", generate.Options)
	}
	if !commands["dev"].Effects.LongRunning || commands["context"].Effects.WritesFiles {
		t.Fatalf("command effects are misleading: %#v", commands)
	}
}

func TestDevEventStreamKeepsProcessOutputAsJSONLines(t *testing.T) {
	var out bytes.Buffer
	sink := newDevEventSink(&out)
	sink.emit("process_started", map[string]any{"pid": 42, "readiness_observed": false})
	writer := newDevEventWriter(sink, "stdout")
	for _, chunk := range []string{"hel", "lo\n\nwor", "ld"} {
		if _, err := writer.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	writer.Flush()
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf("event stream lines=%d:\n%s", len(lines), out.String())
	}
	wantedOutput := []string{"hello", "", "world"}
	for index, line := range lines {
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("invalid JSONL event: %v\n%s", err, line)
		}
		if event["schema_version"] != "spine.dev-event/v1alpha1" {
			t.Fatalf("event schema missing: %#v", event)
		}
		if index > 0 && event["line"] != wantedOutput[index-1] {
			t.Fatalf("process output was split or dropped: got %#v, want %q", event["line"], wantedOutput[index-1])
		}
	}
}

func TestGlobalRootRunsCommandWithoutChangingCallerDirectory(t *testing.T) {
	inTempDir(t)
	var out, errOut bytes.Buffer
	if code := runLegacyNew([]string{"todo", "--module", "example.com/todo"}, &out, &errOut); code != 0 {
		t.Fatalf("new code=%d: %s", code, errOut.String())
	}
	before, _ := os.Getwd()
	out.Reset()
	errOut.Reset()
	if code := Run([]string{"context", "--root", filepath.Join(before, "todo"), "--json"}, &out, &errOut); code != 0 {
		t.Fatalf("context --root code=%d: %s\n%s", code, errOut.String(), out.String())
	}
	after, _ := os.Getwd()
	if after != before {
		t.Fatalf("global root leaked cwd: before=%s after=%s", before, after)
	}
}

func TestDryRunBaseHashGuardsGenerationApply(t *testing.T) {
	inTempDir(t)
	var out, errOut bytes.Buffer
	if code := runLegacyNew([]string{"todo", "--module", "example.com/todo", "--dry-run", "--json"}, &out, &errOut); code != 0 {
		t.Fatalf("dry-run code=%d: %s", code, errOut.String())
	}
	var plan struct {
		BaseHash string          `json:"base_hash"`
		Changes  []plannedChange `json:"changes"`
	}
	if err := json.Unmarshal(out.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if plan.BaseHash == "" || len(plan.Changes) == 0 || plan.Changes[0].Operation != "create" {
		t.Fatalf("incomplete generation plan: %#v", plan)
	}
	out.Reset()
	errOut.Reset()
	if code := runLegacyNew([]string{"todo", "--module", "example.com/todo", "--if-match", plan.BaseHash}, &out, &errOut); code != 0 {
		t.Fatalf("matching plan was rejected: code=%d", code)
	}
	out.Reset()
	errOut.Reset()
	if code := runLegacyNew([]string{"todo", "--module", "example.com/todo", "--force", "--if-match", plan.BaseHash}, &out, &errOut); code == 0 || !strings.Contains(errOut.String(), "changed since dry-run") {
		t.Fatalf("stale plan was accepted: code=%d, stderr=%s", code, errOut.String())
	}
}

func TestAgentContextExplainAndVerificationJSON(t *testing.T) {
	inTempDir(t)
	var out, errOut bytes.Buffer
	if code := runLegacyNew([]string{"todo", "--module", "example.com/todo"}, &out, &errOut); code != 0 {
		t.Fatalf("new code=%d: %s", code, errOut.String())
	}
	if err := os.Chdir("todo"); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errOut.Reset()
	if code := runContext([]string{"--json"}, &out, &errOut); code != 0 {
		t.Fatalf("context code=%d: %s\n%s", code, errOut.String(), out.String())
	}
	var context projectContext
	if err := json.Unmarshal(out.Bytes(), &context); err != nil {
		t.Fatal(err)
	}
	if context.SchemaVersion != contextSchemaVersion || context.ProjectRoot == "" || context.Contract == nil || len(context.NextActions) == 0 {
		t.Fatalf("incomplete agent context: %#v", context)
	}
	if len(context.NextActions) != 2 || context.NextActions[1].Safe {
		t.Fatalf("verification must not be advertised as safe: %#v", context.NextActions)
	}
	for _, action := range context.NextActions {
		if !strings.Contains(action.Command, "--root \"") || !strings.Contains(action.Command, context.ProjectRoot) {
			t.Fatalf("next action does not preserve project root: %#v", action)
		}
	}

	out.Reset()
	errOut.Reset()
	if code := runExplain([]string{"http", "GET", "/health", "--json"}, &out, &errOut); code != 0 {
		t.Fatalf("explain code=%d: %s", code, errOut.String())
	}
	var explanation map[string]any
	if err := json.Unmarshal(out.Bytes(), &explanation); err != nil || explanation["execution_steps"] == nil {
		t.Fatalf("invalid explanation: %v, %#v", err, explanation)
	}

	out.Reset()
	errOut.Reset()
	if code := runGenerate([]string{"consumer", "order", "--topic", "order.created", "--field", "order_id:int64"}, &out, &errOut); code != 0 {
		t.Fatalf("consumer code=%d: %s", code, errOut.String())
	}
	out.Reset()
	errOut.Reset()
	if code := runVerify([]string{"--json"}, &out, &errOut); code == 0 {
		t.Fatal("unimplemented consumer unexpectedly passed verification")
	}
	var verification verificationReport
	if err := json.Unmarshal(out.Bytes(), &verification); err != nil {
		t.Fatalf("decode verification: %v\n%s", err, out.String())
	}
	if verification.Valid || len(verification.Stages) != 1 || verification.Stages[0].Name != "contract" || len(verification.Stages[0].Diagnostics) == 0 {
		t.Fatalf("verification failure lacks structured diagnostics: %#v", verification)
	}
	diagnostic := verification.Stages[0].Diagnostics[0]
	if !diagnostic.Retryable || diagnostic.Hint == "" || len(diagnostic.NextActions) == 0 {
		t.Fatalf("verification diagnostic is not actionable: %#v", diagnostic)
	}
}

func TestCapabilitiesExposeImplementedAndBlockedBoundaries(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := runCapabilities([]string{"--json"}, &out, &errOut); code != 0 {
		t.Fatalf("code=%d: %s", code, errOut.String())
	}
	var report capabilityReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	states := map[string]bool{}
	for _, capability := range report.Capabilities {
		states[capability.Name] = capability.Implemented
	}
	if !states["contract.static-inspection"] || !states["generation.project"] {
		t.Fatalf("implemented capabilities missing: %#v", states)
	}
	if !states["generation.vertical-slice"] {
		t.Fatalf("vertical slice capability missing: %#v", states)
	}
	if !states["generation.consumer"] || !states["generation.websocket"] {
		t.Fatalf("message generation capabilities missing: %#v", states)
	}
	if !states["contract.openapi-export"] {
		t.Fatalf("OpenAPI capability missing: %#v", states)
	}
	if !states["contract.asyncapi-export"] {
		t.Fatalf("AsyncAPI capability missing: %#v", states)
	}
	if !states["contract.runtime-probe-partial"] {
		t.Fatalf("partial runtime probe capability missing: %#v", states)
	}
	if !states["contract.structured-check"] {
		t.Fatalf("structured contract check capability missing: %#v", states)
	}
	if states["contract.runtime-manifest"] || states["doctor.infrastructure-connect"] {
		t.Fatalf("unimplemented capabilities were advertised as available: %#v", states)
	}
}

func TestRuntimeProbeSourceAndComparison(t *testing.T) {
	source := runtimeProbeSource("example.com/demo/internal/application")
	if _, err := format.Source([]byte(source)); err != nil {
		t.Fatalf("invalid runtime probe source: %v\n%s", err, source)
	}
	if !strings.Contains(source, `"%s\n"`) || strings.Contains(source, `"%s\\n"`) {
		t.Fatalf("runtime probe marker must end with a real newline escape:\n%s", source)
	}
	static := applicationContract{
		Complete:   true,
		Consumers:  []namedRegistration{{Name: "order.created", Handler: "(*OrderConsumer).Handle"}},
		WebSockets: []namedRegistration{{Name: "/ws/chat", Handler: "(*ChatSocket).Handle"}},
	}
	matching := compareRuntimeProbe(static, runtimeProbePayload{
		Consumers:  []runtimeProbeRegistration{{Name: "order.created", Handler: "(*OrderConsumer).Handle"}},
		WebSockets: []runtimeProbeRegistration{{Name: "/ws/chat", Handler: "(*ChatSocket).Handle"}},
	})
	if !matching.Validated || !matching.MatchesStatic || len(matching.Diagnostics) != 0 {
		t.Fatalf("matching runtime evidence was rejected: %#v", matching)
	}
	matchingJSON, err := json.Marshal(matching)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(matchingJSON), `"diagnostics":[]`) {
		t.Fatalf("empty runtime diagnostics must remain an array: %s", matchingJSON)
	}
	static.Diagnostics = []contractDiagnostic{{Severity: "error", Code: "TEST_STATIC_ERROR", Message: "invalid static contract"}}
	invalidStatic := compareRuntimeProbe(static, runtimeProbePayload{
		Consumers:  []runtimeProbeRegistration{{Name: "order.created", Handler: "(*OrderConsumer).Handle"}},
		WebSockets: []runtimeProbeRegistration{{Name: "/ws/chat", Handler: "(*ChatSocket).Handle"}},
	})
	if invalidStatic.StaticValid || !invalidStatic.MatchesStatic {
		t.Fatalf("static validity and runtime parity were conflated: %#v", invalidStatic)
	}
	static.Diagnostics = nil
	mismatch := compareRuntimeProbe(static, runtimeProbePayload{
		Consumers: []runtimeProbeRegistration{{Name: "order.changed", Handler: "(*OrderConsumer).Handle"}},
	})
	if mismatch.MatchesStatic || len(mismatch.Diagnostics) != 3 {
		t.Fatalf("runtime drift was not diagnosed: %#v", mismatch)
	}
	codes := map[string]bool{}
	for _, diagnostic := range mismatch.Diagnostics {
		codes[diagnostic.Code] = true
	}
	if !codes["CONSUMER_RUNTIME_REGISTRATION_MISSING"] || !codes["CONSUMER_STATIC_REGISTRATION_MISSING"] || !codes["WEBSOCKET_RUNTIME_REGISTRATION_MISSING"] {
		t.Fatalf("unexpected runtime drift diagnostics: %#v", mismatch.Diagnostics)
	}
}

func TestRuntimeProbeKeepsEmptyRegistriesAsArrays(t *testing.T) {
	report := compareRuntimeProbe(applicationContract{Complete: true, Diagnostics: []contractDiagnostic{}}, runtimeProbePayload{})
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"consumers":[]`, `"websockets":[]`, `"diagnostics":[]`} {
		if !strings.Contains(string(encoded), field) {
			t.Fatalf("runtime probe collection is not a stable array (%s): %s", field, encoded)
		}
	}
}

func TestCompositionImportPathRequiresPackageBelowModuleRoot(t *testing.T) {
	inTempDir(t)
	if err := os.MkdirAll(filepath.Join("internal", "application"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("go.mod", []byte("module example.com/demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("spine.toml", []byte("module = \"example.com/demo\"\nentrypoint = \".\"\ncomposition = \"./internal/application\"\nport = 8080\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path, err := compositionImportPath()
	if err != nil {
		t.Fatal(err)
	}
	if path != "example.com/demo/internal/application" {
		t.Fatalf("unexpected composition import path: %s", path)
	}
}

func TestGenerateTypedConsumerAndWebSocketContracts(t *testing.T) {
	inTempDir(t)
	var out, errOut bytes.Buffer
	if code := runLegacyNew([]string{"events", "--module", "example.com/events"}, &out, &errOut); code != 0 {
		t.Fatalf("new code=%d: %s", code, errOut.String())
	}
	if err := os.Chdir("events"); err != nil {
		t.Fatal(err)
	}
	for _, arguments := range [][]string{
		{"consumer", "order", "--topic", "order.created", "--field", "order_id:int64"},
		{"websocket", "chat", "--path", "/ws/chat", "--field", "message:string"},
	} {
		out.Reset()
		errOut.Reset()
		if code := runGenerate(arguments, &out, &errOut); code != 0 {
			t.Fatalf("generate %s code=%d: %s", arguments[0], code, errOut.String())
		}
	}
	for _, path := range []string{
		filepath.Join("src", "consumer", "order", "module.go"),
		filepath.Join("src", "consumer", "order", "order_handler.go"),
		filepath.Join("src", "consumer", "order", "order_message.go"),
		filepath.Join("src", "websocket", "chat", "module.go"),
		filepath.Join("src", "websocket", "chat", "chat_handler.go"),
		filepath.Join("src", "websocket", "chat", "chat_message.go"),
	} {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("missing generated file %s: %v", path, err)
		}
		if _, err := format.Source(contents); err != nil {
			t.Fatalf("invalid generated Go source %s: %v\n%s", path, err, contents)
		}
	}
	if unformatted, err := unformattedFiles("."); err != nil || len(unformatted) != 0 {
		t.Fatalf("message generators emitted unformatted Go files: %v, %v", unformatted, err)
	}
	contract, err := inspectApplication()
	if err != nil {
		t.Fatal(err)
	}
	if len(contract.Consumers) != 1 || contract.Consumers[0].Name != "order.created" || messagePayloadType(contract.Consumers[0].Parameters) != "OrderEvent" {
		t.Fatalf("unexpected consumer contract: %#v", contract.Consumers)
	}
	if len(contract.WebSockets) != 1 || contract.WebSockets[0].Name != "/ws/chat" || messagePayloadType(contract.WebSockets[0].Parameters) != "ChatMessage" {
		t.Fatalf("unexpected WebSocket contract: %#v", contract.WebSockets)
	}
	diagnosticCodes := map[string]bool{}
	for _, diagnostic := range contract.Diagnostics {
		diagnosticCodes[diagnostic.Code] = true
	}
	if !diagnosticCodes["CONSUMER_HANDLER_NOT_IMPLEMENTED"] || !diagnosticCodes["WEBSOCKET_HANDLER_NOT_IMPLEMENTED"] {
		t.Fatalf("generated fail-closed handlers were not diagnosed: %#v", contract.Diagnostics)
	}
	document, err := buildAsyncAPIDocument(contract)
	if err != nil {
		t.Fatal(err)
	}
	channels, ok := document["channels"].(map[string]any)
	if !ok || len(channels) != 2 {
		t.Fatalf("generated registrations did not reach AsyncAPI: %#v", document["channels"])
	}
}

func TestGenerateResource(t *testing.T) {
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })

	var out, errOut bytes.Buffer
	if code := runGenerate([]string{"resource", "receipt", "--no-register"}, &out, &errOut); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, errOut.String())
	}
	for _, path := range []string{
		filepath.Join("src", "receipt", "receipt_route.go"),
		filepath.Join("src", "receipt", "receipt_repository.go"),
		filepath.Join("src", "receipt", "receipt_service.go"),
		filepath.Join("src", "receipt", "receipt_controller.go"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected generated file %s: %v", path, err)
		}
	}
	if unformatted, err := unformattedFiles("."); err != nil || len(unformatted) != 0 {
		t.Fatalf("resource generator emitted unformatted Go files: %v, %v", unformatted, err)
	}
}

func TestGenerateLayeredComponents(t *testing.T) {
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })

	for _, kind := range []string{"controller", "service", "repository"} {
		var out, errOut bytes.Buffer
		if code := runGenerate([]string{kind, "user", "--no-register"}, &out, &errOut); code != 0 {
			t.Fatalf("%s: code = %d, stderr = %s", kind, code, errOut.String())
		}
		path := filepath.Join("src", "user_"+kind+".go")
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected generated file %s: %v", path, err)
		}
	}
}

func TestGenerateLayeredComponentsWithDependenciesAndRegistration(t *testing.T) {
	inTempDir(t)
	var out, errOut bytes.Buffer
	if code := runLegacyNew([]string{"todo", "--module", "example.com/todo"}, &out, &errOut); code != 0 {
		t.Fatalf("new code=%d: %s", code, errOut.String())
	}
	if err := os.Chdir("todo"); err != nil {
		t.Fatal(err)
	}
	for _, arguments := range [][]string{
		{"repository", "user"},
		{"service", "user", "--depends", "user_repository"},
		{"repository", "audit"},
		{"controller", "user", "--depends", "user_service", "audit_repository"},
		{"controller", "another"},
	} {
		out.Reset()
		errOut.Reset()
		if code := runGenerate(arguments, &out, &errOut); code != 0 {
			t.Fatalf("generate %v code=%d: %s", arguments, code, errOut.String())
		}
	}
	service, err := os.ReadFile(filepath.Join("src", "user_service.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(service), "userRepository *UserRepository") || !strings.Contains(string(service), "userRepository: userRepository") {
		t.Fatalf("service dependency was not generated:\n%s", service)
	}
	controller, err := os.ReadFile(filepath.Join("src", "user_controller.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`userService\s+\*UserService`).Match(controller) || !regexp.MustCompile(`userService:\s+userService`).Match(controller) ||
		!regexp.MustCompile(`auditRepository\s+\*AuditRepository`).Match(controller) || !regexp.MustCompile(`auditRepository:\s+auditRepository`).Match(controller) {
		t.Fatalf("controller dependency was not generated:\n%s", controller)
	}
	application, err := os.ReadFile(filepath.Join("internal", "application", "application.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"src.NewUserRepository", "src.NewUserService", "src.NewAuditRepository", "src.NewUserController", "src.NewAnotherController", `route.Register(app)`} {
		if !strings.Contains(string(application), expected) {
			t.Fatalf("missing %s in composition root:\n%s", expected, application)
		}
	}
	routes, err := os.ReadFile(filepath.Join("src", "route", "route.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`app.Route("GET", "/user/:id", (*src.UserController).Get)`, `app.Route("GET", "/another/:id", (*src.AnotherController).Get)`} {
		if !strings.Contains(string(routes), expected) {
			t.Fatalf("controller route %s was not collected in src/route/route.go:\n%s", expected, routes)
		}
	}
	if strings.Count(string(application), "route.Register(app)") != 1 {
		t.Fatalf("application must register collected controller routes exactly once:\n%s", application)
	}
}

func TestGenerateRejectsDuplicateConstructorNameFromResourceThenComponent(t *testing.T) {
	inTempDir(t)
	var out, errOut bytes.Buffer
	if code := runLegacyNew([]string{"todo", "--module", "example.com/todo"}, &out, &errOut); code != 0 {
		t.Fatalf("new code=%d: %s", code, errOut.String())
	}
	if err := os.Chdir("todo"); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errOut.Reset()
	if code := runGenerate([]string{"resource", "test"}, &out, &errOut); code != 0 {
		t.Fatalf("resource code=%d: %s", code, errOut.String())
	}
	out.Reset()
	errOut.Reset()
	if code := runGenerate([]string{"controller", "test"}, &out, &errOut); code == 0 {
		t.Fatal("duplicate NewTestController registration was accepted")
	}
	if !strings.Contains(errOut.String(), "constructor NewTestController is already registered") || !strings.Contains(errOut.String(), "refusing duplicate registration") {
		t.Fatalf("unexpected duplicate error: %s", errOut.String())
	}
	if _, err := os.Stat(filepath.Join("src", "test_controller.go")); !os.IsNotExist(err) {
		t.Fatalf("rejected component left a generated file: %v", err)
	}
}

func TestGenerateRejectsDuplicateConstructorNameFromComponentThenResource(t *testing.T) {
	inTempDir(t)
	var out, errOut bytes.Buffer
	if code := runLegacyNew([]string{"todo", "--module", "example.com/todo"}, &out, &errOut); code != 0 {
		t.Fatalf("new code=%d: %s", code, errOut.String())
	}
	if err := os.Chdir("todo"); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errOut.Reset()
	if code := runGenerate([]string{"controller", "test"}, &out, &errOut); code != 0 {
		t.Fatalf("controller code=%d: %s", code, errOut.String())
	}
	out.Reset()
	errOut.Reset()
	if code := runGenerate([]string{"resource", "test"}, &out, &errOut); code == 0 {
		t.Fatal("resource with duplicate NewTestController registration was accepted")
	}
	if !strings.Contains(errOut.String(), "constructor NewTestController is already registered") || !strings.Contains(errOut.String(), "refusing duplicate registration") {
		t.Fatalf("unexpected duplicate error: %s", errOut.String())
	}
	if _, err := os.Stat(filepath.Join("src", "test")); !os.IsNotExist(err) {
		t.Fatalf("rejected resource left generated files: %v", err)
	}
}

func TestGenerateTypedVerticalSlice(t *testing.T) {
	inTempDir(t)
	var out, errOut bytes.Buffer
	if code := runLegacyNew([]string{"todo", "--module", "example.com/todo"}, &out, &errOut); code != 0 {
		t.Fatalf("new code=%d: %s", code, errOut.String())
	}
	if err := os.Chdir("todo"); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errOut.Reset()
	if code := runGenerate([]string{"slice", "order", "--storage", "memory", "--field", "name:string", "--field", "quantity:int"}, &out, &errOut); code != 0 {
		t.Fatalf("slice code=%d: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "development-only") {
		t.Fatalf("slice did not disclose memory persistence limit: %s", out.String())
	}
	paths := []string{
		filepath.Join("src", "order", "module.go"),
		filepath.Join("src", "order", "order_model.go"),
		filepath.Join("src", "order", "order_repository.go"),
		filepath.Join("src", "order", "order_service.go"),
		filepath.Join("src", "order", "order_controller.go"),
		filepath.Join("src", "order", "order_service_test.go"),
	}
	for _, path := range paths {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("missing generated slice file %s: %v", path, err)
		}
		if _, err := format.Source(contents); err != nil {
			t.Fatalf("invalid generated Go source %s: %v\n%s", path, err, contents)
		}
	}
	if unformatted, err := unformattedFiles("."); err != nil || len(unformatted) != 0 {
		t.Fatalf("vertical slice generator emitted unformatted Go files: %v, %v", unformatted, err)
	}
	model, err := os.ReadFile(filepath.Join("src", "order", "order_model.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`Name\s+string\s+`+"`json:\"name\"`").Match(model) || !regexp.MustCompile(`Quantity\s+int\s+`+"`json:\"quantity\"`").Match(model) {
		t.Fatalf("typed fields missing from generated model:\n%s", model)
	}
	contract, err := inspectApplication()
	if err != nil {
		t.Fatal(err)
	}
	if len(contract.HTTP) != 3 || len(contract.Resources) != 1 || contractHasErrors(contract) {
		t.Fatalf("generated slice contract is invalid: %#v", contract)
	}
	bindings := map[string][]handlerParameter{}
	for _, route := range contract.HTTP {
		bindings[route.Method+" "+route.Path] = route.Parameters
	}
	post := bindings["POST /order"]
	if len(post) != 1 || post[0].Binding != "body" || post[0].Type != "*CreateOrderRequest" {
		t.Fatalf("POST body signature missing from contract: %#v", post)
	}
	get := bindings["GET /order/:id"]
	if len(get) != 1 || get[0].Binding != "path" || get[0].Key != "id" || get[0].Type != "path.Int" {
		t.Fatalf("GET path signature missing from contract: %#v", get)
	}
	openAPI, err := buildOpenAPIDocument(contract)
	if err != nil {
		t.Fatal(err)
	}
	apiPaths, ok := openAPI["paths"].(map[string]any)
	if !ok {
		t.Fatalf("OpenAPI paths missing: %#v", openAPI)
	}
	orderPath, ok := apiPaths["/order/{id}"].(map[string]any)
	if !ok {
		t.Fatalf("converted path missing: %#v", apiPaths)
	}
	getOperation, ok := orderPath["get"].(map[string]any)
	if !ok || len(getOperation["parameters"].([]any)) != 1 {
		t.Fatalf("OpenAPI path parameter missing: %#v", orderPath)
	}
	createPath := apiPaths["/order"].(map[string]any)
	postOperation := createPath["post"].(map[string]any)
	if _, ok := postOperation["requestBody"]; !ok {
		t.Fatalf("OpenAPI request body missing: %#v", postOperation)
	}
	components := openAPI["components"].(map[string]any)
	schemas := components["schemas"].(map[string]any)
	requestSchema, ok := schemas["CreateOrderRequest"].(map[string]any)
	if !ok {
		t.Fatalf("request schema missing: %#v", schemas)
	}
	properties := requestSchema["properties"].(map[string]any)
	if _, ok := properties["name"]; !ok {
		t.Fatalf("name property missing: %#v", properties)
	}
	if _, ok := properties["quantity"]; !ok {
		t.Fatalf("quantity property missing: %#v", properties)
	}
}

func TestSliceRequiresExplicitStorageAndFields(t *testing.T) {
	inTempDir(t)
	var out, errOut bytes.Buffer
	if code := runGenerate([]string{"slice", "order", "--field", "name:string", "--no-register"}, &out, &errOut); code == 0 || !strings.Contains(errOut.String(), "--storage memory") {
		t.Fatalf("missing storage should fail explicitly: code=%d, stderr=%s", code, errOut.String())
	}
	if _, err := os.Stat(filepath.Join("src", "order")); !os.IsNotExist(err) {
		t.Fatalf("failed slice generation wrote partial files: %v", err)
	}
	out.Reset()
	errOut.Reset()
	if code := runGenerate([]string{"slice", "order", "--storage", "memory", "--no-register"}, &out, &errOut); code == 0 || !strings.Contains(errOut.String(), "at least one --field") {
		t.Fatalf("missing fields should fail explicitly: code=%d, stderr=%s", code, errOut.String())
	}
}

func TestGeneratedTemplatesAreValidGo(t *testing.T) {
	name, err := parseResourceName("blog-posts")
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{
		resourceRouteTemplate(name), repositoryTemplate(name), serviceTemplate(name), controllerTemplate(name),
		standaloneRepositoryTemplate(name, "example", nil), standaloneServiceTemplate(name, "example", nil), standaloneControllerTemplate(name, "example", nil),
	} {
		if _, err := format.Source([]byte(source)); err != nil {
			t.Fatalf("invalid generated source: %v", err)
		}
	}
}

func TestParseResourceName(t *testing.T) {
	got, err := parseResourceName("blog_posts")
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != "BlogPosts" || got.Package != "blog_posts" || got.Route != "blog-posts" {
		t.Fatalf("unexpected name: %#v", got)
	}
}

func TestGenerateResourceRegistersInCheckedCompositionRoot(t *testing.T) {
	inTempDir(t)
	if err := os.WriteFile("go.mod", []byte("module example.com/demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("spine.toml", []byte("module = \"example.com/demo\"\nentrypoint = \".\"\nport = 8080\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	main := `package main

import (
    framework "github.com/NARUBROWN/spine"
    config "github.com/NARUBROWN/spine/pkg/boot"
)

func BuildApplication() (framework.App, config.Options, error) {
    app := framework.New()
    return app, config.Options{}, nil
}

`
	if err := os.WriteFile("main.go", []byte(main), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if code := runGenerate([]string{"resource", "receipt", "--register"}, &out, &errOut); code != 0 {
		t.Fatalf("code=%d: %s", code, errOut.String())
	}
	got, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `receipt "example.com/demo/src/receipt"`) || !strings.Contains(string(got), "if err := receipt.Register(app); err != nil") || !strings.Contains(string(got), "return nil, config.Options{}, err") {
		t.Fatalf("resource was not registered:\n%s", got)
	}
	if code := runGenerate([]string{"resource", "receipt", "--register", "--force"}, &out, &errOut); code != 0 {
		t.Fatalf("idempotent registration failed: %s", errOut.String())
	}
	got, _ = os.ReadFile("main.go")
	if strings.Count(string(got), "receipt.Register(app)") != 1 {
		t.Fatal("resource registration was duplicated")
	}
}

func TestGenerateResourceRejectsUncheckedCompositionRootAtomically(t *testing.T) {
	inTempDir(t)
	if err := os.WriteFile("go.mod", []byte("module example.com/demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("spine.toml", []byte("module = \"example.com/demo\"\nentrypoint = \".\"\nport = 8080\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	main := `package main

import "github.com/NARUBROWN/spine"

func main() {
	app := spine.New()
	_ = app
}
`
	if err := os.WriteFile("main.go", []byte(main), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if code := runGenerate([]string{"resource", "receipt", "--register"}, &out, &errOut); code == 0 {
		t.Fatal("unchecked composition root was accepted")
	}
	if !strings.Contains(errOut.String(), "must return (spine.App, boot.Options, error)") {
		t.Fatalf("unexpected error: %s", errOut.String())
	}
	if _, err := os.Stat(filepath.Join("src", "receipt")); !os.IsNotExist(err) {
		t.Fatalf("failed registration left generated files behind: %v", err)
	}
}

func TestGenerateDryRunDoesNotWrite(t *testing.T) {
	inTempDir(t)
	var out, errOut bytes.Buffer
	if code := runGenerate([]string{"service", "billing", "--dry-run", "--json", "--no-register"}, &out, &errOut); code != 0 {
		t.Fatalf("code=%d: %s", code, errOut.String())
	}
	if _, err := os.Stat(filepath.Join("src", "billing_service.go")); !os.IsNotExist(err) {
		t.Fatalf("dry run created a file: %v", err)
	}
	var result struct {
		DryRun bool   `json:"dry_run"`
		Diff   string `json:"diff"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode dry-run JSON: %v; output: %s", err, out.String())
	}
	if !result.DryRun {
		t.Fatalf("expected dry_run=true: %s", out.String())
	}
	if !strings.HasPrefix(result.Diff, "--- /dev/null\n+++ src/billing_service.go\n") {
		t.Fatalf("unexpected portable dry-run diff: %q", result.Diff)
	}
}

func TestNewCreatesSpineTomlAndCurrentSpineVersion(t *testing.T) {
	inTempDir(t)
	var out, errOut bytes.Buffer
	if code := runLegacyNew([]string{"todo", "--module", "example.com/todo"}, &out, &errOut); code != 0 {
		t.Fatalf("code=%d: %s", code, errOut.String())
	}
	config, err := os.ReadFile(filepath.Join("todo", "spine.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(config), `module = "example.com/todo"`) || !strings.Contains(string(config), `composition = "./internal/application"`) || !strings.Contains(string(config), "port = 8080") {
		t.Fatalf("unexpected config: %s", config)
	}
	mod, err := os.ReadFile(filepath.Join("todo", "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mod), "github.com/NARUBROWN/spine "+platformSpineVersion) {
		t.Fatalf("unexpected go.mod: %s", mod)
	}
}

func TestNewCreatesReusableCompositionRoot(t *testing.T) {
	inTempDir(t)
	var out, errOut bytes.Buffer
	if code := runLegacyNew([]string{"todo", "--module", "example.com/todo"}, &out, &errOut); code != 0 {
		t.Fatalf("code=%d: %s", code, errOut.String())
	}
	mainSource, err := os.ReadFile(filepath.Join("todo", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	applicationSource, err := os.ReadFile(filepath.Join("todo", "internal", "application", "application.go"))
	if err != nil {
		t.Fatal(err)
	}
	applicationTest, err := os.ReadFile(filepath.Join("todo", "internal", "application", "application_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mainSource), `"example.com/todo/internal/application"`) || !strings.Contains(string(mainSource), "application.BuildApplication()") {
		t.Fatalf("main.go does not use the reusable composition root:\n%s", mainSource)
	}
	if !strings.Contains(string(applicationSource), "func BuildApplication() (spine.App, boot.Options, error)") {
		t.Fatalf("application.go does not expose BuildApplication:\n%s", applicationSource)
	}
	if !strings.Contains(string(applicationTest), "app.Validate(options)") {
		t.Fatalf("generated application test does not validate the offline contract:\n%s", applicationTest)
	}
}

func TestGeneratedResourceAndApplicationInspection(t *testing.T) {
	inTempDir(t)
	var out, errOut bytes.Buffer
	if code := runLegacyNew([]string{"todo", "--module", "example.com/todo"}, &out, &errOut); code != 0 {
		t.Fatalf("new code=%d: %s", code, errOut.String())
	}
	if err := os.Chdir("todo"); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errOut.Reset()
	if code := runGenerate([]string{"resource", "orders"}, &out, &errOut); code != 0 {
		t.Fatalf("generate code=%d: %s", code, errOut.String())
	}
	applicationSource, err := os.ReadFile(filepath.Join("internal", "application", "application.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(applicationSource), `orders "example.com/todo/src/orders"`) || !strings.Contains(string(applicationSource), "orders.Register(app)") {
		t.Fatalf("resource was not registered through the composition root:\n%s", applicationSource)
	}
	contract, err := inspectApplication()
	if err != nil {
		t.Fatal(err)
	}
	if contract.AnalysisMode != "static-source" || !contract.Complete || len(contract.Resources) != 1 || len(contract.HTTP) != 2 || len(contract.Constructors) != 4 {
		t.Fatalf("unexpected contract: %#v", contract)
	}
	dependencies := map[string][]string{}
	for _, constructor := range contract.Constructors {
		dependencies[constructor.Target] = constructor.Dependencies
	}
	if got := dependencies["NewOrdersService"]; len(got) != 1 || got[0] != "*OrdersRepository" {
		t.Fatalf("constructor dependency graph was not resolved: %#v", dependencies)
	}
	if contractHasErrors(contract) {
		t.Fatalf("generated application contract has errors: %#v", contract.Diagnostics)
	}
}

func TestInspectionScopesCompositionAndRejectsUncertainRegistrations(t *testing.T) {
	inTempDir(t)
	if err := os.WriteFile("go.mod", []byte("module example.com/app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("spine.toml", []byte("module = \"example.com/app\"\nentrypoint = \".\"\ncomposition = \".\"\nport = 8080\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	source := `package main

import "github.com/NARUBROWN/spine"

func BuildApplication() spine.App {
	app := spine.New()
	app.Route("GET", "/active", (*Controller).Active)
	if enabled {
		app.Route("GET", "/conditional", (*Controller).Conditional)
	}
	registerHelpers(app)
	deferred := func() {
		app.Route("GET", "/deferred", (*Controller).Deferred)
	}
	_ = deferred
	return app
}

func unrelated() {
	app := spine.New()
	app.Route("GET", "/unrelated", (*Controller).Unrelated)
}

func registerHelpers(app spine.App) {}

var enabled bool
type Controller struct{}
func (*Controller) Active() {}
func (*Controller) Conditional() {}
func (*Controller) Deferred() {}
func (*Controller) Unrelated() {}
`
	if err := os.WriteFile("application.go", []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	contract, err := inspectApplication()
	if err != nil {
		t.Fatal(err)
	}
	if contract.Complete {
		t.Fatalf("uncertain composition was marked complete: %#v", contract.Diagnostics)
	}
	paths := map[string]bool{}
	for _, route := range contract.HTTP {
		paths[route.Path] = true
	}
	if !paths["/active"] || !paths["/conditional"] || paths["/deferred"] || paths["/unrelated"] {
		t.Fatalf("inspection escaped the executed composition scope: %#v", contract.HTTP)
	}
	codes := map[string]bool{}
	for _, diagnostic := range contract.Diagnostics {
		codes[diagnostic.Code] = true
	}
	for _, code := range []string{"REGISTRATION_CONDITIONAL", "REGISTRATION_HELPER_UNRESOLVED", "DEFERRED_REGISTRATION_UNRESOLVED"} {
		if !codes[code] {
			t.Fatalf("missing %s diagnostic: %#v", code, contract.Diagnostics)
		}
	}
}

func TestDynamicRegistrationsFailStaticCompleteness(t *testing.T) {
	inTempDir(t)
	if err := os.WriteFile("go.mod", []byte("module example.com/app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("spine.toml", []byte("module = \"example.com/app\"\nentrypoint = \".\"\ncomposition = \".\"\nport = 8080\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	source := `package main
import "github.com/NARUBROWN/spine"
func BuildApplication() spine.App {
	app := spine.New()
	method, path, topic := "GET", "/dynamic", "event.dynamic"
	app.Route(method, path, (*Controller).Get)
	_ = app.Consumers().Register(topic, (*Controller).Consume)
	return app
}
type Controller struct{}
func (*Controller) Get() {}
func (*Controller) Consume() error { return nil }
`
	if err := os.WriteFile("application.go", []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	contract, err := inspectApplication()
	if err != nil {
		t.Fatal(err)
	}
	codes := map[string]string{}
	for _, diagnostic := range contract.Diagnostics {
		codes[diagnostic.Code] = diagnostic.Severity
	}
	if codes["HTTP_ROUTE_DYNAMIC"] != "error" || codes["CONSUMER_REGISTRATION_DYNAMIC"] != "error" || contract.Complete {
		t.Fatalf("dynamic registrations did not fail completeness: %#v", contract.Diagnostics)
	}
	var out, errOut bytes.Buffer
	if code := runContract([]string{"check", "--json"}, &out, &errOut); code == 0 {
		t.Fatalf("structured check accepted an incomplete contract: %s", out.String())
	}
	out.Reset()
	errOut.Reset()
	if code := runContract([]string{"snapshot"}, &out, &errOut); code == 0 || !strings.Contains(errOut.String(), "error diagnostics") {
		t.Fatalf("snapshot accepted an incomplete contract: code=%d, stderr=%s", code, errOut.String())
	}
	out.Reset()
	errOut.Reset()
	if code := runContract([]string{"export", "--format", "openapi", "--output", "openapi.json"}, &out, &errOut); code == 0 || !strings.Contains(errOut.String(), "error diagnostics") {
		t.Fatalf("export accepted an incomplete contract: code=%d, stderr=%s", code, errOut.String())
	}
	if _, err := os.Stat("openapi.json"); !os.IsNotExist(err) {
		t.Fatalf("failed export left an artifact: %v", err)
	}
}

func TestContractCheckProvidesStructuredPolicyResult(t *testing.T) {
	valid := applicationContract{SchemaVersion: contractSchemaVersion, AnalysisMode: "static-source", Complete: true, Diagnostics: []contractDiagnostic{}}
	report := buildContractCheckReport(valid, false)
	if !report.Valid || !report.AnalysisComplete || report.Diagnostics == nil {
		t.Fatalf("valid structured check report is unstable: %#v", report)
	}
	warning := valid
	warning.Diagnostics = []contractDiagnostic{{Severity: "warning", Code: "SECURITY_WARNING", Message: "review required"}}
	if report := buildContractCheckReport(warning, false); !report.Valid || report.WarningCount != 1 {
		t.Fatalf("warning policy unexpectedly failed: %#v", report)
	}
	if report := buildContractCheckReport(warning, true); report.Valid || report.WarningCount != 1 {
		t.Fatalf("warnings-as-errors policy did not fail: %#v", report)
	}
}

func TestContractCheckCommandEmitsMachineReadableEvidence(t *testing.T) {
	inTempDir(t)
	var out, errOut bytes.Buffer
	if code := runLegacyNew([]string{"todo", "--module", "example.com/todo"}, &out, &errOut); code != 0 {
		t.Fatalf("new code=%d: %s", code, errOut.String())
	}
	if err := os.Chdir("todo"); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errOut.Reset()
	if code := runContract([]string{"check", "--json"}, &out, &errOut); code != 0 {
		t.Fatalf("check code=%d: %s", code, errOut.String())
	}
	var report contractCheckReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if !report.Valid || !report.AnalysisComplete || report.SchemaVersion != contractCheckSchemaVersion || len(report.EvidenceBoundary) < 3 || report.Diagnostics == nil {
		t.Fatalf("unexpected contract check evidence: %#v", report)
	}
	if !strings.Contains(strings.Join(report.EvidenceBoundary, "\n"), "os.Getenv") {
		t.Fatalf("environment input boundary is missing: %#v", report.EvidenceBoundary)
	}
}

func TestApplyFileChangesPreflightPreventsPartialWrites(t *testing.T) {
	inTempDir(t)
	if err := os.WriteFile("blocked.txt", []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := applyFileChanges([]fileChange{
		{Path: "created.txt", Contents: "new"},
		{Path: "blocked.txt", Contents: "replacement"},
	}, false)
	if err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("expected overwrite preflight error, got %v", err)
	}
	if _, err := os.Stat("created.txt"); !os.IsNotExist(err) {
		t.Fatalf("preflight failure left a partial file: %v", err)
	}
	contents, err := os.ReadFile("blocked.txt")
	if err != nil || string(contents) != "original" {
		t.Fatalf("existing file changed after preflight failure: %q, %v", contents, err)
	}
}

func TestGenerationLockPreventsConcurrentCompositionWrites(t *testing.T) {
	inTempDir(t)
	first, err := acquireProjectLock(generationLockPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := acquireProjectLock(generationLockPath); err == nil || !strings.Contains(err.Error(), "another Spine generation") {
		t.Fatalf("concurrent generation lock was accepted: %v", err)
	}
	first.release()
	second, err := acquireProjectLock(generationLockPath)
	if err != nil {
		t.Fatalf("released generation lock could not be reacquired: %v", err)
	}
	second.release()
}

func TestGenerationLockRecoversOnlyStaleRegularFile(t *testing.T) {
	inTempDir(t)
	if err := os.WriteFile(generationLockPath, []byte("abandoned"), 0o600); err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-generationLockTTL - time.Minute)
	if err := os.Chtimes(generationLockPath, stale, stale); err != nil {
		t.Fatal(err)
	}
	lock, err := acquireProjectLock(generationLockPath)
	if err != nil {
		t.Fatalf("stale generation lock was not recovered: %v", err)
	}
	lock.release()
	if err := os.Mkdir(generationLockPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := acquireProjectLock(generationLockPath); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("non-regular generation lock was removed or accepted: %v", err)
	}
}

func TestGenerationLockDoesNotReleaseReplacementOwner(t *testing.T) {
	inTempDir(t)
	lock, err := acquireProjectLock(generationLockPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(generationLockPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(generationLockPath, []byte("replacement-owner\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	lock.release()
	contents, err := os.ReadFile(generationLockPath)
	if err != nil || string(contents) != "replacement-owner\n" {
		t.Fatalf("previous owner removed a replacement generation lock: %q, %v", contents, err)
	}
}

func TestInspectFindsConsumersAndWebSockets(t *testing.T) {
	inTempDir(t)
	if err := os.WriteFile("go.mod", []byte("module example.com/app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("spine.toml", []byte("module = \"example.com/app\"\nentrypoint = \".\"\ncomposition = \".\"\nport = 8080\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	source := `package main

import (
	"context"

	"github.com/NARUBROWN/spine"
	"github.com/NARUBROWN/spine/pkg/boot"
	"github.com/NARUBROWN/spine/pkg/route"
	"github.com/NARUBROWN/spine/pkg/ws"
)

func BuildApplication() spine.App {
	app := spine.New()
	_ = boot.Options{
		Kafka: &boot.KafkaOptions{AllowInsecureTransport: true},
		HTTP: &boot.HTTPOptions{WebSocket: boot.WebSocketOptions{AllowedOrigins: []string{"*"}}},
	}
	app.Route("GET", "/orders", (*Socket).HTTP, route.WithInterceptors(routeAuth))
	app.InterceptorFor(boot.InterceptorWebSocket, wsAuth)
	_ = app.Consumers().Register("order.created", (*Consumer).Handle)
	_ = app.WebSocket().Register("/ws/orders", (*Socket).Handle)
	return app
}

type Consumer struct{}
type Socket struct{}
type Message struct {
	Value string ` + "`json:\"value\"`" + `
}

func (c *Consumer) Handle(ctx context.Context, eventName string, message Message) error { return nil }
func (s *Socket) Handle(ctx context.Context, id ws.ConnectionID, message Message) error { return nil }
func (s *Socket) HTTP() {}
`
	if err := os.WriteFile("application.go", []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	contract, err := inspectApplication()
	if err != nil {
		t.Fatal(err)
	}
	if len(contract.Consumers) != 1 || contract.Consumers[0].Name != "order.created" {
		t.Fatalf("consumer registration missing: %#v", contract.Consumers)
	}
	if len(contract.WebSockets) != 1 || contract.WebSockets[0].Name != "/ws/orders" {
		t.Fatalf("WebSocket registration missing: %#v", contract.WebSockets)
	}
	if len(contract.HTTP) != 1 || len(contract.HTTP[0].Interceptors) != 1 || contract.HTTP[0].Interceptors[0] != "routeAuth" {
		t.Fatalf("route interceptor missing: %#v", contract.HTTP)
	}
	if len(contract.Interceptors) != 1 || contract.Interceptors[0].Scope != "boot.InterceptorWebSocket" || contract.Interceptors[0].Target != "wsAuth" {
		t.Fatalf("scoped interceptor missing: %#v", contract.Interceptors)
	}
	diagnosticCodes := map[string]bool{}
	for _, diagnostic := range contract.Diagnostics {
		diagnosticCodes[diagnostic.Code] = true
	}
	if !diagnosticCodes["INSECURE_TRANSPORT_ENABLED"] || !diagnosticCodes["WEBSOCKET_WILDCARD_ORIGIN"] {
		t.Fatalf("security configuration diagnostics missing: %#v", contract.Diagnostics)
	}
	if len(contract.Configuration) < 5 {
		t.Fatalf("boot configuration was not expanded into the contract: %#v", contract.Configuration)
	}
	if len(contract.Consumers[0].Parameters) != 3 || contract.Consumers[0].Parameters[2].Binding != "payload" {
		t.Fatalf("consumer payload signature missing: %#v", contract.Consumers[0])
	}
	if len(contract.WebSockets[0].Parameters) != 3 || contract.WebSockets[0].Parameters[1].Binding != "connection" || contract.WebSockets[0].Parameters[2].Binding != "payload" {
		t.Fatalf("WebSocket payload signature missing: %#v", contract.WebSockets[0])
	}
	asyncAPI, err := buildAsyncAPIDocument(contract)
	if err != nil {
		t.Fatal(err)
	}
	channels := asyncAPI["channels"].(map[string]any)
	operations := asyncAPI["operations"].(map[string]any)
	if len(channels) != 2 || len(operations) != 2 {
		t.Fatalf("AsyncAPI registrations missing: channels=%#v operations=%#v", channels, operations)
	}
	var consumerChannel map[string]any
	for _, channel := range channels {
		candidate := channel.(map[string]any)
		if candidate["address"] == "order.created" {
			consumerChannel = candidate
		}
	}
	if consumerChannel == nil {
		t.Fatalf("consumer channel missing: %#v", channels)
	}
	components := asyncAPI["components"].(map[string]any)
	schemas := components["schemas"].(map[string]any)
	if _, ok := schemas["Message"]; !ok {
		t.Fatalf("AsyncAPI payload schema missing: %#v", schemas)
	}
}

func TestContractSnapshotDiffAndExplain(t *testing.T) {
	inTempDir(t)
	var out, errOut bytes.Buffer
	if code := runLegacyNew([]string{"todo", "--module", "example.com/todo"}, &out, &errOut); code != 0 {
		t.Fatalf("new code=%d: %s", code, errOut.String())
	}
	if err := os.Chdir("todo"); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errOut.Reset()
	if code := runGenerate([]string{"resource", "orders"}, &out, &errOut); code != 0 {
		t.Fatalf("generate code=%d: %s", code, errOut.String())
	}
	out.Reset()
	errOut.Reset()
	if code := runContract([]string{"snapshot"}, &out, &errOut); code != 0 {
		t.Fatalf("snapshot code=%d: %s", code, errOut.String())
	}
	if _, err := os.Stat(defaultContractSnapshot); err != nil {
		t.Fatalf("contract snapshot was not created: %v", err)
	}
	out.Reset()
	errOut.Reset()
	if code := runContract([]string{"diff"}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "matches") {
		t.Fatalf("unchanged diff code=%d, stdout=%s, stderr=%s", code, out.String(), errOut.String())
	}
	out.Reset()
	errOut.Reset()
	if code := runExplain([]string{"http", "GET", "/orders/:id"}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "BeforeResponse") || !strings.Contains(out.String(), "static-source") {
		t.Fatalf("explain code=%d, stdout=%s, stderr=%s", code, out.String(), errOut.String())
	}
	modulePath := filepath.Join("src", "orders", "orders_route.go")
	contents, err := os.ReadFile(modulePath)
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(string(contents), "/orders/:id", "/orders/:orderId", 1)
	if err := os.WriteFile(modulePath, []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errOut.Reset()
	if code := runContract([]string{"diff"}, &out, &errOut); code == 0 || !strings.Contains(errOut.String(), "- http: GET /orders/:id") || !strings.Contains(errOut.String(), "+ http: GET /orders/:orderId") {
		t.Fatalf("changed diff code=%d, stdout=%s, stderr=%s", code, out.String(), errOut.String())
	}
}

func TestDoctorReportsOfflineEvidenceBoundary(t *testing.T) {
	inTempDir(t)
	var out, errOut bytes.Buffer
	if code := runLegacyNew([]string{"todo", "--module", "example.com/todo"}, &out, &errOut); code != 0 {
		t.Fatalf("new code=%d: %s", code, errOut.String())
	}
	if err := os.Chdir("todo"); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errOut.Reset()
	if code := runDoctor([]string{"--json"}, &out, &errOut); code != 0 {
		t.Fatalf("doctor code=%d: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), `"scope": "offline"`) || !strings.Contains(out.String(), `"ok": true`) || !strings.Contains(out.String(), "external databases") {
		t.Fatalf("doctor did not report its evidence boundary: %s", out.String())
	}
	out.Reset()
	errOut.Reset()
	if code := runDoctor([]string{"--connect"}, &out, &errOut); code == 0 || !strings.Contains(errOut.String(), "not implemented") {
		t.Fatalf("doctor --connect must fail explicitly: code=%d, stderr=%s", code, errOut.String())
	}
}

func TestInspectRejectsAmbiguousRoutes(t *testing.T) {
	inTempDir(t)
	if err := os.WriteFile("go.mod", []byte("module example.com/app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("spine.toml", []byte("module = \"example.com/app\"\nentrypoint = \".\"\ncomposition = \".\"\nport = 8080\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	source := `package main
import "github.com/NARUBROWN/spine"
func BuildApplication() spine.App {
	app := spine.New()
	app.Route("GET", "/users/:id", (*Controller).ByID)
	app.Route("GET", "/users/me", (*Controller).Me)
	return app
}
type Controller struct{}
`
	if err := os.WriteFile("application.go", []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	contract, err := inspectApplication()
	if err != nil {
		t.Fatal(err)
	}
	if !contractHasErrors(contract) {
		t.Fatalf("ambiguous route was not rejected: %#v", contract.Diagnostics)
	}
	found := false
	for _, diagnostic := range contract.Diagnostics {
		found = found || diagnostic.Code == "HTTP_ROUTE_AMBIGUOUS"
	}
	if !found {
		t.Fatalf("missing HTTP_ROUTE_AMBIGUOUS diagnostic: %#v", contract.Diagnostics)
	}
}

func TestSourceStateWatchesGoModuleAndProjectConfig(t *testing.T) {
	inTempDir(t)
	if err := os.WriteFile("main.go", []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeE2EFile(t, "go.mod", "module example.com/app\n\ngo 1.25\n")
	before, err := sourceState(".")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("spine.toml", []byte("module = \"example.com/app\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	afterConfig, err := sourceState(".")
	if err != nil {
		t.Fatal(err)
	}
	if before == afterConfig {
		t.Fatal("adding spine.toml did not change the development source state")
	}
	if err := os.WriteFile("go.mod", []byte("module example.com/app\n\ngo 1.25\n// module changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	afterModule, err := sourceState(".")
	if err != nil {
		t.Fatal(err)
	}
	if afterConfig == afterModule {
		t.Fatal("changing go.mod did not change the development source state")
	}
}

func TestBuildDevBinaryBuildsConfiguredEntrypoint(t *testing.T) {
	inTempDir(t)
	if err := os.WriteFile("go.mod", []byte("module example.com/devapp\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("main.go", []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var errOut bytes.Buffer
	binary, directory, err := buildDevBinary(projectConfig{Entrypoint: "."}, &errOut)
	if err != nil {
		t.Fatalf("build failed: %v, stderr=%s", err, errOut.String())
	}
	defer os.RemoveAll(directory)
	if info, err := os.Stat(binary); err != nil || info.IsDir() {
		t.Fatalf("development binary missing: %v", err)
	}
}

func TestCachedDevBinaryReusesMatchingBuild(t *testing.T) {
	inTempDir(t)
	if err := os.WriteFile("go.mod", []byte("module example.com/devcache\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("main.go", []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	state, err := sourceState(".")
	if err != nil {
		t.Fatal(err)
	}
	config := projectConfig{Entrypoint: "."}
	var errOut bytes.Buffer
	cacheRoot := filepath.Join(t.TempDir(), "cache")
	first, firstDir, cached, err := cachedDevBinaryAt(cacheRoot, "project-a", config, state, &errOut)
	if err != nil || cached {
		t.Fatalf("first cached build = %q, cached=%v, err=%v, stderr=%s", first, cached, err, errOut.String())
	}
	second, secondDir, cached, err := cachedDevBinaryAt(cacheRoot, "project-a", config, state, &errOut)
	if err != nil || !cached {
		t.Fatalf("second cached build = %q, cached=%v, err=%v, stderr=%s", second, cached, err, errOut.String())
	}
	if first != second || firstDir != secondDir {
		t.Fatalf("cache path changed: first=%q second=%q", first, second)
	}
	if info, err := os.Stat(second); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("cached development binary missing: %v", err)
	}
}

func TestDevCacheKeyChangesWithBuildIdentity(t *testing.T) {
	config := projectConfig{Entrypoint: "."}
	base := devCacheKey(config, "source-a", "project-a")
	if base == devCacheKey(config, "source-b", "project-a") {
		t.Fatal("source change did not invalidate development cache")
	}
	if base == devCacheKey(projectConfig{Entrypoint: "./cmd/api"}, "source-a", "project-a") {
		t.Fatal("entrypoint change did not invalidate development cache")
	}
	if base == devCacheKey(config, "source-a", "project-b") {
		t.Fatal("project root change did not isolate development cache")
	}
}

func TestDevBinaryPathUsesExecutableExtensionOnlyOnWindows(t *testing.T) {
	if got := devBinaryPath("temporary", "windows"); got != filepath.Join("temporary", "app.exe") {
		t.Fatalf("windows dev binary = %q", got)
	}
	if got := devBinaryPath("temporary", "linux"); got != filepath.Join("temporary", "app") {
		t.Fatalf("linux dev binary = %q", got)
	}
}

func TestDevTerminationUsesImmediateKillOnlyOnWindows(t *testing.T) {
	if !devTerminationUsesKill("windows") {
		t.Fatal("Windows development process should use immediate kill")
	}
	if devTerminationUsesKill("linux") {
		t.Fatal("Linux development process should retain graceful interrupt")
	}
}

func TestRunBuildUsesConfiguredEntrypointAndCreatesBin(t *testing.T) {
	inTempDir(t)
	if err := os.MkdirAll(filepath.Join("cmd", "api"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("go.mod", []byte("module example.com/my-api\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("spine.toml", []byte("module = \"example.com/my-api\"\nentrypoint = \"./cmd/api\"\ncomposition = \"./cmd/api\"\nport = 8080\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("main.go", []byte("this root package must not be built\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("cmd", "api", "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if code := runBuild(&out, &errOut); code != 0 {
		t.Fatalf("build code=%d: %s", code, errOut.String())
	}
	if info, err := os.Stat(filepath.Join("bin", "my-api")); err != nil || info.IsDir() {
		t.Fatalf("configured entrypoint binary missing: %v", err)
	}
}

func TestLoadProjectConfig(t *testing.T) {
	inTempDir(t)
	if err := os.WriteFile("spine.toml", []byte("module = \"example.com/app\"\nentrypoint = \"./cmd/api\"\nport = 3000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	config, err := loadProjectConfig()
	if err != nil || config.Module != "example.com/app" || config.Entrypoint != "./cmd/api" || config.Port != 3000 {
		t.Fatalf("config = %#v, %v", config, err)
	}
}

func TestCompletionAndProjectName(t *testing.T) {
	inTempDir(t)
	if err := os.WriteFile("go.mod", []byte("module example.com/my-api\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	name, err := projectName()
	if err != nil || name != "my-api" {
		t.Fatalf("projectName = %q, %v", name, err)
	}
	var out, errOut bytes.Buffer
	if code := runCompletion([]string{"powershell"}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "Register-ArgumentCompleter") {
		t.Fatalf("completion = %d, %s", code, errOut.String())
	}
}

func TestRunRejectsJSONForLongRunningUnsupportedCommand(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Run([]string{"run", "--json"}, &out, &errOut); code != 2 {
		t.Fatalf("run --json code=%d, stdout=%s, stderr=%s", code, out.String(), errOut.String())
	}
	var result protocolResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode protocol result: %v\n%s", err, out.String())
	}
	if result.Error == nil || result.Error.Code != "USAGE_ERROR" || result.ExitCode != 2 {
		t.Fatalf("unsupported JSON mode was not a usage error: %#v", result)
	}
}

func TestProtocolFailureClassificationIsInternallyConsistent(t *testing.T) {
	var out, errOut bytes.Buffer
	code := writeProtocolResult([]string{"verify", "--json"}, 1, []byte(`{"valid":false}`), "local Go verification failed", &out, &errOut)
	if code != 5 {
		t.Fatalf("verify failure code=%d, output=%s", code, out.String())
	}
	var result protocolResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Error == nil || result.Error.Code != "VALIDATION_FAILED" || result.ExitCode != 5 {
		t.Fatalf("inconsistent validation envelope: %#v", result)
	}
	if got := protocolExitCode([]string{"run"}, 1, "application failed", []byte("startup log")); got != 3 {
		t.Fatalf("ordinary stdout changed runtime failure classification: %d", got)
	}
}

func TestInspectionDiagnosesMalformedRouteWithoutPanicking(t *testing.T) {
	inTempDir(t)
	var out, errOut bytes.Buffer
	if code := runLegacyNew([]string{"todo", "--module", "example.com/todo"}, &out, &errOut); code != 0 {
		t.Fatalf("new code=%d: %s", code, errOut.String())
	}
	if err := os.Chdir("todo"); err != nil {
		t.Fatal(err)
	}
	source := `package application

import (
	"github.com/NARUBROWN/spine"
	"github.com/NARUBROWN/spine/pkg/boot"
)

func BuildApplication() (spine.App, boot.Options, error) {
	app := spine.New()
	app.Route("GET")
	return app, boot.Options{}, nil
}
`
	if err := os.WriteFile(filepath.Join("internal", "application", "application.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	contract, err := inspectApplication()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, diagnostic := range contract.Diagnostics {
		if diagnostic.Code == "HTTP_ROUTE_ARGUMENTS_INVALID" {
			found = true
		}
	}
	if !found {
		t.Fatalf("malformed route diagnostic missing: %#v", contract.Diagnostics)
	}
}

func TestSourceStateHashesEmbeddedAssetsAndContents(t *testing.T) {
	directory := t.TempDir()
	mainPath := filepath.Join(directory, "main.go")
	assetPath := filepath.Join(directory, "message.txt")
	if err := os.WriteFile(mainPath, []byte("package main\nimport _ \"embed\"\n//go:embed message.txt\nvar message string\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(assetPath, []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeE2EFile(t, filepath.Join(directory, "go.mod"), "module example.com/embed\n\ngo 1.25\n")
	before, err := sourceState(directory)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(assetPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(assetPath, []byte("other"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(assetPath, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	after, err := sourceState(directory)
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("same-size embedded asset change with preserved mtime did not invalidate source state")
	}
}

func TestGenerateControllerInsideRoutePackageAvoidsSelfImport(t *testing.T) {
	inTempDir(t)
	var out, errOut bytes.Buffer
	if code := runLegacyNew([]string{"todo", "--module", "example.com/todo"}, &out, &errOut); code != 0 {
		t.Fatalf("new code=%d: %s", code, errOut.String())
	}
	if err := os.MkdirAll(filepath.Join("todo", "src", "route"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(filepath.Join("todo", "src", "route")); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errOut.Reset()
	if code := runGenerate([]string{"controller", "user"}, &out, &errOut); code != 0 {
		t.Fatalf("controller code=%d: %s", code, errOut.String())
	}
	route, err := os.ReadFile("route.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(route), `"example.com/todo/src/route"`) || !strings.Contains(string(route), `(*UserController).Get`) {
		t.Fatalf("route package imported itself:\n%s", route)
	}
}

func TestGenerateControllerRejectsInvalidRouteRegisterSignature(t *testing.T) {
	inTempDir(t)
	var out, errOut bytes.Buffer
	if code := runLegacyNew([]string{"todo", "--module", "example.com/todo"}, &out, &errOut); code != 0 {
		t.Fatalf("new code=%d: %s", code, errOut.String())
	}
	if err := os.Chdir("todo"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join("src", "route"), 0o755); err != nil {
		t.Fatal(err)
	}
	invalid := "package route\n\nfunc Register(value int) {}\n"
	if err := os.WriteFile(filepath.Join("src", "route", "route.go"), []byte(invalid), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errOut.Reset()
	if code := runGenerate([]string{"controller", "user"}, &out, &errOut); code == 0 || !strings.Contains(errOut.String(), "Register(app spine.App) error") {
		t.Fatalf("invalid Register signature accepted: code=%d, stderr=%s", code, errOut.String())
	}
	if _, err := os.Stat(filepath.Join("src", "user_controller.go")); !os.IsNotExist(err) {
		t.Fatalf("failed generation left controller behind: %v", err)
	}
}

func TestGenerateControllerRejectsExistingRouteConflict(t *testing.T) {
	inTempDir(t)
	var out, errOut bytes.Buffer
	if code := runLegacyNew([]string{"todo", "--module", "example.com/todo"}, &out, &errOut); code != 0 {
		t.Fatalf("new code=%d: %s", code, errOut.String())
	}
	if err := os.Chdir("todo"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join("src", "route"), 0o755); err != nil {
		t.Fatal(err)
	}
	conflict := `package route

import "github.com/NARUBROWN/spine"

type ExistingController struct{}

func Register(app spine.App) error {
	app.Route("GET", "/user/:id", (*ExistingController).Get)
	return nil
}
`
	if err := os.WriteFile(filepath.Join("src", "route", "route.go"), []byte(conflict), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errOut.Reset()
	if code := runGenerate([]string{"controller", "user"}, &out, &errOut); code == 0 || !strings.Contains(errOut.String(), "already registered with a different handler") {
		t.Fatalf("route conflict accepted: code=%d, stderr=%s", code, errOut.String())
	}
}

func TestComponentRegistrationChecksOnlyPrimaryCompositionFunction(t *testing.T) {
	inTempDir(t)
	var out, errOut bytes.Buffer
	if code := runLegacyNew([]string{"todo", "--module", "example.com/todo"}, &out, &errOut); code != 0 {
		t.Fatalf("new code=%d: %s", code, errOut.String())
	}
	if err := os.Chdir("todo"); err != nil {
		t.Fatal(err)
	}
	source := `package application

import (
	route "example.com/todo/src/route"
	src "example.com/todo/src"

	"github.com/NARUBROWN/spine"
	"github.com/NARUBROWN/spine/pkg/boot"
)

func wireAlternate(app spine.App) {
	app.Constructor(src.NewUserService)
	_ = route.Register(app)
}

func BuildApplication() (spine.App, boot.Options, error) {
	app := spine.New()
	return app, boot.Options{}, nil
}
`
	applicationPath := filepath.Join("internal", "application", "application.go")
	if err := os.WriteFile(applicationPath, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errOut.Reset()
	if code := runGenerate([]string{"service", "user"}, &out, &errOut); code != 0 {
		t.Fatalf("service code=%d: %s", code, errOut.String())
	}
	application, err := os.ReadFile(applicationPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(application), "app.Constructor(src.NewUserService)") != 2 {
		t.Fatalf("primary builder registration was suppressed by helper:\n%s", application)
	}
	out.Reset()
	errOut.Reset()
	if code := runGenerate([]string{"controller", "widget"}, &out, &errOut); code != 0 {
		t.Fatalf("controller code=%d: %s", code, errOut.String())
	}
	application, err = os.ReadFile(applicationPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(application), "route.Register(app)") != 2 {
		t.Fatalf("primary route registration was suppressed by helper:\n%s", application)
	}
}

func TestDependenciesRejectTestFilesWrongPackagesAndDuplicates(t *testing.T) {
	inTempDir(t)
	var out, errOut bytes.Buffer
	if code := runLegacyNew([]string{"todo", "--module", "example.com/todo"}, &out, &errOut); code != 0 {
		t.Fatalf("new code=%d: %s", code, errOut.String())
	}
	if err := os.Chdir("todo"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll("src", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("src", "mock_service_test.go"), []byte("package src\ntype MockService struct{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := runGenerate([]string{"controller", "test-only", "--depends", "mock_service_test"}, &out, &errOut); code == 0 || !strings.Contains(errOut.String(), "_test.go") {
		t.Fatalf("test-only dependency accepted: code=%d, stderr=%s", code, errOut.String())
	}
	if err := os.WriteFile(filepath.Join("src", "aaa.go"), []byte("package src\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("src", "wrong_service.go"), []byte("package other\ntype WrongService struct{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := findLocalComponent("src", "wrong_service"); err == nil || !strings.Contains(err.Error(), "expected src") {
		t.Fatalf("wrong-package dependency accepted: %v", err)
	}
	out.Reset()
	errOut.Reset()
	if code := runGenerate([]string{"service", "user"}, &out, &errOut); code != 0 {
		t.Fatalf("service code=%d: %s", code, errOut.String())
	}
	out.Reset()
	errOut.Reset()
	if code := runGenerate([]string{"controller", "duplicate", "--depends", "user_service", "user_service"}, &out, &errOut); code == 0 || !strings.Contains(errOut.String(), "duplicate dependency") {
		t.Fatalf("duplicate dependency accepted: code=%d, stderr=%s", code, errOut.String())
	}
}

func inTempDir(t *testing.T) {
	t.Helper()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
}

// runLegacyNew는 기존 구조의 호환성을 검사할 때 사용하는 프로젝트 준비 함수입니다.
func runLegacyNew(args []string, out, errOut io.Writer) int {
	if err := prepareTestModuleCache(); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	return runNew(append(args, "--layout", "legacy", "--skip-install"), out, errOut)
}
