# Spine CLI

Spine CLI는 [Spine](https://github.com/NARUBROWN/spine) 애플리케이션을 생성하고,
소스 코드에서 실행 계약을 검사하고 설명하며 검증하는 Go CLI입니다.

프로젝트 이름은 `spine-cli`입니다. 루트 경로로 설치하면 실행 파일은 `spine-cli`,
`cmd/spine` 경로로 설치하면 실행 파일과 명령은 `spine`입니다.

- 프로젝트와 domain, DTO, repository, service, controller, 메시지 구성 요소 생성
- HTTP, DI, interceptor, consumer, WebSocket 실행 계약 검사
- 계약 검증, snapshot/diff, OpenAPI와 AsyncAPI 내보내기

## 권장 환경과 설치

- 권장 Go 버전: 1.27.3
- 새 프로젝트가 사용하는 Spine 버전: v0.5.1

```sh
go install github.com/NARUBROWN/spine-cli@v0.1.4
spine-cli version
# spine-cli 0.1.4
```

짧은 `spine` 명령을 사용하려면 기존 `cmd/spine` 경로로 설치합니다.

```sh
go install github.com/NARUBROWN/spine-cli/cmd/spine@v0.1.4
spine version
# spine-cli 0.1.4
```

## 빠른 시작

```sh
spine new todo-api --module github.com/acme/todo-api
cd todo-api
go mod tidy
spine dev
```

다른 터미널에서 생성된 health endpoint를 확인합니다.

```sh
curl http://localhost:8080/health
```

새 프로젝트에는 명시적인 `BuildApplication` composition root, `app.Validate`,
`GET /health`, `spine.toml`이 포함됩니다.

## 생성기

새 프로젝트는 `layout = "layered-v1"`을 사용합니다. 역할별 기본 경로 아래에 파일을 바로
두거나, 이름 앞에 선택적인 하위 경로를 지정할 수 있습니다.

| 역할 | 기본 경로 | 생성 내용 |
| --- | --- | --- |
| domain | `database/domain/` | 명시한 필드만 포함하는 타입 |
| repository | `database/repository/` | 구조체와 생성자 |
| DTO | `src/dto/` | JSON 태그가 있는 타입 |
| service | `src/service/` | 구조체와 생성자 |
| controller | `src/controller/` | 구조체와 생성자 |
| route | `src/route/route.go` | 명시한 HTTP 메서드와 경로 등록 |
| application | `internal/application/` | 생성자와 등록 함수 연결 |

```sh
spine g domain account/user --field id:int64 --field name:string
spine g dto account/create-user-request --field name:string
spine g repository account/user
spine g service user --depends database/repository/account:UserRepository
spine g controller admin/user --depends src/service:UserService
```

`domain user`는 `database/domain/user.go`, `domain account/user`는
`database/domain/account/user.go`를 생성합니다. `commerce/account/user`처럼 여러 단계로
묶을 수도 있습니다. 그룹은 역할마다 독립적으로 정하며 실행 위치는 생성 경로에 영향을 주지 않습니다.
CLI는 그룹을 애그리거트로 해석하지 않습니다.

`--depends`는 모듈 기준 패키지 경로와 타입을 받습니다. 여러 의존성은 옵션을 반복해서 지정합니다.
CLI는 실제 타입과 단일 공개 생성자를 확인하고 import와 생성자 인자와 application 등록을 연결합니다.
서로 다른 패키지의 동명 타입도 구분합니다. domain과 DTO는 DI에 등록하지 않습니다.
repository에는 DB 연결이나 CRUD 구현을 추가하지 않습니다.

controller 생성만으로 HTTP 경로가 생기지는 않습니다. 실제 `Get` 메서드를 작성한 뒤 등록합니다.

```sh
# src/controller/admin 패키지에 UserController.Get 메서드가 있을 때 실행
spine g route get-user --method GET --path /users/:id \
  --handler src/controller/admin:UserController.Get
```

`Get`은 경로의 `id`를 받는 핸들러여야 합니다. 예를 들어 Spine의 `path.Int` 매개변수를 사용할 수
있습니다. CLI는 메서드 존재와 경로 바인딩과 기존 경로 충돌을 확인합니다.

`spine g resource account/user`는 같은 그룹의 domain, repository, service, controller 생성을
조합합니다. DTO와 HTTP 경로는 별도로 추가합니다. 생성자는 기본적으로 application에 등록하며,
파일만 생성하려면 `--no-register`를 사용합니다.

consumer와 WebSocket 생성기도 사용할 수 있습니다.

```sh
spine g consumer order --topic order.created --field order_id:int64
spine g websocket chat --path /ws/chat --field message:string
```

생성한 메시지 핸들러는 `not implemented` 오류를 반환합니다. 실제 로직을 작성하고 이 오류를
제거한 뒤 검증하세요. transport와 재시도와 WebSocket 접근 정책은 애플리케이션에서 구성합니다.

`layout`이 없는 기존 프로젝트와 `layout = "legacy"` 프로젝트에는 이전 생성 규칙을 적용합니다.
기존 파일을 이동하거나 구조를 자동 전환하지 않습니다. 이전 구조의 재현이 필요하면
`spine new <project> --layout legacy`를 사용할 수 있습니다. `slice --storage memory`는
legacy에서만 지원하며, 새 구조에 CRUD 생성 기능을 추가하지 않습니다.

## 실행 계약

```sh
# 현재 등록 계약 조회와 단일 실행 경로 설명
spine inspect
spine explain http GET /orders/:id
spine explain consumer order.created
spine explain websocket /ws/orders

# CI 또는 로컬 검증
spine contract check --warnings-as-errors
spine verify

# 의미 단위 계약 변경 추적
spine contract snapshot
spine contract diff

# API 문서 생성
spine contract export --format openapi --output openapi.json
spine contract export --format asyncapi --output asyncapi.json
```

검증 결과는 다음 경계를 가집니다.

- `inspect`, `explain`, `contract check`는 정적 소스 분석입니다.
- 등록 이름의 문자열·문자열 연결·직접적인 `os.Getenv("이름")` 호출과, 이 문자열의 `==`/`!=` 조건은 검사 프로세스의 환경변수로 해석합니다. 검사와 앱 실행에 같은 값을 전달해야 합니다.
- 임의 함수·지역 변수·환경변수 변경 호출은 평가하지 않으며, 해석하지 못한 등록은 계속 오류로 처리합니다.
- 분석할 수 없는 조건부·동적 등록은 오류와 `analysis_complete: false`로 표시합니다.
- `contract probe`는 `BuildApplication`, `app.Validate`, consumer/WebSocket 공개 registry만 대조합니다.
- `verify` 성공은 계약·gofmt·vet·test의 로컬 통과를 뜻합니다. DB, broker, listener,
  배포 환경의 정상 동작은 별도의 E2E 검증이 필요합니다.

## 개발 명령

| 명령 | 동작 |
| --- | --- |
| `spine dev` | 변경을 감지하고, 새 빌드가 성공한 경우에만 앱을 교체 |
| `spine run` | `ENV=production`으로 빌드하고 실행 |
| `spine test` | `go test ./...` 실행 |
| `spine check` | gofmt 상태, `go vet`, `go test` 검사 |
| `spine verify` | 정적 계약 검사 후 `spine check` 수행 |
| `spine build` | `bin/<모듈명>`에 바이너리 생성 |
| `spine doctor` | 도구 체인, 설정, 정적 계약을 오프라인 진단 |
| `spine completion <shell>` | bash, zsh, PowerShell completion 생성 |

## 프로젝트 설정

`spine.toml`은 CLI가 읽는 프로젝트 설정 파일입니다.

```toml
module = "github.com/acme/todo-api"
entrypoint = "."
composition = "./internal/application"
port = 8080
layout = "layered-v1"
```

`module`은 `go.mod`의 module path와 같아야 합니다.

## 안전한 생성

생성기는 기존 파일을 기본적으로 덮어쓰지 않습니다. 먼저 변경 계획을 확인하고,
의도적인 교체에만 `--force`를 사용하세요.

```sh
spine g resource orders --dry-run
spine g resource orders --dry-run --json
spine g resource orders --if-match <base_hash> --json
```

JSON dry-run에는 모든 변경 파일과 diff와 SHA-256, `base_hash`가 포함됩니다.
새 구조의 `g`는 대상 파일뿐 아니라 분석한 Go 소스와 모듈 파일과 `spine.toml`, 생성할 내용도
해시에 포함합니다. 소스의 직접적인 `os.Getenv("이름")` 호출이 참조하는 환경 값도 변경 검사에
포함하며 값 자체는 출력하지 않습니다. 검토 후 입력이나 계획이 달라지면 `--if-match` 적용을 중단합니다.
동일한 요청은 import와 생성자와 경로를 중복 등록하지 않습니다.

## AI Agent에서 사용하기

명령 스키마, JSON 프로토콜, 안전한 생성 절차, 정확한 증거 범위와 저장소 구조는
[`llms.txt`](./llms.txt)를 참고하세요.

```sh
spine schema --json
spine context --json
```

## 저장소 개발

```sh
GOCACHE=/tmp/spine-cli-gocache go test -race ./...
GOCACHE=/tmp/spine-cli-gocache go vet ./...
git diff --check
```

## License

[MIT](./LICENSE)
