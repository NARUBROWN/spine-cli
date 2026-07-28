package cli

import (
	"fmt"
	"io"
)

const version = "0.1.1"

// Run executes the spine project generator command line interface.
func Run(args []string, out, errOut io.Writer) int {
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
  spine new <project> [--module <module-path>]
  spine generate resource <name> [--no-register]
  spine generate slice <name> --storage memory --field name:string
  spine generate consumer <name> --topic <topic> --field name:string
  spine generate websocket <name> --path </path> --field message:string
  spine generate controller <name>
  spine generate service <name>
  spine generate repository <name>

Aliases:
  spine g resource <name>

Examples:
  spine new todo-api --module github.com/acme/todo-api
  cd todo-api
  spine g resource users
  spine g consumer orders --topic order.created --field order_id:int64
  spine g websocket chat --path /ws/chat --field message:string
  spine dev
  spine inspect
  spine explain http GET /users/:id
  spine verify
  spine contract check --json
  spine contract snapshot
  spine contract diff
  spine contract probe
  spine doctor
  spine capabilities --json
  spine check
  go run .`)
}
