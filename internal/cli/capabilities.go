package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
)

const capabilitySchemaVersion = "spine.cli-capabilities/v1alpha1"

type capabilityReport struct {
	SchemaVersion string                 `json:"schema_version"`
	CLIVersion    string                 `json:"cli_version"`
	Capabilities  []capabilityDefinition `json:"capabilities"`
}

type capabilityDefinition struct {
	Name        string `json:"name"`
	Implemented bool   `json:"implemented"`
	Stability   string `json:"stability"`
	Evidence    string `json:"evidence,omitempty"`
	Limit       string `json:"limit,omitempty"`
}

func currentCapabilities() capabilityReport {
	capabilities := []capabilityDefinition{
		{Name: "generation.project", Implemented: true, Stability: "alpha", Evidence: "spine new with reusable composition root and validation test"},
		{Name: "generation.resource.http-get", Implemented: true, Stability: "alpha", Evidence: "resource generator with AST registration"},
		{Name: "generation.controller", Implemented: true, Stability: "alpha"},
		{Name: "generation.service", Implemented: true, Stability: "alpha"},
		{Name: "generation.repository", Implemented: true, Stability: "alpha", Limit: "persistence-neutral skeleton only"},
		{Name: "generation.vertical-slice", Implemented: true, Stability: "alpha", Evidence: "typed model, DTO, repository contract, memory adapter, service, HTTP controller, and service test", Limit: "requires explicit development-only --storage memory; Bun, transaction, and outbox recipes are not implemented"},
		{Name: "generation.consumer", Implemented: true, Stability: "alpha", Evidence: "typed payload, handler, checked composition registration, and AsyncAPI-visible contract", Limit: "transport, delivery, retry, and dead-letter policy remain application infrastructure decisions"},
		{Name: "generation.websocket", Implemented: true, Stability: "alpha", Evidence: "typed message, handler, checked composition registration, and AsyncAPI-visible contract", Limit: "authentication, origin policy, capacity, and response behavior must be selected by the application"},
		{Name: "generation.kafka", Implemented: false, Stability: "planned"},
		{Name: "generation.rabbitmq", Implemented: false, Stability: "planned"},
		{Name: "generation.bun", Implemented: false, Stability: "planned"},
		{Name: "contract.static-inspection", Implemented: true, Stability: "alpha", Evidence: "HTTP, DI, interceptors, consumers, WebSockets, resources, boot options"},
		{Name: "contract.explain", Implemented: true, Stability: "alpha", Limit: "static-source evidence only"},
		{Name: "contract.snapshot-diff", Implemented: true, Stability: "alpha", Evidence: "semantic drift ignores source line movement"},
		{Name: "contract.structured-check", Implemented: true, Stability: "alpha", Evidence: "machine-readable completeness, diagnostics, evidence boundaries, and warnings-as-errors policy"},
		{Name: "contract.openapi-export", Implemented: true, Stability: "alpha", Evidence: "HTTP paths, handler signatures, path parameters, DTO schemas, and response body types", Limit: "runtime-selected response status and dynamic registration remain explicit x-spine evidence"},
		{Name: "contract.asyncapi-export", Implemented: true, Stability: "alpha", Evidence: "AsyncAPI 3.1 channels and receive operations for consumer and WebSocket registrations", Limit: "broker protocol and delivery guarantees require runtime infrastructure evidence"},
		{Name: "contract.runtime-manifest", Implemented: false, Stability: "future", Limit: "intentionally outside the current no-core CLI scope; retained as a future optional Spine TODO"},
		{Name: "contract.runtime-probe-partial", Implemented: true, Stability: "experimental", Evidence: "executes BuildApplication, runs app.Validate, and compares public consumer/WebSocket registries with the static contract", Limit: "HTTP routes, DI constructors, and interceptors require a Spine framework manifest API"},
		{Name: "verification.local", Implemented: true, Stability: "alpha", Evidence: "contract, gofmt, go vet, go test"},
		{Name: "doctor.offline", Implemented: true, Stability: "alpha"},
		{Name: "doctor.infrastructure-connect", Implemented: false, Stability: "future", Limit: "external database and broker readiness is outside the current offline CLI evidence scope"},
	}
	sort.Slice(capabilities, func(i, j int) bool { return capabilities[i].Name < capabilities[j].Name })
	return capabilityReport{SchemaVersion: capabilitySchemaVersion, CLIVersion: version, Capabilities: capabilities}
}

func runCapabilities(args []string, out, errOut io.Writer) int {
	jsonOutput := false
	for _, argument := range args {
		if argument == "--json" {
			jsonOutput = true
		} else {
			fmt.Fprintf(errOut, "unknown option: %s\n", argument)
			return 1
		}
	}
	report := currentCapabilities()
	if jsonOutput {
		encoder := json.NewEncoder(out)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
		return 0
	}
	fmt.Fprintf(out, "Spine CLI capabilities (%s)\n", report.SchemaVersion)
	for _, capability := range report.Capabilities {
		status := "not implemented"
		if capability.Implemented {
			status = "implemented"
		}
		fmt.Fprintf(out, "  [%s/%s] %s", status, capability.Stability, capability.Name)
		if capability.Limit != "" {
			fmt.Fprintf(out, " — %s", capability.Limit)
		}
		fmt.Fprintln(out)
	}
	return 0
}
