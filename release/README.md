# CLI 릴리스 운영

CLI를 `preview → rc → main` 순서로 승격하고 각 채널에서 태그를 발행해 공개 설치와 생성 결과를 검사합니다. GitHub Release는 Production에서만 발행합니다. CLI의 버전과 출시 일정은 Spine과 독립적입니다. 워크플로 파일 추가만으로 GitHub 설정이나 공개 태그가 생성되지는 않습니다.

| 단계 | 태그 예시 | 검증 기록 | GitHub Release |
| --- | --- | --- | --- |
| Preview | `v0.4.3-preview.1` | Actions artifact | 발행하지 않음 |
| RC | `v0.4.3-rc.1` | Actions artifact | 발행하지 않음 |
| Production | `v0.4.3` | Actions artifact와 Release 첨부 파일 | 정식 발행 |

## 정책과 승인

`.github/release-policy.json`에 다음 버전, 관찰 시간, 기본 생성 레시피와 공개 검증 OS를 고정합니다. 초기 대상은 `v0.4.3`이고 Preview는 72시간, RC는 24시간 관찰합니다. `stable_interval_hours: 0`은 정식 출시의 추가 최소 간격을 두지 않는다는 뜻입니다. 관찰 시간과 필수 검증은 그대로 적용합니다.

Preview와 RC는 `vX.Y.Z-preview.N`, `vX.Y.Z-rc.N`, 정식은 `vX.Y.Z` 태그를 사용합니다. 일반 설치의 `@latest`는 정식 버전을 우선합니다. [Go 버전 선택](https://go.dev/ref/mod#version-queries)

Preview 기본 생성은 `platform-contracts-preview` 레시피로 각 Spine 모듈의 `@preview`를 조회합니다. 발행 계획은 조회 조건과 해석된 고정 버전을 manifest에 함께 기록합니다. 검증 중 브랜치가 이동해 계획과 생성 결과가 달라지면 발행을 중단하고 새 계획으로 다시 검증합니다.

CLI Preview 발행 계획을 만들기 전에 코어 Preview 후보를 발행하고, Echo, net/http, Gorilla WebSocket, Kafka, RabbitMQ, NATS와 playground validator를 그 코어 후보로 발행합니다. 각 저장소의 최신 Preview 태그가 Preview HEAD와 일치하고 공개 설치 검증 artifact가 성공해야 합니다. CLI는 각 artifact의 출처와 체크섬, 검증한 코어 버전을 확인하고 그 결과를 발행 계획에 기록합니다. 기본 생성 레시피의 코어와 Echo 버전도 각 발행 태그와 같아야 합니다.

CLI 저장소의 `RELEASE_EVIDENCE_TOKEN` secret에는 코어와 7개 플랫폼 저장소의 Actions 읽기 권한이 있는 자격 증명이 필요합니다. 이 값은 선행 저장소의 검증 artifact 조회에만 사용합니다. CLI 태그 생성은 기존 워크플로 토큰과 승인 환경을 사용합니다. secret이 없거나 읽기 권한이 부족하면 발행을 중단합니다.

전체 Preview 발행은 `scripts/release/release_train.py --plan 계획.json`으로 먼저 확인합니다. JSON 객체에 코어, 위 7개 플랫폼과 CLI의 전체 모듈 경로를 키로 넣고, 각각 `version`과 `source_sha`를 지정합니다. 각 Preview 브랜치에 후보 변경을 미리 병합해야 합니다. 실행 전 모든 저장소의 후보 번호, 진행 중인 발행과 기존 검증 기록을 확인합니다. 확인한 계획에 `--execute`를 추가하면 코어, 플랫폼, CLI 순서로 각 저장소의 기존 Preview 발행 workflow를 실행하고 앞 단계의 성공을 기다립니다. 기존 승인 환경은 그대로 적용되므로 승인이 필요한 단계는 지정 리뷰어가 처리해야 합니다. GitHub CLI에는 각 저장소의 Actions 실행 권한이 필요합니다.

CLI는 태그 생성 전에 선행 발행 기록을 다시 확인하고, 태그 생성 후 기록이 달라지면 최종 manifest를 만들지 않습니다. 순차 실행은 다른 작업자의 동시 발행을 잠그지 않습니다. CLI의 발행 후 검증이 끝날 때까지 별도의 코어와 플랫폼 Preview 발행을 시작하지 마세요.

세 채널 모두 `release-preview`, `release-rc`, `release-stable` 승인 환경을 사용합니다. 지정 리뷰어가 태그, 검증 계획과 릴리스 노트 초안을 검토한 뒤 승인해야 태그를 만듭니다. 환경은 해당 채널 브랜치만 허용하며 관리자 우회를 허용하지 않습니다. [GitHub 승인 환경](https://docs.github.com/en/actions/how-tos/deploy/configure-and-manage-deployments/manage-environments)

릴리스 계획 artifact의 `release-title.txt`, `release-notes.md`, `release-plan.json`을 승인 전에 확인합니다. 본문은 `release/notes/vX.Y.Z.md`와 계획의 버전, 소스, 이전 단계, 레시피 해시, 긴급 단축 사유로 구성합니다. 본문 해시를 계획에 저장하고 태그 생성 직전에 다시 계산합니다. Preview와 RC에서는 이 초안을 GitHub Release에 게시하지 않습니다. 본문을 수정하면 새 소스에서 새 계획을 검토해야 합니다. 테스트 결과는 성공을 확인한 뒤 manifest에 첨부합니다.

## 최초 전환

1. 구현 PR을 기존 `main`에 병합하고 `Verified Source / main`의 성공을 확인합니다. 기본 브랜치가 `main`인 초기 상태에서는 작업 PR의 `main` 대상이 허용됩니다.
2. 병합한 소스를 체크아웃하고 공개 `main`의 전체 SHA를 확인합니다. 아래 명령의 SHA를 실제 값으로 바꿉니다.
3. 저장소 관리 권한이 있는 계정으로 GitHub CLI에 로그인한 뒤 초기 전환을 적용합니다.

```sh
GITHUB_REPOSITORY=NARUBROWN/spine-cli \
  python3 scripts/release/configure_branches.py --apply --bootstrap-sha <MAIN_FULL_SHA>
```

이 명령은 `main`에 제어기 파일이 있는지 확인하고 같은 커밋에서 `preview`, `rc`를 만듭니다. 기존 채널 브랜치가 다른 커밋을 가리키면 중단합니다. 브랜치 강제 이동이나 기존 main 보호 제거는 수행하지 않습니다. 채널별 PR과 필수 상태 규칙, 태그 변경과 삭제 방지, 승인 환경을 구성하고 기본 브랜치를 `preview`로 바꿉니다.

기존 환경에 다른 브랜치 규칙이 있거나 기존 main 보호가 있으면 별도 검토가 필요합니다. API 오류 중간에 일부 설정이 반영될 수 있으므로 결과를 확인한 뒤 같은 소스에서 재실행합니다. 기존 규칙을 자동 삭제하지 않습니다. 설정을 다시 적용할 때는 `--bootstrap-sha`를 생략합니다.

```sh
GITHUB_REPOSITORY=NARUBROWN/spine-cli python3 scripts/release/configure_branches.py
GITHUB_REPOSITORY=NARUBROWN/spine-cli python3 scripts/release/release.py check-environment
```

두 명령은 읽기 전용입니다. 첫 명령은 브랜치, 태그와 승인 설정을 검사합니다. PR 방향 검사는 기본 브랜치가 `preview`로 바뀐 뒤 작업 → preview, preview → rc, rc → main만 허용합니다. rc와 main 승격은 merge commit을 사용합니다.

## 다음 버전 준비

작업 브랜치를 `preview`에서 만듭니다. 버전별 준비 작업에서 정책의 `next_version`, `internal/cli/version.go`의 개발 버전과 `release/notes/vX.Y.Z.md`를 함께 갱신합니다. 일반 변경은 기존 관찰 중인 후보에 섞지 말고 새 Preview로 검증합니다.

공개 설치는 Go 빌드 정보의 실제 태그나 pseudo-version을 표시합니다. 로컬 빌드는 개발 버전으로 구분합니다. `schema`와 `capabilities`의 기존 계약을 유지하며 capabilities에도 같은 버전을 표시합니다.

기본 레시피의 식별자와 모듈 목록을 정책에 기록합니다. 공개 설치 검사에서 CLI가 실제 생성한 `spine.toml`, `go.mod`와 선택된 모듈 그래프를 비교합니다. 기존 프로젝트의 개발 레시피는 보존하며 정식 CLI의 새 기본 레시피에는 정식 모듈 버전을 지정합니다. Preview와 RC에서는 변경 불가능한 후보 모듈 버전도 고정할 수 있지만, 정식 모듈로 변경한 뒤 새 Preview부터 다시 검증해야 합니다.

## 공통 검증

| 검사 | 범위 |
| --- | --- |
| 정책 검사 | 태그 재사용, 관찰 시간, 잘못된 승격, 승인 설정, 본문 변경, OS 증거 누락 |
| Portable CLI | 세 OS의 빌드, 버전, JSON 프로토콜과 선택한 이식성 검사 |
| Unix 종료 | Linux와 macOS에서 CLI PID의 SIGTERM 전달, 4초 정리 완료, 시간 초과와 자식 회수 |
| 전체 회귀 | Linux에서 전체 race 검사, 공개 생성 matrix, WS, 실제 Kafka와 RabbitMQ, 최소 Go 1.25.5 |
| 공개 설치 | 세 OS의 독립 GOBIN, GOMODCACHE, GOCACHE에서 두 실행 진입점 설치 |
| 생성 앱 | 두 layout과 HTTP, WS, 복합 플랫폼, worker 조합의 생성, 버전, 계약, verify, update |
| 실제 HTTP 종료 | 두 layout의 run과 dev에서 4초 요청 완료와 리스너 해제 |

브로커와 최소 Go 환경이 없어 필수 E2E가 skip되면 `check_tests.py`가 검증 실패로 처리합니다. Windows는 전체 Unix 신호 회귀와 같은 범위를 제공하지 않습니다. 공개 설치 검사의 Windows 앱 종료는 강제 종료 fallback과 리스너 해제만 확인합니다.

## Preview 발행

작업 PR을 `preview`에 병합한 후 해당 브랜치에서 `Publish Preview`를 수동 실행합니다. 버전은 새 `vX.Y.Z-preview.N`, source_sha는 그 브랜치 HEAD의 전체 SHA를 지정합니다.

워크플로는 다음 순서로 실행합니다.

1. 브랜치 HEAD, 정책과 승인 환경을 검사하고 계획과 릴리스 노트 초안을 artifact로 만듭니다.
2. 정확한 SHA의 공통 CI와 세 OS의 공개 SHA 설치 검사를 실행합니다.
3. `release-preview` 승인 대기에서 태그, 계획과 릴리스 노트 초안, 검증 결과를 검토합니다.
4. 승인 후 HEAD와 정책, 본문 해시를 다시 검사하고 새 태그를 만듭니다.
5. 세 OS에서 그 태그를 공개 설치하고 동일한 생성 검사를 실행합니다.
6. 모두 성공하면 `release-manifest-<태그>` Actions artifact에 `release-manifest.json`을 저장합니다. Preview의 GitHub Release는 만들지 않습니다.

GitHub 토큰이 만든 태그의 push 이벤트에 의존하지 않습니다. 태그 전후 검사는 재사용 워크플로를 직접 호출합니다. 발행 검증 작업 하나가 실패하면 뒤 단계는 실행하지 않습니다. Discord 알림 실패는 경고로 처리해 발행 검증을 계속합니다.

## RC와 Production 승격

Preview 관찰을 마치면 검증한 후보를 `preview → rc` PR로 병합합니다. 후보가 merge commit의 두 번째 부모이며 소스 트리가 같은지 검사합니다. rc 브랜치에서 `Publish RC`를 실행하고 출처 Preview 태그와 새 RC 버전을 지정합니다. 같은 버전 계열의 공개 태그, 성공한 발행 워크플로와 Actions manifest가 필요합니다. RC도 태그와 Actions 검증 기록만 남깁니다.

RC 관찰을 마치면 같은 방식으로 `rc → main` PR을 병합합니다. main에서 `Publish Stable`을 실행하고 출처 RC 태그를 지정합니다. 정식 버전은 RC에서 결정합니다. 각 단계에서 전체 검사, 세 OS의 공개 설치, 초안 검토와 환경 승인을 반복합니다. Production의 태그 설치 검증까지 성공하면 정식 GitHub Release를 만들고 manifest를 첨부합니다.

승격 검사는 출처 태그의 Actions artifact가 하나인지 확인하고 artifact ID로 내려받아 SHA-256을 검증합니다. 저장소, 버전, 브랜치, 소스 SHA와 트리, 실행 ID와 재실행 번호, 레시피, 세 OS의 검증 결과가 일치해야 합니다. 발행 워크플로 전체가 성공해야 승격할 수 있습니다. 출처 artifact의 ID와 해시도 승인 계획에 고정합니다.

Preview와 RC의 관찰 시간은 태그 설치 검증이 끝난 뒤 manifest artifact를 저장한 시점부터 계산합니다. artifact 보관 기간은 90일로 요청합니다. 저장소의 보관 정책 안에서 사용해야 하며, 기록이 삭제되거나 만료되면 해당 후보의 승격을 중단하고 새 Preview부터 검증합니다. [GitHub Actions artifact 보관](https://github.com/actions/upload-artifact#retention-period)

승격 merge의 커밋 SHA는 달라도 소스 트리와 레시피는 같아야 합니다. 코드나 의존성, 릴리스 글을 수정하면 새 Preview로 돌아갑니다. 승인 대기 중 채널 HEAD가 바뀌어도 중단합니다. 긴급 관찰 시간 단축은 20자 이상의 사유가 필요하며 CI, 공개 설치와 승인은 생략하지 않습니다.

정식 태그가 생성되면 GitHub Release 글이 공개되기 전에도 Go의 `@latest`에 반영될 수 있습니다. 태그 전 검증과 승인을 생략하면 안 됩니다.

## Spine과의 호환 조합

Spine만 바뀌고 기존 CLI가 호환되면 검증된 정식 CLI를 그대로 고정합니다. 새 API 때문에 CLI가 바뀌어야 하면 후보 CLI와 후보 Spine 버전을 정확히 고정해 검사합니다. 두 저장소의 정식 출시를 서로의 필수 선행 조건으로 만들지 않습니다.

Spine 후보를 정식 버전으로 바꾸는 CLI 레시피 수정도 새 후보 검증 대상입니다. 코어 측에서 생성 앱의 의존성을 후보로 변경한 검증은 CLI 기본 생성 결과의 검증과 구분합니다.

## 종료 대기 설정

run과 dev는 Unix에서 종료 요청을 앱의 SIGINT로 전달하고 기본 15초 동안 기다립니다. `SPINE_CLI_SHUTDOWN_TIMEOUT=30s`처럼 양의 duration을 지정할 수 있습니다. 0, 음수와 잘못된 값은 앱을 시작하기 전에 거부합니다. dev의 재시작도 같은 대기를 사용합니다. 제한 시간을 넘으면 강제 종료를 기록하고 CLI 종료 요청에서는 실패 코드로 반환합니다.

새 HTTP 앱은 `EnableGracefulShutdown: true`를 사용합니다. 기존 앱은 이 옵션을 별도로 적용해야 합니다. 앱의 종료 제한 시간을 늘리면 CLI의 대기도 충분히 길게 지정하세요. Windows는 자식 프로세스 interrupt 전송을 지원하지 않아 기존 강제 종료 경로를 유지합니다.

## 실패와 복구

태그 생성 전 실패는 수정 후 새 계획으로 검증합니다. Preview나 RC의 태그 생성 후 설치 검사가 실패하면 해당 후보는 승격할 수 없습니다. 실패한 실행은 workflow 로그와 artifact에서 확인하고 새 번호로 다시 발행합니다.

CLI `v0.4.3-preview.11`은 코어 태그가 CLI 발행 중 생성되어 태그 생성 후 세 운영체제 검사가 실패했습니다. 기존 태그와 기존 계획을 수정하거나 별도 단일 운영체제 검증 결과로 공식 manifest를 만들지 않습니다. 코어에 맞춘 모든 플랫폼 후보의 발행 검증을 완료한 다음 CLI Preview를 새 번호로 발행하고 세 운영체제의 공개 설치와 최종 manifest를 다시 확인하세요.

정식 태그 이후 실패는 태그를 삭제하거나 이동해 감추지 않습니다. 이전 검증 버전을 명시해 CLI를 재설치하고 수정 버전을 준비합니다. 잘못된 버전의 자동 선택을 막아야 하면 새 정식 버전의 go.mod에 retract를 추가하는 방법을 검토합니다. [Go retract](https://go.dev/ref/mod#go-mod-file-retract)

CLI 재설치는 생성 앱을 원복하지 않습니다. 프로젝트 소스, go.mod, go.sum은 프로젝트의 Git 이력이나 백업으로 별도 복구합니다.

## 도입 완료 판단

설정 적용과 워크플로 추가는 준비 단계입니다. 실제 Preview 공개 설치, RC 승격, 최종 글 승인, Production 공개 설치까지 성공한 뒤 첫 운영 완료로 판단합니다. 로컬 단위 검사나 로컬 모듈 프록시 설치 성공을 GitHub의 공개 발행 성공으로 보고하지 않습니다.

## Discord 태그 알림

태그 생성에 성공하면 바로 [Spine 공통 Discord Action](https://github.com/NARUBROWN/spine/tree/b55c182f4d0722fca5aaf3b963bca20cf0dcc4d4/.github/actions/discord-tag)을 호출합니다. 알림 제목에 저장소 이름과 태그를 표시하고 태그 페이지와 발행 실행 기록으로 연결합니다. 이 알림은 태그 생성 시점의 기록이며 공개 설치 검증과 Release 게시 완료를 보장하지 않습니다.

| 태그 형식 | GitHub Actions repository secret |
| --- | --- |
| `vX.Y.Z-preview.N` | `DISCORD_WEBHOOK_PREVIEW` |
| `vX.Y.Z-rc.N` | `DISCORD_WEBHOOK_RC` |
| `vX.Y.Z` | `DISCORD_WEBHOOK_RELEASE` |

각 Discord 채널의 webhook URL을 이 저장소의 **Settings → Secrets and variables → Actions**에 위 이름으로 등록합니다. URL은 코드나 로그에 넣지 않습니다. 공통 Action은 전체 커밋 SHA로 고정하며 호출 workflow는 위 세 secret만 전달합니다.

`GITHUB_TOKEN`으로 만든 태그의 후속 이벤트에 의존하지 않고 기존 발행 workflow에서 직접 전송합니다. secret 누락과 전송 오류는 경고를 남기고 발행 검증을 계속합니다. 자동 재시도는 하지 않으며 응답 유실 시 채널 수신 여부를 먼저 확인합니다. 수동 태그 push와 GitHub Release 이벤트는 별도 알림으로 연결하지 않습니다.
