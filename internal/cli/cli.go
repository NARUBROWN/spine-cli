package cli

import (
	"fmt"
	"io"
)

const version = "0.1.0"

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
		fmt.Fprintln(out, "spine", version)
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
  spine generate controller <name>
  spine generate service <name>
  spine generate repository <name>

Aliases:
  spine g resource <name>

Examples:
  spine new todo-api --module github.com/acme/todo-api
  cd todo-api
  spine g resource users
  spine dev
  spine check
  go run .`)
}
