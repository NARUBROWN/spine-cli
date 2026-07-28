package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

type doctorReport struct {
	Scope  string        `json:"scope"`
	OK     bool          `json:"ok"`
	Checks []doctorCheck `json:"checks"`
	Limits []string      `json:"limits"`
}

type doctorCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

func runDoctor(args []string, out, errOut io.Writer) int {
	jsonOutput := false
	for _, argument := range args {
		switch argument {
		case "--json":
			jsonOutput = true
		case "--connect":
			fmt.Fprintln(errOut, "spine doctor --connect is not implemented; the current doctor intentionally provides offline evidence only")
			return 1
		default:
			fmt.Fprintf(errOut, "unknown option: %s\n", argument)
			return 1
		}
	}
	report := inspectDoctorReport()
	if jsonOutput {
		encoder := json.NewEncoder(out)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
	} else {
		fmt.Fprintf(out, "Spine project doctor (%s)\n", report.Scope)
		for _, check := range report.Checks {
			fmt.Fprintf(out, "  [%s] %s: %s\n", check.Status, check.Name, check.Message)
		}
		for _, limit := range report.Limits {
			fmt.Fprintf(out, "  [not checked] %s\n", limit)
		}
	}
	if !report.OK {
		return 1
	}
	return 0
}

func inspectDoctorReport() doctorReport {
	report := doctorReport{
		Scope:  "offline",
		OK:     true,
		Checks: []doctorCheck{},
		Limits: []string{"external databases", "Kafka and RabbitMQ connectivity", "network listeners", "deployed runtime behavior"},
	}
	add := func(name string, err error, success string) {
		if err != nil {
			report.OK = false
			report.Checks = append(report.Checks, doctorCheck{Name: name, Status: "error", Message: err.Error()})
			return
		}
		report.Checks = append(report.Checks, doctorCheck{Name: name, Status: "ok", Message: success})
	}

	goPath, err := exec.LookPath("go")
	add("Go toolchain", err, fmt.Sprintf("%s (%s)", runtime.Version(), goPath))
	config, configErr := loadProjectConfig()
	add("spine.toml", configErr, "project configuration is valid")
	if configErr != nil {
		return report
	}
	module, moduleErr := modulePath()
	add("module", moduleErr, module)
	_, entrypointErr := os.Stat(config.Entrypoint)
	add("entrypoint", entrypointErr, config.Entrypoint)
	_, compositionErr := os.Stat(config.Composition)
	add("composition path", compositionErr, config.Composition)
	spineVersion, dependencyErr := spineDependencyVersion("go.mod")
	add("Spine dependency", dependencyErr, spineVersion)
	contract, contractErr := inspectApplication()
	if contractErr == nil && contractHasErrors(contract) {
		var messages []string
		for _, diagnostic := range contract.Diagnostics {
			if diagnostic.Severity == "error" {
				messages = append(messages, diagnostic.Code+": "+diagnostic.Message)
			}
		}
		contractErr = fmt.Errorf("%s", strings.Join(messages, "; "))
	}
	add("static execution contract", contractErr, fmt.Sprintf("%d HTTP, %d consumer, %d WebSocket registrations", len(contract.HTTP), len(contract.Consumers), len(contract.WebSockets)))
	return report
}

func spineDependencyVersion(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(strings.TrimSpace(scanner.Text()))
		for index, field := range fields {
			if field == spineImportPath && index+1 < len(fields) {
				return fields[index+1], nil
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("go.mod does not require %s", spineImportPath)
}
