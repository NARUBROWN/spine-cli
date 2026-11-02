package cli

import (
	"encoding/json"
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLayeredGeneratedProjectEndToEnd(t *testing.T) {
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	name := "spine"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binary := filepath.Join(t.TempDir(), name)
	build := exec.Command("go", "build", "-o", binary, "./cmd/spine")
	build.Dir = repository
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("CLI build: %v\n%s", err, output)
	}
	workspace := t.TempDir()
	apply := func(root string, args ...string) {
		t.Helper()
		planArgs := append(append([]string{}, args...), "--dry-run", "--json")
		plan := runE2E(binary, root, nil, planArgs...)
		assertE2ESuccess(t, plan, "dry-run")
		var d struct {
			Result struct {
				BaseHash string `json:"base_hash"`
			}
		}
		if err := json.Unmarshal([]byte(plan.stdout), &d); err != nil {
			t.Fatal(err)
		}
		if d.Result.BaseHash == "" {
			t.Fatal("dry-run hash missing")
		}
		args = append(args, "--if-match", d.Result.BaseHash, "--json")
		result := runE2E(binary, root, nil, args...)
		assertE2ESuccess(t, result, "apply reviewed plan")
	}
	apply(workspace, "new", "todo", "--module", "example.com/todo")
	project := filepath.Join(workspace, "todo")
	compositionPath := filepath.Join(project, "internal/application/application.go")
	composition, err := os.ReadFile(compositionPath)
	if err != nil {
		t.Fatal(err)
	}
	compositionText := strings.Replace(string(composition), "import (", "import (\n_ \"example.com/todo/database/repository/account\"", 1)
	compositionText = strings.Replace(compositionText, "app := platform.New()", "app := platform.New()\nif \"disabled\" == \"enabled\" { app.Route(\"GET\", \"/health\", (*controller.HealthController).Get) }", 1)
	writeE2EFile(t, compositionPath, compositionText)
	for _, group := range []string{"account/user", "admin/user"} {
		apply(project, "g", "resource", group)
	}
	apply(project, "g", "domain", "profile", "--field", "id:int64", "--field", "name:string")
	apply(project, "g", "dto", "account/create-user-request", "--field", "name:string")
	apply(project, "g", "service", "report", "--depends", "database/repository/account:UserRepository", "--depends", "database/repository/admin:UserRepository")
	apply(project, "g", "controller", "admin/report", "--depends", "src/service:ReportService")
	controller := filepath.Join(project, "src/controller/admin/report_controller.go")
	source, err := os.ReadFile(controller)
	if err != nil {
		t.Fatal(err)
	}
	source = append(source, []byte("\nfunc (c ReportController) Get() error { return nil }\n")...)
	source = []byte(strings.Replace(string(source), "package admin", `package admin
import (
 query "github.com/NARUBROWN/spine/pkg/path"
 path "github.com/NARUBROWN/spine/pkg/query"
)`, 1))
	source = append(source, []byte("\nfunc (c *ReportController) Find(id query.Int, values path.Values) error { return nil }\n")...)
	source, err = format.Source(source)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(controller, source, 0o644); err != nil {
		t.Fatal(err)
	}
	apply(project, "g", "route", "report", "--method", "GET", "--path", "/report", "--handler", "src/controller/admin:ReportController.Get")
	apply(project, "g", "route", "find-report", "--method", "GET", "--path", "/reports/:id", "--handler", "src/controller/admin:ReportController.Find")
	for _, group := range []string{"commerce/account/user", "identity/account/user"} {
		apply(project, "g", "repository", group)
	}
	apply(project, "g", "service", "audit", "--depends", "database/repository/commerce/account:UserRepository", "--depends", "database/repository/identity/account:UserRepository")
	for _, group := range []string{"account", "admin"} {
		apply(project, "g", "consumer", group+"/order", "--topic", group+".order.created", "--field", "order_id:int64")
		path := filepath.Join(project, "src/consumer", group, "order/order_handler.go")
		implementLayeredMessageHandler(t, path, "order consumer handler is not implemented")
	}
	apply(project, "g", "websocket", "account/chat", "--path", "/ws/chat", "--field", "message:string")
	implementLayeredMessageHandler(t, filepath.Join(project, "src/websocket/account/chat/chat_handler.go"), "chat websocket handler is not implemented")
	context := runE2E(binary, workspace, nil, "context", "--root", project, "--json")
	assertE2ESuccess(t, context, "layered context")
	if !strings.Contains(context.stdout, "generation_roles") || !strings.Contains(context.stdout, "ReportService") || !strings.Contains(context.stdout, layeredLayout) {
		t.Fatalf("context lost layout inventory: %s", context.stdout)
	}
	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir = project
	if output, err := tidy.CombinedOutput(); err != nil {
		t.Fatalf("generated project dependencies: %v\n%s", err, output)
	}
	verification := runE2E(binary, workspace, nil, "verify", "--root", project, "--json")
	assertE2ESuccess(t, verification, "layered project Go and static verification")
	probe := runE2E(binary, workspace, nil, "contract", "probe", "--root", project, "--json")
	assertE2ESuccess(t, probe, "listener-free app.Validate and registry probe")
}

func implementLayeredMessageHandler(t *testing.T, path, sentinel string) {
	t.Helper()
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	implemented := strings.ReplaceAll(string(source), "\"errors\"", "")
	implemented = strings.ReplaceAll(implemented, "return errors.New(\""+sentinel+"\")", "return nil")
	formatted, err := format.Source([]byte(implemented))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, formatted, 0o644); err != nil {
		t.Fatal(err)
	}
}
