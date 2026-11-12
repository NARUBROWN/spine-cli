# Spine CLI

[Spine](https://github.com/NARUBROWN/spine) 앱을 생성하고, 실행 계약을 검사하고, API 명세를 내보내는 Go CLI입니다. 설치한 실행 파일은 `spine`입니다.

**Preview 채널** | [공개 태그 `v0.4.4-preview.4`](https://github.com/NARUBROWN/spine-cli/tree/v0.4.4-preview.4)

개발 중인 기능을 시험하고 호환성 문제를 확인하는 채널입니다.

다른 채널은 [RC](https://github.com/NARUBROWN/spine-cli/tree/rc) | [Stable](https://github.com/NARUBROWN/spine-cli/tree/main)에서 확인하세요.

## 빠른 시작

Go 1.25.5 이상이 필요합니다. `--validator playground`를 선택한 앱에는 Go 1.26.0 이상이 필요합니다.

```sh
go install github.com/NARUBROWN/spine-cli/cmd/spine@v0.4.4-preview.5
spine version
spine new todo-api --module github.com/acme/todo-api --channel preview --no-interactive
cd todo-api
spine dev
```

```sh
curl http://localhost:8080/health
```

`/health`에서 HTTP 200과 `{"status":"ok"}`가 반환됩니다. `--channel preview`는 코어와 플랫폼의 개발 브랜치를 조회합니다. 실제로 선택된 버전은 생성된 `go.mod`에서 확인하세요.

## 자주 쓰는 명령

| 명령 | 하는 일 |
| --- | --- |
| `spine new` | Spine 프로젝트 생성 |
| `spine dev` | 개발 서버 실행과 변경 감지 |
| `spine g resource` | 리소스에 필요한 구성 요소 생성 |
| `spine contract check` | 앱에 등록된 실행 계약 검사 |
| `spine verify` | 정적 계약, 서식, `go vet`, 테스트 검사 |
| `spine update` | 프로젝트의 Spine과 플랫폼 의존성 갱신 |

기존 파일을 수정하는 생성 작업은 `--dry-run --json`으로 변경을 확인하고, 검토한 `base_hash`를 `--if-match`로 전달하세요. CLI 자체는 위의 `go install` 명령으로 갱신합니다.

## Spine Cloud 미리보기

`spine cloud auth login`으로 로그인하고, `spine.toml`이 있는 프로젝트에서 `spine deploy`를 실행합니다. 지원 옵션은 `spine cloud auth --help`와 `spine deploy --help`에서 확인하세요.

Echo 또는 net/http 앱에 프런트엔드가 설정되어 있으면 `spine deploy`가 프런트엔드를 빌드하고 정적 파일을 Linux ARM64 실행 파일에 내장합니다. 배포 전에 프런트엔드의 npm 의존성을 설치하세요. 정적 파일을 포함한 실행 파일은 32 MiB 이하여야 합니다.

## React에서 루트 환경 변수 사용하기

React CSR 통합은 `spine.toml`이 있는 루트의 `.env`에 선언한 변수를 프런트엔드 빌드에 전달합니다.
변수 이름에 접두사를 붙이거나 프런트엔드 폴더에 `.env`를 복사할 필요가 없습니다.

```dotenv
# 프로젝트 루트의 .env
API_URL=/api
APP_NAME=Spine
```

```ts
const apiURL = import.meta.env.API_URL
```

`.env`에 선언한 이름과 같은 실행 환경 변수가 있으면 그 값을 우선합니다. 값은 문자열이며,
없는 변수는 `undefined`입니다. `.env`에 넣은 값은 프런트엔드 번들에 포함되어 브라우저에 공개됩니다.

`spine dev`는 루트 `.env`의 생성, 수정, 삭제를 감지해 프런트엔드를 다시 빌드합니다. 페이지를
새로고침하면 변경된 값을 읽습니다. `spine run`, `spine build`, `spine deploy`는 빌드 시점의 값을 사용하므로
배포 환경에서 값을 바꿨다면 다시 빌드해야 합니다. 읽는 파일은 루트 `.env` 하나이며,
프런트엔드 폴더의 `.env`와 모드별 `.env.*`는 읽지 않습니다. 서버의 `.env` 로딩은 앱에서 관리합니다.

## 더 보기

- [플랫폼 연결](PLATFORMS.md)
- [버전별 마이그레이션](migration/)
- [자동화를 위한 명령 계약](llms.txt)
- [릴리스 운영](release/README.md)
- [MIT 라이선스](LICENSE)
