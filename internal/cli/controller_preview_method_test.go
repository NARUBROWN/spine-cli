package cli

import (
	"fmt"
	"strings"
	"testing"
)

func TestPreviewStableMethodPreflightRejectsInvalidGeneratedCallbacks(t *testing.T) {
	for _, tc := range []struct{ name, handler, problem string }{
		{"multiple_responses", `func(*UserController)Handle()(httpx.Response[string],httpx.Response[int]){return httpx.Response[string]{},httpx.Response[int]{}}`, "HTTP_RETURN_UNSUPPORTED"},
		{"conflicting_xml", "type Request struct{A string `json:\"a\" xml:\"same\"`;B string `json:\"b\" xml:\"same>child\"`};" + `func(*UserController)Handle(input.Body[Request])error{return nil}`, "HTTP_INPUT_CONTRACT_INVALID"},
		{"valid", `func(*UserController)Handle()(httpx.Response[string],error){return httpx.Response[string]{},nil}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inspector := previewStableHTTPFixture(t, tc.handler+"\n"+`func(*UserController)RegisterRoutes(r *api.Registry){}`, "v0.7.5-preview.1")
			inspector.inspectControllerRoutes()
			signature := inspector.controllerMethods["(*example.com/typed.UserController).Handle"]
			if signature == nil {
				t.Fatal("route generation cannot resolve the controller method signature")
			}
			err := checkControllerInputs(signature, "v0.7.5-preview.1")
			if err != nil {
				err = fmt.Errorf("HTTP_INPUT_CONTRACT_INVALID: %w", err)
			} else if err = checkControllerReturns(signature, "v0.7.5-preview.1"); err != nil {
				err = fmt.Errorf("HTTP_RETURN_UNSUPPORTED: %w", err)
			}
			if tc.problem == "" && err != nil || tc.problem != "" && (err == nil || !strings.Contains(err.Error(), tc.problem)) {
				t.Fatalf("생성할 콜백의 메서드 계약 검사가 다릅니다: %v", err)
			}
			if err := checkControllerInputs(signature, "v0.7.2-preview.3"); err != nil {
				t.Fatalf("이전 코어의 메서드에 새 계약을 적용했습니다: %v", err)
			}
			if err := checkControllerReturns(signature, "v0.7.2-preview.3"); err != nil {
				t.Fatalf("이전 코어의 메서드에 새 계약을 적용했습니다: %v", err)
			}
		})
	}
}
