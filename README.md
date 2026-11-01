# Spine CLI

Spine CLI는 [Spine](https://github.com/NARUBROWN/spine) 애플리케이션을 생성하고,
소스 코드에서 실행 계약을 검사하고 설명하며 검증하는 Go CLI입니다.

프로젝트 이름은 `spine-cli`입니다. 루트 경로로 설치하면 실행 파일은 `spine-cli`,
`cmd/spine` 경로로 설치하면 실행 파일과 명령은 `spine`입니다.

- 프로젝트와 domain, DTO, repository, service, controller, 메시지 구성 요소 생성
- HTTP, DI, interceptor, consumer, WebSocket 실행 계약 검사
- 계약 검증, snapshot/diff, OpenAPI와 AsyncAPI 내보내기

## 권장 환경과 설치

- 최소 Go 버전: 1.25.5
- 새 프로젝트의 플랫폼: Echo 개발 레시피 `echo-dev-ded538a25193`
- Spine: `v0.5.3-0.20261101161224-490135eda06f`
- Echo 플랫폼: `v0.0.0-20261101230311-ded538a25193` (Echo v5.4.4)

```sh
go install github.com/NARUBROWN/spine-cli@v0.2.0
spine-cli version
# spine-cli 0.2.0
```

짧은 `spine` 명령을 사용하려면 기존 `cmd/spine` 경로로 설치합니다.

```sh
go install github.com/NARUBROWN/spine-cli/cmd/spine@v0.2.0
spine version
# spine-cli 0.2.0
```

## 빠른 시작

```sh
spine new todo-api --module github.com/acme/todo-api
cd todo-api
spine dev
```

다른 터미널에서 생성된 health endpoint를 확인합니다.

```sh
curl http://localhost:8080/health
```

현재 개발 브랜치의 `new`는 Echo 플랫폼 연결과 의존성 설치, 정적 계약 검사와 빌드를
완료한 뒤 프로젝트를 생성합니다. 서버는 `spine dev`로 시작합니다. 설치 과정에서는
애플리케이션 초기화, 테스트나 listener를 실행하지 않습니다. 위 설치 예시의 v0.2.0 릴리스에는
이 플랫폼 통합이 아직 포함되지 않았습니다.

파일만 생성하려면 `spine new todo-api --skip-install`을 사용하고, 생성 후 `go mod tidy`를
실행하세요. `--platform echo`는 기본 플랫폼을 명시합니다. 설치 포함 `--dry-run`은 정확한
의존성 계획을 위해 네트워크와 Go 캐시를 사용할 수 있으며, 오프라인 계획은
`--skip-install --dry-run`으로 확인합니다. `--force`는 `--skip-install`과 함께만 허용합니다.
활성 Go workspace는 설치 범위 밖이므로 `GOWORK=off`로 실행하거나 설치를 생략하세요.

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

`spine g route --help`처럼 생성기별 도움말을 확인할 수 있습니다. `--help`와 `-h`는
프로젝트 설정을 읽거나 파일을 생성하지 않습니다.

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

HTTP 요청 DTO는 `Create(req *CreateProductRequest)`처럼 구조체 포인터로 받습니다.
값 DTO나 기본 resolver가 처리하지 못하는 인자는 경로 생성과 계약 검사에서 오류로 진단합니다.
Spine wrapper도 포인터로 받으면 본문 DTO로 해석되며, 경로 인자는 `path.Int`처럼 값으로 받습니다.
외부 타입이나 사용자 정의 resolver의 지원 여부를 소스만으로 확인할 수 없으면 분석 불완전으로
표시합니다. 이는 실제 HTTP 요청을 실행한 결과와 구분됩니다.

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

`platform.New()`, `spine.New(platform.Option())`,
`spine.New(spine.WithHTTPAdapter(platform.NewFactory()))`와
`spine.NewWithHTTPAdapter(platform.NewFactory())`를 같은 Echo 선택으로 인식합니다.
`inspect`의 `http_adapter`는 소스에서 확인한 선택과 오프라인 모듈 그래프의 실제 버전을
보여줍니다. 런타임 factory를 실행해 관찰한 결과는 아닙니다. 새 커널에서 HTTP를 활성화하고
factory를 빠뜨리면 `HTTP_ADAPTER_MISSING`으로 진단합니다. 기존 v0.5.2 앱은 내장 Echo를
`legacy-implicit`으로 표시합니다.

계약 payload는 `spine.contract/v1alpha2`입니다. 이전 snapshot은 현재 계약을 검토한 뒤
`spine contract snapshot --force`로 다시 생성해야 합니다. CLI 바깥 JSON envelope는
`spine.cli-result/v1`을 유지합니다.

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
- 분석할 수 없는 조건부 등록과 동적 등록, 현재 모듈 밖의 `Register(app)` 호출은 오류와
  `analysis_complete: false`로 표시합니다.
- `contract probe`는 `BuildApplication`, `app.Validate`, consumer/WebSocket 공개 registry만 대조합니다.
- `verify` 성공은 계약·gofmt·vet·test의 로컬 통과를 뜻합니다. DB, broker, listener,
  배포 환경의 정상 동작은 별도의 E2E 검증이 필요합니다.

OpenAPI와 AsyncAPI는 타입을 선언한 패키지와 import 범위를 기준으로 스키마를 구분합니다.
JSON 태그가 없으면 Go 필드 이름을 그대로 사용하고, `time.Time`은 `string`과 `date-time`으로
표현합니다. 해석할 수 없는 타입은 `x-spine-unresolved-type`으로 표시합니다. 이 표시는 문서의
분석 한계이며 실제 API의 요청 필드가 아닙니다. AsyncAPI의 채널과 operation 식별자는 원래 이름을
구분하도록 만들며, 문서에 기록된 참조를 사용해야 합니다.

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

`run`, `test`, `check`, `build`는 `--help` 또는 `-h`로 도움말을 확인할 수 있습니다.
그 밖의 명령별 옵션은 실행 전에 오류로 처리합니다. 테스트 필터는 `go test ./... -run <이름>`으로
직접 실행하세요.

`run`은 매번 `go build`를 호출해 Go 자체의 빌드 캐시와 로컬 의존성 변경을 반영합니다.
`dev`는 Go가 열거한 소스와 embed 파일, 모듈과 프로젝트 설정을 감시합니다. 일반 로그 기록은
재시작 조건에서 제외하며 유효한 설정 변경 시 새 포트를 전달합니다. SIGTERM에서도 자식 앱을
종료하고 회수합니다.

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
일반 dry-run도 `Base hash:`와 `--if-match` 적용 안내를 출력합니다. 검토한 명령에서
`--dry-run`을 제거하고 출력된 해시를 `--if-match`에 전달하면 같은 계획을 적용할 수 있습니다.
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
