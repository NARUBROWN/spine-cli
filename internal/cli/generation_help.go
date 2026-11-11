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
			fmt.Fprintln(out, "Usage: spine new <project> [--module <module-path>] [--layout layered-v1|legacy] [--channel preview|rc|main] [--platform provider|none] [--event-platform [name:]provider ...] [--validator none|provider] [--websocket-platform none|provider] [--frontend none|provider] [--frontend-root <path>] [--interactive|--no-interactive] [--skip-install] [--dry-run] [--json] [--if-match <hash>] [--force]")
			fmt.Fprintln(out, "\nWhen run in a terminal, the wizard selects the dependency channel, HTTP adapter, event adapters, WebSocket provider, validator, and frontend in that order. Defaults: main channel, layered-v1, HTTP echo, no events, no WebSocket, no validator, and no frontend. Enter a choice number or search by name. Unlisted provider names follow the provider module contract.\n--channel: preview resolves moving @preview branches, rc selects the latest published RC or release tag, and main selects the latest published release tag. The selected channel is stored in spine.toml and reused by spine update.\n--interactive: start the wizard for non-terminal input. It cannot be combined with --json. --no-interactive: skip the wizard, including in a terminal.\n--frontend: React CSR is built in. Other providers resolve github.com/NARUBROWN/spine-frontend-<provider> from the selected channel and read spine-frontend.json. --frontend-root defaults to frontend.\n--websocket-platform requires an HTTP provider. --event-platform can be repeated. --platform none creates an event worker. playground requires Go 1.26.0 or later.\n--skip-install: resolve versions and metadata, then generate files without installing dependencies or verifying the build. Run go mod tidy afterward. When a frontend is selected, also run npm --prefix <frontend-root> install.\n--force: available only with --skip-install.\n\nExamples:\n  spine new shop-api --module example.com/shop\n  spine new shop-api --module example.com/shop --channel rc\n  spine new shop-api --module example.com/shop --frontend react --frontend-root web")
			return true, 0
		}
		usage, ok := generatorUsage(kind)
		if !ok {
			fmt.Fprintf(errOut, "unknown generator: %s\n", kind)
			return true, 1
		}
		fmt.Fprintln(out, "Usage: spine g "+usage)
		fmt.Fprintln(out, "\nOptions: --dry-run --json --if-match <hash> --force")
		if generatorRegisters(kind) {
			fmt.Fprintln(out, "Registration options: --register --no-register")
		}
		fmt.Fprintln(out, "Global option: --root <directory>")
		printGeneratorGuide(kind, out)
		return true, 0
	}
	return false, 0
}

func generationValueOption(argument string) bool {
	switch argument {
	case "--module", "--layout", "--channel", "--platform", "--event-platform", "--validator", "--websocket-platform", "--frontend", "--frontend-root", "--if-match", "--field", "--depends", "--method", "--path", "--handler", "--topic", "--storage":
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

func generatorRegisters(kind string) bool {
	switch kind {
	case "", "repository", "service", "controller", "resource", "consumer", "websocket", "slice":
		return true
	}
	return false
}

func printGeneratorGuide(kind string, out io.Writer) {
	if kind == "" {
		fmt.Fprintln(out, "\nlayered-v1 roots: domain=database/domain, repository=src/repository, dto=src/dto, service=src/service, controller=src/controller, route=src/route/route.go.\nEach role accepts an independent optional group. Run spine g <kind> --help for focused examples.\n\nExamples:\n  spine g domain account/user --field id:int64\n  spine g repository account/user\n  spine g route get-user --method GET --path /users/:id --handler src/controller/account:UserController.Get")
		return
	}
	examples := []string{}
	if role, ok := roleFor(kind); ok {
		fmt.Fprintf(out, "\nlayered-v1 output: files under %s/[group/]. The group is optional.\n", role.Root)
		if role.Constructor {
			fmt.Fprintf(out, "Generated type names use the %s suffix and connect constructors to DI registration. --no-register generates only the file.\n--depends accepts a module-relative package path and type.\nIn legacy layouts, --depends accepts a file name from the same package.\n", role.Suffix)
		} else {
			fmt.Fprintln(out, "Only the specified fields are generated. The type is not registered with DI. Domain and DTO generators require layered-v1.")
		}
	}
	switch kind {
	case "domain":
		examples = []string{"spine g domain user --field id:int64", "spine g domain account/user --field name:string"}
	case "dto":
		fmt.Fprintln(out, "DTO fields include JSON tags. On current Spine, receive HTTP body arguments as input.Body[CreateUserRequest].")
		examples = []string{"spine g dto create-user-request --field name:string", "spine g dto account/create-user-request --field name:string"}
	case "repository":
		examples = []string{"spine g repository user", "spine g repository account/user"}
	case "service":
		examples = []string{"spine g service user", "spine g service account/user --depends src/repository/account:UserRepository"}
	case "controller":
		fmt.Fprintln(out, "A controller generates a struct and constructor. Implement the method, then register its HTTP path with spine g route.\nReturn successful responses with httpx.Response[T], httpx.Binary, or httpx.Redirect. Return failures as error.")
		examples = []string{"spine g controller user", "spine g controller admin/user --depends src/service/account:UserService"}
	case "resource":
		fmt.Fprintln(out, "In layered-v1, this generates domain, repository, service, and controller types in the same group and registers them with DI. Add DTOs and HTTP paths separately.")
		examples = []string{"spine g resource user", "spine g resource account/user --no-register"}
	case "route":
		fmt.Fprintln(out, "In layered-v1, core v0.7.0-preview.5 or later registers routes in the controller's RegisterRoutes method. Older cores use src/route/route.go. Implement the handler method first.\n--handler uses module-relative package/path:Type.Method syntax. With a supporting Preview core, declare one scalar input.Path argument for each route key in route order. Use input.Body[T], input.Query[T] and input.Header[T] for body, query and header DTOs; use input.Path[T] for path DTOs.\nSuccessful returns: httpx.Response[T], httpx.Binary, or httpx.Redirect. Failure returns: error or (httpx.Response[T], error).\nWrap raw DTO, slice, and string values in httpx.Response[T].Body.")
		examples = []string{"spine g route create-user --method POST --path /users --handler src/controller:UserController.Create", "spine g route get-user --method GET --path /users/:id --handler src/controller/account:UserController.Get"}
	case "consumer":
		fmt.Fprintln(out, "Output: src/consumer/[group/]name. The generator registers the constructor and receive contract. Replace the unimplemented error with application logic before verification.")
		examples = []string{"spine g consumer order --topic order.created --field order_id:int64", "spine g consumer account/order --topic order.created --field order_id:int64"}
	case "websocket":
		fmt.Fprintln(out, "Output: src/websocket/[group/]name. The generator registers the constructor and WebSocket path. Replace the unimplemented error with application logic before verification.")
		examples = []string{"spine g websocket chat --path /ws/chat --field message:string", "spine g websocket account/chat --path /ws/chat --field message:string"}
	case "slice":
		fmt.Fprintln(out, "Available only for legacy layouts. Use the role-specific generators in new layered-v1 applications.")
		examples = []string{"spine g slice order --storage memory --field name:string"}
	}
	if kind != "slice" {
		fmt.Fprintln(out, "\nThe following examples use layered-v1.")
	}
	fmt.Fprintln(out, "\nExamples:")
	for _, example := range examples {
		fmt.Fprintln(out, "  "+example)
	}
	fmt.Fprintln(out, "\nApply directly or use --dry-run to inspect changes first. Use the JSON plan and --if-match to automate application of a reviewed plan.\nNew applications use layered-v1. Existing applications without a layout keep legacy rules.")
}
