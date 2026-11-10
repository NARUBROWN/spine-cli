package cli

import (
	"strings"
	"testing"
)

func TestHeaderMarkerContract(t *testing.T) {
	inspector := controllerFixture(t, "type Headers struct { RequestID string `header:\"X-Request-ID\"`; Token string; Ignored string `header:\"-\"` }; func(c *UserController)RegisterRoutes(r *api.Registry){r.GET(\"/users\",func(input.Header[Headers])error{return nil})}")
	inspector.inspectControllerRoutes()
	inspector.resolveHandlerSignatures()
	if contractHasErrors(*inspector.contract) {
		t.Fatalf("diagnostics=%+v", inspector.contract.Diagnostics)
	}
	p := inspector.contract.HTTP[0].Parameters[0]
	if p.Binding != "header" || strings.Join(p.Keys, ",") != "X-Request-Id,Token" {
		t.Fatalf("parameter=%+v", p)
	}
}

func TestHeaderMarkerRejectsInvalidContract(t *testing.T) {
	for _, fields := range []string{
		"A string `header:\"X-ID\"`; B string `header:\"x-id\"`",
		"A string `header:\"bad name\"`",
		"A string `query:\"q\"`",
	} {
		t.Run(fields, func(t *testing.T) {
			inspector := controllerFixture(t, "type Headers struct { "+fields+" }; func(c *UserController)RegisterRoutes(r *api.Registry){r.GET(\"/users\",func(input.Header[Headers])error{return nil})}")
			inspector.inspectControllerRoutes()
			inspector.resolveHandlerSignatures()
			if !contractHasErrors(*inspector.contract) {
				t.Fatalf("accepted invalid header: %+v", inspector.contract.HTTP)
			}
		})
	}
}
