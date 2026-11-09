# Spine CLI

[Spine](https://github.com/NARUBROWN/spine) 앱을 생성하고, 실행 계약을 검사하고, API 명세를 내보내는 Go CLI입니다. 설치한 실행 파일은 `spine`입니다.

**RC 채널** | [공개 태그 `v0.4.3-rc.3`](https://github.com/NARUBROWN/spine-cli/tree/v0.4.3-rc.3)

정식 릴리스 전에 기존 앱과의 호환성을 확인하는 채널입니다.

다른 채널은 [Preview](https://github.com/NARUBROWN/spine-cli/tree/preview) | [Stable](https://github.com/NARUBROWN/spine-cli/tree/main)에서 확인하세요.

## 빠른 시작

Go 1.25.5 이상이 필요합니다. `--validator playground`를 선택한 앱에는 Go 1.26.0 이상이 필요합니다.

```sh
go install github.com/NARUBROWN/spine-cli/cmd/spine@v0.4.3-rc.3
spine version
spine new todo-api --module github.com/acme/todo-api --channel rc --no-interactive
cd todo-api
spine dev
```

```sh
curl http://localhost:8080/health
```

`/health`에서 HTTP 200과 `{"status":"ok"}`가 반환됩니다. `--channel rc`는 공개 RC와 정식 태그 중 최신 버전을 선택합니다. 정식 의존성이 선택될 수 있으니 생성된 `go.mod`를 확인하세요.

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

## 더 보기

- [플랫폼 연결](PLATFORMS.md)
- [버전별 마이그레이션](migration/)
- [자동화를 위한 명령 계약](llms.txt)
- [릴리스 운영](release/README.md)
- [MIT 라이선스](LICENSE)
