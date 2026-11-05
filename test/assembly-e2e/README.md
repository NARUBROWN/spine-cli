# CLI 생성 앱의 공개 플랫폼 E2E

이 검증은 현재 checkout의 CLI를 빌드하고 `spine new`가 실제 만든 프로젝트를 대상으로 실행합니다.
프레임워크 저장소의 E2E 성공과 별도로 유지합니다. 생성과 update는 앱을 실행하지 않으며,
아래 테스트 단계에서만 생성 앱과 실제 브로커를 실행합니다.

## 공개 레시피와 Go 버전

`platform-contracts-v0.7.0-preview.2` 레시피는 다음 공개 Preview 모듈을 사용합니다. 생성 프로젝트에는 local `replace`가 없습니다.

| 모듈 | 버전 |
| --- | --- |
| Spine | `v0.7.0-preview.2` |
| Echo 플랫폼 | `v0.2.0-preview.1` |
| Gorilla WS 플랫폼 | `v0.2.0-preview.1` |
| Kafka 플랫폼 | `v0.2.0-preview.1` |
| RabbitMQ 플랫폼 | `v0.2.0-preview.1` |
| playground 검증기 | `v0.2.0-preview.1` |

CLI와 검증기 없는 생성 앱의 최소 버전은 Go 1.25.5입니다. playground는 go-playground v10.30.6을
사용하므로 Go 1.26.0 이상이 필요합니다. 설치는 `GOTOOLCHAIN=local`로 검사하며 자동 다운로드하지 않습니다.
Go 1.25.5 실행 파일이 따로 있으면 다음과 같이 최소 버전 설치와 거부 경로를 확인합니다.

```sh
SPINE_TEST_GO125=/absolute/path/to/go1.25.5/bin/go \
  go test ./internal/cli -run '^TestGeneratedAssemblyMinimumToolchain$' -count=1 -v
```

이번 Preview 조합은 [코어 v0.7.0-preview.2](https://github.com/NARUBROWN/spine/tree/v0.7.0-preview.2)의 공개 태그를 사용합니다. 기존 정식 레시피와 이전 개발 레시피는 별도 회귀 검사로 보존합니다.

## 회귀 테스트와 생성 matrix

```sh
go test -race ./... -count=1 -timeout=10m
go vet ./...
git diff --check

go test ./internal/cli -run '^TestGeneratedAssemblyPublicMatrix$' -count=1 -v
```

matrix는 `layered-v1`과 `legacy`에서 각각 다음 7개 구성을 검사합니다.

- HTTP만 사용하고 validator 생략
- HTTP + Kafka
- HTTP + RabbitMQ
- HTTP 없는 Kafka worker
- 이름이 다른 두 Kafka와 RabbitMQ를 함께 등록
- HTTP + playground + 명시적 Gorilla WS
- HTTP + Kafka + RabbitMQ + playground

각 구성은 dry-run의 diff, 변경 목록, base_hash를 확인하고 `--if-match`로 적용합니다.
공개 모듈 설치, 선택 버전 검사, 정적 계약, Go build, `context`, `contract check`, `verify`를 실행합니다.
검증기 구성은 생성 앱에서 race 테스트도 수행합니다. HTTP 검증기 fixture는 원래 `BuildApplication`에
테스트 핸들러만 추가해 JSON, 폼, WS, 잘못된 JSON/타입/규칙, 핸들러 차단과 병렬 요청을 확인합니다.
검증기를 생략한 앱은 같은 `validate` 태그를 자동 검사하지 않는 것을 확인합니다.

생성된 설정 테스트는 누락 값, 잘못된 boolean과 factory 오류 반환을 검사하며 브로커에는 연결하지 않습니다.
`all` 구성의 update는 같은 선택 레시피로 의존성만 검사하고 composition 원본 바이트를 보존합니다.
recipe가 없는 기존 앱이 v0.6.0 Echo 레시피를 유지하는 회귀 검사도 별도로 수행합니다.

## 실제 브로커 실행

Go 1.26.0 이상과 Docker가 필요합니다. 저장소 루트에서 실행합니다.

```sh
docker compose -p spine-cli-27 -f test/assembly-e2e/compose.yaml up -d

SPINE_TEST_KAFKA_BROKER=127.0.0.1:29092 \
SPINE_TEST_RABBITMQ_URL=amqp://guest:guest@127.0.0.1:25672/ \
  go test ./internal/cli -run '^TestGeneratedAssemblyBrokerE2E$' \
  -count=1 -v -timeout=12m

docker compose -p spine-cli-27 -f test/assembly-e2e/compose.yaml down -v
```

compose는 Redpanda v24.3.18과 RabbitMQ 3-management를 사용하고 로컬 주소에만 포트를 엽니다.
실행 직후 브로커가 준비될 때까지 기다린 뒤 테스트를 시작하세요. 두 환경 변수가 없으면 broker suite를
명시적으로 건너뜁니다. 생략된 결과를 실제 브로커 검증 통과로 보고하지 않습니다.

두 layout × Kafka/RabbitMQ × HTTP/worker/WebSocket의 12개 경로를 실행합니다. 매번 실제 CLI가 공개 모듈로
프로젝트를 생성하고 설치, 정적 검사를 마칩니다. 그 뒤 테스트 코드만 추가하고 `go test -race`를 실행합니다.
원래 `application.go`는 변경하지 않으며 실행 전후 SHA-256도 기록합니다. 테스트에 필요한 native
클라이언트 의존성은 fixture의 `go mod tidy`에서 추가합니다. 이때도 local `replace`는 사용하지 않습니다.

HTTP 앱은 잘못된 DTO를 400으로 차단하고, 정상 HTTP 요청을 이벤트로 발행합니다.
WebSocket 앱은 `--websocket-platform gorilla`로 생성하고 잘못된 메시지의 1008 차단과 정상 WS 메시지의 이벤트 발행을 확인합니다. worker 앱은 외부 producer의 정상 이벤트로 시작합니다. 모든 경로에서 consumer 검증 → 핸들러 →
결과 이벤트를 확인한 뒤, HTTP 검증을 우회한 외부 producer의 잘못된 DTO를 전송합니다.

- Kafka: 정상 입력의 committed offset이 1인 것을 native API로 확인합니다. 잘못된 DTO는 핸들러에
  도달하지 않으며 NACK 뒤 다시 검증에서 실패합니다. 종료 후 같은 group으로 다시 읽으면 잘못된
  메시지가 반환되고 offset은 1을 유지합니다. 정상 메시지는 재전달되지 않습니다.
- RabbitMQ: 정상 결과와 잘못된 DTO의 검증 실패, 핸들러 차단을 확인합니다. 현재 factory의 reject
  정책과 runtime의 실패 시 NACK 경로를 사용하며, consumer 종료 후 native queue가 비어 있어
  미확인 전달이 남지 않는 것을 검사합니다. NACK 콜백을 교체하는 계측은 사용하지 않습니다.
- 종료: 앱, native client를 닫고 HTTP listener의 재사용을 확인합니다. 테스트가 만든 고유 topic,
  queue, exchange만 삭제합니다. compose 정리는 위 프로젝트의 컨테이너와 네트워크만 대상으로 합니다.

이 결과는 exactly-once나 DB/브로커 원자적 처리, 운영 환경의 TLS, 인증, 장애 복구를 증명하지 않습니다.
복수 factory는 정적 검사와 build/test로 검증하며, 위 실제 처리 경로는 플랫폼별 단일 factory 구성을 대상으로 합니다.

## 증거 구분

NATS JetStream은 별도 `TestGeneratedNATSAssemblyE2E`로 검사합니다.

```sh
SPINE_TEST_NATS_URL=nats://127.0.0.1:4222 \
  go test -race ./internal/cli -run '^TestGeneratedNATSAssemblyE2E$' -count=1 -v -timeout=10m
```

두 layout에서 `--event-platform events:nats`로 HTTP 앱과 worker 네 조합을 생성합니다.
dry-run의 diff와 base_hash를 검토하고 `--if-match`로 적용한 뒤 정적 계약, 부분 runtime probe,
실제 발행과 소비, 결과 이벤트, ACK, 잘못된 DTO의 reject, 정상 종료와 update의 소스 보존을 검사합니다.
`SPINE_TEST_GO125`가 있으면 NATS가 Go 1.25.5에서 목적지를 만들기 전에 거부되는지도 확인합니다.
CI는 네 조합과 최소 Go 검사의 누락이나 skip을 실패로 처리합니다.

모듈 게시 전이나 플랫폼 자체 CI에서는 `SPINE_TEST_NATS_MODULE_DIR`로 로컬 NATS 모듈을 지정할 수 있습니다.
이때 별도 모듈 프록시와 캐시를 사용하고 `replace`는 넣지 않습니다. 이 결과를 공개 저장소의 설치 증거로 보고하지 않습니다.

| 구분 | 근거 | 확인 범위 |
| --- | --- | --- |
| CLI 회귀 | 전체 `go test -race`, vet, diff 검사 | 생성, 분석, 해시, rollback, 기존 HTTP/native hook, 등록 동작 |
| 생성 결과 | 공개 모듈 matrix 14개 구성 | 설치, 버전, 정적 검사, build/test, update 소스 보존 |
| HTTP/검증기 | 생성 앱의 실제 HTTP/WS 테스트 | JSON, 폼, WS, 규칙 오류, 핸들러 차단, 생략 동작 |
| 최소 도구 체인 | 실제 Go 1.25.5 설치/verify | 이벤트 앱 통과, playground Go 1.26 요구와 목적지 미게시 |
| 외부 E2E | 생성 앱의 실제 Kafka/RabbitMQ 12개 경로와 NATS 4개 경로 | 발행, consumer, 결과, settlement, 실패 정책, 종료 |

현재 checkout에서 다시 실행한 결과만 이번 Preview의 증거로 사용합니다. 이전 버전에서 통과한 결과를 이번 조합의 검증으로 대신하지 않습니다. 실제 실행 로그는 작업 결과와 PR에 구분해 보고합니다.

## 독립 WS 생성 E2E

```sh
go test ./internal/cli -run '^TestGeneratedWebSocketPublicAssembly$' -count=1 -v
```

두 layout과 validator none/playground의 4개 조합에서 실제 CLI dry-run/apply, context/inspect/contract check/verify, 공개 모듈 그래프와 WS 메시지, 1008 검증 종료, 1000 정상 종료, listener 해제를 검사합니다. 원래 BuildApplication 바이트와 SHA-256을 보존합니다.
기존 public-contracts-20261103 레시피는 이전 버전으로 보존합니다. 새 프로젝트만 현재 분리 코어를 기본 선택하며 기존 앱의 update는 소스를 재작성하지 않습니다.

이전 `platform-contracts-20261103` WS 개발 레시피도 당시 버전으로 보존합니다. 정식 버전은 `platform-contracts-v0.6.5`에서 선택하며 소스를 자동으로 바꾸지 않습니다.
