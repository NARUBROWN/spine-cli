package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
)

const version = "0.4.0"

// Run은 Spine CLI 명령을 실행하고 요청한 출력 형식으로 결과를 전달합니다.
func Run(args []string, out, errOut io.Writer) int {
	normalizedArgs, jsonOutput := normalizeJSONArguments(args)
	commandArgs, root, rootErr := extractRootArgument(normalizedArgs)
	transactionCommand := commandWithoutRoot(normalizedArgs) == "new" || commandWithoutRoot(normalizedArgs) == "update"
	if rootErr != nil {
		code := 1
		if transactionCommand {
			code = 2
		}
		if jsonOutput {
			return writeProtocolResult(normalizedArgs, code, nil, rootErr.Error(), out, errOut)
		}
		fmt.Fprintln(errOut, rootErr)
		return code
	}
	if len(commandArgs) > 0 && commandArgs[0] == "update" {
		var help bytes.Buffer
		if updateHelp(commandArgs[1:], &help) {
			if jsonOutput {
				return writeProtocolResult(commandArgs, 0, help.Bytes(), "", out, errOut)
			}
			fmt.Fprint(out, help.String())
			return 0
		}
	}
	if len(commandArgs) > 0 && (commandArgs[0] == "new" || commandArgs[0] == "g" || commandArgs[0] == "generate") {
		command := commandArgs[0]
		if command == "g" {
			command = "generate"
		}
		var helpOut, helpErr bytes.Buffer
		if handled, code := generationHelp(command, commandArgs[1:], &helpOut, &helpErr); handled {
			if jsonOutput {
				return writeProtocolResult(commandArgs, code, helpOut.Bytes(), helpErr.String(), out, errOut)
			}
			fmt.Fprint(out, helpOut.String())
			fmt.Fprint(errOut, helpErr.String())
			return code
		}
	}
	if root != "" {
		previous, err := os.Getwd()
		if err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
		if err := os.Chdir(root); err != nil {
			code := 1
			if transactionCommand {
				code = 3
			}
			if jsonOutput {
				return writeProtocolResult(commandArgs, code, nil, fmt.Sprintf("could not enter --root %s: %v", root, err), out, errOut)
			}
			fmt.Fprintf(errOut, "could not enter --root %s: %v\n", root, err)
			return code
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
	// 실행 명령은 인자를 검사한 뒤에만 프로젝트를 읽거나 외부 명령을 시작합니다.
	switch args[0] {
	case "run", "test", "check", "build":
		if len(args) == 2 && (args[1] == "--help" || args[1] == "-h") {
			fmt.Fprintf(out, "Usage: spine %s [--root <directory>]\n\nThis command accepts no command-specific options. Use go test directly for Go test filters.\n", args[0])
			return 0
		}
		if len(args) > 1 {
			fmt.Fprintf(errOut, "unknown option or argument for %s: %s\nUsage: spine %s [--root <directory>]\n", args[0], args[1], args[0])
			return 1
		}
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
	case "update":
		return runUpdate(args[1:], out, errOut)
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
  spine new <project> [--module <module-path>] [--layout layered-v1|legacy] [--platform echo] [--skip-install] [--dry-run] [--json] [--if-match <hash>]
  spine update [--dry-run] [--if-match <hash>] [--json]
  spine g domain <[group/]name> [--field name:type]
  spine g dto <[group/]name> [--field name:type]
  spine g repository <[group/]name>
  spine g service <[group/]name> [--depends package/path:Type]
  spine g controller <[group/]name> [--depends package/path:Type]
  spine g route <name> --method GET --path /users --handler package/path:Type.Method
  spine g resource <[group/]name> [--no-register]
  spine generate consumer <name> --topic <topic> --field name:string
  spine generate websocket <name> --path </path> --field message:string
  spine schema [--json]
  spine context [--json]

Layout:
  New projects use layered-v1 role roots with optional nested groups.
  Existing projects without layout retain legacy rules, including slice generation.

Global options:
  --root <directory>  Run from an explicit project directory.

Aliases:
  spine g resource <name>

Examples:
  spine new todo-api --module github.com/acme/todo-api
  cd todo-api
  spine update --dry-run
  spine g resource users
  spine g consumer orders --topic order.created --field order_id:int64
  spine g websocket chat --path /ws/chat --field message:string
  spine dev --events jsonl
  spine inspect
  spine explain http GET /health
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
