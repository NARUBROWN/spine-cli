# Spine CLI

Spine 애플리케이션의 기술적인 골격을 생성하는 Go CLI입니다.

제품 방향과 Nest CLI/FastAPI CLI 대비 완료 기준은
[실행 계약 컴파일러 전략](docs/execution-contract-compiler.md)에 정리되어 있습니다.

## 설치와 실행

```sh
go install .
spine new todo-api --module github.com/acme/todo-api
cd todo-api
go mod tidy
spine dev
```

생성된 앱은 `GET /health` 엔드포인트, 명시적인 `boot.HTTPOptions`, 시작 전 `app.Validate` 검사를 포함합니다.

## 선택적 Resource 구성

```sh
spine g resource user # 생성과 동시에 composition root에 등록
spine g resource receipt
```

생성 결과:

```text
src/receipt/
  module.go
  receipt_controller.go
  receipt_service.go
  receipt_repository.go
```

`resource`는 선택적인 기능 경계입니다. 모듈식 또는 DDD 스타일로 구성하고 싶을 때 사용합니다. `module.go`의 `Register(app spine.App) error`가 해당 resource의 생성자와 라우트를 등록하며, CLI가 검사 가능한 composition root에 이를 자동으로 등록합니다. 필요한 경우에만 `--no-register`를 사용하세요.

```go
if err := receipt.Register(app); err != nil {
    return nil, boot.Options{}, err
}
if err := user.Register(app); err != nil {
    return nil, boot.Options{}, err
}
```

도메인 모델은 생성하지 않으므로, DDD를 적용한다면 필요한 구조를 resource 내부에 직접 구성할 수 있습니다.

```text
src/user/
  module.go
  user_controller.go
  user_service.go
  user_repository.go
  domain/                    # 필요할 때 사용자가 추가
  persistence/               # 필요할 때 사용자가 추가
```

전통적인 모놀리식 구성을 원한다면 `resource`를 선언할 필요가 없습니다. `main.go` 또는 사용자가 선택한 패키지에서 생성자와 라우트를 직접 등록하면 됩니다.

```go
app.Constructor(NewRepository, NewService, NewController)
app.Route("GET", "/users/:id", (*Controller).Get)
```

CLI는 도메인 및 영속성 구조를 결정하지 않습니다. 다음 항목은 프로젝트 요구에 맞게 사용자가 구성합니다.

- 도메인 모델과 애그리거트 경계
- Bun, GORM 등의 ORM 모델
- 모델을 resource 패키지에 둘지 별도 persistence 패키지에 둘지 여부
- 패키지 간 의존 방향과 관계 매핑
- repository의 인터페이스와 구현 분리 여부

따라서 `resource` 명령은 모델, ORM 태그, 테이블 관계 또는 도메인 타입을 생성하지 않습니다.

## 개별 생성기

```sh
spine g controller metrics
spine g service billing
spine g repository sessions
```

개별 생성기는 resource 패키지가 아니라 전통적인 레이어별 디렉터리에 생성됩니다.

```sh
spine g controller user
spine g service user
spine g repository user
```

```text
src/
  controller/
    user_controller.go
  service/
    user_service.go
  repository/
    user_repository.go
```

각 개별 생성 결과는 다른 레이어의 구체 타입을 자동으로 참조하지 않습니다. 따라서 하나만 생성해도 컴파일할 수 있고, 의존성과 조립 방식은 사용자가 직접 결정할 수 있습니다.

## 검증 가능한 Vertical Slice

```sh
spine g slice order \
  --storage memory \
  --field name:string \
  --field quantity:int
```

`slice`는 typed model과 요청 DTO, repository interface, 동시성 안전 메모리 adapter,
service, POST/GET controller, 등록 코드와 service test를 함께 생성합니다. 메모리 저장소는
프로세스 종료 시 데이터가 사라지는 개발용 구현이므로 `--storage memory`를 명시해야만
생성됩니다. Bun·트랜잭션·outbox가 구현된 것처럼 대체하거나 표시하지 않습니다.

## 메시지 계약 생성기

```sh
spine g consumer order \
  --topic order.created \
  --field order_id:int64

spine g websocket chat \
  --path /ws/chat \
  --field message:string
```

두 생성기는 typed payload, handler, 오류 전파형 `Register` 함수를 만들고 composition root에
원자적으로 등록합니다. 생성 직후 `spine inspect`, `spine explain`, AsyncAPI export에서 같은
consumer/WebSocket 계약을 확인할 수 있습니다. Kafka·RabbitMQ 같은 실제 transport,
delivery/retry/dead-letter 정책, WebSocket 인증·origin·capacity 정책은 추측하지 않습니다.
handler는 구현 전까지 명시적인 `not implemented` 오류를 반환하므로 메시지를 성공 처리한 것처럼
조용히 승인하지 않습니다. 이 상태는 계약 검사에도 오류로 표시되어 `spine verify`가 실패합니다.
`TODO`를 애플리케이션 로직으로 교체한 뒤 해당 오류를 제거해야 합니다.

## 개발 워크플로우

```sh
# Go 소스와 프로젝트 설정 변경을 감지합니다. 새 빌드가 성공한 경우에만 앱을 교체합니다.
spine dev

# 운영 환경 변수(SPINE_ENV=production)로 실행합니다.
spine run

# 정적 검사와 테스트를 실행합니다.
spine check

# bin/<모듈명>에 바이너리를 생성합니다.
spine build
```

## 실행 계약 검사

```sh
# composition root와 등록된 resource를 따라가며 실행 계약을 출력합니다.
spine inspect
spine inspect --json

# 특정 등록의 handler와 정적 실행 순서를 설명합니다.
spine explain http GET /orders/:id
spine explain consumer order.created
spine explain websocket /ws/orders

# 정적 실행 계약, gofmt, go vet, go test를 로컬에서 검증합니다.
spine verify

# 계약을 저장하고 현재 코드와 의미 단위로 비교합니다.
spine contract check --json
spine contract check --warnings-as-errors
spine contract snapshot
spine contract diff
spine contract probe
spine contract export --format openapi --output openapi.json
spine contract export --format asyncapi --output asyncapi.json

# 로컬 도구 체인, 설정과 정적 계약을 진단합니다.
spine doctor
spine doctor --json

# 자동화와 AI planner가 실제 구현 범위를 확인할 수 있습니다.
spine capabilities --json
```

`inspect`의 현재 분석 방식은 `static-source`입니다. HTTP route, constructor와 DI 의존성,
전역·route interceptor, consumer, WebSocket과 resource 등록을 네트워크 연결 없이 분석합니다.
분석 범위는 composition 함수와 resource `Register` 본문으로 제한됩니다. 조건부·helper·함수 리터럴,
동적 등록 이름과 해석할 수 없는 handler/constructor signature는 완전한 계약처럼 취급하지 않고
오류 diagnostic과 `analysis_complete: false`로 표시합니다.
`contract check --json`은 CI와 AI 도구를 위해 completeness, 오류·경고 수와 evidence boundary를
구조화해 출력합니다. 보안 경고도 차단하려면 `--warnings-as-errors`를 사용합니다.
`verify`의 Go 명령은 필요한 모듈을 해석할 수 있지만, 성공하더라도 외부 데이터베이스,
브로커, listener 또는 배포 환경의 정상 동작을 증명하지 않습니다.
`contract diff`는 소스 줄 번호 변화는 무시하고 route, handler, constructor, interceptor,
consumer, WebSocket과 resource의 의미 변화가 있을 때 실패합니다.
`contract probe`는 composition root를 별도 프로세스에서 실행하고 `app.Validate`를 통과시킨 뒤,
현재 공개 API로 관찰 가능한 consumer/WebSocket runtime registry를 정적 계약과 대조합니다.
정적 계약에 오류가 있거나 두 등록 집합이 다르면 runtime registry를 읽었더라도 명령은 실패합니다.
HTTP route, constructor와 interceptor는 현재 `spine.App`가 읽기 API를 제공하지 않으므로
runtime evidence라고 표시하지 않고 명시적인 관찰 한계로 출력합니다.
OpenAPI export는 확인 가능한 HTTP path, path parameter, DTO struct와 반환 body type을
내보냅니다. 런타임에서 정해지는 응답 status는 추정하지 않고 `default` response와
`x-spine-*` 근거로 남깁니다. AsyncAPI 3.1 export는 consumer와 WebSocket channel,
payload schema와 receive operation을 생성하지만 실제 broker protocol과 전달 보장은 추정하지 않습니다.
`doctor`의 범위도 기본적으로 오프라인입니다. 아직 지원하지 않는 `doctor --connect`는
성공처럼 처리하지 않고 명시적으로 실패합니다.
`capabilities`는 미구현 transport/ORM generator와 runtime 검사를 `implemented: false`로 공개하여
계획 단계에서 아직 생성할 수 없는 기능을 지원하는 것처럼 취급하지 않게 합니다.
Spine 본체의 전체 runtime manifest는 현재 no-core CLI 범위에 포함하지 않으며 미래 TODO로만 남깁니다.

`spine.toml`은 CLI가 읽는 유일한 프로젝트 설정 파일입니다. 새 프로젝트에 자동 생성되며 모듈 경로, 실행 엔트리포인트, composition root와 포트를 포함합니다.

```toml
module = "github.com/acme/todo-api"
entrypoint = "."
composition = "./internal/application"
port = 8080
```

## 안전한 생성과 셸 지원

생성 명령은 기존 파일을 덮어쓰지 않습니다. 의도적으로 교체할 때만 `--force`를 사용하세요. 결과만 확인하려면 `--dry-run`, 자동화 용도에는 `--json`을 사용할 수 있습니다. `--dry-run`은 적용할 파일별 diff를 함께 출력하며 JSON 결과에도 `diff` 필드가 포함됩니다.
파일 계획은 사전 검사 후 원자적으로 반영되며, 프로젝트 단위 generation lock이 동시에 실행된
두 CLI 프로세스의 composition root lost update를 차단합니다. 비정상 종료로 남은 일반 lock 파일은
10분이 지난 뒤에만 stale lock으로 회수하고 symlink나 다른 비정상 대상은 자동 삭제하지 않습니다.

```sh
spine g resource orders --dry-run
spine g resource orders --json
spine completion powershell | Invoke-Expression
```
