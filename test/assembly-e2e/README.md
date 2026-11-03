# CLI 생성 앱의 공개 플랫폼 E2E

이 검증은 현재 checkout의 CLI를 빌드하고 `spine new`가 실제 만든 프로젝트를 대상으로 실행합니다.
프레임워크 저장소의 E2E 성공과 별도로 유지합니다. 생성과 update는 앱을 실행하지 않으며,
아래 테스트 단계에서만 생성 앱과 실제 브로커를 실행합니다.

## 공개 레시피와 Go 버전

`public-contracts-20261103`은 다음 공개 모듈을 사용합니다. 생성 프로젝트에는 local `replace`가 없습니다.

| 모듈 | 버전 |
| --- | --- |
| Spine | `v0.6.1-0.20261103010611-6e60b34204d3` |
| Echo 플랫폼 | `v0.1.0` |
| Kafka 플랫폼 | `v0.0.0-20261103001523-e4b87708f841` |
| RabbitMQ 플랫폼 | `v0.0.0-20261103001525-b2be17ee8a11` |
| playground 검증기 | `v0.0.0-20261103011253-5508c82a155e` |

CLI와 검증기 없는 생성 앱의 최소 버전은 Go 1.25.5입니다. playground는 go-playground v10.30.6을
사용하므로 Go 1.26.0 이상이 필요합니다. 설치는 `GOTOOLCHAIN=local`로 검사하며 자동 다운로드하지 않습니다.
Go 1.25.5 실행 파일이 따로 있으면 다음과 같이 최소 버전 설치와 거부 경로를 확인합니다.

```sh
SPINE_TEST_GO125=/absolute/path/to/go1.25.5/bin/go \
  go test ./internal/cli -run '^TestGeneratedAssemblyMinimumToolchain$' -count=1 -v
```

2026-11-03 확인 시 [코어 PR #16](https://github.com/NARUBROWN/spine/pull/16)은 OPEN/DRAFT이고
head는 `7ebae28b79265547b773f7346086a6a254d52ccf`입니다. 최신 정식 코어 태그는 v0.6.0이며,
위 설치 결과는 PR 병합이나 정식 릴리스를 뜻하지 않습니다. 코어 runtime banner의 고정된 버전 문자열보다
Go 모듈 그래프의 실제 선택 버전을 설치 근거로 사용합니다.

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
- HTTP + playground
- HTTP + Kafka + RabbitMQ + playground

각 구성은 dry-run의 diff·변경 목록·base_hash를 확인하고 `--if-match`로 적용합니다.
공개 모듈 설치, 선택 버전 검사, 정적 계약, Go build, `context`, `contract check`, `verify`를 실행합니다.
검증기 구성은 생성 앱에서 race 테스트도 수행합니다. HTTP 검증기 fixture는 원래 `BuildApplication`에
테스트 핸들러만 추가해 JSON·폼·WS, 잘못된 JSON/타입/규칙, 핸들러 차단과 병렬 요청을 확인합니다.
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

두 layout × Kafka/RabbitMQ × HTTP/worker의 8개 경로를 실행합니다. 매번 실제 CLI가 공개 모듈로
프로젝트를 생성하고 설치·정적 검사를 마칩니다. 그 뒤 테스트 코드만 추가하고 `go test -race`를 실행합니다.
원래 `application.go`는 변경하지 않으며 실행 전후 SHA-256도 기록합니다. 테스트에 필요한 native
클라이언트 의존성은 fixture의 `go mod tidy`에서 추가합니다. 이때도 local `replace`는 사용하지 않습니다.

HTTP 앱은 잘못된 DTO를 400으로 차단하고, 정상 HTTP 요청을 이벤트로 발행합니다.
worker 앱은 외부 producer의 정상 이벤트로 시작합니다. 두 경로 모두 consumer 검증 → 핸들러 →
결과 이벤트를 확인한 뒤, HTTP 검증을 우회한 외부 producer의 잘못된 DTO를 전송합니다.

- Kafka: 정상 입력의 committed offset이 1인 것을 native API로 확인합니다. 잘못된 DTO는 핸들러에
  도달하지 않으며 NACK 뒤 다시 검증에서 실패합니다. 종료 후 같은 group으로 다시 읽으면 잘못된
  메시지가 반환되고 offset은 1을 유지합니다. 정상 메시지는 재전달되지 않습니다.
- RabbitMQ: 정상 결과와 잘못된 DTO의 검증 실패·핸들러 차단을 확인합니다. 현재 factory의 reject
  정책과 runtime의 실패 시 NACK 경로를 사용하며, consumer 종료 후 native queue가 비어 있어
  미확인 전달이 남지 않는 것을 검사합니다. NACK 콜백을 교체하는 계측은 사용하지 않습니다.
- 종료: 앱·native client를 닫고 HTTP listener의 재사용을 확인합니다. 테스트가 만든 고유 topic,
  queue, exchange만 삭제합니다. compose 정리는 위 프로젝트의 컨테이너와 네트워크만 대상으로 합니다.

이 결과는 exactly-once나 DB/브로커 원자적 처리, 운영 환경의 TLS·인증·장애 복구를 증명하지 않습니다.
복수 factory는 정적 검사와 build/test로 검증하며, 위 실제 처리 경로는 플랫폼별 단일 factory 구성을 대상으로 합니다.

## 증거 구분

| 구분 | 근거 | 확인 범위 |
| --- | --- | --- |
| CLI 회귀 | 전체 `go test -race`, vet, diff 검사 | 생성·분석·해시·rollback·기존 HTTP/native hook·등록 동작 |
| 생성 결과 | 공개 모듈 matrix 14개 구성 | 설치·버전·정적 검사·build/test·update 소스 보존 |
| HTTP/검증기 | 생성 앱의 실제 HTTP/WS 테스트 | JSON·폼·WS·규칙 오류·핸들러 차단·생략 동작 |
| 최소 도구 체인 | 실제 Go 1.25.5 설치/verify | 이벤트 앱 통과, playground Go 1.26 요구와 목적지 미게시 |
| 외부 E2E | 생성 앱의 실제 Kafka/RabbitMQ 8개 경로 | 발행·consumer·결과·settlement·재전달 정책·종료 |

2026-11-03 검증 결과는 이 표의 범위에 한정합니다. 실제 실행 로그는 작업 결과와 PR에 구분해 보고합니다.
