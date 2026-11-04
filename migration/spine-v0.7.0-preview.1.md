# Spine v0.7.0 Preview 대응

이 문서는 이번 변경을 포함한 CLI Preview 소스에서 새 앱을 만들거나 기존 입력을 전환하는 절차입니다. 새 생성 레시피는 코어 `v0.7.0-preview.1`과 선택한 플랫폼의 `v0.2.0-preview.1`을 사용합니다. 정식 CLI의 `@latest` 설치에 이 변경이 포함됐다고 가정하지 마세요.

## 생성 레시피 확인

CLI 소스를 빌드한 뒤 schema와 capabilities를 확인합니다.

```sh
go build -o ./bin/spine ./cmd/spine
./bin/spine schema --json
./bin/spine capabilities --json
./bin/spine new shop --module example.com/shop --validator playground --dry-run --json
./bin/spine new shop --module example.com/shop --validator playground --if-match <base_hash> --json
```

playground 앱에는 Go 1.26.0 이상이 필요합니다. 검증기를 생략한 앱과 CLI 자체의 최소 버전은 Go 1.25.5입니다. 생성 계획의 `target_modules`, `changes`, `diff`와 `base_hash`를 검토한 뒤 같은 hash로 적용하세요. 실제 설치 모드에서는 공개 모듈을 해석하고 정적 검사와 빌드를 실행합니다. `--skip-install`은 파일만 생성합니다.

기존 `platform-contracts-v0.6.5`, 개발 레시피와 recipe 없는 Echo 프로젝트의 `update`는 각 레시피를 유지합니다. Preview로 전환할 때는 전체 버전 조합과 소스를 함께 검토하고, 새 레시피로 `spine.toml`을 바꾼 뒤 `update --dry-run --json`의 diff를 확인하세요. `update`가 앱 소스를 자동으로 전환하지는 않습니다.

## 출처별 입력 DTO

본문, path, query와 header를 별도 인자로 받습니다. path 키는 필드의 태그와 경로 이름을 맞춥니다.

```go
type ProductPath struct {
    ID int64 `path:"id" validate:"gt=0"`
}

type ProductQuery struct {
    Tags []string `query:"tag"`
    Page *int     `query:"page" validate:"omitempty,min=1"`
}

type ProductHeaders struct {
    Host string `header:"Host"`
}

func (*ProductController) Get(path *ProductPath, query *ProductQuery, headers *ProductHeaders) httpx.Response[map[string]any] {
    return httpx.Response[map[string]any]{Body: map[string]any{
        "id": path.ID, "tags": query.Tags, "host": headers.Host,
    }}
}
```

등록 경로가 `/products/:id`라면 `path:"id"`가 해당 값을 받습니다. query 배열은 반복 키와 쉼표 구분을 지원하며 공백을 임의로 제거하지 않습니다. `header:"Host"`는 요청의 Host 값을 받습니다. 필수 필드와 규칙은 등록한 validator 계약으로 판단하며, 포인터는 누락 또는 null 표현에 사용합니다. path 필드 자체는 생략할 수 없는 입력입니다.

`query.Values`, `header.Values`, `query.Pagination`과 path 래퍼는 Deprecated로 유지합니다. Preview 코어에서 이 래퍼는 값 인자로만 사용하며, 새 출처 DTO와 한 핸들러에서 혼용할 수 없습니다. 기존 본문 DTO와 context는 Deprecated 입력과 함께 쓸 수 있습니다.

CLI는 Deprecated 입력에 `HTTP_INPUT_API_DEPRECATED` 경고를 표시합니다. `--warnings-as-errors`를 사용하면 경고도 검사 실패가 됩니다. 새 DTO의 혼합 출처, 태그나 키 충돌, 같은 DTO 타입의 반복, 여러 본문 인자와 Deprecated API 혼용을 정적으로 검사합니다. 필드 decoder와 validator 규칙은 실행하지 않으므로 코어의 시작 검증과 실제 요청도 별도로 확인하세요.

## OpenAPI 활성화와 내보내기

HTTP로 명세를 제공하는 기능은 기본 비활성화입니다. composition의 HTTP 옵션에서 명시적으로 켭니다.

```go
HTTP: &boot.HTTPOptions{
    OpenAPI: boot.OpenAPIOptions{
        Enabled: true,
        Path:    "/openapi.json",
        Title:   "Shop API",
        Version: "preview",
    },
},
```

`Path`를 생략하면 `/openapi.json`을 사용합니다. CLI 없이 앱을 실행해도 명세를 제공합니다. 검증기를 등록하지 않은 앱은 필드 규칙을 추측하지 않고 경로 중심 정보만 제공합니다.

CLI로 명세 파일이 필요하면 프로젝트에서 다음 명령을 실행합니다.

```sh
spine contract export --format openapi --source runtime --output openapi.json
```

런타임 내보내기는 import할 수 있는 composition 패키지의 `BuildApplication() (spine.App, boot.Options, error)`를 호출한 뒤 코어의 `DescribeHTTP`와 `openapi.Generate`를 사용합니다. HTTP 리소스의 `Enabled` 설정과 별개로 명세를 내보내며, 리스너와 DI 생성자, 요청 핸들러를 시작하지 않습니다. 다만 composition 사용자 코드와 Go 도구는 실행하므로 설정 helper가 요구하는 환경 변수와 실행 부작용을 확인하세요. 앱의 HTTP가 비활성화돼 있거나 호환 코어 API가 없으면 실패합니다.

`--output`을 생략하면 JSON을 stdout으로 출력합니다. composition 로그는 명세 JSON에 섞지 않습니다. 기존 파일을 바꾸려면 `--force`가 필요합니다. 런타임 오류를 static 결과로 대체하지 않습니다.

`--source static`이 기본값이며 기존 AST 내보내기를 유지합니다. 새 path DTO의 이름은 반영하지만 필드 타입과 validator 제약은 미해결 정보로 남을 수 있습니다. AsyncAPI는 static만 지원합니다. 런타임 내보내기는 전체 DI와 interceptor 검증이나 브로커 준비 상태의 증거가 아닙니다.

## 전환 결과 확인

```sh
spine context --json
spine contract check --json
spine verify --json
```

`contract check`의 분석 완료 여부와 오류를 확인한 뒤 실제 HTTP 요청, 필요한 WebSocket과 브로커 통신을 검증하세요. CLI 소스의 회귀 테스트, 생성 앱의 빌드, 실제 요청과 외부 인프라 검증은 구분해서 기록합니다.
