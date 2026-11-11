package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestHTTPInputPreflightRejectsInvalidMarkerDTOs(t *testing.T) {
	for _, tc := range []struct {
		name, declaration, marker string
	}{
		{"header tag typo", "type Bad struct { ID string `headers:\"X-ID\"` }", "Header"},
		{"nested input source", "type Nested struct { Search string `query:\"search\"` }; type Bad struct { Filter *Nested }", "Body"},
		{"duplicate form key", "type Bad struct { A string `form:\"same\"`; B string `form:\"same\"` }", "Body"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inspector := controllerFixture(t, tc.declaration+`
func(c *UserController) RegisterRoutes(r *api.Registry) {
 r.POST("/request",func(input.`+tc.marker+`[Bad]) error {return nil})
}`)
			inspector.contract.HTTPAdapter.SelectedModules[0].Version = "v0.7.2-preview.3"
			inspector.inspectControllerRoutes()
			inspector.resolveHandlerSignatures()
			for _, diagnostic := range inspector.contract.Diagnostics {
				if diagnostic.Code == "HTTP_INPUT_CONTRACT_INVALID" {
					return
				}
			}
			t.Fatalf("missing invalid input diagnostic: %+v", inspector.contract.Diagnostics)
		})
	}
}

func TestStaticOpenAPIDescribesXMLAndTextResponse(t *testing.T) {
	inspector := controllerFixture(t, "type Wire struct { ID string `json:\"json_id\" xml:\"xml_id\"` }\n"+`
type Text = httpx.Response[string]
func(c *UserController) RegisterRoutes(r *api.Registry) {
 r.POST("/wire",func(input.Body[Wire]) *Text {return nil})
}`)
	inspector.contract.HTTPAdapter.SelectedModules[0].Version = "v0.7.2-preview.3"
	inspector.inspectControllerRoutes()
	inspector.resolveHandlerSignatures()
	document, err := buildOpenAPIDocument(*inspector.contract)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{`"application/xml":`, `"text/xml":`, `"xml_id":`, `"json_id":`, `"text/plain":`} {
		if !strings.Contains(string(encoded), fragment) {
			t.Fatalf("missing %s in %s", fragment, encoded)
		}
	}
	if strings.Contains(string(encoded), `"default":{"content":{"application/json"`) {
		t.Fatalf("text response described as JSON: %s", encoded)
	}
}

func TestGenericHTTPAliasesAndResponsePayload(t *testing.T) {
	inspector := controllerFixture(t, `type Query[T any] = input.Query[T]
type Response[T any] = httpx.Response[T]
type QueryDTO struct { Text string }
type Reply[T any] struct { Value T }
func(c *UserController) RegisterRoutes(r *api.Registry) {
	r.GET("/generic", func(q Query[QueryDTO]) Response[Reply[string]] { return Response[Reply[string]]{} })
}`)
	inspector.contract.HTTPAdapter.SelectedModules[0].Version = "v0.7.5-preview.1"
	inspector.inspectControllerRoutes()
	inspector.resolveHandlerSignatures()
	if contractHasErrors(*inspector.contract) {
		t.Fatalf("valid generic handler rejected: %+v", inspector.contract.Diagnostics)
	}
	route := inspector.contract.HTTP[0]
	if got := route.Parameters[0]; got.Binding != "query" || len(got.InputFields) != 1 || got.InputFields[0].Name != "text" {
		t.Fatalf("wrong query binding: %+v", got)
	}
}

func TestStaticWebSocketMatchesCorePathAndReturnContracts(t *testing.T) {
	inspector := controllerFixture(t, `
func (*UserController) RegisterRoutes(*api.Registry) {}
type SocketError struct{}
func (*SocketError) Error() string { return "socket error" }
func (*UserController) Invalid() string { return "" }
func (*UserController) Valid() *SocketError { return nil }
`)
	inspector.contract.HTTPAdapter.SelectedModules[0].Version = "v0.7.5-preview.1"
	inspector.contract.WebSockets = []namedRegistration{
		{Name: "/ws/:id", Handler: "(*example.com/typed.UserController).Valid"},
		{Name: "/ws/invalid", Handler: "(*example.com/typed.UserController).Invalid"},
		{Name: "/ws/valid", Handler: "(*example.com/typed.UserController).Valid"},
	}
	inspector.inspectControllerRoutes()
	inspector.resolveHandlerSignatures()
	counts := map[string]int{}
	for _, diagnostic := range inspector.contract.Diagnostics {
		if diagnostic.Severity == "error" {
			counts[diagnostic.Code]++
		}
	}
	if len(counts) != 2 || counts["WEBSOCKET_PATH_INVALID"] != 1 || counts["WEBSOCKET_RETURN_UNSUPPORTED"] != 1 {
		t.Fatalf("wrong WebSocket diagnostics: %+v", inspector.contract.Diagnostics)
	}
}
