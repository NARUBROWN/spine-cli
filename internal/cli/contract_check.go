package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const contractCheckSchemaVersion = "spine.contract-check/v1alpha1"

type contractCheckReport struct {
	SchemaVersion    string                  `json:"schema_version"`
	ContractSchema   string                  `json:"contract_schema"`
	AnalysisMode     string                  `json:"analysis_mode"`
	AnalysisComplete bool                    `json:"analysis_complete"`
	Valid            bool                    `json:"valid"`
	WarningsAsErrors bool                    `json:"warnings_as_errors"`
	ErrorCount       int                     `json:"error_count"`
	WarningCount     int                     `json:"warning_count"`
	Diagnostics      []contractDiagnostic    `json:"diagnostics"`
	EventAdapters    []eventAdapterSelection `json:"event_adapters"`
	Validator        validatorSelection      `json:"validator"`
	HTTPAdapter      *httpAdapterSelection   `json:"http_adapter,omitempty"`
	EvidenceBoundary []string                `json:"evidence_boundary"`
}

func runContractCheck(args []string, out, errOut io.Writer) int {
	jsonOutput, warningsAsErrors := false, false
	for _, argument := range args {
		switch argument {
		case "--json":
			jsonOutput = true
		case "--warnings-as-errors":
			warningsAsErrors = true
		default:
			fmt.Fprintf(errOut, "unknown option: %s\n", argument)
			return 1
		}
	}
	contract, err := inspectApplication()
	if err != nil {
		fmt.Fprintf(errOut, "contract inspection failed: %v\n", err)
		return 1
	}
	report := buildContractCheckReport(contract, warningsAsErrors)
	if jsonOutput {
		encoder := json.NewEncoder(out)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
	} else {
		for _, diagnostic := range report.Diagnostics {
			printContractDiagnostic(out, diagnostic, "")
		}
		status := "passed"
		if !report.Valid {
			status = "failed"
		}
		fmt.Fprintf(out, "Static Spine contract check %s: %d errors, %d warnings.\n", status, report.ErrorCount, report.WarningCount)
		fmt.Fprintln(out, "Evidence boundary: project source only; runtime HTTP/DI/interceptor state and external infrastructure were not observed.")
	}
	if !report.Valid {
		return 1
	}
	return 0
}

func buildContractCheckReport(contract applicationContract, warningsAsErrors bool) contractCheckReport {
	report := contractCheckReport{
		SchemaVersion:    contractCheckSchemaVersion,
		ContractSchema:   contract.SchemaVersion,
		AnalysisMode:     contract.AnalysisMode,
		AnalysisComplete: contract.Complete,
		Valid:            true,
		EventAdapters:    contract.EventAdapters, Validator: contract.Validator,
		WarningsAsErrors: warningsAsErrors,
		Diagnostics:      append([]contractDiagnostic{}, contract.Diagnostics...),
		EvidenceBoundary: []string{
			"project source was inspected without claiming complete Go program execution",
			"direct os.Getenv registration names and string equality conditions use this inspection process's environment; use the same inputs at runtime",
			"runtime HTTP routes, DI constructors, and interceptors were not observed",
			"platform settings, factory capabilities and validator rules were not executed or evaluated",
			"external databases, brokers, listeners, and deployed behavior were not checked",
		},
	}
	if contract.HTTPAdapter.Resolution != "" {
		adapter := contract.HTTPAdapter
		report.HTTPAdapter = &adapter
	}
	for index := range report.Diagnostics {
		diagnostic := &report.Diagnostics[index]
		diagnostic.Retryable = true
		if diagnostic.Hint == "" {
			diagnostic.Hint = "Make the registration explicit in the composition root and run the contract check again."
		}
		if len(diagnostic.NextActions) == 0 {
			diagnostic.NextActions = []nextAction{{Command: "spine inspect --json", Reason: "Inspect the complete static evidence and source location", Safe: true}, {Command: "spine contract check --json", Reason: "Revalidate after correcting the source", Safe: true}}
		}
		if strings.Contains(diagnostic.Code, "NOT_IMPLEMENTED") {
			diagnostic.Hint = "Implement the generated handler and remove its explicit not-implemented sentinel."
		}
	}
	for _, diagnostic := range report.Diagnostics {
		switch diagnostic.Severity {
		case "error":
			report.ErrorCount++
		case "warning":
			report.WarningCount++
		}
	}
	report.Valid = report.ErrorCount == 0 && (!warningsAsErrors || report.WarningCount == 0)
	return report
}

func printContractDiagnostic(out io.Writer, diagnostic contractDiagnostic, indent string) {
	fmt.Fprintf(out, "%s[%s] %s: %s (%s)\n", indent, diagnostic.Severity, diagnostic.Code, diagnostic.Message, diagnostic.Source)
	if diagnostic.Hint != "" {
		fmt.Fprintf(out, "%s  Hint: %s\n", indent, diagnostic.Hint)
	}
}
