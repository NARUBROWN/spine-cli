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
			fmt.Fprintln(out, "Usage: spine new <project> [--module <module-path>] [--layout layered-v1|legacy] [--platform echo|none] [--event-platform [name:]provider ...] [--validator none|provider] [--websocket-platform none|gorilla] [--skip-install] [--dry-run] [--json] [--if-match <hash>] [--force]")
			fmt.Fprintln(out, "\n기본값: layered-v1, HTTP echo, WebSocket none, validator none. --websocket-platform gorilla는 독립 WS 구현을 선택하며 HTTP echo가 필요합니다. 공개 pseudo-version 레시피의 의존성 설치와 정적 검사/빌드. 이벤트는 --event-platform을 반복해 선택합니다. --platform none은 이벤트 worker입니다. playground는 Go 1.26.0 이상이 필요합니다. 생성 후 cd <project>와 spine dev를 실행합니다.\n--skip-install: 파일만 생성. 이때는 go mod tidy 후 spine dev를 실행합니다.\n--force: --skip-install과 함께만 사용합니다.\n\n예:\n  spine new shop-api --module example.com/shop\n  spine new shop-api --module example.com/shop --skip-install")
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
	case "--module", "--layout", "--platform", "--event-platform", "--validator", "--websocket-platform", "--if-match", "--field", "--depends", "--method", "--path", "--handler", "--topic", "--storage":
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
		fmt.Fprintln(out, "\nlayered-v1 기본 경로: domain=database/domain, repository=src/repository, dto=src/dto, service=src/service, controller=src/controller, route=src/route/route.go.\n그룹은 역할마다 독립적으로 선택합니다. g <kind> --help에서 예제를 확인하세요.\n\n예:\n  spine g domain account/user --field id:int64\n  spine g repository account/user\n  spine g route get-user --method GET --path /users/:id --handler src/controller/account:UserController.Get")
		return
	}
	examples := []string{}
	if role, ok := roleFor(kind); ok {
		fmt.Fprintf(out, "\nlayered-v1 생성 위치: %s/[group/] 파일. 그룹은 선택 사항입니다.\n", role.Root)
		if role.Constructor {
			fmt.Fprintf(out, "타입 이름에는 %s를 붙이고 생성자와 DI 등록을 연결합니다. --no-register는 파일만 생성합니다.\n--depends는 실제 모듈 상대 패키지 경로와 타입을 받습니다.\nlegacy의 --depends는 같은 패키지의 파일 이름을 받습니다.\n", role.Suffix)
		} else {
			fmt.Fprintln(out, "명시한 필드만 생성하며 DI에 등록하지 않습니다. domain과 DTO 생성기는 layered-v1에서 사용합니다.")
		}
	}
	switch kind {
	case "domain":
		examples = []string{"spine g domain user --field id:int64", "spine g domain account/user --field name:string"}
	case "dto":
		fmt.Fprintln(out, "DTO는 JSON 태그를 생성합니다. HTTP 본문 인자는 *CreateUserRequest처럼 포인터로 받습니다.")
		examples = []string{"spine g dto create-user-request --field name:string", "spine g dto account/create-user-request --field name:string"}
	case "repository":
		examples = []string{"spine g repository user", "spine g repository account/user"}
	case "service":
		examples = []string{"spine g service user", "spine g service account/user --depends src/repository/account:UserRepository"}
	case "controller":
		fmt.Fprintln(out, "controller는 구조체와 생성자를 만듭니다. 메서드를 작성한 뒤 g route로 HTTP 경로를 등록합니다.\n성공 응답은 httpx.Response[T], httpx.Binary, httpx.Redirect를 사용하고 실패는 error로 반환합니다.")
		examples = []string{"spine g controller user", "spine g controller admin/user --depends src/service/account:UserService"}
	case "resource":
		fmt.Fprintln(out, "layered-v1: 같은 그룹의 domain/repository/service/controller와 DI 등록을 생성합니다. DTO와 HTTP 경로는 별도로 추가합니다.")
		examples = []string{"spine g resource user", "spine g resource account/user --no-register"}
	case "route":
		fmt.Fprintln(out, "layered-v1 등록 위치: src/route/route.go. 실제 핸들러 메서드를 먼저 작성합니다.\n--handler는 모듈 상대 package/path:Type.Method입니다. Preview 코어에서는 path 태그가 있는 구조체 포인터로 :id를 받습니다. 본문, query와 header도 출처별 DTO 포인터로 분리합니다.\n성공 응답: httpx.Response[T], httpx.Binary, httpx.Redirect. 실패 응답: error 또는 (httpx.Response[T], error).\nraw DTO/string은 직접 반환하지 말고 httpx.Response[T].Body로 감쌉니다.")
		examples = []string{"spine g route create-user --method POST --path /users --handler src/controller:UserController.Create", "spine g route get-user --method GET --path /users/:id --handler src/controller/account:UserController.Get"}
	case "consumer":
		fmt.Fprintln(out, "생성 위치: src/consumer/[group/]name. 생성자와 수신 계약을 등록합니다. 미구현 오류를 업무 코드로 바꾼 뒤 검증하세요.")
		examples = []string{"spine g consumer order --topic order.created --field order_id:int64", "spine g consumer account/order --topic order.created --field order_id:int64"}
	case "websocket":
		fmt.Fprintln(out, "생성 위치: src/websocket/[group/]name. 생성자와 WS 경로를 등록합니다. 미구현 오류를 업무 코드로 바꾼 뒤 검증하세요.")
		examples = []string{"spine g websocket chat --path /ws/chat --field message:string", "spine g websocket account/chat --path /ws/chat --field message:string"}
	case "slice":
		fmt.Fprintln(out, "legacy 전용입니다. 새 layered-v1 앱에서는 역할별 생성기를 사용합니다.")
		examples = []string{"spine g slice order --storage memory --field name:string"}
	}
	if kind != "slice" {
		fmt.Fprintln(out, "\n다음 예제는 layered-v1 기준입니다.")
	}
	fmt.Fprintln(out, "\n예:")
	for _, example := range examples {
		fmt.Fprintln(out, "  "+example)
	}
	fmt.Fprintln(out, "\n직접 적용하거나 --dry-run으로 변경을 먼저 확인할 수 있습니다. JSON 계획과 --if-match는 자동화와 검토한 계획 적용에 사용합니다.\n새 앱은 layered-v1, 기존 layout 미지정 앱은 legacy 규칙을 사용합니다.")
}
