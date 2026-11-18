package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const synchronousPublisherContract = `package publish
import "context"
type Fields map[string]any
type Options struct{}
type Option func(*Options)
type Publisher interface{Event(context.Context,string,Fields,...Option)error}
`

func TestApplicationPublisherParticipatesInStaticDIWithoutExecution(t *testing.T) {
	for _, tc := range []struct{ name, body, code string }{
		{"official", `func(*UserController)Constructor(pub.Publisher)*UserController{panic("must not execute")}`, ""},
		{"alias", `type PublisherAlias = pub.Publisher;func(*UserController)Constructor(PublisherAlias)*UserController{panic("must not execute")}`, ""},
		{"structural interface", `type EventSender interface{Event(context.Context,string,pub.Fields,...pub.Option)error};func(*UserController)Constructor(EventSender)*UserController{panic("must not execute")}`, ""},
		{"exact user interface provider", `type EventSender interface{Event(context.Context,string,pub.Fields,...pub.Option)error};func(*UserController)Constructor(EventSender)*UserController{panic("must not execute")};func NewSender()EventSender{panic("must not execute")}`, ""},
		{"official exact provider wins", `type Mock struct{};func(*Mock)Event(context.Context,string,pub.Fields,...pub.Option)error{panic("must not execute")};func NewSender()*Mock{panic("must not execute")};func(*UserController)Constructor(pub.Publisher)*UserController{panic("must not execute")}`, ""},
		{"ambiguous structural providers", `type EventSender interface{Event(context.Context,string,pub.Fields,...pub.Option)error};type Mock struct{};func(*Mock)Event(context.Context,string,pub.Fields,...pub.Option)error{panic("must not execute")};func NewSender()*Mock{panic("must not execute")};func(*UserController)Constructor(EventSender)*UserController{panic("must not execute")}`, "COMPONENT_DEPENDENCIES_INVALID"},
		{"same name struct", `type Publisher struct{};func(*UserController)Constructor(*Publisher)*UserController{panic("must not execute")}`, "COMPONENT_DEPENDENCIES_INVALID"},
		{"extended interface", `type Publisher interface{pub.Publisher;Extra()};func(*UserController)Constructor(Publisher)*UserController{panic("must not execute")}`, "COMPONENT_DEPENDENCIES_INVALID"},
		{"pointer to interface", `func(*UserController)Constructor(*pub.Publisher)*UserController{panic("must not execute")}`, "COMPONENT_DEPENDENCIES_INVALID"},
		{"reserved provider", `func(*UserController)Constructor(pub.Publisher)*UserController{panic("must not execute")};func NewSender()pub.Publisher{panic("must not execute")}`, "EVENT_PUBLISHER_RESERVED"},
		{"reserved alias provider", `type PublisherAlias=pub.Publisher;func(*UserController)Constructor(pub.Publisher)*UserController{panic("must not execute")};func NewSender()PublisherAlias{panic("must not execute")}`, "EVENT_PUBLISHER_RESERVED"},
		{"unrelated missing dependency", `func(*UserController)Constructor(pub.Publisher,*DTO)*UserController{panic("must not execute")}`, "COMPONENT_DEPENDENCIES_INVALID"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Fixture init and constructor bodies panic if inspection executes user code.
			inspector := controllerFixture(t, `var _ context.Context;type LoadedPublisher=pub.Publisher
func(*UserController)RegisterRoutes(r *api.Registry){r.GET("/users",func()httpx.Response[string]{return httpx.Response[string]{Body:"ok"}})}
`+tc.body)
			file := filepath.Join("core", "pkg", "event", "publish", "publish.go")
			if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, []byte(synchronousPublisherContract), 0644); err != nil {
				t.Fatal(err)
			}
			source, err := os.ReadFile("controller.go")
			if err != nil {
				t.Fatal(err)
			}
			source = []byte(strings.Replace(string(source), `"mime/multipart"`, `"mime/multipart";"context";pub "github.com/NARUBROWN/spine/pkg/event/publish"`, 1))
			if err := os.WriteFile("controller.go", source, 0644); err != nil {
				t.Fatal(err)
			}
			inspector.sources = nil
			inspector.contract.Constructors = []contractRegistration{{Target: "example.com/typed.UserController.Constructor", Role: "Controller", object: true}}
			if strings.Contains(tc.body, "func NewSender") {
				inspector.contract.Constructors = append(inspector.contract.Constructors, contractRegistration{Target: "example.com/typed.NewSender", Role: "Custom"})
			}
			inspector.inspectControllerRoutes()
			found := false
			for _, d := range inspector.contract.Diagnostics {
				found = found || d.Code == tc.code
				if tc.code == "" && d.Severity == "error" {
					t.Fatal(d)
				}
			}
			if tc.code != "" && !found {
				t.Fatalf("missing %s: %+v", tc.code, inspector.contract.Diagnostics)
			}
			if tc.code == "" && len(inspector.contract.HTTP) != 1 {
				t.Fatalf("routes=%+v", inspector.contract.HTTP)
			}
		})
	}
}
