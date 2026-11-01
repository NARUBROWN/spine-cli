package cli

import (
	"fmt"
	"io"
	"strings"
)

// generationHelp는 프로젝트나 생성 대상을 읽기 전에 도움말 요청을 처리합니다.
func generationHelp(command string, args []string, out, errOut io.Writer) (bool, int) {
	kind := ""
	for index, argument := range args {
		if argument != "--help" && argument != "-h" {
			continue
		}
		// 옵션 값으로 전달한 문자열은 도움말 요청으로 해석하지 않습니다.
		if index > 0 && generationValueOption(args[index-1]) {
			continue
		}
		if command == "generate" && len(args) > 0 && !strings.HasPrefix(args[0], "-") {
			kind = args[0]
		}
		if command == "new" {
			fmt.Fprintln(out, "Usage: spine new <project> [--module <module-path>] [--layout layered-v1|legacy] [--platform echo] [--skip-install] [--dry-run] [--json] [--if-match <hash>] [--force with --skip-install]")
			return true, 0
		}
		usage, ok := generatorUsage(kind)
		if !ok {
			fmt.Fprintf(errOut, "unknown generator: %s\n", kind)
			return true, 1
		}
		fmt.Fprintln(out, "Usage: spine g "+usage)
		fmt.Fprintln(out, "\nOptions: --dry-run --json --if-match <hash> --force")
		if kind != "route" {
			fmt.Fprintln(out, "Registration options: --register --no-register")
		}
		fmt.Fprintln(out, "Global option: --root <directory>")
		fmt.Fprintln(out, "\nNew projects use layered-v1 role roots and optional nested groups.\nLegacy projects retain their existing generator rules; slice is legacy-only.")
		return true, 0
	}
	return false, 0
}

func generationValueOption(argument string) bool {
	switch argument {
	case "--module", "--layout", "--platform", "--if-match", "--field", "--depends", "--method", "--path", "--handler", "--topic", "--storage":
		return true
	}
	return false
}

func generatorUsage(kind string) (string, bool) {
	switch kind {
	case "":
		return "<domain|dto|repository|service|controller|resource|route|consumer|websocket|slice> <name> [options]", true
	case "domain", "dto":
		return kind + " <[group/]name> [--field name:type ...]", true
	case "repository", "service", "controller":
		return kind + " <[group/]name> [--depends package/path:Type ...]", true
	case "resource":
		return "resource <[group/]name> [--no-register]", true
	case "route":
		return "route <name> --method <HTTP-method> --path </path> --handler <package/path:Type.Method>", true
	case "consumer":
		return "consumer <[group/]name> --topic <topic> --field name:type ...", true
	case "websocket":
		return "websocket <[group/]name> --path </path> --field name:type ...", true
	case "slice":
		return "slice <name> --storage memory --field name:type ... (legacy only)", true
	}
	return "", false
}
