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
		{Name: "cloud.deploy", Implemented: true, Stability: "experimental", Evidence: "validate and build a static Linux ARM64 artifact, submit to Spine Cloud and wait for deployment", Limit: "requires a compatible Spine Cloud service and agent; frontend assets are not supported"},
		{Name: "cloud.auth", Implemented: true, Stability: "experimental", Evidence: "browser device authorization, system credential storage, session refresh and current-session logout", Limit: "requires a compatible Spine Cloud service and system credential store"},
		{Name: "frontend.react-csr-build", Implemented: true, Stability: "experimental", Evidence: "esbuild Go API bundles React JSX/TSX, CSS imports and imported assets; run and build use production minification; implicit detection requires a supported Echo or net/http composition", Limit: "frontend package dependencies must already be installed; TypeScript type checking remains a separate project tool"},
		{Name: "frontend.dev-watch", Implemented: true, Stability: "experimental", Evidence: "spine dev fingerprints files under frontend_root and uses an incremental esbuild context; front-only changes replace static output without rebuilding the Go process", Limit: "poll interval is 500ms; node_modules and generated output are excluded"},
		{Name: "frontend.static-fallback", Implemented: true, Stability: "experimental", Evidence: "Go build overlays add middleware to existing Echo and net/http composition roots; backend routes run first, then static files and extensionless HTML routes; missing assets remain 404", Limit: "composition must call the Echo or net/http platform Option or NewFactory directly inside BuildApplication; API routes outside frontend_api_prefix can be mistaken for SPA navigation when they return 404 to an HTML request"},
		{Name: "frontend.license-notices", Implemented: true, Stability: "experimental", Evidence: "esbuild and Go module licenses are embedded in the CLI and printed by spine licenses; bundled npm package license files are gathered into THIRD-PARTY-LICENSES.txt and esbuild legal comments are linked", Limit: "update the embedded notice list when the CLI dependency graph changes; npm packages without package metadata or recognizable license files need manual review"},
		{Name: "frontend.provider-contract", Implemented: true, Stability: "experimental", Evidence: "built-in React CSR and dynamically resolved spine-frontend-<provider> modules from the selected dependency channel use the declarative spine-frontend.json schema for esbuild settings, entry candidates, npm dependencies and starter files", Limit: "only the esbuild engine is supported; provider module code is not executed; names resolve through the fixed NARUBROWN module namespace rather than a registry-wide search"},
		{Name: "contract.component-roles", Implemented: true, Stability: "experimental", Evidence: "one app.Constructor call accepts constructor.Custom, Repository, Service, Controller and Interceptor markers; canonical imports, factory and method contracts, dependencies and role isolation are checked", Limit: "Custom accepts existing named factories or object prototypes; other roles require object prototypes; dynamic expressions are unresolved; no constructors are executed"},
		{Name: "contract.controller-routes", Implemented: true, Stability: "experimental", Evidence: "go/types interface and DI return-type analysis, compile-time path constants, Registry forwarding helpers, typed callbacks and aliases", Limit: "requires installed matching pkg/api core; dynamic paths/dispatch, recursive helpers and unresolved conditions fail completeness; constructors and init functions are not executed"},
		{Name: "generation.controller-routes", Implemented: true, Stability: "experimental", Evidence: "core v0.7.0-preview.5+ layered scaffolds/controllers use RegisterRoutes; route AST plans add typed callbacks to the controller with dry-run/if-match", Limit: "older cores keep explicit app.Route generation; new core candidate must be available before public installation"},
		{Name: "generation.project.websocket", Implemented: true, Stability: "experimental", Evidence: "new --websocket-platform resolves independent provider metadata and generates its option", Limit: "default none; HTTP required; WebSocket route registration remains explicit"},
		{Name: "generation.project.validator", Implemented: true, Stability: "experimental", Evidence: "new --validator provider resolves spine-validator-<provider> from the selected dependency channel and declarative spine-platform.json; none omits the implementation", Limit: "minimum Go and constructor come from metadata; existing playground versions retain a compatibility descriptor"},
		{Name: "generation.project.event-platforms", Implemented: true, Stability: "experimental", Evidence: "new --event-platform [name:]provider resolves spine-<provider>-platform from the selected dependency channel and declarative configuration metadata", Limit: "missing repositories or metadata fail before destination publication; Kafka/RabbitMQ compatibility descriptors remain; no broker or application initialization during generation"},
		{Name: "contract.assembly-selection", Implemented: true, Stability: "alpha", Evidence: "event and validator factories use metadata from selected module cache; duplicate/mismatch/version and dynamic-expression diagnostics", Limit: "factory configuration values and validator rules are not executed or proven; missing metadata, unknown helpers, mutations and option expansions fail the gate"},
		{Name: "generation.resource", Implemented: true, Stability: "alpha", Evidence: "layered-v1 composes domain, repository, service and controller plans", Limit: "DTO and HTTP routes require explicit commands"},
		{Name: "generation.layered-layout", Implemented: true, Stability: "alpha", Evidence: "layered-v1 role roots, optional nested groups, qualified dependency references and atomic plans"},
		{Name: "generation.domain", Implemented: true, Stability: "alpha", Limit: "layered-v1 only; explicit fields without implicit IDs or ORM tags"},
		{Name: "generation.dto", Implemented: true, Stability: "alpha", Limit: "layered-v1 only; data types are not DI registrations"},
		{Name: "generation.route", Implemented: true, Stability: "alpha", Limit: "layered-v1 only; explicit existing handler, method and path"},
		{Name: "agent.command-schema", Implemented: true, Stability: "alpha", Evidence: "machine-readable arguments, options, side effects, and output support"},
		{Name: "agent.project-context", Implemented: true, Stability: "alpha", Evidence: "bounded project, capability, doctor, and contract context with next actions"},
		{Name: "agent.result-protocol", Implemented: true, Stability: "alpha", Evidence: "versioned success and error envelope for --json and --format json"},
		{Name: "generation.optimistic-concurrency", Implemented: true, Stability: "alpha", Evidence: "plain and JSON dry-run expose the apply hash; --if-match guards every planned target; layered-v1 also hashes source inputs, configuration and proposed contents"},
		{Name: "generation.project", Implemented: true, Stability: "alpha", Evidence: "spine new with a selected preview, rc or main dependency channel, dynamically selected HTTP, WebSocket, event and validator implementations, dependency installation, static contract and build gates; --skip-install skips installation/build but still resolves versions"},
		{Name: "generation.project.interactive", Implemented: true, Stability: "experimental", Evidence: "spine new prompts for the dependency channel, HTTP, event, WebSocket, validator and frontend providers when stdin is a terminal; menu names and provider IDs can be entered directly; --interactive and --no-interactive control prompts", Limit: "custom provider lookup follows each module naming contract and does not enumerate GitHub or Go registries"},
		{Name: "generation.project.echo-platform", Implemented: true, Stability: "experimental", Evidence: "channel-aware dependency resolution, staged installation and guarded publication", Limit: "development recipe; single module; --force requires --skip-install"},
		{Name: "project.update", Implemented: true, Stability: "experimental", Evidence: "spine update resolves the selected bundled recipe in a temporary project copy, checks selected versions, static contract and build, then publishes go.mod/go.sum with rollback and optimistic concurrency", Limit: "single module; no CLI self-update, source migration or automatic downgrade; workspaces, vendor, custom GOFLAGS, local replacements and symlink inputs require separate handling"},
		{Name: "contract.http-adapter-selection", Implemented: true, Stability: "alpha", Evidence: "known HTTP factories and metadata-declared options, HTTP configuration diagnostics and offline selected module graph in spine.contract/v1alpha2", Limit: "source and module metadata only; does not execute factory capabilities or initialize an application"},
		{Name: "generation.resource.http-get", Implemented: true, Stability: "alpha", Evidence: "legacy resource generator with AST registration", Limit: "legacy only; layered-v1 resources compose role generators without implicit HTTP routes"},
		{Name: "generation.controller", Implemented: true, Stability: "alpha"},
		{Name: "generation.service", Implemented: true, Stability: "alpha"},
		{Name: "generation.repository", Implemented: true, Stability: "alpha", Limit: "persistence-neutral skeleton only"},
		{Name: "generation.vertical-slice", Implemented: true, Stability: "alpha", Evidence: "typed model, DTO, repository contract, memory adapter, service, HTTP controller, and service test", Limit: "legacy only; requires explicit development-only --storage memory; Bun, transaction, and outbox recipes are not implemented"},
		{Name: "generation.consumer", Implemented: true, Stability: "alpha", Evidence: "typed payload, handler, checked composition registration, and AsyncAPI-visible contract", Limit: "transport, delivery, retry, and dead-letter policy remain application infrastructure decisions"},
		{Name: "generation.websocket", Implemented: true, Stability: "alpha", Evidence: "typed message, handler, checked composition registration, and AsyncAPI-visible contract", Limit: "authentication, origin policy, capacity, and response behavior must be selected by the application"},
		{Name: "generation.kafka", Implemented: true, Stability: "experimental", Evidence: "new --event-platform [name:]kafka generates pinned dependency, environment configuration and WithEventAdapter", Limit: "no g kafka command; consumer handlers and topic provisioning remain explicit"},
		{Name: "generation.rabbitmq", Implemented: true, Stability: "experimental", Evidence: "new --event-platform [name:]rabbitmq generates pinned dependency, environment configuration and WithEventAdapter", Limit: "no g rabbitmq command; failure policy defaults to reject; broker readiness requires separate E2E"},
		{Name: "generation.nats", Implemented: true, Stability: "experimental", Evidence: "new --event-platform [name:]nats resolves the selected dependency channel and generates JetStream configuration and WithEventAdapter", Limit: "Go 1.26.0+; pre-provisioned JetStream stream; failure policy defaults to reject; Core NATS pub/sub and request/reply are not included"},
		{Name: "generation.bun", Implemented: false, Stability: "planned"},
		{Name: "contract.static-inspection", Implemented: true, Stability: "alpha", Evidence: "HTTP, DI, interceptors, consumers, WebSockets, resources, boot options; explicit os.Getenv registration inputs and string equality conditions", Limit: "environment-dependent registrations use the inspection process's inputs; arbitrary functions and local variables are not evaluated"},
		{Name: "contract.explain", Implemented: true, Stability: "alpha", Limit: "static-source evidence only"},
		{Name: "contract.snapshot-diff", Implemented: true, Stability: "alpha", Evidence: "semantic drift ignores source line movement"},
		{Name: "contract.structured-check", Implemented: true, Stability: "alpha", Evidence: "machine-readable completeness, diagnostics, evidence boundaries, and warnings-as-errors policy"},
		{Name: "contract.http-return-types", Implemented: true, Stability: "alpha", Evidence: "shared source-scoped HTTP return checks for route plans and static gates; httpx wrappers, aliases and error methods", Limit: "dynamic interfaces and unknown external types are unresolved; runtime values, nil results and JSON serialization are not executed"},
		{Name: "generation.human-guidance", Implemented: true, Stability: "alpha", Evidence: "file actions, actual type references and registrations; role help and generated first API guide"},
		{Name: "contract.http-parameter-bindings", Implemented: true, Stability: "alpha", Evidence: "core-version-aware path/query/header DTO bindings, named path keys, source/key/type collisions and deprecated-wrapper migration diagnostics shared by route generation and static contracts", Limit: "validator rules, field decoders and external embedded declarations are not executed; static checks do not replace core startup validation"},
		{Name: "contract.openapi-export", Implemented: true, Stability: "alpha", Evidence: "default static AST export; --source runtime executes BuildApplication and uses the public core DescribeHTTP and openapi.Generate APIs without starting a listener", Limit: "runtime export needs Spine v0.7.0-preview.1 or compatible APIs and executes composition code and Go tooling; static DTO constraints remain partial; constructors and external readiness are not verified"},
		{Name: "contract.asyncapi-export", Implemented: true, Stability: "alpha", Evidence: "AsyncAPI 3.1 channels and receive operations for consumer and WebSocket registrations", Limit: "channel and operation IDs preserve original identities; broker protocol and delivery guarantees require runtime infrastructure evidence"},
		{Name: "contract.runtime-manifest", Implemented: false, Stability: "future", Limit: "a unified DI, interceptor and transport manifest is unavailable; runtime HTTP descriptions are used only by explicit OpenAPI export"},
		{Name: "contract.runtime-probe-partial", Implemented: true, Stability: "experimental", Evidence: "executes BuildApplication, runs app.Validate, and compares public consumer/WebSocket registries with the static contract", Limit: "HTTP descriptions are not reconciled by this probe; DI constructors and interceptors remain unobserved"},
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
