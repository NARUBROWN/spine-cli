# 첫 HTTP API 만들기

이 예제는 이름과 수량을 검사하고 JSON을 반환하는 `POST /products`를 만듭니다. 데이터 저장은 포함하지 않습니다. 기본 `layered-v1` 앱에서 실행합니다.

```sh
spine new shop-api --module example.com/shop
cd shop-api
```

<!-- existing-project -->

## 1. DTO와 controller 생성

프로젝트 루트에서 실행합니다. 직접 적용하는 예제이며, 변경을 먼저 보려면 같은 명령에 `--dry-run`을 붙입니다.

```sh
spine g dto api/create-product-request --field name:string --field quantity:int
spine g dto api/product-response --field name:string --field quantity:int
spine g controller api/product
```

요청과 응답 DTO는 `src/dto/api/`에, controller는 `src/controller/api/product_controller.go`에 생성됩니다. 생성자는 application에 등록됩니다.

## 2. 핸들러 구현

`src/controller/api/product_controller.go`를 아래 내용으로 작성합니다. import의 모듈 경로는 `go.mod`의 module과 같아야 합니다.

```go
package api

import (
    "strings"

    dto "example.com/shop/src/dto/api"
    "github.com/NARUBROWN/spine/pkg/httperr"
    "github.com/NARUBROWN/spine/pkg/httpx"
)

type ProductController struct{}

func NewProductController() *ProductController { return &ProductController{} }

func (*ProductController) Create(req *dto.CreateProductRequest) (httpx.Response[dto.ProductResponse], error) {
    name := strings.TrimSpace(req.Name)
    if name == "" || req.Quantity < 0 {
        return httpx.Response[dto.ProductResponse]{}, httperr.BadRequest("name is required and quantity must be nonnegative")
    }
    return httpx.Response[dto.ProductResponse]{
        Body: dto.ProductResponse{Name: name, Quantity: req.Quantity},
        Options: httpx.ResponseOptions{Status: 201},
    }, nil
}
```

본문 DTO는 포인터로 받습니다. 성공 값은 `httpx.Response[T]`의 `Body`에 넣고, 상태 코드는 `Options.Status`로 지정합니다. 기본 상태는 200입니다. 파일과 리다이렉트에는 각각 `httpx.Binary`와 `httpx.Redirect`를 사용합니다. raw DTO, slice, string을 성공 값으로 직접 반환하면 기본 반환 핸들러가 처리하지 못합니다.

실패는 `httperr.BadRequest` 같은 HTTP 오류를 `error`로 반환합니다. 예상하지 못한 일반 오류는 500으로 처리됩니다. `:id` 경로를 추가할 때는 `path.Int` 같은 값 인자를 사용합니다.

## 3. 경로 등록과 검사

```sh
gofmt -w src/controller/api/product_controller.go
spine g route create-product --method POST --path /products --handler src/controller/api:ProductController.Create
spine contract check
spine verify
```

route는 `src/route/route.go`에 추가됩니다. contract check는 정적 계약을 검사하고 verify는 정적 검사와 Go 검증을 실행합니다. 실제 HTTP 요청은 다음 단계에서 별도로 확인합니다.

## 4. 서버 실행과 응답 확인

```sh
spine dev
```

다른 터미널에서 실행합니다. `spine.toml`의 port를 바꿨다면 아래 주소도 바꿉니다.

```sh
curl -i -X POST http://localhost:8080/products \
  -H 'Content-Type: application/json' \
  -d '{"name":"Bolt","quantity":5}'
```

기대 결과는 HTTP 201과 `{"name":"Bolt","quantity":5}`입니다.

```sh
curl -i -X POST http://localhost:8080/products \
  -H 'Content-Type: application/json' \
  -d '{"name":"","quantity":-1}'
```

기대 결과는 HTTP 400입니다. 잘못된 JSON이나 quantity의 잘못된 타입도 400으로 처리됩니다. 종료할 때는 dev 터미널에서 Ctrl+C를 누릅니다.
