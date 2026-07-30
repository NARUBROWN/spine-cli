package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
)

const version = "0.1.3"

// Run executes the spine project generator command line interface.
func Run(args []string, out, errOut io.Writer) int {
	normalizedArgs, jsonOutput := normalizeJSONArguments(args)
	commandArgs, root, rootErr := extractRootArgument(normalizedArgs)
	if rootErr != nil {
		if jsonOutput {
			return writeProtocolResult(normalizedArgs, 1, nil, rootErr.Error(), out, errOut)
		}
		fmt.Fprintln(errOut, rootErr)
		return 1
	}
	if root != "" {
		previous, err := os.Getwd()
		if err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
		if err := os.Chdir(root); err != nil {
			if jsonOutput {
				return writeProtocolResult(commandArgs, 1, nil, fmt.Sprintf("could not enter --root %s: %v", root, err), out, errOut)
			}
			fmt.Fprintf(errOut, "could not enter --root %s: %v\n", root, err)
			return 1
		}
		defer os.Chdir(previous)
	}
	if jsonOutput && !supportsJSONProtocol(commandArgs) {
		command := protocolCommand(commandArgs)
		if command == "" {
			command = "this command"
		}
		return writeProtocolResult(commandArgs, 1, nil, fmt.Sprintf("%s does not support --json", command), out, errOut)
	}
	if !jsonOutput {
		return runCommand(commandArgs, out, errOut)
	}

	var commandOut, commandErr bytes.Buffer
	code := runCommand(commandArgs, &commandOut, &commandErr)
	return writeProtocolResult(commandArgs, code, commandOut.Bytes(), commandErr.String(), out, errOut)
}

func runCommand(args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		printHelp(out)
		return 0
	}

	switch args[0] {
	case "help", "--help", "-h":
		printHelp(out)
		return 0
	case "version", "--version", "-v":
		fmt.Fprintln(out, "spine-cli", version)
		return 0
	case "new":
		return runNew(args[1:], out, errOut)
	case "generate", "g":
		return runGenerate(args[1:], out, errOut)
	case "dev":
		return runDev(args[1:], out, errOut)
	case "run":
		return runProject(false, out, errOut)
	case "test":
		return runGo([]string{"test", "./..."}, false, out, errOut)
	case "check":
		return runCheck(out, errOut)
	case "inspect":
		return runInspect(args[1:], out, errOut)
	case "explain":
		return runExplain(args[1:], out, errOut)
	case "verify":
		return runVerify(args[1:], out, errOut)
	case "contract":
		return runContract(args[1:], out, errOut)
	case "doctor":
		return runDoctor(args[1:], out, errOut)
	case "capabilities":
		return runCapabilities(args[1:], out, errOut)
	case "schema":
		return runSchema(args[1:], out, errOut)
	case "context":
		return runContext(args[1:], out, errOut)
	case "build":
		return runBuild(out, errOut)
	case "completion":
		return runCompletion(args[1:], out, errOut)
	default:
		fmt.Fprintf(errOut, "unknown command: %s\n\n", args[0])
		printHelp(errOut)
		return 1
	}
}

func printHelp(w io.Writer) {
	fmt.Fprintln(w, `Spine CLI - scaffold explicit, execution-centric Spine applications.

Usage:
  spine new <project> [--module <module-path>] [--dry-run] [--json] [--if-match <hash>]
  spine generate resource <name> [--no-register]
  spine generate slice <name> --storage memory --field name:string
  spine generate consumer <name> --topic <topic> --field name:string
  spine generate websocket <name> --path </path> --field message:string
  spine generate controller <name>
  spine generate service <name>
  spine generate repository <name>
  spine schema [--json]
  spine context [--json]

Global options:
  --root <directory>  Run from an explicit project directory.

Aliases:
  spine g resource <name>

Examples:
  spine new todo-api --module github.com/acme/todo-api
  cd todo-api
  spine g resource users
  spine g consumer orders --topic order.created --field order_id:int64
  spine g websocket chat --path /ws/chat --field message:string
  spine dev --events jsonl
  spine inspect
  spine explain http GET /users/:id
  spine verify
  spine contract check --json
  spine contract snapshot
  spine contract diff
  spine contract probe
  spine doctor
  spine capabilities --json
  spine schema --json
  spine context --json
  spine check
  go run .`)
}
