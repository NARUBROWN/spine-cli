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

아래 Preview 예제에는 CLI `v0.4.3-preview.2`에 포함되는 기능이 필요합니다. 해당 소스 체크아웃의 루트에서는 다음과 같이 설치합니다. 로컬 소스 설치본은 `0.4.3-dev`로 표시됩니다.

```sh
go install ./cmd/spine
spine version
```

`v0.4.3-preview.2` 태그가 공개된 뒤에는 정확한 공개 버전으로 설치할 수 있습니다. 공개 설치본은 실제 태그 버전을 표시합니다.

```sh
go install github.com/NARUBROWN/spine-cli/cmd/spine@v0.4.3-preview.2
spine version
```

일반 정식 설치의 `@latest`와 Preview 설치 버전을 구분하세요. `v0.4.3-preview.1`은 설치 안내와 생성 템플릿의 명령 표기 회귀로 폐기했으며 같은 태그를 재사용하지 않습니다.

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

파일만 생성하려면 `spine new todo-api --skip-install`을 사용하고, 생성 후 `go mod tidy`를
실행하세요. `--platform echo`는 기본 플랫폼을 명시합니다. 설치 포함 `--dry-run`은 정확한
의존성 계획을 위해 네트워크와 Go 캐시를 사용할 수 있으며, 오프라인 계획은
`--skip-install --dry-run`으로 확인합니다. `--force`는 `--skip-install`과 함께만 허용합니다.
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
```

| 옵션 | 값과 기본 동작 |
| --- | --- |
| `--platform` | `echo` (기본값), `none` (이벤트 플랫폼 필요) |
| `--event-platform` | 반복 가능한 `[name:]kafka` 또는 `[name:]rabbitmq`; 이름 생략 시 provider 이름 사용 |
| `--websocket-platform` | `none` (기본값), `gorilla` (HTTP echo 필요) |
| `--validator` | `none` (기본값), `playground` |

이벤트 이름은 `[a-z][a-z0-9_]*`이며 중복을 거부합니다. 같은 provider도 서로 다른 이름으로 등록할 수 있습니다.
선택한 플랫폼과 검증기만 직접 의존성으로 추가하고, `NewFactory(Config)`와 `playground.New()`의
오류를 `BuildApplication`에서 반환합니다. HTTP 없는 worker에는 Echo와 HTTP health 경로를 넣지 않습니다.

이벤트 설정은 `internal/application/platform_config.go`에서 이름별 환경 변수로 읽습니다.
이름 `events`의 Kafka에는 `SPINE_EVENTS_BROKERS` (쉼표로 구분)와 `SPINE_EVENTS_GROUP_ID`가 필요합니다.
RabbitMQ에는 `SPINE_EVENTS_URL`과 `SPINE_EVENTS_EXCHANGE`가 필요합니다. 두 플랫폼 모두
`SPINE_EVENTS_TOPIC_PREFIX`, RabbitMQ에는 `SPINE_EVENTS_QUEUE_PREFIX`도 선택할 수 있습니다.
기본 전송은 TLS이고 로컬 평문 브로커에는 `SPINE_EVENTS_ALLOW_INSECURE=true`를 명시합니다.
설정 누락과 잘못된 boolean, factory 설정 오류는 실행 시 반환됩니다. 설치 중에는 설정 helper와 factory를 실행하지 않습니다.

factory는 consume/publish를 모두 활성화합니다. consumer 생성은 `spine g consumer`로 별도 요청하고
생성된 미구현 핸들러를 작성하세요. 여러 플랫폼은 같은 consumer registry를 사용하므로
각 브로커의 topic/prefix와 중복 처리 정책을 함께 설계해야 합니다. RabbitMQ의 기본 실패 정책은 reject입니다.
검증기를 선택한 앱은 DTO의 `validate` 태그를 HTTP JSON, 폼, WS와 consumer 핸들러 전에 검사합니다.
`none`이면 이 자동 검증을 생략합니다.

WebSocket 앱은 `spine new chat --websocket-platform gorilla --validator playground`로 독립 WS 구현을 선택합니다. `gorillaplatform.Option()`은 HTTP 옵션과 별개로 등록합니다. 기존 이벤트 factory, validator 오류 처리와 API는 유지합니다. WS 경로를 등록하기 전에 구현을 선택해야 하며 `none` 상태의 새 코어는 암묵적으로 Gorilla를 포함하지 않습니다.

### 공개 버전 레시피

새 앱의 `platform-contracts-v0.7.0-preview.1` 레시피는 다음 공개 Preview 태그를 고정합니다.
이 레시피의 안정성 표시는 `prerelease`입니다. 기존 `platform-contracts-v0.6.5` 프로젝트의
`update`는 기존 정식 버전 조합을 유지하며, 앱 소스를 자동으로 바꾸지 않습니다.

| 모듈 | 버전 |
| --- | --- |
| Spine | `v0.7.0-preview.1` |
| Echo 플랫폼 | `v0.2.0-preview.1` |
| Gorilla WS 플랫폼 | `v0.2.0-preview.1` |
| Kafka 플랫폼 | `v0.2.0-preview.1` |
| RabbitMQ 플랫폼 | `v0.2.0-preview.1` |
| playground 검증기 | `v0.2.0-preview.1` |

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
platform = "echo"
recipe = "platform-contracts-v0.7.0-preview.1"
validator = "none"
websocket_platform = "none"
# 이벤트 선택 시 예: event_platforms = ["events:kafka", "jobs:rabbitmq"]
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
