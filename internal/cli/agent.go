package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const commandSchemaVersion = "spine.command-schema/v1alpha1"
const contextSchemaVersion = "spine.project-context/v1alpha1"

type commandSchema struct {
	SchemaVersion   string                      `json:"schema_version"`
	CLIVersion      string                      `json:"cli_version"`
	Protocol        string                      `json:"protocol_schema"`
	GlobalOptions   map[string]optionDefinition `json:"global_options"`
	Commands        []commandDefinition         `json:"commands"`
	GenerationRoles []generationRole            `json:"generation_roles"`
	Layouts         []string                    `json:"layouts"`
}

type commandDefinition struct {
	Name           string                      `json:"name"`
	Summary        string                      `json:"summary"`
	Arguments      []argumentDefinition        `json:"arguments,omitempty"`
	Options        map[string]optionDefinition `json:"options,omitempty"`
	Effects        commandEffects              `json:"effects"`
	SupportsJSON   bool                        `json:"supports_json"`
	SupportsDryRun bool                        `json:"supports_dry_run"`
}

type argumentDefinition struct {
	Name     string   `json:"name"`
	Required bool     `json:"required"`
	Enum     []string `json:"enum,omitempty"`
}

type optionDefinition struct {
	Type        string   `json:"type"`
	Required    bool     `json:"required,omitempty"`
	Repeatable  bool     `json:"repeatable,omitempty"`
	Enum        []string `json:"enum,omitempty"`
	Description string   `json:"description,omitempty"`
}

type commandEffects struct {
	WritesFiles bool `json:"writes_files"`
	Network     bool `json:"network"`
	LongRunning bool `json:"long_running"`
}

func currentCommandSchema() commandSchema {
	jsonOption := optionDefinition{Type: "boolean", Description: "Emit the versioned machine-readable result protocol"}
	commands := []commandDefinition{
		{Name: "build", Summary: "Build the configured application binary", Effects: commandEffects{WritesFiles: true, Network: true}},
		{Name: "capabilities", Summary: "Discover implemented features and explicit limits", Options: map[string]optionDefinition{"json": jsonOption}, Effects: commandEffects{}, SupportsJSON: true},
		{Name: "check", Summary: "Run formatting, vet, and test checks", Effects: commandEffects{Network: true}},
		{Name: "completion", Summary: "Generate shell completion", Arguments: []argumentDefinition{{Name: "shell", Required: true, Enum: []string{"powershell", "bash", "zsh"}}}, Effects: commandEffects{}},
		{Name: "context", Summary: "Read a bounded project, doctor, and execution-contract context", Options: map[string]optionDefinition{"json": jsonOption}, Effects: commandEffects{}, SupportsJSON: true},
		{Name: "contract check", Summary: "Validate the static execution contract", Options: map[string]optionDefinition{"json": jsonOption, "warnings-as-errors": {Type: "boolean"}}, Effects: commandEffects{}, SupportsJSON: true},
		{Name: "contract diff", Summary: "Compare the current contract with a semantic snapshot", Options: map[string]optionDefinition{"json": jsonOption, "against": {Type: "string"}}, Effects: commandEffects{}, SupportsJSON: true},
		{Name: "contract export", Summary: "Export an OpenAPI or AsyncAPI document", Options: map[string]optionDefinition{"format": {Type: "string", Required: true, Enum: []string{"openapi", "asyncapi"}}, "output": {Type: "string", Required: true}, "force": {Type: "boolean"}}, Effects: commandEffects{WritesFiles: true}},
		{Name: "contract probe", Summary: "Run isolated partial runtime reconciliation", Options: map[string]optionDefinition{"json": jsonOption}, Effects: commandEffects{WritesFiles: true, Network: true}, SupportsJSON: true},
		{Name: "contract snapshot", Summary: "Persist the canonical semantic contract", Options: map[string]optionDefinition{"output": {Type: "string"}, "force": {Type: "boolean"}}, Effects: commandEffects{WritesFiles: true}},
		{Name: "doctor", Summary: "Check local project prerequisites without external connections", Options: map[string]optionDefinition{"json": jsonOption}, Effects: commandEffects{}, SupportsJSON: true},
		{Name: "explain", Summary: "Explain one HTTP, consumer, or WebSocket execution path", Arguments: []argumentDefinition{{Name: "kind", Required: true, Enum: []string{"http", "consumer", "websocket"}}, {Name: "selector", Required: true}}, Options: map[string]optionDefinition{"json": jsonOption}, Effects: commandEffects{}, SupportsJSON: true},
		{Name: "generate", Summary: "Generate and optionally register an application component", Arguments: []argumentDefinition{{Name: "kind", Required: true, Enum: []string{"resource", "slice", "consumer", "websocket", "controller", "service", "repository"}}, {Name: "name", Required: true}}, Options: map[string]optionDefinition{"json": jsonOption, "dry-run": {Type: "boolean"}, "if-match": {Type: "string", Description: "Require the target files to match a dry-run base hash"}, "force": {Type: "boolean"}, "register": {Type: "boolean"}, "no-register": {Type: "boolean"}, "storage": {Type: "string", Enum: []string{"memory"}}, "field": {Type: "string", Repeatable: true}, "depends": {Type: "string", Repeatable: true}, "topic": {Type: "string"}, "path": {Type: "string"}}, Effects: commandEffects{WritesFiles: true}, SupportsJSON: true, SupportsDryRun: true},
		{Name: "inspect", Summary: "Inspect the canonical static execution contract", Options: map[string]optionDefinition{"json": jsonOption}, Effects: commandEffects{}, SupportsJSON: true},
		{Name: "new", Summary: "Create a Spine project and install the Echo platform", Arguments: []argumentDefinition{{Name: "project", Required: true}}, Options: map[string]optionDefinition{"json": jsonOption, "module": {Type: "string"}, "dry-run": {Type: "boolean"}, "if-match": {Type: "string"}, "force": {Type: "boolean"}}, Effects: commandEffects{WritesFiles: true}, SupportsJSON: true, SupportsDryRun: true},
		{Name: "schema", Summary: "Discover commands, arguments, options, and side effects", Options: map[string]optionDefinition{"json": jsonOption}, Effects: commandEffects{}, SupportsJSON: true},
		{Name: "test", Summary: "Run all Go tests", Effects: commandEffects{Network: true}},
		{Name: "verify", Summary: "Run contract, formatting, vet, and test verification", Options: map[string]optionDefinition{"json": jsonOption, "warnings-as-errors": {Type: "boolean"}}, Effects: commandEffects{Network: true}, SupportsJSON: true},
		{Name: "version", Summary: "Print the Spine CLI version", Effects: commandEffects{}},
		{Name: "dev", Summary: "Watch, rebuild, and run the application", Options: map[string]optionDefinition{"events": {Type: "string", Enum: []string{"jsonl"}, Description: "Stream lifecycle and child-process output as versioned JSON Lines"}}, Effects: commandEffects{WritesFiles: true, Network: true, LongRunning: true}},
		{Name: "run", Summary: "Build and run the application in production mode", Effects: commandEffects{WritesFiles: true, Network: true, LongRunning: true}},
	}
	sort.Slice(commands, func(i, j int) bool { return commands[i].Name < commands[j].Name })
	for index := range commands {
		c := &commands[index]
		if c.Name == "generate" {
			c.Arguments[0].Enum = append(c.Arguments[0].Enum, "domain", "dto", "route")
			c.Arguments[1].Name = "[group/]name"
			c.Options["method"] = optionDefinition{Type: "string", Description: "HTTP method for explicit route registration"}
			c.Options["handler"] = optionDefinition{Type: "string", Description: "Module-relative package/path:Type.Method"}
			d := c.Options["depends"]
			d.Description = "layered-v1: module-relative package/path:Type; legacy: local file name"
			c.Options["depends"] = d
		}
		if c.Name == "new" {
			c.Options["platform"] = optionDefinition{Type: "string", Enum: []string{"echo"}}
			c.Options["skip-install"] = optionDefinition{Type: "boolean", Description: "Generate files without dependency installation or build verification; offline dry-run"}
			c.Effects.Network = true
			c.Options["layout"] = optionDefinition{Type: "string", Enum: []string{layeredLayout, "legacy"}, Description: "New projects default to layered-v1; legacy preserves the previous generation rules"}
		}
		switch c.Name {
		case "generate", "new":
			c.Options["help"] = optionDefinition{Type: "boolean", Description: "Show help before reading project configuration or generating files"}
		case "run", "test", "check", "build":
			c.Options = map[string]optionDefinition{"help": {Type: "boolean", Description: "Show command help without executing; other command-specific options are rejected"}}
		}
	}
	return commandSchema{SchemaVersion: commandSchemaVersion, CLIVersion: version, Protocol: protocolSchemaVersion, GlobalOptions: map[string]optionDefinition{"root": {Type: "string", Description: "Run the command from an explicit project directory"}}, Commands: commands, GenerationRoles: generationRoles(), Layouts: []string{layeredLayout, "legacy"}}
}

func runSchema(args []string, out, errOut io.Writer) int {
	jsonOutput, err := onlyJSONOption(args)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	report := currentCommandSchema()
	if jsonOutput {
		return encodeIndentedJSON(report, out, errOut)
	}
	fmt.Fprintf(out, "Spine command schema (%s)\n", report.SchemaVersion)
	for _, command := range report.Commands {
		fmt.Fprintf(out, "  %-18s %s\n", command.Name, command.Summary)
	}
	return 0
}

type projectContext struct {
	SchemaVersion    string               `json:"schema_version"`
	CLIVersion       string               `json:"cli_version"`
	ProjectRoot      string               `json:"project_root"`
	Config           projectContextConfig `json:"config"`
	Capabilities     capabilityReport     `json:"capabilities"`
	Doctor           doctorReport         `json:"doctor"`
	Contract         *contractCheckReport `json:"contract,omitempty"`
	NextActions      []nextAction         `json:"next_actions"`
	EvidenceBoundary []string             `json:"evidence_boundary"`
	GenerationRoles  []generationRole     `json:"generation_roles,omitempty"`
	Components       []layoutComponent    `json:"components,omitempty"`
}

type projectContextConfig struct {
	Module      string `json:"module"`
	Entrypoint  string `json:"entrypoint"`
	Composition string `json:"composition"`
	Port        int    `json:"port"`
	Layout      string `json:"layout"`
	Platform    string `json:"platform,omitempty"`
}

type nextAction struct {
	Command string `json:"command"`
	Reason  string `json:"reason"`
	Safe    bool   `json:"safe"`
}

func runContext(args []string, out, errOut io.Writer) int {
	jsonOutput, optionErr := onlyJSONOption(args)
	if optionErr != nil {
		fmt.Fprintln(errOut, optionErr)
		return 1
	}
	root, err := projectRoot()
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	previous, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	if err := os.Chdir(root); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	defer os.Chdir(previous)
	absoluteRoot, _ := filepath.Abs(root)
	config, err := loadProjectConfig()
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	doctor := inspectDoctorReport()
	context := projectContext{
		SchemaVersion: contextSchemaVersion,
		CLIVersion:    version,
		ProjectRoot:   filepath.Clean(absoluteRoot),
		Config:        projectContextConfig{Module: config.Module, Entrypoint: config.Entrypoint, Composition: config.Composition, Port: config.Port, Layout: config.Layout, Platform: config.Platform},
		Capabilities:  currentCapabilities(),
		Doctor:        doctor,
		NextActions: []nextAction{
			{Command: "spine contract check --root " + quoteCommandArgument(absoluteRoot) + " --json", Reason: "Validate the static execution contract before changing code", Safe: true},
			{Command: "spine verify --root " + quoteCommandArgument(absoluteRoot) + " --json", Reason: "Run local verification after changes", Safe: false},
		},
		EvidenceBoundary: []string{"project source and local toolchain only", "external databases, brokers, listeners, and deployed behavior were not observed"},
	}
	var inventoryErr error
	if config.Layout == layeredLayout {
		context.GenerationRoles = generationRoles()
		context.Components, inventoryErr = layoutInventory(config.Module)
	} else if context.Config.Layout == "" {
		context.Config.Layout = "legacy"
	}
	contract, inspectErr := inspectApplication()
	if inspectErr == nil {
		report := buildContractCheckReport(contract, false)
		context.Contract = &report
	}
	if jsonOutput {
		if code := encodeIndentedJSON(context, out, errOut); code != 0 {
			return code
		}
	} else {
		fmt.Fprintf(out, "Spine project context (%s)\n  root: %s\n  module: %s\n  doctor: %t\n", context.SchemaVersion, context.ProjectRoot, context.Config.Module, context.Doctor.OK)
	}
	if inventoryErr != nil {
		fmt.Fprintf(errOut, "layout inventory failed: %v\n", inventoryErr)
	}
	if inspectErr != nil {
		fmt.Fprintf(errOut, "contract inspection failed: %v\n", inspectErr)
		return 1
	}
	if inventoryErr != nil || !doctor.OK || (context.Contract != nil && !context.Contract.Valid) {
		return 1
	}
	return 0
}

func quoteCommandArgument(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}

func onlyJSONOption(args []string) (bool, error) {
	jsonOutput := false
	for _, argument := range args {
		if argument != "--json" {
			return false, fmt.Errorf("unknown option: %s", argument)
		}
		jsonOutput = true
	}
	return jsonOutput, nil
}

func encodeIndentedJSON(value any, out, errOut io.Writer) int {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	return 0
}
