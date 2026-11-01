package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const protocolSchemaVersion = "spine.cli-result/v1"

type protocolError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
	Hint      string `json:"hint,omitempty"`
}

type protocolResult struct {
	SchemaVersion string         `json:"schema_version"`
	OK            bool           `json:"ok"`
	Command       string         `json:"command"`
	ExitCode      int            `json:"exit_code"`
	Result        any            `json:"result,omitempty"`
	Error         *protocolError `json:"error,omitempty"`
}

func normalizeJSONArguments(args []string) ([]string, bool) {
	normalized := make([]string, 0, len(args)+1)
	jsonOutput := false
	for index := 0; index < len(args); index++ {
		if args[index] == "--format" && index+1 < len(args) && args[index+1] == "json" {
			jsonOutput = true
			normalized = append(normalized, "--json")
			index++
			continue
		}
		if args[index] == "--json" {
			jsonOutput = true
		}
		normalized = append(normalized, args[index])
	}
	return normalized, jsonOutput
}

func extractRootArgument(args []string) ([]string, string, error) {
	result := make([]string, 0, len(args))
	root := ""
	for index := 0; index < len(args); index++ {
		if args[index] != "--root" {
			result = append(result, args[index])
			continue
		}
		if index+1 >= len(args) || strings.HasPrefix(args[index+1], "-") {
			return nil, "", fmt.Errorf("--root requires a directory")
		}
		if root != "" {
			return nil, "", fmt.Errorf("--root may only be specified once")
		}
		index++
		root = args[index]
	}
	return result, root, nil
}

func supportsJSONProtocol(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "new", "generate", "g", "inspect", "explain", "verify", "doctor", "capabilities", "schema", "context":
		return true
	case "contract":
		if len(args) < 2 {
			return true
		}
		switch args[1] {
		case "check", "diff", "probe":
			return true
		}
	}
	return false
}

func writeProtocolResult(args []string, commandCode int, stdout []byte, stderr string, out, errOut io.Writer) int {
	result := protocolResult{
		SchemaVersion: protocolSchemaVersion,
		OK:            commandCode == 0,
		Command:       protocolCommand(args),
		ExitCode:      protocolExitCode(args, commandCode, stderr, stdout),
	}
	trimmedOut := strings.TrimSpace(string(stdout))
	if trimmedOut != "" {
		var payload any
		if err := json.Unmarshal([]byte(trimmedOut), &payload); err == nil {
			result.Result = payload
		} else {
			result.Result = map[string]any{"output": trimmedOut}
		}
	}
	if commandCode != 0 {
		message := strings.TrimSpace(stderr)
		if message == "" {
			message = "command failed"
		}
		result.Error = classifyProtocolError(message, result.ExitCode)
	}
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		fmt.Fprintln(errOut, err)
		return 10
	}
	return result.ExitCode
}

func protocolCommand(args []string) string {
	var command []string
	for _, argument := range args {
		if strings.HasPrefix(argument, "-") {
			break
		}
		command = append(command, argument)
		if len(command) == 2 {
			break
		}
	}
	return strings.Join(command, " ")
}

func protocolExitCode(args []string, commandCode int, stderr string, stdout []byte) int {
	if commandCode == 0 {
		return 0
	}
	if commandWithoutRoot(args) == "new" {
		return commandCode
	}
	lower := strings.ToLower(stderr)
	switch {
	case strings.Contains(lower, "usage:"), strings.Contains(lower, "unknown option"), strings.Contains(lower, "unknown command"), strings.Contains(lower, "requires"), strings.Contains(lower, "does not support --json"):
		return 2
	case strings.Contains(lower, "already exists"), strings.Contains(lower, "lock"), strings.Contains(lower, "changed since"), strings.Contains(lower, "overwrite"):
		return 4
	case strings.Contains(lower, "contract"), strings.Contains(lower, "validation"):
		return 5
	case protocolCommand(args) == "verify":
		return 5
	case (protocolCommand(args) == "context" || protocolCommand(args) == "doctor") && len(stdout) > 0:
		return 5
	case strings.HasPrefix(protocolCommand(args), "contract ") && len(stdout) > 0:
		return 5
	default:
		return 3
	}
}

func classifyProtocolError(message string, exitCode int) *protocolError {
	code := "PRECONDITION_FAILED"
	hint := "Inspect the error and project context, then retry with corrected inputs."
	switch exitCode {
	case 2:
		code = "USAGE_ERROR"
		hint = "Run `spine schema --json` to inspect valid arguments and options."
	case 4:
		code = "CONFLICT"
		hint = "Create a new dry-run plan and retry only after reviewing the changed files."
	case 10:
		code = "INTERNAL_ERROR"
		hint = "Inspect the error details before retrying."
	case 5:
		code = "VALIDATION_FAILED"
		hint = "Run `spine context --json` and inspect contract diagnostics."
	}
	return &protocolError{Code: code, Message: message, Retryable: exitCode != 10, Hint: hint}
}

// root 파싱 오류가 있어도 전역 옵션의 값을 명령 이름으로 오인하지 않습니다.
func commandWithoutRoot(args []string) string {
	for index := 0; index < len(args); index++ {
		if args[index] == "--root" {
			index++
			continue
		}
		return args[index]
	}
	return ""
}
