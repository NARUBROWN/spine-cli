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
import("context";"github.com/NARUBROWN/spine/pkg/event/publish")
type lifecycleService struct{closed bool;publisher publish.Publisher}
func newService(publisher publish.Publisher)(*lifecycleService,error){return &lifecycleService{publisher:publisher},nil}
func stopService(_ context.Context,s *lifecycleService)error{s.closed=true;return nil}
`

const previewLifecycleTest = `package application
import(
 "context"
 "errors"
 "testing"
 "github.com/NARUBROWN/spine"
 "github.com/NARUBROWN/spine/pkg/boot"
 "github.com/NARUBROWN/spine/pkg/event/publish"
 event "github.com/NARUBROWN/spine/contract/event"
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
type recordedPublisher struct{message event.Message;err error;started,release chan struct{}}
func(p *recordedPublisher)Publish(ctx context.Context,m event.Message)error{
 p.message=m
 if p.started!=nil{close(p.started);select{case<-p.release:case<-ctx.Done():return ctx.Err()}}
 return p.err
}
func(*recordedPublisher)Close(context.Context)error{return nil}
type publisherFactory struct{first,next *recordedPublisher;creates int}
func(*publisherFactory)Capabilities()event.Capabilities{return event.Capabilities{ContractVersion:event.ContractVersion,Publish:true}}
func(*publisherFactory)NewReader(context.Context,event.Subscription)(event.Reader,error){return nil,event.ErrUnsupportedCapability}
func(f *publisherFactory)NewPublisher(context.Context)(event.Publisher,error){f.creates++;if f.creates==1{return f.first,nil};return f.next,nil}
func TestDIPublisherWaitsAndPreservesOutboxMetadata(t *testing.T){
 failure:=errors.New("broker failed")
 first:=&recordedPublisher{err:failure,started:make(chan struct{}),release:make(chan struct{})}
 next:=&recordedPublisher{}
 factory:=&publisherFactory{first:first,next:next}
 service:=&lifecycleService{}
 app:=spine.New(spine.WithEventAdapter("events",factory),spine.WithShutdownHook(stopService))
 app.Constructor(func(p publish.Publisher)*lifecycleService{service.publisher=p;return service})
 prepared,err:=app.Prepare(t.Context(),boot.Options{});if err!=nil{t.Fatal(err)};defer prepared.Close(context.Background())
 options:=[]publish.Option{publish.WithMessageID("outbox-42"),publish.WithMetadata(map[string]string{"trace":"same"})}
 done:=make(chan error,1)
 go func(){done<-service.publisher.Event(t.Context(),"order.created",publish.Fields{"id":"42"},options...)}()
 <-first.started
 select{case err:=<-done:t.Fatalf("returned before transmission result: %v",err);default:}
 close(first.release)
 if err:=<-done;!errors.Is(err,failure){t.Fatal(err)}
 if err:=service.publisher.Event(t.Context(),"order.created",publish.Fields{"id":"42"},options...);err!=nil{t.Fatal(err)}
 for _,message:=range []event.Message{first.message,next.message}{
  if message.EventName!="order.created"||string(message.Payload)!=` + "`" + `{"id":"42"}` + "`" + `||message.Metadata["message_id"]!="outbox-42"||message.Metadata["trace"]!="same"{t.Fatalf("%+v",message)}
 }
 if err:=prepared.Close(t.Context());err!=nil{t.Fatal(err)}
 if err:=service.publisher.Event(t.Context(),"order.created",nil);!errors.Is(err,event.ErrInvalidState){t.Fatal(err)}
}

`

const previewConditionalSource = `package application
import(
 "encoding/json"
 "context"
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
