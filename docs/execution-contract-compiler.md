# Spine CLI 차별화 전략: 실행 계약 컴파일러

기준일: 2026-07-28

## 결정

Spine CLI는 Nest CLI의 schematic 개수나 FastAPI CLI의 서버 실행 편의성을 그대로 따라가지 않는다.
핵심 제품은 파일 생성기가 아니라 다음 변환을 수행하는 **실행 계약 컴파일러**다.

```text
Go source + spine.toml
  -> 정적 실행 계약
  -> 의미 단위 검증과 drift 검사
  -> OpenAPI / AsyncAPI / AI-readable capabilities
  -> 격리된 부분 runtime probe

future Spine TODO
  -> immutable runtime manifest와 전체 등록 교차 검증
```

생성기는 이 파이프라인에 검증 가능한 입력을 공급하는 수단이다. 생성기 수 자체는 성공 지표가 아니다.

## 경쟁 CLI의 공식 범위

Nest CLI 공식 문서는 주요 명령을 `new`, `generate`, `build`, `start`, `add`, `info`로 설명하고,
프로젝트 scaffold, schematic 기반 파일 생성, 개발 실행과 production build를 중심으로 둔다.
`nest g resource`는 REST, microservice, GraphQL, WebSocket 유형의 entity, DTO, controller,
service와 test boilerplate까지 생성한다.

- [Nest CLI overview](https://docs.nestjs.com/cli/overview)
- [Nest CLI CRUD generator](https://docs.nestjs.com/recipes/crud-generator)
- [Nest CLI usage](https://docs.nestjs.com/cli/usages)

FastAPI CLI 공식 범위는 Python 애플리케이션을 탐색해 Uvicorn 개발/운영 서버로 실행하는
`fastapi dev`, `fastapi run`과 선택적인 배포 흐름에 집중한다. FastAPI 자체의 OpenAPI 생성은
강력하지만 CLI의 중심은 애플리케이션 탐색과 실행이다.

- [FastAPI CLI repository](https://github.com/fastapi/fastapi-cli)
- [FastAPI first steps and CLI entrypoint](https://fastapi.tiangolo.com/tutorial/first-steps/)

따라서 Spine이 이길 수 있는 영역은 더 많은 boilerplate가 아니라 **코드에 선언된 실제 실행 의미를
빌드·CI·문서·AI 도구가 공유하는 하나의 검증 가능한 계약으로 만드는 것**이다.

## 확보해야 하는 moat

### 1. 하나의 semantic contract

HTTP route, handler signature와 binding, DI constructor와 dependency, interceptor scope,
consumer topic, WebSocket path, boot option과 보안상 중요한 설정을 같은 IR로 표현한다.
줄 번호 이동은 drift가 아니고 route, signature, scope, payload, 설정 변화는 drift여야 한다.

현재 근거:

- `spine inspect`, `spine inspect --json`
- `spine explain`
- `spine contract snapshot`, `spine contract diff`
- `spine contract export --format openapi|asyncapi`

### 2. fail-closed generation

생성 성공은 파일 존재가 아니라 다음 조건을 의미해야 한다.

- 모든 대상 파일과 composition 변경을 한 계획으로 사전 검사하고 원자적으로 반영한다.
- 자동 등록 오류가 `BuildApplication` 오류로 전파된다.
- typed consumer와 WebSocket handler는 구현 전까지 `not implemented` 오류를 반환한다.
- 미구현 handler는 계약 오류가 되어 `spine verify`를 통과하지 못한다.
- 개발용 메모리 저장소는 명시적인 `--storage memory` 없이는 생성하지 않는다.

### 3. static/runtime reconciliation

정적 분석만으로 동적 helper, 조건부 등록과 runtime option을 완전히 증명할 수 없다.
반대로 runtime reflection만으로 source evidence와 변경 의도를 설명할 수 없다. 두 증거를 대조해야 한다.

현재 `spine contract probe`는 별도 프로세스에서 composition root를 실행하고 `app.Validate`를
호출한 뒤, 공개 registry로 확인 가능한 consumer와 WebSocket을 정적 계약과 비교한다.
결과의 `evidence_mode`는 `runtime-public-registry-partial`이며 HTTP, DI, interceptor를 runtime에서
확인한 것처럼 표시하지 않는다.

현재 제품 경계는 여기까지다. HTTP, DI와 interceptor의 runtime 조회는 지원하는 것처럼 표시하지 않는다.
Spine 본체의 immutable runtime manifest를 통한 전체 비교는 현재 CLI 완료 조건이 아닌 미래 TODO다.

### 4. capability honesty

`spine capabilities --json`은 AI planner와 자동화가 구현 여부와 한계를 기계적으로 읽는 계약이다.
Kafka consumer 등록을 생성할 수 있다는 사실과 Kafka transport 설정, delivery guarantee,
retry/DLQ를 구성할 수 있다는 주장을 분리한다. Bun skeleton도 transaction/outbox 지원으로 표시하지 않는다.

### 5. verification ladder

검증 결과는 범위를 섞지 않는다.

```text
source contract
  -> gofmt / go vet / go test
  -> isolated BuildApplication + app.Validate
  -> public consumer/WebSocket registry parity
  -> external E2E and deployed observation
```

아래 단계의 성공으로 위 단계가 자동 증명되지는 않는다. 특히 오프라인 검증은 database, broker,
listener나 배포 환경의 정상 동작을 주장하지 않는다.

## 현재 no-core 범위 완료 조건

1. 분석기는 실제 composition 함수와 resource `Register` 본문 밖의 등록을 계약으로 오인하지 않는다.
2. 조건부, helper, 함수 리터럴, 동적 이름과 해석 불가능한 signature는 검증 불확실성 오류가 된다.
3. 정적으로 완결된 HTTP, DI, interceptor, consumer와 WebSocket 계약은 하나의 canonical IR과 semantic diff를 만든다.
4. 구조화된 contract check가 completeness, 오류·경고 수, 정책과 evidence boundary를 출력한다.
5. generator는 원자적으로 등록하며 미구현 메시지 handler와 개발용 저장소 한계를 fail-closed로 표현한다.
6. 부분 runtime probe는 `BuildApplication`, `app.Validate`, public consumer/WebSocket registry만 증명한다.
7. OpenAPI/AsyncAPI export는 불완전한 계약을 거부하고 확인하지 못한 runtime 의미를 추측하지 않는다.
8. 생성 앱의 `inspect -> check -> snapshot -> probe -> verify -> export` 흐름과 실패 경계가 회귀 테스트로 검증된다.

## 미래 Spine TODO: 최소 manifest API

`spine.App` 인터페이스에 메서드를 직접 추가하면 외부 mock 구현을 깨뜨릴 수 있으므로 별도 capability
interface가 안전하다.

```go
type ManifestProvider interface {
    Manifest() manifest.Application
}
```

내부 `*app`만 이 인터페이스를 구현하고 CLI runtime probe가 type assertion으로 선택한다.
manifest 타입은 public package에 두되 내부 router/bootstrap 타입이나 실행 가능한 함수 값을 노출하지 않는다.

필수 manifest 항목:

- 정규화된 HTTP method/path와 handler descriptor
- constructor descriptor와 dependency type
- global/transport/route interceptor descriptor와 순서
- consumer topic과 handler descriptor
- WebSocket path와 handler descriptor
- custom transport 종류
- 보안 판단에 필요한 redacted option facts
- manifest schema version과 Spine version

불변 조건:

- manifest 생성은 constructor를 실행하거나 network listener를 열지 않는다.
- 반환 slice와 map은 복사본이며 내부 registry를 변경할 수 없다.
- DSN, password, token과 원문 credential은 포함하지 않는다.
- 함수 pointer 주소처럼 빌드마다 달라지는 값은 semantic identity로 사용하지 않는다.
- 동일 계약은 소스 줄 이동과 프로세스 재실행 후에도 동일한 canonical representation을 만든다.

## 미래 manifest TODO 완료 조건

나중에 Spine 본체 작업을 시작하더라도 다음을 모두 증명하기 전에는 runtime manifest 기능을 완료로 표시하지 않는다.

1. 정적 계약과 runtime manifest가 HTTP, DI, interceptor, consumer, WebSocket 전체를 대조한다.
2. helper 또는 조건문으로 추가된 동적 등록을 runtime-only drift로 탐지한다.
3. 정적 등록이 runtime composition에서 누락되면 missing drift로 탐지한다.
4. handler, payload, interceptor 순서와 boot security fact 변화가 semantic diff에 반영된다.
5. manifest 호출 중 constructor 실행, broker 연결과 listener bind가 발생하지 않는 회귀 테스트가 있다.
6. credential redaction과 immutable copy 회귀 테스트가 있다.
7. 생성 앱에서 `inspect -> snapshot -> probe -> verify -> export` 수직 흐름이 통과한다.
8. 외부 database/broker E2E는 별도 증거로 실행되고 오프라인 결과와 구분된다.

## 명시적 비목표

- Nest schematic 전체 목록 복제
- ORM, transaction 또는 outbox 정책의 무근거 자동 선택
- Kafka와 RabbitMQ 전달 의미를 하나의 generic 설정으로 숨기기
- 정적 분석 결과를 runtime truth로 표시하기
- 단순 테스트 통과를 production readiness로 표시하기
- 동적 Go 코드를 완전히 해석할 수 있다고 주장하기

이 원칙을 지키면 Spine CLI의 경쟁력은 “무엇을 빨리 생성하는가”가 아니라
“생성된 애플리케이션이 실제로 무엇을 실행할지 얼마나 정확하게 증명하는가”에서 나온다.
