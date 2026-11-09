# 플랫폼과 validator 동적 조회

HTTP, WebSocket, 이벤트 플랫폼과 validator는 CLI를 수정하지 않고 저장소 규칙과 `spine-platform.json`으로 연결합니다. CLI는 요청한 이름의 `@preview`를 정규 모듈 버전으로 해석한 뒤, 그 버전의 메타데이터를 읽습니다.

| 옵션 | 조회할 모듈 |
| --- | --- |
| `--event-platform [name:]provider` | `github.com/NARUBROWN/spine-<provider>-platform@preview` |
| `--platform provider` | `github.com/NARUBROWN/spine-<provider>-platform@preview` |
| `--websocket-platform provider` | `github.com/NARUBROWN/spine-<provider>-platform@preview` |
| `--validator provider` | `github.com/NARUBROWN/spine-validator-<provider>@preview` |
| `--validator none` | 검증기 생략, 기본값 |

provider 이름은 영문 소문자로 시작하며 소문자, 숫자, `-`, `_`를 사용합니다. 구분자를 연속하거나 이름 끝에 넣을 수 없습니다. 이벤트 등록 이름은 기존 `[a-z][a-z0-9_]*` 규칙을 따릅니다. 등록 이름을 생략하면 provider 이름의 `-`를 `_`로 바꿉니다.

HTTP와 WebSocket은 각각 별도 provider 모듈을 사용하며, WebSocket을 선택하려면 HTTP가 필요합니다. 한 모듈을 서로 다른 플랫폼 종류에 동시에 사용할 수 없습니다. 기본값은 HTTP `echo`, WebSocket `none`입니다.

예를 들어 `myhttp`와 `mysocket` 모듈이 다음 형식의 메타데이터를 제공한다면 `--platform myhttp --websocket-platform mysocket`으로 선택할 수 있습니다. 이 이름은 형식을 설명하는 예시입니다.

## 생성과 검토

```sh
spine new worker --platform none --event-platform events:nats \
  --validator playground --dry-run --json
spine new worker --platform none --event-platform events:nats \
  --validator playground --if-match <base_hash> --json
```

결과의 `module_queries`는 조회 경로, `target_modules`는 해석한 버전, `platform_descriptors`는 메타데이터 SHA-256입니다. 버전과 메타데이터가 바뀌면 이전 계획의 `--if-match`를 거부합니다. 메타데이터의 최소 Go 버전과 기본 레시피에서 더 높은 버전을 선택하며 실제 의존성 요구는 Go 도구로 확인합니다.

`--skip-install`도 Go와 네트워크 또는 모듈 캐시가 필요합니다. 선택한 모듈을 Go 캐시에 내려받아 메타데이터를 읽지만 프로젝트의 전체 의존성 설치, 정적 검사와 빌드는 생략합니다. 메타데이터 조회 중에는 플랫폼의 생성자와 코드를 실행하지 않습니다.

## 메타데이터 형식

선택할 모듈의 루트에 `spine-platform.json`을 포함하세요. 파일 크기는 64 KiB 이하, 설정 필드는 64개 이하입니다. 알 수 없는 키와 지원하지 않는 스키마, kind 또는 계약 버전은 거부합니다.

| 필드 | 내용 |
| --- | --- |
| `schema_version` | `spine.platform/v1`, CLI 메타데이터 형식 |
| `kind` | `http`, `websocket`, `event`, `validator` 중 하나 |
| `provider` | 옵션에 지정한 provider와 같은 이름 |
| `contract_version` | HTTP는 `spine.http-adapter/v3`, WebSocket은 `spine.websocket-adapter/v1`, event는 `spine.event-adapter/v1`, validator는 `spine.validation/v1` |
| `package` | Go 패키지 이름 |
| `factory` | event와 validator의 생성자 또는 HTTP와 WebSocket의 옵션 함수 이름 |
| `config_type` | 공개 설정 구조체 이름. event는 필수, validator는 생략 가능 |
| `minimum_go` | `1.26.0`처럼 전체 Go 버전 |
| `fields` | 설정 구조체 필드 목록. 설정 없는 validator는 생략 |

이벤트 생성자는 `Factory(Config) (event.Factory, error)` 형태입니다. validator 생성자는 설정이 있으면 `Factory(Config) (validator.Validator, error)`, 없으면 `Factory() (validator.Validator, error)` 형태입니다. 반환하는 구체 타입이 해당 인터페이스를 구현해도 됩니다. 생성자와 설정 필드는 실제 모듈에 있어야 하며 기본 생성의 컴파일 단계에서 확인합니다.

HTTP와 WebSocket의 `factory`는 인자 없이 `spine.Option`을 반환하는 공개 함수입니다. 이 두 종류의 메타데이터에는 `config_type`과 `fields`를 넣지 않습니다. Echo, net/http, Gorilla의 기존 메타데이터 없는 공개 버전은 CLI의 호환 설명을 사용합니다.

예를 들어 `--platform myhttp`에 사용할 모듈의 메타데이터는 다음과 같습니다.

```json
{
  "schema_version": "spine.platform/v1",
  "kind": "http",
  "provider": "myhttp",
  "contract_version": "spine.http-adapter/v3",
  "package": "platform",
  "factory": "Option",
  "minimum_go": "1.25.5"
}
```

WebSocket 모듈은 `kind`에 `websocket`, `contract_version`에 `spine.websocket-adapter/v1`을 지정합니다. 서로 다른 종류의 provider는 별도 모듈을 사용합니다.

설정 없는 validator 예제입니다.

```json
{
  "schema_version": "spine.platform/v1",
  "kind": "validator",
  "provider": "myrules",
  "contract_version": "spine.validation/v1",
  "package": "myrules",
  "factory": "New",
  "minimum_go": "1.26.0"
}
```

설정 구조체가 있으면 `config_type`과 `fields`를 추가합니다.

```json
{
  "field": "URLs",
  "type": "strings",
  "env": "URLS",
  "required": true,
  "example": ["nats://127.0.0.1:4222"]
}
```

`field`는 공개 Go 필드 이름입니다. `type`은 `string`, `strings`, `bool`, `int`, `duration` 중 하나이며 `duration`은 `"5s"`처럼 선언합니다. `strings`의 환경 값은 쉼표로 구분합니다. `env`는 대문자 환경 변수 접미사입니다. 환경 값을 읽지 않는 필드는 `default`를 선언해야 합니다. `default`와 `example`에는 해당 타입의 JSON 리터럴만 허용하며 Go 코드나 템플릿을 넣지 않습니다.

필수 환경 변수는 `required: true`와 비어 있지 않은 `example`이 필요합니다. `example`은 생성 앱의 설정 검사에 사용하며 실제 운영 값을 대신하지 않습니다. 이벤트 환경 변수는 `SPINE_<NAME>_<ENV>`, validator는 `SPINE_VALIDATOR_<PROVIDER>_<ENV>`를 사용합니다. validator provider의 `-`는 환경 변수에서 `_`로 바꿉니다.

NATS의 전체 예제는 해당 모듈의 `spine-platform.json`에 있습니다. 기존 메타데이터 없는 Kafka, RabbitMQ와 playground 공개 버전은 CLI의 호환 설명을 사용합니다. 새 구현에는 이 호환 경로를 자동으로 적용하지 않습니다.

## 실패와 검증 범위

| 상황 | 결과 |
| --- | --- |
| 이름 규칙 위반 | 사용법 오류, 종료 코드 2 |
| 저장소나 Preview 브랜치를 찾지 못하거나 접근할 수 없음 | 기대한 모듈 경로와 Go 조회 원인을 표시, 종료 코드 3 |
| 네트워크 또는 도구 오류 | 조회 실패의 원인을 보존하며 저장소 부재로 단정하지 않음 |
| 메타데이터 없음 또는 스키마 불일치 | 모듈 경로와 메타데이터 오류를 표시, 종료 코드 3 |
| 검토 후 버전이나 메타데이터 변경 | 이전 계획 거부, 종료 코드 4 |

실패한 조회는 프로젝트 파일을 게시하지 않습니다. `spine update`는 의존성과 메타데이터를 다시 확인하며 앱 소스는 바꾸지 않습니다. 이전 고정 레시피에 새 provider를 추가한 경우 자동으로 Preview 레시피로 전환하지 않습니다.

정적 계약 검사는 선택한 모듈 캐시의 메타데이터로 생성자를 식별합니다. factory 설정값과 검증 규칙, 실제 역할과 연결 가능성은 실행하거나 증명하지 않습니다. 실제 브로커와 HTTP 검증은 별도 E2E로 확인하세요. [생성 앱 검사](test/assembly-e2e/README.md)는 CLI에 등록하지 않은 이벤트 구현과 설정형 validator를 함께 생성해 검사합니다.
