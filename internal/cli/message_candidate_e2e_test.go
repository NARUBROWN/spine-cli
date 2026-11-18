package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This opt-in test uses the selected source candidate, not a published core tag.
func TestMessageContextCandidateEndToEnd(t *testing.T) {
	core := os.Getenv("SPINE_CONTROLLER_CORE_SOURCE")
	if core == "" {
		t.Skip("SPINE_CONTROLLER_CORE_SOURCE is required for source-candidate E2E")
	}
	core, err := filepath.Abs(core)
	if err != nil {
		t.Fatal(err)
	}
	for _, layout := range []string{layeredLayout, "legacy"} {
		t.Run(layout, func(t *testing.T) {
			root := t.TempDir()
			options := newProjectOptions{Module: "example.com/messages", Layout: layout, Platform: "nethttp", Validator: "none", WebSocketPlatform: "gorilla", Modules: []selectedModule{{Path: spineImportPath, Version: "v0.7.6-preview.1"}, {Path: nethttpPlatformPath, Version: "v0.1.0-preview.2"}, {Path: gorillaPlatformPath, Version: "v0.2.0-preview.2"}}}
			files, err := newProjectFiles(options)
			if err != nil {
				t.Fatal(err)
			}
			for name, contents := range files {
				path := filepath.Join(root, name)
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
				writeE2EFile(t, path, contents)
			}
			generatedGo(t, root, "mod", "edit", "-replace="+spineImportPath+"="+core)
			generatedGo(t, root, "mod", "tidy")
			t.Chdir(root)
			t.Setenv("GOWORK", "off")
			// Use the same planner entry points as the CLI, for both layouts.
			for _, args := range [][]string{{"consumer", "audit", "--topic", "audit.created", "--field", "value:string"}, {"websocket", "audit", "--path", "/ws/audit", "--field", "value:string"}} {
				if layout == layeredLayout {
					layeredGenerate(t, args...)
				} else {
					runGenerateForCandidate(t, args)
				}
			}
			generatedGo(t, root, "test", "./...") // Unimplemented generated handlers must still compile.
			socketPath := filepath.Join(root, "src/websocket/audit/audit_handler.go")
			source, err := os.ReadFile(socketPath)
			if err != nil {
				t.Fatal(err)
			}
			source = []byte(strings.Replace(string(source), `return errors.New("audit websocket handler is not implemented")`, `ctx.Set("value",message.Value.Value)
 value,ok:=ctx.Get("value");if !ok{return errors.New("frame store missing")}
 return s.sender.Send(ctx,connectionID.Value,ws.TextMessage,[]byte(value.(string)))`, 1))
			writeE2EFile(t, socketPath, string(source))
			consumerPath := filepath.Join(root, "src/consumer/audit/audit_handler.go")
			source, err = os.ReadFile(consumerPath)
			if err != nil {
				t.Fatal(err)
			}
			source = []byte(strings.Replace(string(source), `return errors.New("audit consumer handler is not implemented")`, `ctx.Set("value",event.Value)
 if value,ok:=ctx.Get("value");!ok||value!=event.Value{return errors.New("delivery store missing")}
 if event.Value=="fail" {return errors.New("expected failure")};return nil`, 1))
			writeE2EFile(t, consumerPath, string(source))
			writeE2EFile(t, filepath.Join(root, "message_runtime_test.go"), messageCandidateRuntimeTest)
			generatedGo(t, root, "mod", "edit", "-require=github.com/NARUBROWN/spine-gorilla-platform@v0.2.0-preview.2")
			generatedGo(t, root, "mod", "tidy")
			t.Log(generatedGo(t, root, "test", "-race", "./...", "-count=1", "-timeout=60s"))
			contract, err := inspectApplication()
			if err != nil || contractHasErrors(contract) || len(contract.WebSockets) != 1 || len(contract.Consumers) != 1 {
				t.Fatalf("contract=%+v err=%v", contract, err)
			}
			if contract.WebSockets[0].Parameters[0].Binding != "context" || contract.Consumers[0].Parameters[0].Binding != "context" {
				t.Fatal(contract.WebSockets, contract.Consumers)
			}
		})
	}
}

func runGenerateForCandidate(t *testing.T, args []string) {
	t.Helper()
	var out, errOut bytes.Buffer
	if code := runGenerate(args, &out, &errOut); code != 0 {
		t.Fatalf("generate: %d %s %s", code, out.String(), errOut.String())
	}
}

const messageCandidateRuntimeTest = `package main
import (
 "context"
 "fmt"
 "net"
 "net/http"
 "sync"
 "testing"
 "time"
 consumer "example.com/messages/src/consumer/audit"
 socket "example.com/messages/src/websocket/audit"
 "github.com/NARUBROWN/spine"
 gorilla "github.com/NARUBROWN/spine-gorilla-platform"
 nethttp "github.com/NARUBROWN/spine-nethttp-platform"
 event "github.com/NARUBROWN/spine/contract/event"
 "github.com/NARUBROWN/spine/pkg/api"
 "github.com/NARUBROWN/spine/pkg/boot"
 view "github.com/NARUBROWN/spine/pkg/spine"
 "github.com/gorilla/websocket"
)

type probe struct{}
func (*probe)RegisterRoutes(r *api.Registry){
 r.GET("/ctx",func(ctx view.Ctx)error{
  ctx.ExecutionContext().Set("probe","ok")
  if value,ok:=ctx.ExecutionContext().Get("probe");!ok||value!="ok"{return fmt.Errorf("HTTP store missing")}
  for _,value:=range []any{ctx,ctx.ExecutionContext()}{if _,ok:=value.(interface{Close()});ok{return fmt.Errorf("internal Close exposed")}}
  response,ok:=ctx.HTTPResponseContext();if !ok{return fmt.Errorf("response missing")};return response.WriteString(200,"ok")
 })
}
type factory struct{input chan event.Delivery}
func(*factory)Capabilities()event.Capabilities{return event.Capabilities{ContractVersion:event.ContractVersion,Consume:true}}
func(f *factory)NewReader(context.Context,event.Subscription)(event.Reader,error){return &reader{input:f.input,closed:make(chan struct{})},nil}
func(*factory)NewPublisher(context.Context)(event.Publisher,error){return nil,fmt.Errorf("unused")}
type reader struct{input chan event.Delivery;closed chan struct{};once sync.Once}
func(r *reader)Read(ctx context.Context)(event.Delivery,error){select{case d:=<-r.input:return d,nil;case <-ctx.Done():return nil,ctx.Err();case <-r.closed:return nil,context.Canceled}}
func(r *reader)Close(context.Context)error{r.once.Do(func(){close(r.closed)});return nil}
type delivery struct{payload string;settled chan bool}
func(d *delivery)Message()event.Message{return event.Message{EventName:"audit.created",Payload:[]byte(d.payload)}}
func(d *delivery)Ack(context.Context)error{d.settled<-true;return nil}
func(d *delivery)Nack(context.Context)error{d.settled<-false;return nil}
func TestGeneratedContextRuntime(t *testing.T){
 f:=&factory{input:make(chan event.Delivery,2)}
 app:=spine.New(nethttp.Option(),gorilla.Option(),spine.WithEventAdapter("fixture",f))
 app.Constructor(func()*probe{return &probe{}})
 if err:=consumer.Register(app);err!=nil{t.Fatal(err)};if err:=socket.Register(app);err!=nil{t.Fatal(err)}
 listener,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)};address:=listener.Addr().String();listener.Close()
 options:=boot.Options{Address:address,HTTP:&boot.HTTPOptions{},EnableGracefulShutdown:true,ShutdownTimeout:3*time.Second}
 ctx,cancel:=context.WithCancel(context.Background());done:=make(chan error,1);go func(){done<-app.RunContext(ctx,options)}()
 defer func(){cancel();select{case err:=<-done:if err!=nil{t.Error(err)};case <-time.After(5*time.Second):t.Error("shutdown timeout")}}()
 client:=&http.Client{Timeout:time.Second};deadline:=time.Now().Add(10*time.Second)
 for{response,err:=client.Get("http://"+address+"/ctx");if err==nil{response.Body.Close();if response.StatusCode==200{break}};if time.Now().After(deadline){t.Fatal("HTTP context route not ready",err)};time.Sleep(20*time.Millisecond)}
 connection,_,err:=websocket.DefaultDialer.Dial("ws://"+address+"/ws/audit",nil);if err!=nil{t.Fatal(err)};defer connection.Close();connection.SetReadDeadline(time.Now().Add(3*time.Second))
 if err:=connection.WriteJSON(map[string]string{"value":"hello"});err!=nil{t.Fatal(err)}
 _,body,err:=connection.ReadMessage();if err!=nil||string(body)!="hello"{t.Fatalf("WS response %q %v",body,err)}
 for _,tc:=range []struct{value string;ack bool}{{"ok",true},{"fail",false}}{
  d:=&delivery{payload:fmt.Sprintf("{\"value\":%q}",tc.value),settled:make(chan bool,1)};f.input<-d
  select{case ack:=<-d.settled:if ack!=tc.ack{t.Fatalf("settlement=%v want=%v",ack,tc.ack)};case <-time.After(3*time.Second):t.Fatal("event not settled")}
 }
}
`
