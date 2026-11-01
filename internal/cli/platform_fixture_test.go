package cli

// 공개 모듈을 사용하는 생성 프로젝트에서 실행하는 HTTP와 WS 수명 검증입니다.
const generatedPlatformLifecycleTest = `package application
import (
 "context"
 "net"
 "net/http"
 "net/http/httptest"
 "testing"
 "time"
 "github.com/gorilla/websocket"
 "github.com/NARUBROWN/spine"
 "github.com/NARUBROWN/spine/pkg/ws"
)
type platformSocket struct{}
type platformMessage struct { Message string }
func (*platformSocket) Handle(ctx context.Context, message platformMessage) error {
 return ws.Send(ctx, ws.TextMessage, []byte(message.Message))
}
func TestPlatformWebSocketAndPort(t *testing.T) {
 listener, err := net.Listen("tcp", "127.0.0.1:0")
 if err != nil { t.Fatal(err) }
 address := listener.Addr().String()
 _, port, _ := net.SplitHostPort(address)
 listener.Close()
 t.Setenv("SPINE_PORT", port)
 app, options, err := BuildApplication()
 if err != nil { t.Fatal(err) }
 if options.Address != ":"+port { t.Fatalf("SPINE_PORT ignored: %s", options.Address) }
 options.Address = address
 options.EnableGracefulShutdown = true
 options.ShutdownTimeout = time.Second
 app.Constructor(func() *platformSocket { return &platformSocket{} })
 if err := app.WebSocket().Register("/ws/echo", (*platformSocket).Handle); err != nil { t.Fatal(err) }
 ctx, cancel := context.WithCancel(context.Background())
 defer cancel()
 done := make(chan error, 1)
 go func() { done <- app.RunContext(ctx, options) }()
 stopped := false
 defer func() { if !stopped { cancel(); select { case <-done: case <-time.After(5*time.Second): t.Error("WS fixture did not stop") } } }()
 deadline := time.Now().Add(10*time.Second)
 var connection *websocket.Conn
 for {
  connection, _, err = websocket.DefaultDialer.Dial("ws://"+address+"/ws/echo", nil)
  if err == nil { break }
  if time.Now().After(deadline) { t.Fatalf("WS readiness: %v", err) }
  time.Sleep(20*time.Millisecond)
 }
 defer connection.Close()
 connection.SetReadDeadline(time.Now().Add(3*time.Second))
 if err := connection.WriteJSON(map[string]string{"Message":"hello"}); err != nil { t.Fatal(err) }
 _, reply, err := connection.ReadMessage()
 if err != nil || string(reply) != "hello" { t.Fatalf("WS reply %s: %v", reply, err) }
 cancel()
 select { case err := <-done: stopped = true; if err != nil { t.Fatal(err) }; case <-time.After(5*time.Second): t.Fatal("shutdown with open WS timed out") }
 if _, _, err := connection.ReadMessage(); err == nil { t.Fatal("shutdown left WS open") }
 rebound, err := net.Listen("tcp", address)
 if err != nil { t.Fatalf("shutdown left listener open: %v", err) }
 rebound.Close()
}
func TestPreparedHandlerAndExternalListenerOwnership(t *testing.T) {
 app, options, err := BuildApplication()
 if err != nil { t.Fatal(err) }
 hookCalled := false
 app.Transport(func(any) { hookCalled = true })
 prepared, err := spine.Prepare(context.Background(), app, options)
 if err != nil { t.Fatal(err) }
 ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
 defer cancel()
 defer prepared.Close(ctx)
 if !hookCalled { t.Fatal("native hook was not called") }
 response := httptest.NewRecorder()
 prepared.Handler().ServeHTTP(response, httptest.NewRequest("GET", "/health", nil))
 if response.Code != 200 { t.Fatalf("prepared handler status %d", response.Code) }
 server := httptest.NewServer(prepared.Handler())
 defer server.Close()
 if err := prepared.Close(ctx); err != nil { t.Fatal(err) }
 response = httptest.NewRecorder()
 prepared.Handler().ServeHTTP(response, httptest.NewRequest("GET", "/health", nil))
 if response.Code != 503 { t.Fatalf("closed handler status %d", response.Code) }
 client := &http.Client{Timeout:time.Second}
 external, err := client.Get(server.URL+"/health")
 if err != nil { t.Fatalf("Close affected external listener: %v", err) }
 external.Body.Close()
 if external.StatusCode != 503 { t.Fatalf("external handler status %d", external.StatusCode) }
}
`
