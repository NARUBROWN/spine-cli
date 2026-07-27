# Spine CLI

Spine 애플리케이션의 기술적인 골격을 생성하는 Go CLI입니다.

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
spine g resource user # 생성과 동시에 main.go에 등록
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

`resource`는 선택적인 기능 경계입니다. 모듈식 또는 DDD 스타일로 구성하고 싶을 때 사용합니다. `module.go`의 `Register(app spine.App)`가 해당 resource의 생성자와 라우트를 등록하며, CLI가 `main.go`에 이를 자동으로 등록합니다. 필요한 경우에만 `--no-register`를 사용하세요.

```go
receipt.Register(app)
user.Register(app)
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

## 개발 워크플로우

```sh
# Go 파일 변경을 감지해 앱을 재시작합니다.
spine dev

# 운영 환경 변수(SPINE_ENV=production)로 실행합니다.
spine run

# 정적 검사와 테스트를 실행합니다.
spine check

# bin/<모듈명>에 바이너리를 생성합니다.
spine build
```

`spine.toml`은 CLI가 읽는 유일한 프로젝트 설정 파일입니다. 새 프로젝트에 자동 생성되며 모듈 경로, 실행 엔트리포인트, 포트를 포함합니다.

```toml
module = "github.com/acme/todo-api"
entrypoint = "."
port = 8080
```

## 안전한 생성과 셸 지원

생성 명령은 기존 파일을 덮어쓰지 않습니다. 의도적으로 교체할 때만 `--force`를 사용하세요. 결과만 확인하려면 `--dry-run`, 자동화 용도에는 `--json`을 사용할 수 있습니다.

```sh
spine g resource orders --dry-run
spine g resource orders --json
spine completion powershell | Invoke-Expression
```
