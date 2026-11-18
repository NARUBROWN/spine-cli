package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSplitMessageContextGeneration(t *testing.T) {
	for _, layout := range []string{layeredLayout, "legacy"} {
		t.Run(layout, func(t *testing.T) {
			inTempDir(t)
			core := filepath.Join(t.TempDir(), "core")
			if err := os.MkdirAll(filepath.Join(core, "pkg/spine"), 0755); err != nil {
				t.Fatal(err)
			}
			writeE2EFile(t, filepath.Join(core, "go.mod"), "module "+spineImportPath+"\ngo 1.25.0\n")
			writeE2EFile(t, filepath.Join(core, "pkg/spine/types.go"), `package spine
import "context"
type Ctx interface{Get(string)(any,bool)}
type WebSocketCtx interface{context.Context;Set(string,any);Get(string)(any,bool)}
type EventCtx interface{context.Context;Set(string,any);Get(string)(any,bool)}
`)
			writeE2EFile(t, "go.mod", "module example.com/messages\ngo 1.25.0\nrequire "+spineImportPath+" v0.7.6-preview.1\nreplace "+spineImportPath+" => "+filepath.ToSlash(core)+"\n")
			writeE2EFile(t, "spine.toml", "module = \"example.com/messages\"\nentrypoint = \".\"\ncomposition = \".\"\nlayout = \""+layout+"\"\nport = 8080\n")
			for _, kind := range []string{"consumer", "websocket"} {
				contextType, err := messageContext(kind)
				if err != nil {
					t.Fatal(err)
				}
				want := "spine.EventCtx"
				if kind == "websocket" {
					want = "spine.WebSocketCtx"
				}
				if contextType != want {
					t.Fatal(contextType)
				}
				name, _ := parseResourceName("audit")
				files := messageModuleFiles(kind, name, "/ws/audit", nil, contextType)
				if layout == layeredLayout {
					planner := &layeredPlanner{Sources: layoutSources{}, Changes: map[string]fileChange{}}
					if err := planner.addMessageModule(layeredRequest{Kind: kind, Name: generationName{Name: name}, Topic: "audit.created", Path: "/ws/audit"}); err != nil {
						t.Fatal(err)
					}
					files = map[string]string{}
					for path, change := range planner.Changes {
						files[path] = change.Contents
					}
				}
				handler := files[filepath.Join("src", kind, "audit", "audit_handler.go")]
				if !strings.Contains(handler, "ctx "+want) || strings.Contains(handler, "ctx spine.Ctx") {
					t.Fatal(handler)
				}
				if kind == "websocket" {
					for _, required := range []string{"r.WS(", "input.Message[AuditMessage]", "sender ws.Sender", "Constructor(sender ws.Sender)"} {
						if !strings.Contains(handler, required) {
							t.Fatalf("missing %s:\n%s", required, handler)
						}
					}
					if strings.Contains(files[filepath.Join("src", kind, "audit", "module.go")], "app.WebSocket()") {
						t.Fatal("generated legacy WS registration")
					}
				}
			}
		})
	}
}

func TestMessageContextTransportBindingsAndAliases(t *testing.T) {
	i := controllerFixture(t, `type Frame = spine.WebSocketCtx
 type Delivery = spine.EventCtx
 func(c *UserController)RegisterRoutes(r *api.Registry){r.WS("/ws",func(ctx Frame,m input.Message[DTO])error{return nil})}
 func(c *UserController)Consume(ctx Delivery,name string,body DTO)error{return nil}`)
	i.contract.Consumers = []namedRegistration{{Name: "audit", Handler: "(*example.com/typed.UserController).Consume"}}
	i.inspectControllerRoutes()
	i.resolveHandlerSignatures()
	for _, d := range i.contract.Diagnostics {
		if d.Severity == "error" {
			t.Fatal(d)
		}
	}
	if len(i.contract.WebSockets) != 1 || i.contract.WebSockets[0].Parameters[0].Binding != "context" {
		t.Fatal(i.contract.WebSockets)
	}
	if i.contract.Consumers[0].Parameters[0].Binding != "context" || i.contract.Consumers[0].Parameters[1].Binding != "event-name" {
		t.Fatal(i.contract.Consumers)
	}
}

func TestHTTPAndWebSocketRejectOtherTransportContexts(t *testing.T) {
	for _, tc := range []struct{ method, context string }{{"GET", "EventCtx"}, {"GET", "WebSocketCtx"}, {"WS", "EventCtx"}, {"WS", "Ctx"}} {
		t.Run(tc.method+tc.context, func(t *testing.T) {
			i := controllerFixture(t, `func(c *UserController)RegisterRoutes(r *api.Registry){r.`+tc.method+`("/test",func(ctx spine.`+tc.context+`)error{return nil})}`)
			i.inspectControllerRoutes()
			i.resolveHandlerSignatures()
			if !contractHasErrors(*i.contract) {
				t.Fatal("wrong transport context accepted", i.contract)
			}
		})
	}
}

func TestConsumerRejectsOtherTransportContexts(t *testing.T) {
	for _, contextType := range []string{"spine.Ctx", "spine.WebSocketCtx", "context.Context"} {
		t.Run(contextType, func(t *testing.T) {
			i := controllerFixture(t, `func(c *UserController)RegisterRoutes(r *api.Registry){}
 func(c *UserController)Consume(ctx `+contextType+`,name string,body DTO)error{return nil}`)
			cwd, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			i.contract.HTTPAdapter.SelectedModules = []selectedModule{{Path: spineImportPath, Version: "v0.7.6-preview.1", Dir: filepath.Join(cwd, "core")}}
			i.contract.Consumers = []namedRegistration{{Name: "audit", Handler: "(*example.com/typed.UserController).Consume"}}
			i.inspectControllerRoutes()
			i.resolveHandlerSignatures()
			found := false
			for _, d := range i.contract.Diagnostics {
				found = found || d.Code == "CONSUMER_PARAMETER_UNSUPPORTED"
			}
			if !found {
				t.Fatal(i.contract.Diagnostics)
			}
		})
	}
}
