# Spine CLI

Spine CLI는 [Spine](https://github.com/NARUBROWN/spine) 애플리케이션을 생성하고,
소스 코드에서 실행 계약을 검사하고 설명하며 검증하는 Go CLI입니다.

프로젝트 이름은 `spine-cli`입니다. 공식 설치 경로는 `github.com/NARUBROWN/spine-cli/cmd/spine`이며,
설치되는 실행 파일과 호출 명령은 `spine`입니다.

이 문서는 Preview 브랜치의 동작을 설명합니다. 일반 설치의 `@latest`는 정식 버전을 선택하므로,
이번 코어 Preview 대응은 해당 변경을 포함한 CLI 소스 또는 공개된 CLI Preview 태그로 확인하세요.

- 프로젝트와 domain, DTO, repository, service, controller, 메시지 구성 요소 생성
- HTTP, DI, interceptor, consumer, WebSocket 실행 계약 검사
- 계약 검증, snapshot/diff, OpenAPI와 AsyncAPI 내보내기

## 역할별 생성자 등록 후보

코어 `v0.7.0-preview.7` 후보를 대상으로 하는 생성기는 다섯 역할을 하나의 `app.Constructor(...)` 호출에 모읍니다. `constructor`의 import 경로는 `github.com/NARUBROWN/spine/pkg/constructor`입니다.

```go
app.Constructor(
    constructor.Custom(database.NewBunDB),
    constructor.Repository(&repository.UserRepository{}),
    constructor.Service(&service.UserService{}),
    constructor.Controller(&controller.UserController{}),
    constructor.Interceptor(&interceptor.TxInterceptor{}),
)
```

`Custom`은 기존 생성자 함수나 객체 표식을 받으며 나머지 역할은 객체 표식을 받습니다. CLI는 역할 계약과 의존성을 정적으로 검사합니다. 이전 코어의 생성 방식은 유지하며, 후보 소스 검증이 공개 코어와 CLI 태그의 설치 검증을 뜻하지는 않습니다.

## 권장 환경과 설치

- CLI와 검증기 없는 생성 앱: Go 1.25.5 이상
- `--validator playground`를 선택한 앱: Go 1.26.0 이상 (go-playground v10.30.6)
- 기본 HTTP 플랫폼: Echo
- Spine, Echo 플랫폼과 Echo 버전은 설치한 CLI에 포함된 검증 레시피가 선택합니다. 생성된 `go.mod`에서 확인하세요.

일반 설치에는 `@latest`를 사용합니다. 현재 프로젝트의 `go.mod`와 별개로 최신 공개 CLI를 설치하므로, 릴리스마다 설치 명령의 버전을 바꿀 필요가 없습니다.
기존 앱은 [버전별 마이그레이션 가이드](migration/)에서 대상 버전에 맞는 문서를 선택하세요.

아래 예제에서 사용하는 `spine` 명령을 설치합니다. 특정 버전이 필요하면
`@latest` 대신 `@v0.4.0`처럼 공개된 태그를 지정합니다.

```sh
go install github.com/NARUBROWN/spine-cli/cmd/spine@latest
spine version
```

### 이번 Preview 설치

컨트롤러 경로와 역할 표식 예제에는 CLI `v0.4.3-preview.8` 후보에 포함되는 기능이 필요합니다. 공개 발행 전에는 소스 조합으로 확인합니다. 해당 소스 체크아웃의 루트에서는 다음과 같이 설치합니다. 로컬 소스 설치본은 `0.4.3-dev`로 표시됩니다.

```sh
go install ./cmd/spine
spine version
```

Preview 브랜치의 최신 CLI는 다음 명령으로 설치합니다. `spine version`은 Go가 해석한 태그 또는 pseudo-version을 표시합니다.

```sh
go install github.com/NARUBROWN/spine-cli/cmd/spine@preview
spine version
```

`@preview`는 발행된 태그만이 아니라 Preview 브랜치의 최신 커밋을 선택합니다. 재현이 필요하면 `@v0.4.3-preview.3`처럼 공개 태그를 명시합니다. 일반 정식 설치는 `@latest`를 사용합니다.

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

`new`는 Echo 플랫폼 연결과 의존성 설치, 정적 계약 검사와 빌드를
완료한 뒤 프로젝트를 생성합니다. 서버는 `spine dev`로 시작합니다. 설치 과정에서는
애플리케이션 초기화, 테스트나 listener를 실행하지 않습니다.

설치와 빌드를 생략하려면 `spine new todo-api --skip-install`을 사용하고, 생성 후 `go mod tidy`를
실행하세요. Preview 브랜치 버전 조회에는 이 옵션에서도 Go와 네트워크 또는 모듈 프록시 캐시가 필요합니다. `--platform echo`는 기본 플랫폼을 명시합니다. 설치 포함 `--dry-run`은 정확한
의존성 계획을 위해 네트워크와 Go 캐시를 사용할 수 있습니다.
`--skip-install --dry-run`은 브랜치를 조회한 뒤 설치 없이 생성 파일을 보여줍니다. `--force`는 `--skip-install`과 함께만 허용합니다.
활성 Go workspace는 설치 범위 밖이므로 `GOWORK=off`로 실행하거나 설치를 생략하세요.

새 프로젝트에는 명시적인 `BuildApplication` composition root, `app.Validate`,
HTTP 사용 시 `GET /health`, `spine.toml`이 포함됩니다.

## HTTP, 이벤트, 검증기 선택

새 프로젝트는 `spine.New(echoplatform.Option(), spine.WithEventAdapter(...), spine.WithValidator(...))`로
하나의 App을 조립합니다. 기본값은 HTTP `echo`, 검증기 `none`이며 두 layout에 같은 옵션을 적용합니다.

```sh
# HTTP + Kafka + 검증기: diff와 base_hash를 검토한 뒤 같은 명령에 적용
spine new shop --module example.com/shop --event-platform events:kafka \
  --validator playground --dry-run --json
spine new shop --module example.com/shop --event-platform events:kafka \
  --validator playground --if-match <base_hash> --json

# HTTP 없는 worker
spine new worker --platform none --event-platform jobs:rabbitmq

# 복수 이벤트 플랫폼
spine new gateway --event-platform events:kafka --event-platform jobs:rabbitmq

# NATS JetStream worker, Go 1.26 이상 필요
spine new nats-worker --platform none --event-platform events:nats
```

| 옵션 | 값과 기본 동작 |
| --- | --- |
| `--platform` | HTTP provider, `echo` (기본값), `none` (이벤트 플랫폼 필요) |
| `--event-platform` | 반복 가능한 `[name:]provider`; `spine-<provider>-platform@preview` 조회 |
| `--websocket-platform` | WebSocket provider 또는 `none` (기본값), provider 선택 시 HTTP 필요 |
| `--validator` | `none` (기본값) 또는 provider; `spine-validator-<provider>@preview` 조회 |

이벤트 이름은 `[a-z][a-z0-9_]*`이며 중복을 거부합니다. 같은 provider도 서로 다른 이름으로 등록할 수 있습니다.
선택한 플랫폼과 검증기만 직접 의존성으로 추가하고, 메타데이터에 선언한 생성자의
오류를 `BuildApplication`에서 반환합니다. HTTP 없는 worker에는 Echo와 HTTP health 경로를 넣지 않습니다.

이벤트 설정은 `internal/application/platform_config.go`에서 이름별 환경 변수로 읽습니다.
이름 `events`의 Kafka에는 `SPINE_EVENTS_BROKERS` (쉼표로 구분)와 `SPINE_EVENTS_GROUP_ID`가 필요합니다.
RabbitMQ에는 `SPINE_EVENTS_URL`과 `SPINE_EVENTS_EXCHANGE`가 필요합니다. 두 플랫폼 모두
`SPINE_EVENTS_TOPIC_PREFIX`, RabbitMQ에는 `SPINE_EVENTS_QUEUE_PREFIX`도 선택할 수 있습니다.
기본 전송은 TLS이고 로컬 평문 브로커에는 `SPINE_EVENTS_ALLOW_INSECURE=true`를 명시합니다.

NATS는 JetStream을 사용합니다. `SPINE_EVENTS_URLS` (쉼표로 구분), `SPINE_EVENTS_STREAM`,
`SPINE_EVENTS_CONSUMER_PREFIX`가 필요하며 stream은 실행 전에 생성해야 합니다.
`SPINE_EVENTS_TOPIC_PREFIX`와 인증용 `SPINE_EVENTS_USER`, `SPINE_EVENTS_PASSWORD`,
`SPINE_EVENTS_TOKEN`, `SPINE_EVENTS_CREDENTIALS_FILE`을 선택할 수 있습니다. 인증 방식은 하나만 설정하세요.
생성한 기본 실패 정책은 reject이며 NATS와 playground 검증기는 Go 1.26.0 이상이 필요합니다.
Core NATS pub/sub와 request/reply, 자동 DLQ는 생성 범위에 포함하지 않습니다.

HTTP, WebSocket, 이벤트와 validator는 `NARUBROWN`의 저장소 명명 규칙과 선택 버전의 `spine-platform.json`으로 조회합니다.
저장소나 Preview 브랜치를 찾지 못하면 조회 경로와 원인을 표시하고 프로젝트를 만들지 않습니다.
새 provider의 설정과 생성자, 최소 Go 버전은 메타데이터로 읽으므로 CLI를 패치할 필요가 없습니다.
기존 Kafka, RabbitMQ와 playground의 메타데이터 없는 버전은 호환 경로를 유지합니다.
[메타데이터 형식과 조회 규칙](PLATFORMS.md)을 참고하세요. `--skip-install`도 메타데이터 조회를 위해 모듈을 캐시에 내려받을 수 있습니다.
설정 누락과 잘못된 boolean, factory 설정 오류는 실행 시 반환됩니다. 설치 중에는 설정 helper와 factory를 실행하지 않습니다.

기본 Kafka, RabbitMQ와 NATS 설정은 consume/publish를 모두 활성화합니다. consumer 생성은 `spine g consumer`로 별도 요청하고
생성된 미구현 핸들러를 작성하세요. 여러 플랫폼은 같은 consumer registry를 사용하므로
각 브로커의 topic/prefix와 중복 처리 정책을 함께 설계해야 합니다. RabbitMQ의 기본 실패 정책은 reject입니다.
playground를 선택한 앱은 DTO의 `validate` 태그를 HTTP JSON, 폼, WS와 consumer 핸들러 전에 검사합니다. 다른 validator의 규칙과 태그는 해당 모듈의 문서를 따릅니다.
`none`이면 이 자동 검증을 생략합니다.

WebSocket 앱은 `spine new chat --websocket-platform gorilla --validator playground`로 독립 WS 구현을 선택합니다. `gorillaplatform.Option()`은 HTTP 옵션과 별개로 등록합니다. 기존 이벤트 factory, validator 오류 처리와 API는 유지합니다. WS 경로를 등록하기 전에 구현을 선택해야 하며 `none` 상태의 새 코어는 암묵적으로 Gorilla를 포함하지 않습니다.

### 공개 버전 레시피

새 앱의 `platform-contracts-preview` 레시피는 코어와 선택한 Echo, Gorilla, Kafka, RabbitMQ, playground 모듈을 `@preview`로 조회합니다. 해석된 태그 또는 pseudo-version을 `go.mod`와 JSON 결과의 `target_modules`에 기록하고, 원래 조회 조건은 `module_queries`에 제공합니다. `spine update`도 이 레시피에서는 브랜치를 다시 조회합니다.

`--dry-run` 이후 브랜치가 이동해 생성 또는 업데이트 내용이 바뀌면 기존 해시를 이용한 적용은 실패합니다. 새 계획을 검토한 뒤 적용하세요. 기존 `platform-contracts-v0.7.0-preview.1`, `platform-contracts-v0.7.0-preview.2`와 정식 `platform-contracts-v0.6.5` 앱은 레시피를 직접 바꾸기 전까지 기존 버전 조합을 유지합니다.

공개 계약은 `contract/http`, `contract/event`, `contract/websocket`, `contract/validator`입니다.
이전 `adapter`, `adapter/http`, `adapter/event`, `wsadapter`는 호환 별칭입니다.
기존 `public-contracts-20261103` 레시피는 이전 버전으로 보존하며 `update`가 소스나 WS 선택을 조용히 추가하지 않습니다.
생성 앱과 브로커 E2E의 재현 절차와 증거 범위는 [E2E 안내](test/assembly-e2e/README.md)를 참고하세요.

### 구조체 입력과 런타임 OpenAPI

Preview 코어의 path, query와 header 입력은 출처별 구조체 포인터로 받습니다. 기존 래퍼는 Deprecated로
유지하지만 새 출처 DTO와 한 핸들러에서 혼용하면 실패합니다. CLI의 route 생성과 정적 검사도
선택된 코어 버전에 맞춰 이를 확인합니다. 새 Preview 프로젝트의 경로 입력 템플릿은 path DTO를 사용합니다.

OpenAPI는 기본 비활성화이며 `boot.HTTPOptions.OpenAPI`의 `Enabled: true`로 활성화합니다.
CLI의 `contract export --format openapi --source runtime`은 `BuildApplication`과 Go 도구를 실행하고
코어 명세를 내보냅니다. 기본 `--source static`은 기존 소스 분석을 유지합니다.
[Preview 대응과 전환 절차](migration/spine-v0.7.0-preview.1.md)에서 실행 경계와 예제를 확인하세요.

## 프로젝트 의존성 갱신

기존 Echo 프로젝트의 의존성은 `spine update`로 갱신할 수 있습니다.
새 앱은 `spine.toml`의 `recipe`와 플랫폼, 검증기 선택을 사용합니다. `recipe`가 없는 기존 Echo 앱은
기존 `echo-v0.1.0-spine-v0.6.0` 레시피를 유지합니다. 의존성 갱신으로 새 API 연결 소스를 생성하지 않습니다.

```sh
spine update --dry-run
spine update
```

완료 출력에는 이전 require 버전과 갱신 후 실제 선택 버전, 변경 파일과 검사 결과가 표시됩니다.
이미 같은 의존성 파일이면 다시 쓰지 않습니다. 레시피보다 새로운 버전은 낮추지 않으므로,
그 경우에는 해당 버전을 지원하는 CLI를 먼저 설치해야 합니다. CLI 실행 파일의 버전은
이 명령으로 바뀌지 않습니다.

임시 프로젝트 복사본에서 `go mod tidy`, 모듈 호환성 검사, 정적 계약 검사와 설정된
entrypoint 빌드를 실행합니다. 성공한 뒤 `go.mod`와 `go.sum`을 반영하며, 실패하거나
검사 도중 원본 입력이 바뀌면 반영하지 않습니다. 이 과정에서 애플리케이션 초기화,
테스트와 서버는 실행하지 않습니다. 갱신 후에는 `spine verify`와 실제 API 요청을 별도로 확인하세요.
`go mod tidy`에 따라 간접 의존성과 체크섬도 바뀔 수 있습니다.

자동화에서는 다음과 같이 정확한 diff와 입력 hash를 검토해 적용합니다.

```sh
spine update --root ./shop-api --dry-run --json
spine update --root ./shop-api --if-match <base_hash> --json
```

`--dry-run`도 네트워크와 Go 캐시를 사용할 수 있으며, 프로젝트 파일은 바꾸지 않습니다.
업무 소스와 `spine.toml`은 유지합니다. 이전 시작 API가 새 모듈과 맞지 않으면 진단을 확인해
소스를 먼저 수정한 뒤 다시 실행합니다. 소스나 디렉터리 구조를 자동으로 마이그레이션하지 않습니다.

지원 범위는 단일 Go 모듈의 기본 빌드입니다. 활성 workspace는 `GOWORK=off`로 분리하고,
사용자 지정 GOFLAGS, vendor, local replace와 symlink 입력은 별도로 처리해야 합니다.
저장된 Go 설정의 GOFLAGS를 명시적으로 해제할 때는 `GOFLAGS=' '`를 사용할 수 있습니다.

## 첫 업무 API

[DTO 생성부터 핸들러, 오류 응답, route 등록과 curl 확인까지의 예제](internal/cli/first_api.md)를 따라 POST /products를 만들 수 있습니다. 생성된 앱 README에도 실제 모듈 경로가 반영된 예제가 포함됩니다.

의존성을 직접 추가하거나 변경한 뒤에는 `go mod tidy`와 `go mod download all`을 실행하세요. 정적 계약 검사의 모듈 분석은 네트워크에 연결하지 않으므로 전체 모듈 메타데이터가 캐시에 필요합니다. `MODULE_GRAPH_UNAVAILABLE`이 발생하면 두 명령으로 캐시를 준비한 뒤 다시 검사합니다.

HTTP 성공 응답은 `httpx.Response[T]`, `httpx.Binary`, `httpx.Redirect`로 반환합니다.
DTO, slice와 string을 직접 반환하지 않고 `httpx.Response[T].Body`에 넣습니다.
본문 DTO는 포인터로 받고 실패는 `error`로 반환합니다. 알려진 미지원 반환형은 route 생성과 계약 검사에서 파일 위치와 수정 방법을 안내합니다.

## 생성기

새 프로젝트는 `layout = "layered-v1"`을 사용합니다. 역할별 기본 경로 아래에 파일을 바로
두거나, 이름 앞에 선택적인 하위 경로를 지정할 수 있습니다.

| 역할 | 기본 경로 | 생성 내용 |
| --- | --- | --- |
| domain | `database/domain/` | 명시한 필드만 포함하는 타입 |
| repository | `src/repository/` | 구조체와 생성자 |
| DTO | `src/dto/` | JSON 태그가 있는 타입 |
| service | `src/service/` | 구조체와 생성자 |
| controller | `src/controller/` | 구조체와 생성자 |
| route | `src/route/route.go` | 명시한 HTTP 메서드와 경로 등록 |
| application | `internal/application/` | 생성자와 등록 함수 연결 |

```sh
spine g domain account/user --field id:int64 --field name:string
spine g dto account/create-user-request --field name:string
spine g repository account/user
spine g service user --depends src/repository/account:UserRepository
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
완료 출력에는 생성/수정한 파일, 실제 타입 참조와 등록 위치가 표시됩니다. JSON에는 types, registrations와 routes가 추가됩니다.
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
`spine.New(spine.WithHTTPAdapter(platform.NewFactory()))`를 같은 Echo 선택으로 인식합니다.
`platform.WithEcho(func(*echo.Echo))`를 전달하는 플랫폼 생성 옵션도 인식하지만 콜백이나 native 경로는 실행하거나 분석하지 않습니다.
`websocket_adapter`는 Gorilla 옵션 또는 `WithWebSocketAdapter(NewFactory())`를, `event_adapters`와 `validator`는 이름 있는 `WithEventAdapter`와 `WithValidator`의 생성식을 보여 줍니다.
알려진 factory의 직접 지역 변수 대입을 추적하며 중복 이름, 설정과 등록 불일치, 오래된 코어를 진단합니다.
임의 helper, 재대입, 가변 옵션과 사용자 정의 검증기 옵션은 미해결로 표시합니다.
이벤트의 `configuration: runtime-values-not-evaluated`는 환경 설정값, 연결 가능성, factory capability를 확인하지 않았다는 뜻입니다.
Spine v0.6.0에서 제거된 `NewWithHTTPAdapter`와 `App.Transport`는 새 생성식과 `WithEcho`로 바꾸도록 진단합니다.
리스너 없이 앱을 준비하는 테스트는 `app.Prepare(ctx, options)`를 사용합니다.
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
spine contract export --format openapi --source runtime --output runtime-openapi.json
spine contract export --format asyncapi --output asyncapi.json
```

검증 결과는 다음 경계를 가집니다.

- `inspect`, `explain`, `contract check`는 정적 소스 분석입니다.
- 등록 이름의 문자열, 문자열 연결, 직접적인 `os.Getenv("이름")` 호출과, 이 문자열의 `==`/`!=` 조건은 검사 프로세스의 환경변수로 해석합니다. 검사와 앱 실행에 같은 값을 전달해야 합니다.
- 임의 함수, 지역 변수, 환경변수 변경 호출은 평가하지 않으며, 해석하지 못한 등록은 계속 오류로 처리합니다.
- 분석할 수 없는 조건부 등록과 동적 등록, 현재 모듈 밖의 `Register(app)` 호출은 오류와
  `analysis_complete: false`로 표시합니다.
- `contract probe`는 `BuildApplication`, `app.Validate`, consumer/WebSocket 공개 registry만 대조합니다.
- `verify` 성공은 계약, gofmt, vet, test의 로컬 통과를 뜻합니다. DB, broker, listener,
  배포 환경의 정상 동작은 별도의 E2E 검증이 필요합니다.

OpenAPI와 AsyncAPI는 타입을 선언한 패키지와 import 범위를 기준으로 스키마를 구분합니다.
JSON 태그가 없으면 Go 필드 이름을 그대로 사용하고, `time.Time`은 `string`과 `date-time`으로
표현합니다. 해석할 수 없는 타입은 `x-spine-unresolved-type`으로 표시합니다. 이 표시는 문서의
분석 한계이며 실제 API의 요청 필드가 아닙니다. 정적 OpenAPI는 분석 가능한 path, query, header DTO와 폼 파일을 표현하지만 validator 규칙은 실행하지 않고 `x-spine-validation-unresolved`로 표시합니다. query와 header의 필수 여부도 정적으로 확정할 수 없으므로 생성 문서에서 선택 입력으로 취급될 수 있습니다. 정확한 검증 규칙이 필요하면 runtime 출력을 사용하세요. 본문 media type을 확인할 수 없으면 `x-spine-unresolved-content`로 표시합니다.
AsyncAPI의 채널과 operation 식별자는 원래 이름을
구분하도록 만들며, 문서에 기록된 참조를 사용해야 합니다.

## 개발 명령

| 명령 | 동작 |
| --- | --- |
| `spine dev` | 변경을 감지하고, 새 빌드가 성공한 경우에만 앱을 교체 |
| `spine run` | 프론트를 production 빌드한 뒤 `ENV=production`으로 Go 앱을 빌드하고 실행 |
| `spine test` | `go test ./...` 실행 |
| `spine check` | gofmt 상태, `go vet`, `go test` 검사 |
| `spine verify` | 정적 계약 검사 후 `spine check` 수행 |
| `spine build` | 프론트 production 산출물과 `bin/<모듈명>` 바이너리 생성 |
| `spine licenses` | CLI에 포함된 제삼자 라이선스 고지 출력 |
| `spine doctor` | 도구 체인, 설정, 정적 계약을 오프라인 진단 |
| `spine completion <shell>` | bash, zsh, PowerShell completion 생성 |

`run`, `test`, `check`, `build`는 `--help` 또는 `-h`로 도움말을 확인할 수 있습니다.
그 밖의 명령별 옵션은 실행 전에 오류로 처리합니다. 테스트 필터는 `go test ./... -run <이름>`으로
직접 실행하세요.

`run`은 한 번의 명령 실행에서 production 프론트 번들과 Go 앱을 각각 한 번 빌드합니다. Go 빌드는
Go 자체의 빌드 캐시와 로컬 의존성 변경을 반영합니다. `dev`는 Go 소스를 감시해 변경 시 앱을
재빌드하고 재시작하며, 프론트 파일만 바뀌면 증분 번들만 교체합니다. 설정 파일 변경 시에는
앱을 다시 빌드해 새 포트와 프론트 설정을 전달합니다. 일반 로그 기록은 재시작 조건에서 제외하며
SIGTERM에서도 자식 앱을 종료하고 회수합니다.

### React CSR 프론트엔드

`spine new`를 터미널에서 실행하면 HTTP, event, WebSocket, validator, frontend 순서로 선택합니다.
번호와 이름 검색을 지원하며 `목록에 없음`을 고른 뒤 provider 이름을 입력할 수 있습니다.
비대화형 환경에서 `--interactive`로 질문을 켜거나 `--no-interactive`로 끌 수 있습니다.

React CSR provider는 내장되어 있습니다. 기본 경로는 `frontend`이며 `spine new`에서 다른
`--frontend-root`를 지정할 수 있습니다. 기존 프로젝트에서는 `spine.toml`의 `frontend_root`로
경로를 정합니다. React 엔트리는 `<frontend_root>/src/main.tsx`, `.jsx`, `.ts`, `.js` 순서로
감지합니다. `<frontend_root>/index.html`은 선택 사항이며, 없으면 `#root`가 있는 기본 HTML을
만듭니다. 프론트 의존성은 프로젝트에서 설치해야 합니다. CLI는 npm 패키지를 설치하지 않으며
TypeScript 타입 검사는 수행하지 않습니다.

```toml
frontend_enabled = true
frontend_provider = "react"
frontend_root = "frontend"
frontend_entry = "src/main.tsx" # 선택 사항, 자동 감지 경로를 바꿀 때 사용
frontend_output = "bin/frontend"
frontend_api_prefix = "/api"
```

기존 프로젝트에서 frontend 설정을 생략하면 조립 코드가 Echo 또는 `net/http` provider를 직접
사용하는 경우에만 `frontend` 경로에서 React entry를 자동 감지합니다.
`spine new`에서 frontend를 사용하지 않으면 `frontend_enabled = false`를 기록해 이후 파일 추가가
자동으로 연결되지 않게 합니다. 기존 자동 감지를 끄려면 같은 설정을 직접 추가하세요.

설정 키는 모두 선택 사항입니다. 기본 frontend 설정은 `frontend_provider = "react"`,
`frontend_root = "frontend"`,
`frontend_output = "bin/frontend"`, `frontend_api_prefix = "/api"`입니다. 엔트리를 자동 감지하지
못했으면 `frontend_entry`를 지정하세요. 빌드 결과는 같은 HTTP listener에서 제공합니다. 기존
Echo 및 `net/http` 플랫폼 라우트가 먼저 처리되고, 정적 파일과 확장자가 없는 HTML 경로는 앱의
404 뒤에 제공됩니다. API가 설정한 prefix 밖에 있고 HTML 요청에 404를 반환하면 SPA 경로로
판정될 수 있습니다. 존재하지 않는 확장자 파일은 404를 유지합니다.

목록에 없는 frontend provider는 이름으로 Go module 경로를 만듭니다.
`<provider>`는 소문자로 시작하고 영문 소문자, 숫자, 하이픈, 밑줄로 구성합니다. `solid`라면
`github.com/NARUBROWN/spine-frontend-solid@preview`에서
`spine-frontend.json`을 읽습니다. 이 규약은 전체 registry를 검색하지 않으며, provider 이름으로
정해지는 module 경로를 조회합니다. provider 저장소의 JSON은 다음 형식입니다.

```json
{
  "schema_version": "spine.frontend/v1",
  "provider": "solid",
  "framework": "Solid",
  "engine": "esbuild",
  "jsx": "automatic",
  "jsx_import_source": "solid-js",
  "entry_candidates": ["src/main.tsx", "src/main.ts"],
  "loaders": {".js": "jsx"},
  "npm_dependencies": {"solid-js": "1.9.9"},
  "starter_files": [
    {"path": "index.html", "contents": "<!doctype html><div id=\"root\"></div>"},
    {"path": "src/main.tsx", "contents": "// provider starter"}
  ]
}
```

`engine`은 `esbuild`여야 합니다. provider module은 실행하지 않습니다. CLI는 선언된 entry,
loader, npm dependency와 시작 파일만 사용하며, 생성 프로젝트의
`<frontend_root>/spine-frontend.json`에는 `starter_files`를 제외한 빌드 설정을 저장합니다.
시작 파일은 상대 경로만 허용하고 총 크기는 1 MiB 이하이며 `index.html`과 entry 후보 하나를
포함해야 합니다.

`spine dev`는 esbuild 증분 context를 유지하고 프론트 변경만 재빌드하므로 Go 앱을 재시작하지
않습니다. `spine run`과 `spine build`는 최적화된 번들을 한 번 생성합니다. 빌드 결과에는 번들
패키지의 사용 가능한 LICENSE 파일을 모은 `THIRD-PARTY-LICENSES.txt`와 esbuild가 연결한 법적
주석이 포함됩니다. `spine licenses`는 esbuild를 포함한 CLI Go 의존성의 라이선스 전문을 출력합니다.

## 프로젝트 설정

`spine.toml`은 CLI가 읽는 프로젝트 설정 파일입니다.

```toml
module = "github.com/acme/todo-api"
entrypoint = "."
composition = "./internal/application"
port = 8080
layout = "layered-v1"
platform = "echo"
recipe = "platform-contracts-preview"
validator = "none"
websocket_platform = "none"
# 이벤트 선택 시 예: event_platforms = ["events:kafka", "jobs:rabbitmq"]
# React CSR 사용 시 선택 설정은 위 React CSR 프론트엔드 절을 참고하세요.
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

## 릴리스 운영

CLI의 Preview, RC, Production 전환과 공개 설치 검증은 [릴리스 운영 절차](release/README.md)를 참고하세요. 모든 단계에서 태그를 발행하고 GitHub Release는 Production에서만 만듭니다. 공개 설치는 실제 설치 버전을 표시하고 로컬 빌드는 개발 버전으로 구분합니다.

`run`과 `dev`의 종료 대기는 기본 15초입니다. `SPINE_CLI_SHUTDOWN_TIMEOUT=30s`처럼 설정할 수 있습니다. 생성 HTTP 앱은 정상 종료를 활성화하며 기존 앱에는 `EnableGracefulShutdown: true`를 직접 적용해야 합니다. Windows의 자식 종료는 강제 종료 fallback을 사용합니다.

## 컨트롤러 경로 등록 후보

현재 CLI 소스는 코어 `v0.7.0-preview.5` 이상을 선택한 layered 프로젝트에서 `RegisterRoutes(*api.Registry)`를 생성합니다. 공개 Preview가 아직 이 코어를 포함하지 않으면 기존 생성 방식을 사용합니다. 소스 구현과 공개 설치 지원은 별도로 확인하세요.

`new`는 해당 코어에서 `route.go` 없이 Health 컨트롤러와 구체 생성자 등록을 만듭니다. `g controller`와 `g resource`도 같은 계약을 사용합니다. 기존 메서드를 대상으로 한 `g route --handler package/path:Type.Method`는 사용자 본문을 보존하고 같은 컨트롤러의 `RegisterRoutes` 안에 typed callback을 추가합니다. 새 방식에서는 비공개 핸들러 메서드도 같은 패키지에서 호출할 수 있습니다. dry-run의 파일, diff와 base_hash를 검토한 뒤 if-match로 적용합니다. 이전 코어와 수동 app.Route 프로젝트는 기존 방식을 유지합니다.

정적 검사는 실제 Go 타입으로 인터페이스 구현, DI 반환 타입, 상수 경로와 Registry를 전달하는 보조 호출을 해석합니다. 함수 리터럴의 업무 코드를 등록 코드로 실행하지 않으며 생성자와 init도 실행하지 않습니다. 동적 경로와 함수 값, 재귀 및 미확정 조건은 분석 불완전으로 진단합니다. 선택한 core의 pkg/api와 의존성이 설치돼 있어야 합니다.

`input.Path[T]`, `input.Query[T]`, `input.Body[T]`의 payload와 입력 키를 정적 계약과 OpenAPI에 반영합니다. Path와 Query의 기본 lowerCamel 이름은 명시 source 태그보다 우선하지 않습니다. 수동 검증 규칙과 동적 응답 상태는 추측하지 않습니다.

단일 경로 변수에는 `input.Path[int64]`처럼 기본 스칼라와 타입 별칭을 사용할 수 있습니다. 정적 분석은 경로 키의 수와 중복, 고정 크기 정수의 범위를 검사합니다. `int`, `uint`, `uintptr`의 플랫폼별 범위는 추측하지 않고 `x-spine-platform-range-unresolved`로 표시합니다. 코어 `v0.7.0-preview.7` 이하는 스칼라 경로 입력을 지원하지 않아 오류로 거부합니다. 교체된 코어나 지원 여부를 확인할 수 없는 버전에는 경고를 표시하므로 실제 코어 동작을 확인하세요. `--warnings-as-errors`는 이 경고도 오류로 처리합니다.

새 컨트롤러의 `--source runtime` 내보내기는 Prepare를 자동 실행하지 않습니다. 수집 전에는 명시적 오류와 정적 내보내기 안내를 반환합니다. 실제 준비가 필요한 앱에서는 호출자가 `spine.DescribePreparedHTTP`를 사용합니다.

소스 후보 설치, 실제 HTTP와 정적 계약을 함께 검증하려면 짝이 맞는 코어 체크아웃을 지정합니다.

```sh
SPINE_CONTROLLER_CORE_SOURCE=/path/to/spine go test ./internal/cli -run TestControllerCandidateEndToEnd -count=1
```

이 검사는 로컬 소스 조합 검증이며 공개 태그 조합 검증을 대체하지 않습니다.
