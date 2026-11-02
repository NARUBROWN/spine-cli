package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func httpReturnProject(t *testing.T, layout, imports, declarations, results, body string) {
	t.Helper()
	httpBindingProject(t, layout, imports, declarations, "", "/probe")
	source, _ := os.ReadFile("handlers/controller.go")
	text := strings.Replace(string(source), "Get() error { return nil }", "Get() "+results+" { "+body+" }", 1)
	writeE2EFile(t, "handlers/controller.go", text)
}

func TestHTTPReturnsMatchDefaultHandlers(t *testing.T) {
	for _, layout := range []string{layeredLayout, "legacy"} {
		for _, scenario := range []struct{ name, imports, declarations, results, body, code string }{
			{"no return", "", "", "", "return", ""},
			{"error only", "", "", "error", "return nil", ""},
			{"wrapper", `import h "github.com/NARUBROWN/spine/pkg/httpx"`, "type Product struct { Name string }", "h.Response[Product]", "return h.Response[Product]{}", ""},
			{"pointer wrapper", `import h "github.com/NARUBROWN/spine/pkg/httpx"`, "", "*h.Response[string]", "return &h.Response[string]{Body:\"ok\"}", ""},
			{"binary redirect", `import h "github.com/NARUBROWN/spine/pkg/httpx"`, "", "(*h.Binary, *h.Redirect, error)", "return nil,&h.Redirect{},nil", ""},
			{"alias wrapper", `import h "github.com/NARUBROWN/spine/pkg/httpx"`, "type Result = h.Response[map[string]int]", "(Result,error)", "return Result{},nil", ""},
			{"alias string body", `import h "github.com/NARUBROWN/spine/pkg/httpx"`, "type Text = string", "h.Response[Text]", "return h.Response[Text]{}", ""},
			{"time body", `import (h "github.com/NARUBROWN/spine/pkg/httpx";"time")`, "", "h.Response[time.Time]", "return h.Response[time.Time]{}", ""},
			{"custom value error", "", "type Problem struct{}; func (Problem) Error() string { return \"problem\" }", "Problem", "return Problem{}", ""},
			{"custom pointer error", "", "type Problem struct{}; func (*Problem) Error() string { return \"problem\" }", "*Problem", "return &Problem{}", ""},
			{"embedded error", "", "type Problem struct{ error }", "Problem", "return Problem{}", ""},
			{"alias receiver error", "", "type Problem struct{};type Alias = Problem;func (Alias) Error() string {return \"bad\"}", "Problem", "return Problem{}", ""},
			{"error interface", "", "type Problem interface { error; Extra() }", "Problem", "return nil", ""},
			{"named error interface", "", "type Problem error", "Problem", "return nil", ""},
			{"known HTTP error", `import h "github.com/NARUBROWN/spine/pkg/httperr"`, "", "*h.HTTPError", "return h.BadRequest(\"bad\")", ""},
			{"ignored extra success", `import h "github.com/NARUBROWN/spine/pkg/httpx"`, "", "(h.Response[string],string)", "return h.Response[string]{},\"ignored\"", ""},
			{"scalar", "", "", "string", "return \"ok\"", "HTTP_RETURN_TYPE_UNSUPPORTED"},
			{"raw DTO", "", "type Product struct{}", "(Product,error)", "return Product{},nil", "HTTP_RETURN_TYPE_UNSUPPORTED"},
			{"raw slice", "", "type Product struct{}", "[]Product", "return []Product{{}}", "HTTP_RETURN_TYPE_UNSUPPORTED"},
			{"raw map", "", "", "map[string]string", "return map[string]string{}", "HTTP_RETURN_TYPE_UNSUPPORTED"},
			{"defined wrapper", `import h "github.com/NARUBROWN/spine/pkg/httpx"`, "type Result h.Response[string]", "Result", "return Result{}", "HTTP_RETURN_TYPE_UNSUPPORTED"},
			{"defined string body", `import h "github.com/NARUBROWN/spine/pkg/httpx"`, "type Text string", "h.Response[Text]", "return h.Response[Text]{}", "HTTP_RETURN_TYPE_UNSUPPORTED"},
			{"double pointer", `import h "github.com/NARUBROWN/spine/pkg/httpx"`, "type Result = *h.Response[string]", "*Result", "return nil", "HTTP_RETURN_TYPE_UNSUPPORTED"},
			{"pointer error method on value", "", "type Problem struct{};func (*Problem) Error() string { return \"bad\" }", "Problem", "return Problem{}", "HTTP_RETURN_TYPE_UNSUPPORTED"},
			{"shadowed promotion", "", "type Problem struct{error};func (*Problem) Error() string {return \"bad\"}", "Problem", "return Problem{}", "HTTP_RETURN_TYPE_UNSUPPORTED"},
			{"unknown interface", "", "", "any", "return nil", "HTTP_RETURN_TYPE_UNRESOLVED"},
			{"unknown external", `import custom "example.net/custom"`, "", "custom.Response", "return custom.Response{}", "HTTP_RETURN_TYPE_UNRESOLVED"},
			{"wrong httpx package", `import httpx "example.net/httpx"`, "", "httpx.Response[string]", "return httpx.Response[string]{}", "HTTP_RETURN_TYPE_UNRESOLVED"},
		} {
			t.Run(layout+"/"+scenario.name, func(t *testing.T) {
				httpReturnProject(t, layout, scenario.imports, scenario.declarations, scenario.results, scenario.body)
				contract, err := inspectApplication()
				if err != nil {
					t.Fatal(err)
				}
				if scenario.code == "" {
					if contractHasErrors(contract) || !contract.Complete {
						t.Fatalf("valid return rejected: %+v", contract.Diagnostics)
					}
					return
				}
				found := false
				for _, diagnostic := range contract.Diagnostics {
					if diagnostic.Code == scenario.code {
						found = strings.Contains(diagnostic.Message, "Get") && strings.Contains(diagnostic.Source, "handlers/controller.go:") && strings.Contains(diagnostic.Hint, "httpx.Response")
					}
				}
				if !found || contract.Complete {
					t.Fatalf("missing return diagnostic %s: %+v", scenario.code, contract.Diagnostics)
				}
				for _, command := range [][]string{{"contract", "check", "--json"}, {"verify", "--json"}} {
					var out, errOut bytes.Buffer
					if code := Run(command, &out, &errOut); code != 5 || !strings.Contains(out.String(), scenario.code) {
						t.Fatalf("invalid return passed %v: %d %s %s", command, code, out.String(), errOut.String())
					}
				}
			})
		}
	}
}

func TestRouteReturnFailureDoesNotChangeProject(t *testing.T) {
	httpReturnProject(t, layeredLayout, "", "", "string", "return \"ok\"")
	path := "internal/application/application.go"
	source, _ := os.ReadFile(path)
	writeE2EFile(t, path, strings.Replace(string(source), `app.Route("POST", "/probe", (*probe.Controller).Get)`, "", 1))
	beforeRoute, _ := os.ReadFile(layeredRoutePath)
	beforeComposition, _ := os.ReadFile(path)
	for _, dry := range []bool{false, true} {
		args := []string{"route", "probe", "--method", "POST", "--path", "/probe", "--handler", "handlers:Controller.Get"}
		if dry {
			args = append(args, "--dry-run")
		}
		var out, errOut bytes.Buffer
		if code := runGenerate(args, &out, &errOut); code == 0 || !strings.Contains(errOut.String(), "HTTP_RETURN_TYPE_UNSUPPORTED") || !strings.Contains(errOut.String(), "handlers/controller.go:") || !strings.Contains(errOut.String(), "httpx.Response[string]") {
			t.Fatalf("route did not explain invalid return: %d %s", code, errOut.String())
		}
	}
	afterRoute, _ := os.ReadFile(layeredRoutePath)
	afterComposition, _ := os.ReadFile(path)
	if !bytes.Equal(beforeRoute, afterRoute) || !bytes.Equal(beforeComposition, afterComposition) {
		t.Fatal("rejected route changed source")
	}
}

func TestPlainReturnDiagnosticsIncludeSourceAndRepair(t *testing.T) {
	httpReturnProject(t, layeredLayout, "", "", "string", "return \"bad\"")
	for _, args := range [][]string{{"contract", "check"}, {"verify"}, {"inspect"}} {
		var out, errOut bytes.Buffer
		Run(args, &out, &errOut)
		text := out.String() + errOut.String()
		if !strings.Contains(text, "HTTP_RETURN_TYPE_UNSUPPORTED") || !strings.Contains(text, "handlers/controller.go:") || !strings.Contains(text, "Hint: Return httpx.Response[string]") {
			t.Fatalf("plain diagnostic lacks repair: %v %s", args, text)
		}
	}
}
