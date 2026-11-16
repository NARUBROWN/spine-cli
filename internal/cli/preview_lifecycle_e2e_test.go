package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Source-candidate coverage for APIs introduced together in core v0.7.6.
func TestPreviewLifecycleCandidateEndToEnd(t *testing.T) {
	core := os.Getenv("SPINE_CONTROLLER_CORE_SOURCE")
	if core == "" {
		t.Skip("SPINE_CONTROLLER_CORE_SOURCE is required for source-candidate E2E")
	}
	core, err := filepath.Abs(core)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	options := newProjectOptions{Module: "example.com/lifecycle", Layout: layeredLayout, Platform: "nethttp", Validator: "none", WebSocketPlatform: "none", Modules: []selectedModule{{Path: spineImportPath, Version: "v0.7.6-preview.1"}, {Path: nethttpPlatformPath, Version: "v0.1.0-preview.2"}}}
	files, err := newProjectFiles(options)
	if err != nil {
		t.Fatal(err)
	}
	for name, contents := range files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0755); err != nil {
			t.Fatal(err)
		}
		writeE2EFile(t, filepath.Join(root, name), contents)
	}
	generatedGo(t, root, "mod", "edit", "-replace="+spineImportPath+"="+core)
	path := filepath.Join(root, "internal/application/application.go")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	source := strings.Replace(string(data), "spine.New(", "spine.New(spine.WithShutdownHook(stopService),", 1)
	source = strings.Replace(source, "app.Constructor(", "app.Constructor(newService)\napp.Constructor(", 1)
	writeE2EFile(t, path, source)
	addGeneratedTest(t, root, "lifecycle.go", previewLifecycleSource)
	addGeneratedTest(t, root, "lifecycle_test.go", previewLifecycleTest)
	generatedGo(t, root, "mod", "tidy")
	generatedGo(t, root, "fmt", "./...")
	t.Chdir(root)
	var out, errOut bytes.Buffer
	if code := Run([]string{"verify", "--warnings-as-errors", "--json"}, &out, &errOut); code != 0 {
		t.Fatalf("verify: %d\n%s\n%s", code, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), `"name": "gofmt-vet-test"`) {
		t.Fatalf("verify did not reach Go checks: %s", out.String())
	}
	t.Log(generatedGo(t, root, "test", "-race", "./...", "-count=1"))
	addGeneratedTest(t, root, "conditional.go", previewConditionalSource)
	source = strings.Replace(source, "spine.New(", "spine.New(spine.WithValidator(conditionalValidator{}),", 1)
	source = strings.Replace(source, "app.Constructor(", `app.Constructor(func()*conditionalController{return &conditionalController{}})
app.Route("POST", "/conditional", (*conditionalController).Create)
 app.Constructor(`, 1)
	writeE2EFile(t, path, source)
	document, err := executeRuntimeOpenAPIWithPreparation(true)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{`"/conditional"`, `"if"`, `"then"`, `"undo"`} {
		if !bytes.Contains(document, []byte(required)) {
			t.Fatalf("runtime conditional schema missing %s: %s", required, document)
		}
	}

}

const previewLifecycleSource = `package application
import "context"
type lifecycleService struct{closed bool}
func newService()(*lifecycleService,error){return &lifecycleService{},nil}
func stopService(_ context.Context,s *lifecycleService)error{s.closed=true;return nil}
`

const previewLifecycleTest = `package application
import(
 "context"
 "encoding/json"
 "errors"
 "testing"
 "time"
 "github.com/NARUBROWN/spine"
 "github.com/NARUBROWN/spine/pkg/boot"
 "github.com/NARUBROWN/spine/pkg/event/publish"
)
func TestHookUsesFallibleConstructorDependency(t *testing.T){
 service:=&lifecycleService{}
 app:=spine.New(spine.WithShutdownHook(stopService))
 app.Constructor(func()(*lifecycleService,error){return service,nil})
 prepared,err:=app.Prepare(context.Background(),boot.Options{});if err!=nil{t.Fatal(err)}
 if service.closed{t.Fatal("hook ran before shutdown")}
 if err:=prepared.Close(context.Background());err!=nil{t.Fatal(err)}
 if !service.closed{t.Fatal("injected service was not closed")}
}
func TestConstructorErrorPropagates(t *testing.T){
 failure:=errors.New("initialization failed")
 app:=spine.New(spine.WithShutdownHook(stopService))
 app.Constructor(func()(*lifecycleService,error){return nil,failure})
 prepared,err:=app.Prepare(context.Background(),boot.Options{})
 if prepared!=nil||!errors.Is(err,failure){t.Fatalf("%v %v",prepared,err)}
}
type recordedPublisher struct{events []publish.DomainEvent}
func(p *recordedPublisher)Publish(events ...publish.DomainEvent){p.events=append(p.events,events...)}
type legacyEvent struct{}
func(legacyEvent)Name()string{return "legacy.created"}
func(legacyEvent)OccurredAt()time.Time{return time.Time{}}
func TestFieldsAndDomainEvents(t *testing.T){
 recorder:=&recordedPublisher{};ctx:=publish.WithPublisher(context.Background(),recorder)
 if err:=publish.Event(ctx,"order.created",publish.Fields{"id":"42"});err!=nil{t.Fatal(err)}
 publish.DomainEvents(ctx,legacyEvent{})
 if len(recorder.events)!=2||recorder.events[0].Name()!="order.created"||recorder.events[1].Name()!="legacy.created"{t.Fatal(recorder.events)}
 data,err:=json.Marshal(recorder.events[0]);if err!=nil||string(data)!=` + "`" + `{"id":"42"}` + "`" + `{t.Fatalf("%s %v",data,err)}
}
`

const previewConditionalSource = `package application
import(
 "context"
 "encoding/json"
 "reflect"
 "github.com/NARUBROWN/spine/pkg/input"
 "github.com/NARUBROWN/spine/pkg/httpx"
 validation "github.com/NARUBROWN/spine/contract/validator"
)
type conditionalBody struct{Operation string;ID string}
type conditionalValidator struct{}
func(conditionalValidator)Validate(context.Context,any)error{return nil}
func(conditionalValidator)DescribeSchema(t reflect.Type)(validation.SchemaDescription,error){
 if t!=reflect.TypeFor[conditionalBody](){return validation.SchemaDescription{},nil}
 return validation.SchemaDescription{ConditionalRequired:[]validation.ConditionalRequired{{Index:[]int{1},When:[]validation.FieldCondition{{Index:[]int{0},Value:json.RawMessage("\"undo\"")}}}}},nil
}
type conditionalController struct{}
func(*conditionalController)Create(input.Body[conditionalBody])httpx.Response[string]{return httpx.Response[string]{Body:"ok"}}
`
