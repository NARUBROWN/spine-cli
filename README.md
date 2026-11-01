# Spine CLI

Spine CLI는 [Spine](https://github.com/NARUBROWN/spine) 애플리케이션을 생성하고,
소스 코드에서 실행 계약을 검사·설명·검증하는 Go CLI입니다.

프로젝트 이름은 `spine-cli`입니다. 루트 경로로 설치하면 실행 파일은 `spine-cli`,
`cmd/spine` 경로로 설치하면 실행 파일과 명령은 `spine`입니다.

- 프로젝트와 HTTP·consumer·WebSocket 구성 요소 생성
- HTTP, DI, interceptor, consumer, WebSocket 실행 계약 검사
- 계약 검증, snapshot/diff, OpenAPI·AsyncAPI 내보내기

## 요구사항과 설치

- Go 1.27.3 이상 (Go 1.27.x 검증 기준: 1.27.3)
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

생성 결과는 기본적으로 composition root에 등록됩니다. 등록 없이 파일만 만들려면
`--no-register`를 사용합니다.

| 명령 | 생성 내용 | 주의점 |
| --- | --- | --- |
| `spine g resource user` | controller, service, repository, GET route | 도메인 모델과 ORM은 생성하지 않음 |
| `spine g slice order --storage memory --field name:string` | typed vertical slice와 service test | 메모리 저장소는 개발용 |
| `spine g controller user` | 독립 controller와 route 등록 | `--depends`로 같은 package의 구성 요소 주입 가능 |
| `spine g service user` | 독립 service | 조립 방식은 사용자가 결정 |
| `spine g repository user` | persistence-neutral repository 골격 | DB adapter는 사용자가 구현 |
| `spine g consumer order --topic order.created --field order_id:int64` | typed event와 handler | handler 구현 전에는 검증 실패 |
| `spine g websocket chat --path /ws/chat --field message:string` | typed message와 handler | 인증·origin·capacity 정책은 직접 구성 |

레이어별 구성 요소는 파일 이름으로 의존성을 연결할 수 있습니다.

```sh
spine g repository user
spine g service user --depends user_repository
spine g controller user --depends user_service
```

`resource`는 선택적인 기능 경계일 뿐입니다. 도메인 모델, aggregate, ORM tag,
테이블 관계, transaction, outbox는 프로젝트 요구에 맞게 직접 설계합니다.
consumer와 WebSocket handler는 생성 직후 명시적인 `not implemented` 오류를 반환하므로,
실제 로직을 구현한 뒤 해당 오류를 제거해야 합니다.

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

JSON dry-run에는 대상별 diff와 SHA-256, 전체 대상 상태를 나타내는 `base_hash`가 포함됩니다.
`--if-match`는 계획을 확인한 뒤 파일이 달라졌다면 적용 전에 중단합니다.

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
