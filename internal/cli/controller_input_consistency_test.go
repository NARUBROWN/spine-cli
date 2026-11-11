package cli

import (
	"os"
	"strings"
	"testing"
)

func TestControllerHeaderListsMatchCoreRules(t *testing.T) {
	for _, test := range []struct {
		name, field, declaration string
		invalid                  bool
	}{
		{"authorization-list", "Authorization []string", "", true},
		{"explicit-case-insensitive", "Value []string `header:\"aUtHoRiZaTiOn\"`", "", true},
		{"challenge-list", "Value []string `header:\"WWW-Authenticate\"`", "", true},
		{"cookie-list", "Cookie []string", "", true},
		{"authorization-scalar", "Authorization string", "", false},
		{"accept-list", "Accept []string", "", false},
		{"extension-list", "Values []string `header:\"X-Values\"`", "", false},
		{"custom-text-list", "Authorization Credentials", "type Credentials []string; func (*Credentials) UnmarshalText([]byte) error { return nil }", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			inspector := controllerFixture(t, test.declaration+"\ntype Headers struct { "+test.field+" }; func (*UserController) RegisterRoutes(r *api.Registry) { r.GET(\"/headers\", func(input.Header[Headers]) error { return nil }) }")
			inspector.inspectControllerRoutes()
			inspector.resolveHandlerSignatures()
			invalid := false
			for _, diagnostic := range inspector.contract.Diagnostics {
				invalid = invalid || diagnostic.Code == "HTTP_INPUT_CONTRACT_INVALID"
			}
			if invalid != test.invalid || !test.invalid && len(inspector.contract.Diagnostics) != 0 {
				t.Fatalf("헤더 목록 지원 규칙이 다릅니다: %+v", inspector.contract.Diagnostics)
			}
		})
	}
}

func TestControllerInputsResolveExternalStructsAndEmbeddedAliases(t *testing.T) {
	inspector := controllerFixture(t, `
type Foreign = shared.Request
func (*UserController) RegisterRoutes(r *api.Registry) {
 r.POST("/body", func(input.Body[Foreign]) error { return nil })
 r.GET("/query", func(input.Query[shared.Query]) error { return nil })
}`)
	module, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	writeE2EFile(t, "go.mod", string(module)+"\nrequire example.com/shared v0.0.0\nreplace example.com/shared => ./shared\n")
	if err := os.MkdirAll("shared", 0755); err != nil {
		t.Fatal(err)
	}
	writeE2EFile(t, "shared/go.mod", "module example.com/shared\n\ngo 1.25.5\n")
	writeE2EFile(t, "shared/request.go", "package shared\ntype Request struct { Name string `json:\"name\"` }; type Base struct { Page int `query:\"page\"` }; type Query struct { Base; Name string `query:\"name\"` }\nfunc init() { panic(\"의존 모듈의 init을 실행하면 안 됩니다\") }\n")
	controller, err := os.ReadFile("controller.go")
	if err != nil {
		t.Fatal(err)
	}
	writeE2EFile(t, "controller.go", strings.Replace(string(controller), "import (", "import (\n shared \"example.com/shared\"", 1))
	inspector.sources, _, err = readLayoutSources()
	if err != nil {
		t.Fatal(err)
	}
	inspector.inspectControllerRoutes()
	inspector.resolveHandlerSignatures()
	if len(inspector.contract.HTTP) != 2 || len(inspector.contract.Diagnostics) != 0 {
		t.Fatalf("정상 외부 DTO를 거부했습니다: %+v", inspector.contract.Diagnostics)
	}
	for _, route := range inspector.contract.HTTP {
		if route.Path == "/query" && strings.Join(route.Parameters[0].Keys, ",") != "page,name" {
			t.Fatalf("외부 DTO의 임베딩과 필드 태그를 보존해야 합니다: %+v", route.Parameters)
		}
	}
}

func TestUnknownExternalInputIsUnresolvedRatherThanInvalid(t *testing.T) {
	httpBindingProject(t, "legacy", `import ("example.com/missing"; "github.com/NARUBROWN/spine/pkg/input")`, "", "body input.Body[missing.Request]", "/probe")
	contract, err := inspectApplication()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, diagnostic := range contract.Diagnostics {
		if diagnostic.Code == "HTTP_INPUT_CONTRACT_INVALID" {
			t.Fatalf("해석하지 못한 타입을 잘못된 입력으로 단정했습니다: %+v", diagnostic)
		}
		found = found || diagnostic.Code == "HTTP_PARAMETER_BINDING_UNRESOLVED"
	}
	if !found {
		t.Fatalf("미해석 입력 진단이 필요합니다: %+v", contract.Diagnostics)
	}
}

func TestControllerCheckMatchesCoreInputAndReturnTypes(t *testing.T) {
	for _, tc := range []struct{ name, source, code string }{
		{
			"same payload across sources",
			`type Query = input.Query[DTO]; type Headers = input.Header[DTO]
			func (*UserController) RegisterRoutes(r *api.Registry) {
				r.GET("/items", func(Query, Headers) error { return nil })
			}`,
			"HTTP_INPUT_DTO_DUPLICATED",
		},
		{
			"unsupported form under JSON decoder",
			"type Body struct { Value map[string]int `form:\"value\"` }; func (*Body) UnmarshalJSON([]byte) error { return nil }; " +
				`func (*UserController) RegisterRoutes(r *api.Registry) {
					r.POST("/items", func(input.Body[Body]) error { return nil })
				}`,
			"HTTP_INPUT_CONTRACT_INVALID",
		},
		{
			"unsupported response payload",
			`type ResultWithChannel = httpx.Response[struct{ Value chan int }]
			func (*UserController) RegisterRoutes(r *api.Registry) {
				r.GET("/items", func() ResultWithChannel { return ResultWithChannel{} })
			}`,
			"HTTP_RETURN_UNSUPPORTED",
		},
		{
			"supported custom JSON serialization",
			`type JSONChannel chan int
			func (JSONChannel) MarshalJSON() ([]byte, error) { return nil, nil }
			func (*UserController) RegisterRoutes(r *api.Registry) {
				r.GET("/items", func() httpx.Response[JSONChannel] { return httpx.Response[JSONChannel]{} })
			}`,
			"",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inspector := controllerFixture(t, tc.source)
			inspector.inspectControllerRoutes()
			inspector.resolveHandlerSignatures()
			for _, diagnostic := range inspector.contract.Diagnostics {
				if diagnostic.Code == tc.code {
					return
				}
			}
			if tc.code != "" || contractHasErrors(*inspector.contract) {
				t.Fatalf("기대 진단 %q, 실제 진단 %+v", tc.code, inspector.contract.Diagnostics)
			}
		})
	}
}
