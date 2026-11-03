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
		{Name: "generation.project.websocket", Implemented: true, Stability: "experimental", Evidence: "new --websocket-platform gorilla generates an independent Gorilla option and pinned public module", Limit: "default none; HTTP echo required; WebSocket route registration remains explicit"},
		{Name: "generation.project.validator", Implemented: true, Stability: "experimental", Evidence: "new --validator playground generates playground.New error handling and WithValidator; none omits the implementation", Limit: "Spine v0.6.5 release recipe; playground requires Go 1.26.0+"},
		{Name: "generation.project.event-platforms", Implemented: true, Stability: "experimental", Evidence: "HTTP+events, event-only workers and multiple uniquely named Kafka/RabbitMQ factories", Limit: "public pseudo-versions; no broker or application execution during generation"},
		{Name: "contract.assembly-selection", Implemented: true, Stability: "alpha", Evidence: "static named event factories and optional playground validator, duplicate/mismatch/version and dynamic-expression diagnostics", Limit: "factory configuration values and validator rules are not executed or proven; unknown helpers, mutations and option expansions fail the gate"},
		{Name: "generation.resource", Implemented: true, Stability: "alpha", Evidence: "layered-v1 composes domain, repository, service and controller plans", Limit: "DTO and HTTP routes require explicit commands"},
		{Name: "generation.layered-layout", Implemented: true, Stability: "alpha", Evidence: "layered-v1 role roots, optional nested groups, qualified dependency references and atomic plans"},
		{Name: "generation.domain", Implemented: true, Stability: "alpha", Limit: "layered-v1 only; explicit fields without implicit IDs or ORM tags"},
		{Name: "generation.dto", Implemented: true, Stability: "alpha", Limit: "layered-v1 only; data types are not DI registrations"},
		{Name: "generation.route", Implemented: true, Stability: "alpha", Limit: "layered-v1 only; explicit existing handler, method and path"},
		{Name: "agent.command-schema", Implemented: true, Stability: "alpha", Evidence: "machine-readable arguments, options, side effects, and output support"},
		{Name: "agent.project-context", Implemented: true, Stability: "alpha", Evidence: "bounded project, capability, doctor, and contract context with next actions"},
		{Name: "agent.result-protocol", Implemented: true, Stability: "alpha", Evidence: "versioned success and error envelope for --json and --format json"},
		{Name: "generation.optimistic-concurrency", Implemented: true, Stability: "alpha", Evidence: "plain and JSON dry-run expose the apply hash; --if-match guards every planned target; layered-v1 also hashes source inputs, configuration and proposed contents"},
		{Name: "generation.project", Implemented: true, Stability: "alpha", Evidence: "spine new with spine.New composition and selected HTTP/event/validator implementations, dependency installation, static contract and build gates; --skip-install scaffolds offline"},
		{Name: "generation.project.echo-platform", Implemented: true, Stability: "experimental", Evidence: "pinned public Echo platform recipe, staged installation and guarded publication", Limit: "development recipe; single module; --force requires --skip-install"},
		{Name: "project.update", Implemented: true, Stability: "experimental", Evidence: "spine update resolves the selected bundled recipe in a temporary project copy, checks selected versions, static contract and build, then publishes go.mod/go.sum with rollback and optimistic concurrency", Limit: "single module; no CLI self-update, source migration or automatic downgrade; workspaces, vendor, custom GOFLAGS, local replacements and symlink inputs require separate handling"},
		{Name: "contract.http-adapter-selection", Implemented: true, Stability: "alpha", Evidence: "canonical Echo factories, HTTP configuration diagnostics and offline selected module graph in spine.contract/v1alpha2", Limit: "source and module metadata only; does not execute factory capabilities or initialize an application"},
		{Name: "generation.resource.http-get", Implemented: true, Stability: "alpha", Evidence: "legacy resource generator with AST registration", Limit: "legacy only; layered-v1 resources compose role generators without implicit HTTP routes"},
		{Name: "generation.controller", Implemented: true, Stability: "alpha"},
		{Name: "generation.service", Implemented: true, Stability: "alpha"},
		{Name: "generation.repository", Implemented: true, Stability: "alpha", Limit: "persistence-neutral skeleton only"},
		{Name: "generation.vertical-slice", Implemented: true, Stability: "alpha", Evidence: "typed model, DTO, repository contract, memory adapter, service, HTTP controller, and service test", Limit: "legacy only; requires explicit development-only --storage memory; Bun, transaction, and outbox recipes are not implemented"},
		{Name: "generation.consumer", Implemented: true, Stability: "alpha", Evidence: "typed payload, handler, checked composition registration, and AsyncAPI-visible contract", Limit: "transport, delivery, retry, and dead-letter policy remain application infrastructure decisions"},
		{Name: "generation.websocket", Implemented: true, Stability: "alpha", Evidence: "typed message, handler, checked composition registration, and AsyncAPI-visible contract", Limit: "authentication, origin policy, capacity, and response behavior must be selected by the application"},
		{Name: "generation.kafka", Implemented: true, Stability: "experimental", Evidence: "new --event-platform [name:]kafka generates pinned dependency, environment configuration and WithEventAdapter", Limit: "no g kafka command; consumer handlers and topic provisioning remain explicit"},
		{Name: "generation.rabbitmq", Implemented: true, Stability: "experimental", Evidence: "new --event-platform [name:]rabbitmq generates pinned dependency, environment configuration and WithEventAdapter", Limit: "no g rabbitmq command; failure policy defaults to reject; broker readiness requires separate E2E"},
		{Name: "generation.bun", Implemented: false, Stability: "planned"},
		{Name: "contract.static-inspection", Implemented: true, Stability: "alpha", Evidence: "HTTP, DI, interceptors, consumers, WebSockets, resources, boot options; explicit os.Getenv registration inputs and string equality conditions", Limit: "environment-dependent registrations use the inspection process's inputs; arbitrary functions and local variables are not evaluated"},
		{Name: "contract.explain", Implemented: true, Stability: "alpha", Limit: "static-source evidence only"},
		{Name: "contract.snapshot-diff", Implemented: true, Stability: "alpha", Evidence: "semantic drift ignores source line movement"},
		{Name: "contract.structured-check", Implemented: true, Stability: "alpha", Evidence: "machine-readable completeness, diagnostics, evidence boundaries, and warnings-as-errors policy"},
		{Name: "contract.http-return-types", Implemented: true, Stability: "alpha", Evidence: "shared source-scoped HTTP return checks for route plans and static gates; httpx wrappers, aliases and error methods", Limit: "dynamic interfaces and unknown external types are unresolved; runtime values, nil results and JSON serialization are not executed"},
		{Name: "generation.human-guidance", Implemented: true, Stability: "alpha", Evidence: "file actions, actual type references and registrations; role help and generated first API guide"},
		{Name: "contract.http-parameter-bindings", Implemented: true, Stability: "alpha", Evidence: "shared source-scoped default resolver checks for route generation and static contracts, including DTO pointer and supported framework wrapper types", Limit: "external type declarations and custom argument resolvers are not executed or inferred; unresolved support fails the contract gate"},
		{Name: "contract.openapi-export", Implemented: true, Stability: "alpha", Evidence: "HTTP paths, handler signatures, path parameters, DTO schemas, and response body types", Limit: "package-scoped DTOs and time.Time are mapped; unsupported types use x-spine-unresolved-type; runtime status remains unknown"},
		{Name: "contract.asyncapi-export", Implemented: true, Stability: "alpha", Evidence: "AsyncAPI 3.1 channels and receive operations for consumer and WebSocket registrations", Limit: "channel and operation IDs preserve original identities; broker protocol and delivery guarantees require runtime infrastructure evidence"},
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
