package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestLayeredRejectsInactiveDependencyProviders(t *testing.T) {
	layeredProject(t)
	layeredGenerate(t, "repository", "reader", "--no-register")
	writeE2EFile(t, "database/repository/reader_repository.go", `package repository
type Database struct{}
func NewDatabase() *Database { return &Database{} }
type ReaderRepository struct{}
func NewReaderRepository(db *Database) *ReaderRepository { return &ReaderRepository{} }
`)
	composition, _ := os.ReadFile("internal/application/application.go")
	composition = bytes.Replace(composition, []byte("import ("), []byte("import (\nrepository \"example.com/todo/database/repository\""), 1)
	composition = bytes.Replace(composition, []byte("app := platform.New()"), []byte("app := platform.New()\nif \"disabled\" == \"enabled\" { app.Constructor(repository.NewDatabase) }"), 1)
	writeE2EFile(t, "internal/application/application.go", string(composition))
	var out, errOut bytes.Buffer
	if code := runGenerate([]string{"service", "reader", "--depends", "database/repository:ReaderRepository"}, &out, &errOut); code == 0 {
		t.Fatal("inactive provider satisfied a required dependency")
	}
	after, _ := os.ReadFile("internal/application/application.go")
	if !bytes.Equal(after, composition) {
		t.Fatal("invalid provider plan changed composition")
	}
	if _, err := os.Stat("src/service/reader_service.go"); !os.IsNotExist(err) {
		t.Fatal("invalid provider plan created a service")
	}
}

func TestLayeredInactiveRoutesDoNotConflict(t *testing.T) {
	layeredProject(t)
	composition, _ := os.ReadFile("internal/application/application.go")
	composition = bytes.Replace(composition, []byte("app := platform.New()"), []byte("app := platform.New()\nif \"disabled\" == \"enabled\" { app.Route(\"GET\", \"/health\", (*controller.HealthController).Get) }"), 1)
	writeE2EFile(t, "internal/application/application.go", string(composition))
	layeredGenerate(t, "service", "status")
	contract, err := inspectApplication()
	if err != nil || contractHasErrors(contract) || len(contract.HTTP) != 1 {
		t.Fatalf("inactive branch polluted the contract: %v %#v", err, contract)
	}
}

func TestLayeredRejectsSurplusPathParameters(t *testing.T) {
	layeredProject(t)
	layeredGenerate(t, "controller", "reader")
	controller, _ := os.ReadFile("src/controller/reader_controller.go")
	source := strings.Replace(string(controller), "package controller", "package controller\nimport \"github.com/NARUBROWN/spine/pkg/path\"", 1)
	source += "\nfunc (c *ReaderController) Get(first path.Int, second path.Int) string { return \"ok\" }\n"
	writeE2EFile(t, "src/controller/reader_controller.go", source)
	before, _ := os.ReadFile(layeredRoutePath)
	var out, errOut bytes.Buffer
	if code := runGenerate([]string{"route", "reader", "--method", "GET", "--path", "/readers/:id", "--handler", "src/controller:ReaderController.Get"}, &out, &errOut); code == 0 {
		t.Fatal("two path parameters were accepted for one path key")
	}
	after, _ := os.ReadFile(layeredRoutePath)
	if !bytes.Equal(before, after) {
		t.Fatal("invalid path binding changed routes")
	}
}

func TestLayeredRegistrationConvertsBlankImportsToNamedImports(t *testing.T) {
	layeredProject(t)
	composition, _ := os.ReadFile("internal/application/application.go")
	composition = bytes.Replace(composition, []byte("import ("), []byte("import (\n_ \"example.com/todo/database/repository\""), 1)
	writeE2EFile(t, "internal/application/application.go", string(composition))
	layeredGenerate(t, "repository", "reader")
	after, _ := os.ReadFile("internal/application/application.go")
	if bytes.Contains(after, []byte("_.NewReaderRepository")) || bytes.Contains(after, []byte("_ \"example.com/todo/database/repository\"")) {
		t.Fatalf("blank import was used as a package name: %s", after)
	}
}

func TestLayeredSwappedBindingAliasesRemainUnambiguous(t *testing.T) {
	layeredProject(t)
	layeredGenerate(t, "controller", "reader")
	controller, _ := os.ReadFile("src/controller/reader_controller.go")
	source := strings.Replace(string(controller), "package controller", `package controller
import (
 query "github.com/NARUBROWN/spine/pkg/path"
 path "github.com/NARUBROWN/spine/pkg/query"
)`, 1)
	source += "\nfunc (c *ReaderController) Get(id query.Int, name path.Values) string { return \"ok\" }\n"
	writeE2EFile(t, "src/controller/reader_controller.go", source)
	layeredGenerate(t, "route", "reader", "--method", "GET", "--path", "/readers/:id", "--handler", "src/controller:ReaderController.Get")
	contract, err := inspectApplication()
	if err != nil || contractHasErrors(contract) {
		t.Fatalf("swapped aliases changed binding types: %v %#v", err, contract.Diagnostics)
	}
}

func TestLayeredIfMatchIncludesRegistrationEnvironment(t *testing.T) {
	layeredProject(t)
	t.Setenv("SPINE_LAYOUT_TEST_ROUTE", "off")
	composition, _ := os.ReadFile("internal/application/application.go")
	composition = bytes.Replace(composition, []byte("app := platform.New()"), []byte("app := platform.New()\nif os.Getenv(\"SPINE_LAYOUT_TEST_ROUTE\") == \"on\" { app.Route(\"GET\", \"/enabled\", (*controller.HealthController).Get) }"), 1)
	writeE2EFile(t, "internal/application/application.go", string(composition))
	var out, errOut bytes.Buffer
	if code := runGenerate([]string{"service", "status", "--dry-run", "--json"}, &out, &errOut); code != 0 {
		t.Fatal(errOut.String())
	}
	var plan struct {
		BaseHash string `json:"base_hash"`
	}
	if err := json.Unmarshal(out.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SPINE_LAYOUT_TEST_ROUTE", "on")
	out.Reset()
	errOut.Reset()
	if code := runGenerate([]string{"service", "status", "--if-match", plan.BaseHash}, &out, &errOut); code == 0 || !strings.Contains(errOut.String(), "changed since dry-run") {
		t.Fatalf("changed registration environment was accepted: %s", errOut.String())
	}
	if _, err := os.Stat("src/service/status_service.go"); !os.IsNotExist(err) {
		t.Fatal("stale environment plan created a service")
	}
}

func TestLayeredRouteGenerationIgnoresInactiveExistingRegistration(t *testing.T) {
	layeredProject(t)
	layeredGenerate(t, "controller", "reader")
	controller, _ := os.ReadFile("src/controller/reader_controller.go")
	writeE2EFile(t, "src/controller/reader_controller.go", string(controller)+"\nfunc (*ReaderController) Get() string { return \"ok\" }\n")
	route, _ := os.ReadFile(layeredRoutePath)
	route = bytes.Replace(route, []byte("return nil"), []byte("if \"disabled\" == \"enabled\" { app.Route(\"GET\", \"/readers\", (*controller.ReaderController).Get) }; return nil"), 1)
	writeE2EFile(t, layeredRoutePath, string(route))
	layeredGenerate(t, "route", "reader", "--method", "GET", "--path", "/readers", "--handler", "src/controller:ReaderController.Get")
	contract, err := inspectApplication()
	if err != nil || contractHasErrors(contract) || len(contract.HTTP) != 2 {
		t.Fatalf("route generation reused an inactive registration: %v %#v", err, contract.HTTP)
	}
}

func TestLayeredGenerationActivatesAnInactiveConstructor(t *testing.T) {
	layeredProject(t)
	layeredGenerate(t, "repository", "reader", "--no-register")
	composition, _ := os.ReadFile("internal/application/application.go")
	composition = bytes.Replace(composition, []byte("import ("), []byte("import (\nrepository \"example.com/todo/database/repository\""), 1)
	composition = bytes.Replace(composition, []byte("app := platform.New()"), []byte("app := platform.New()\nif \"disabled\" == \"enabled\" { app.Constructor(repository.NewReaderRepository) }"), 1)
	writeE2EFile(t, "internal/application/application.go", string(composition))
	layeredGenerate(t, "repository", "reader")
	contract, err := inspectApplication()
	if err != nil || contractHasErrors(contract) {
		t.Fatal(err, contract.Diagnostics)
	}
	for _, constructor := range contract.Constructors {
		if constructor.Target == "example.com/todo/database/repository.NewReaderRepository" {
			return
		}
	}
	t.Fatal("requested constructor remained inactive")
}

func TestLayeredContextPreservesDiagnosticsForBrokenSource(t *testing.T) {
	layeredProject(t)
	writeE2EFile(t, "src/controller/broken.go", "package controller\nfunc (\n")
	var out, errOut bytes.Buffer
	if code := Run([]string{"context", "--json"}, &out, &errOut); code != 5 {
		t.Fatalf("broken project must return a diagnostic payload with exit 5, got %d: %s", code, out.String())
	}
	var response struct {
		OK     bool           `json:"ok"`
		Result projectContext `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.OK || response.Result.ProjectRoot == "" || response.Result.Config.Layout != layeredLayout || response.Result.Doctor.OK || len(response.Result.NextActions) == 0 || len(response.Result.GenerationRoles) != 7 {
		t.Fatalf("context discarded useful diagnostics: %s", out.String())
	}
}

func TestLayeredRejectsGenerationInsideANestedModule(t *testing.T) {
	layeredProject(t)
	if err := os.MkdirAll("database/domain/account", 0o755); err != nil {
		t.Fatal(err)
	}
	writeE2EFile(t, "database/domain/account/go.mod", "module example.com/account\n\ngo 1.25\n")
	var out, errOut bytes.Buffer
	if code := runGenerate([]string{"domain", "account/user"}, &out, &errOut); code == 0 {
		t.Fatal("generation wrote into a different Go module")
	}
	if _, err := os.Stat("database/domain/account/user.go"); !os.IsNotExist(err) {
		t.Fatal("nested module retained a generated file")
	}
}
