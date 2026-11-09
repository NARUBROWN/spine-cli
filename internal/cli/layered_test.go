package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func layeredProject(t *testing.T) {
	if err := prepareTestModuleCache(); err != nil {
		t.Fatal(err)
	}
	t.Helper()
	inTempDir(t)
	var out, errOut bytes.Buffer
	if code := runNew([]string{"todo", "--module", "example.com/todo", "--channel", "preview", "--skip-install"}, &out, &errOut); code != 0 {
		t.Fatalf("new: %s", errOut.String())
	}
	if err := os.Chdir("todo"); err != nil {
		t.Fatal(err)
	}
}

// layeredGenerate는 실제 적용 전에 같은 요청의 dry-run 계획을 확인합니다.
func layeredGenerate(t *testing.T, args ...string) map[string]any {
	t.Helper()
	var out, errOut bytes.Buffer
	planArgs := append(append([]string{}, args...), "--dry-run", "--json")
	if code := runGenerate(planArgs, &out, &errOut); code != 0 {
		t.Fatalf("plan %v: %s", args, errOut.String())
	}
	var plan map[string]any
	if err := json.Unmarshal(out.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if plan["base_hash"] == "" || plan["layout"] != layeredLayout {
		t.Fatalf("missing plan metadata: %v", plan)
	}
	out.Reset()
	errOut.Reset()
	applyArgs := append(append([]string{}, args...), "--if-match", plan["base_hash"].(string), "--json")
	if code := runGenerate(applyArgs, &out, &errOut); code != 0 {
		t.Fatalf("apply %v: %s", args, errOut.String())
	}
	return plan
}

func TestLayeredRolePathsFlatGroupedAndNested(t *testing.T) {
	layeredProject(t)
	for _, role := range componentRoles() {
		for _, name := range []string{"user", "account/user", "commerce/account/user"} {
			layeredGenerate(t, role.Name, name, "--no-register")
			parsed, _ := parseGenerationName(name)
			file := parsed.Name.Package
			if role.Constructor {
				file += "_" + role.Name
			}
			path := filepath.Join(role.Root, parsed.Group, file+".go")
			source, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(source), "package "+filepath.Base(filepath.Dir(path))) {
				t.Fatalf("wrong package in %s: %s", path, source)
			}
		}
	}
	layeredGenerate(t, "domain", "account/member", "--field", "id:int64", "--field", "name:string")
	model, _ := os.ReadFile("database/domain/account/member.go")
	if !strings.Contains(string(model), "ID") || strings.Contains(string(model), "json:") || strings.Contains(string(model), "bun:") || strings.Contains(string(model), "CreatedAt") {
		t.Fatalf("implicit model policy: %s", model)
	}
	layeredGenerate(t, "dto", "account/create-member-request", "--field", "name:string")
	dto, _ := os.ReadFile("src/dto/account/create_member_request.go")
	if !strings.Contains(string(dto), "CreateMemberRequest") || !strings.Contains(string(dto), "json:\"name\"") {
		t.Fatalf("wrong DTO: %s", dto)
	}
}

func TestLayeredPlansAreIndependentOfWorkingDirectory(t *testing.T) {
	layeredProject(t)
	var out, errOut bytes.Buffer
	args := []string{"repository", "account/user", "--no-register", "--dry-run", "--json"}
	if code := runGenerate(args, &out, &errOut); code != 0 {
		t.Fatal(errOut.String())
	}
	first := out.String()
	out.Reset()
	if err := os.Chdir("internal/application"); err != nil {
		t.Fatal(err)
	}
	if code := runGenerate(args, &out, &errOut); code != 0 {
		t.Fatal(errOut.String())
	}
	if first != out.String() {
		t.Fatalf("cwd changed plan:\n%s\n%s", first, out.String())
	}
}

func TestLayeredQualifiedDependenciesAndDuplicateTypeNames(t *testing.T) {
	layeredProject(t)
	layeredGenerate(t, "repository", "account/user")
	layeredGenerate(t, "repository", "admin/user")
	layeredGenerate(t, "service", "user", "--depends", "src/repository/account:UserRepository", "--depends", "src/repository/admin:UserRepository")
	layeredGenerate(t, "controller", "admin/user", "--depends", "src/service:UserService")
	controller, _ := os.ReadFile("src/controller/admin/user_controller.go")
	if strings.Contains(string(controller), "func (c *UserController) Get") {
		t.Fatalf("controller invented an HTTP operation: %s", controller)
	}
	contract, err := inspectApplication()
	if err != nil || contractHasErrors(contract) {
		t.Fatalf("qualified contract failed: %v, %#v", err, contract.Diagnostics)
	}
	found := map[string]bool{}
	for _, ctor := range contract.Constructors {
		found[ctor.Target] = true
	}
	for _, key := range []string{"example.com/todo/src/repository/account.NewUserRepository", "example.com/todo/src/repository/admin.NewUserRepository"} {
		if !found[key] {
			t.Fatalf("constructor identity lost: %v", found)
		}
	}
	service, _ := os.ReadFile("src/service/user_service.go")
	if !strings.Contains(string(service), "account.UserRepository") || !strings.Contains(string(service), "admin.UserRepository") {
		t.Fatalf("dependencies collapsed: %s", service)
	}
	layeredGenerate(t, "repository", "account/user")
	application, _ := os.ReadFile("internal/application/application.go")
	if strings.Count(string(application), "account.NewUserRepository") != 1 {
		t.Fatalf("constructor duplicated: %s", application)
	}
}

func TestLayeredExplicitRoutesValidateHandlersAndRemainIdempotent(t *testing.T) {
	layeredProject(t)
	layeredGenerate(t, "controller", "admin/user")
	path := "src/controller/admin/user_controller.go"
	source, _ := os.ReadFile(path)
	source = append(source, []byte("\nfunc (c *UserController) Get() error { return nil }\n")...)
	if err := os.WriteFile(path, source, 0o644); err != nil {
		t.Fatal(err)
	}
	args := []string{"route", "get-user", "--method", "GET", "--path", "/users", "--handler", "src/controller/admin:UserController.Get"}
	layeredGenerate(t, args...)
	layeredGenerate(t, args...)
	routes, _ := os.ReadFile("src/route/route.go")
	if strings.Count(string(routes), "\"/users\"") != 1 {
		t.Fatalf("route duplicated: %s", routes)
	}
	contract, err := inspectApplication()
	if err != nil || contractHasErrors(contract) {
		t.Fatalf("route contract failed: %v %#v", err, contract.Diagnostics)
	}
	var out, errOut bytes.Buffer
	for _, bad := range [][]string{
		{"route", "missing", "--method", "GET", "--path", "/missing", "--handler", "src/controller/admin:UserController.Missing"},
		{"route", "conflict", "--method", "GET", "--path", "/users", "--handler", "src/controller:HealthController.Get"},
		{"route", "binding", "--method", "GET", "--path", "/users/:id", "--handler", "src/controller/admin:UserController.Get"},
	} {
		out.Reset()
		errOut.Reset()
		if code := runGenerate(bad, &out, &errOut); code == 0 {
			t.Fatalf("invalid route accepted: %v", bad)
		}
	}
	after, _ := os.ReadFile("src/route/route.go")
	if !bytes.Equal(after, routes) {
		t.Fatal("failed route changed existing source")
	}
}

func TestLayeredResourceMatchesIndividualGenerators(t *testing.T) {
	layeredProject(t)
	layeredGenerate(t, "resource", "commerce/account/user")
	paths := []string{"database/domain/commerce/account/user.go", "src/repository/commerce/account/user_repository.go", "src/service/commerce/account/user_service.go", "src/controller/commerce/account/user_controller.go", "internal/application/application.go"}
	before := map[string]string{}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		before[path] = string(data)
	}
	layeredGenerate(t, "domain", "commerce/account/user")
	layeredGenerate(t, "repository", "commerce/account/user")
	layeredGenerate(t, "service", "commerce/account/user", "--depends", "src/repository/commerce/account:UserRepository")
	layeredGenerate(t, "controller", "commerce/account/user", "--depends", "src/service/commerce/account:UserService")
	for _, path := range paths {
		data, _ := os.ReadFile(path)
		if string(data) != before[path] {
			t.Fatalf("individual generator drifted from resource: %s", path)
		}
	}
	contract, err := inspectApplication()
	if err != nil || contractHasErrors(contract) || len(contract.HTTP) != 1 {
		t.Fatalf("resource generated implicit API: %v %#v", err, contract)
	}
}

func TestLayeredIfMatchIncludesInputsAndProposedContents(t *testing.T) {
	layeredProject(t)
	layeredGenerate(t, "repository", "account/user")
	var out, errOut bytes.Buffer
	args := []string{"service", "user", "--depends", "src/repository/account:UserRepository", "--dry-run", "--json"}
	if code := runGenerate(args, &out, &errOut); code != 0 {
		t.Fatal(errOut.String())
	}
	var plan map[string]any
	_ = json.Unmarshal(out.Bytes(), &plan)
	path := "src/repository/account/user_repository.go"
	source, _ := os.ReadFile(path)
	_ = os.WriteFile(path, append(source, []byte("\n// 입력이 바뀌었습니다.\n")...), 0o644)
	out.Reset()
	errOut.Reset()
	if code := runGenerate([]string{"service", "user", "--depends", "src/repository/account:UserRepository", "--if-match", plan["base_hash"].(string)}, &out, &errOut); code == 0 || !strings.Contains(errOut.String(), "changed since dry-run") {
		t.Fatalf("changed dependency accepted: %s", errOut.String())
	}
	if _, err := os.Stat("src/service/user_service.go"); !os.IsNotExist(err) {
		t.Fatal("stale plan wrote a service")
	}
	out.Reset()
	errOut.Reset()
	if code := runGenerate([]string{"domain", "member", "--field", "name:string", "--dry-run", "--json"}, &out, &errOut); code != 0 {
		t.Fatal(errOut.String())
	}
	_ = json.Unmarshal(out.Bytes(), &plan)
	if code := runGenerate([]string{"domain", "member", "--field", "age:int", "--if-match", plan["base_hash"].(string)}, &out, &errOut); code == 0 {
		t.Fatal("different proposed contents accepted")
	}
}

func TestLayeredRejectsUnsafePathsPackageConflictsAndDataInjection(t *testing.T) {
	layeredProject(t)
	for _, name := range []string{"../user", "account/../../user", "/user", "a//b", "a/", "a\\b", "type/user", "a/.hidden/user"} {
		var out, errOut bytes.Buffer
		if code := runGenerate([]string{"domain", name}, &out, &errOut); code == 0 {
			t.Fatalf("invalid path accepted: %q", name)
		}
	}
	layeredGenerate(t, "domain", "user")
	var out, errOut bytes.Buffer
	if code := runGenerate([]string{"service", "user", "--depends", "database/domain:User"}, &out, &errOut); code == 0 {
		t.Fatal("data type injected without a provider")
	}
	_ = os.MkdirAll("src/service/account", 0o755)
	_ = os.WriteFile("src/service/account/other.go", []byte("package wrong\n"), 0o644)
	if code := runGenerate([]string{"service", "account/user"}, &out, &errOut); code == 0 {
		t.Fatal("package conflict accepted")
	}
	if err := os.Symlink(t.TempDir(), "database/domain/linked"); err != nil {
		t.Fatal(err)
	}
	if code := runGenerate([]string{"domain", "linked/user"}, &out, &errOut); code == 0 {
		t.Fatal("symlink role path accepted")
	}
}

func TestLayeredInterfaceAndValueDependencies(t *testing.T) {
	layeredProject(t)
	_ = os.MkdirAll("src/repository", 0o755)
	source := `package repository
type Reader interface { Read() string }
type record struct{}
func (record) Read() string { return "ok" }
func NewReader() Reader { return record{} }
type Value struct{}
func NewValue() Value { return Value{} }
`
	_ = os.WriteFile("src/repository/reader.go", []byte(source), 0o644)
	layeredGenerate(t, "service", "report", "--depends", "src/repository:Reader", "--depends", "src/repository:Value")
	generated, _ := os.ReadFile("src/service/report_service.go")
	if strings.Contains(string(generated), "*repository.Reader") || strings.Contains(string(generated), "*repository.Value") {
		t.Fatalf("interface/value changed to pointer: %s", generated)
	}
}

func TestLayeredRejectsDependencyCyclesAtomically(t *testing.T) {
	layeredProject(t)
	_ = os.MkdirAll("src/service", 0o755)
	_ = os.WriteFile("src/service/a.go", []byte("package service\ntype A struct{}\nfunc NewA(b *BService) *A { return &A{} }\n"), 0o644)
	before, _ := os.ReadFile("internal/application/application.go")
	var out, errOut bytes.Buffer
	if code := runGenerate([]string{"service", "b", "--depends", "src/service:A"}, &out, &errOut); code == 0 || !strings.Contains(errOut.String(), "cycle") {
		t.Fatalf("cycle accepted: %s", errOut.String())
	}
	after, _ := os.ReadFile("internal/application/application.go")
	if !bytes.Equal(before, after) {
		t.Fatal("cycle changed composition")
	}
	if _, err := os.Stat("src/service/b_service.go"); !os.IsNotExist(err) {
		t.Fatal("cycle left generated file")
	}
}

func TestLayeredDryRunAndConflictsLeaveAllFilesUntouched(t *testing.T) {
	layeredProject(t)
	before, err := os.ReadFile("internal/application/application.go")
	if err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if code := Run([]string{"g", "resource", "account/member", "--dry-run", "--json"}, &out, &errOut); code != 0 {
		t.Fatal(out.String(), errOut.String())
	}
	if _, err := os.Stat("database"); !os.IsNotExist(err) {
		t.Fatal("dry-run created database files")
	}
	custom := "package account\ntype MemberService struct { Custom bool }\nfunc NewMemberService() *MemberService { return &MemberService{Custom: true} }\n"
	if err := os.MkdirAll("src/service/account", 0o755); err != nil {
		t.Fatal(err)
	}
	writeE2EFile(t, "src/service/account/member_service.go", custom)
	out.Reset()
	errOut.Reset()
	if code := Run([]string{"g", "resource", "account/member", "--json"}, &out, &errOut); code != 4 {
		t.Fatalf("conflict exit code %d: %s %s", code, out.String(), errOut.String())
	}
	var failure struct {
		OK     bool `json:"ok"`
		Result struct {
			Diff string `json:"diff"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &failure); err != nil || failure.OK || !strings.Contains(failure.Result.Diff, "Custom") {
		t.Fatalf("conflict did not expose the proposed diff: %v %s", err, out.String())
	}
	after, _ := os.ReadFile("internal/application/application.go")
	service, _ := os.ReadFile("src/service/account/member_service.go")
	if !bytes.Equal(before, after) || string(service) != custom {
		t.Fatal("failed resource changed existing source")
	}
	if _, err := os.Stat("database"); !os.IsNotExist(err) {
		t.Fatal("failed resource left domain or repository files")
	}
	if _, err := os.Stat("src/controller/account"); !os.IsNotExist(err) {
		t.Fatal("failed resource left a controller")
	}
}

func TestLayeredAliasedPathTypesAndCompositionRouteConflicts(t *testing.T) {
	layeredProject(t)
	layeredGenerate(t, "controller", "member", "--no-register")
	path := "src/controller/member_controller.go"
	source, _ := os.ReadFile(path)
	updated := strings.Replace(string(source), "package controller", "package controller\nimport routepath \"github.com/NARUBROWN/spine/pkg/path\"", 1)
	updated += "\nfunc (c MemberController) Get(id routepath.Int) error { return nil }\n"
	writeE2EFile(t, path, updated)
	layeredGenerate(t, "route", "member", "--method", "GET", "--path", "/members/:id", "--handler", "src/controller:MemberController.Get")
	contract, err := inspectApplication()
	if err != nil || contractHasErrors(contract) {
		t.Fatalf("valid aliased path type was unresolved: %v %#v", err, contract.Diagnostics)
	}
	composition, _ := os.ReadFile("internal/application/application.go")
	composition = bytes.Replace(composition, []byte("app := spine.New(echoplatform.Option())"), []byte("app := spine.New(echoplatform.Option())\napp.Route(\"GET\", \"/direct/:key\", (*controller.MemberController).Get)"), 1)
	writeE2EFile(t, "internal/application/application.go", string(composition))
	before, _ := os.ReadFile(layeredRoutePath)
	var out, errOut bytes.Buffer
	if code := runGenerate([]string{"route", "conflict", "--method", "GET", "--path", "/direct/:id", "--handler", "src/controller:MemberController.Get"}, &out, &errOut); code == 0 || !strings.Contains(errOut.String(), "overlaps") {
		t.Fatalf("composition route conflict accepted: %s", errOut.String())
	}
	after, _ := os.ReadFile(layeredRoutePath)
	if !bytes.Equal(before, after) {
		t.Fatal("conflicting route modified the route file")
	}
}

func TestLayeredSchemaAndContextExposeTheSameRolePaths(t *testing.T) {
	layeredProject(t)
	layeredGenerate(t, "domain", "commerce/account/member")
	layeredGenerate(t, "dto", "request")
	composition, _ := os.ReadFile("internal/application/application.go")
	composition = bytes.Replace(composition, []byte("app := spine.New(echoplatform.Option())"), []byte("app := spine.New(echoplatform.Option())\napp.Constructor(newLocalDependency)"), 1)
	composition = append(composition, []byte("\ntype localDependency struct{}\nfunc newLocalDependency() *localDependency { return &localDependency{} }\n")...)
	writeE2EFile(t, "internal/application/application.go", string(composition))
	var out, errOut bytes.Buffer
	if code := runContext([]string{"--json"}, &out, &errOut); code != 0 {
		t.Fatal(out.String(), errOut.String())
	}
	var context projectContext
	if err := json.Unmarshal(out.Bytes(), &context); err != nil {
		t.Fatal(err)
	}
	expected, _ := json.Marshal(currentCommandSchema().GenerationRoles)
	actual, _ := json.Marshal(context.GenerationRoles)
	if !bytes.Equal(expected, actual) || len(context.GenerationRoles) != 7 || context.Config.Layout != layeredLayout {
		t.Fatalf("schema/context paths differ: %s, %s", expected, actual)
	}
	found := false
	for _, component := range context.Components {
		if component.Role == "domain" && component.Group == "commerce/account" && component.Package == "example.com/todo/database/domain/commerce/account" && component.Type == "Member" {
			found = true
		}
	}
	if !found {
		t.Fatalf("context lost grouped domain identity: %#v", context.Components)
	}
	contract, err := inspectApplication()
	if err != nil || contractHasErrors(contract) {
		t.Fatalf("local constructor became unresolved: %v %#v", err, contract.Diagnostics)
	}
}
