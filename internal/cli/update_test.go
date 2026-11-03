package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestUpdateRequirementsPreserveProjectPolicy(t *testing.T) {
	before := "module example.com/shop\n\ngo 1.25.5\n\n// keep project policy\nrequire (\n github.com/NARUBROWN/spine v0.5.2\n example.com/other v1.2.3\n)\n\nexclude example.com/other v1.1.0\nreplace example.com/other v1.2.3 => example.com/fork v1.2.4\n"
	after, previous, err := updateModuleRequirements([]byte(before), "example.com/shop")
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"// keep project policy", "example.com/other v1.2.3", "exclude example.com/other v1.1.0", "example.com/fork v1.2.4", platformSpineVersion, echoPlatformVersion, platformEchoVersion} {
		if !bytes.Contains(after, []byte(text)) {
			t.Fatalf("lost %q: %s", text, after)
		}
	}
	if selectedModuleVersion(previous, spineImportPath) != "v0.5.2" {
		t.Fatalf("incorrect before version: %+v", previous)
	}
}

func TestUpdateRefusesDowngradesAndUnverifiedReplacements(t *testing.T) {
	for _, test := range []struct{ name, requirements, suffix, module, want string }{
		{"spine-newer", spineImportPath + " v0.7.0", "", "example.com/shop", "newer than recipe"},
		{"platform-newer", spineImportPath + " v0.5.2\n" + echoPlatformPath + " v0.2.0", "", "example.com/shop", "newer than recipe"},
		{"echo-newer", spineImportPath + " v0.5.2\ngithub.com/labstack/echo/v5 v5.4.5", "", "example.com/shop", "newer than recipe"},
		{"local-replace", spineImportPath + " v0.5.2", "replace example.com/local => ../local", "example.com/shop", "local replace"},
		{"core-replace", spineImportPath + " v0.5.2", "replace " + spineImportPath + " => example.com/fork v0.5.2", "example.com/shop", "verified public recipe"},
		{"module-mismatch", spineImportPath + " v0.5.2", "", "example.com/wrong", "must match go.mod"},
		{"not-spine", "example.com/other v1.0.0", "", "example.com/shop", "must already require"},
	} {
		t.Run(test.name, func(t *testing.T) {
			contents := fmt.Sprintf("module example.com/shop\n\ngo 1.25.5\nrequire (\n%s\n)\n%s\n", test.requirements, test.suffix)
			if _, _, err := updateModuleRequirements([]byte(contents), test.module); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v; want %q", err, test.want)
			}
		})
	}
}

func TestUpdateHelpAndUsageHaveNoProjectSideEffects(t *testing.T) {
	inTempDir(t)
	t.Setenv("PATH", t.TempDir())
	for _, args := range [][]string{{"update", "--help"}, {"update", "-h", "--root", "missing"}, {"update", "--help", "--root", "missing", "--json"}} {
		var out, errOut bytes.Buffer
		if code := Run(args, &out, &errOut); code != 0 || !strings.Contains(out.String(), "spine update") {
			t.Fatalf("help %v: %d %s %s", args, code, out.String(), errOut.String())
		}
	}
	for _, args := range [][]string{{"update", "--force"}, {"update", "--if-match"}, {"update", "--if-match", "--json"}, {"update", "--if-match", "one", "--if-match", "two"}, {"update", "spine"}, {"update", "--root"}} {
		for _, suffix := range [][]string{nil, {"--json"}} {
			var out, errOut bytes.Buffer
			if code := Run(append(append([]string{}, args...), suffix...), &out, &errOut); code != 2 {
				t.Fatalf("usage %v: %d %s %s", args, code, out.String(), errOut.String())
			}
		}
	}
	entries, _ := os.ReadDir(".")
	if len(entries) != 0 {
		t.Fatalf("help/usage changed directory: %v", entries)
	}
}

func TestUpdateSnapshotCopiesAssetsAndRejectsSymlinks(t *testing.T) {
	inTempDir(t)
	root, _ := os.Getwd()
	writeE2EFile(t, "go.mod", "module example.com/snapshot\n\ngo 1.25.5\n")
	writeE2EFile(t, "main.go", "package main\nfunc main(){}\n")
	for _, directory := range []string{"assets", ".git"} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeE2EFile(t, "assets/message.txt", "hello")
	writeE2EFile(t, ".git/ignored", "ignored")
	writeE2EFile(t, generationLockPath, "owned")
	stage := filepath.Join(t.TempDir(), "project")
	before, err := snapshotUpdateProject(context.Background(), root, stage)
	if err != nil {
		t.Fatal(err)
	}
	asset, err := os.ReadFile(filepath.Join(stage, "assets/message.txt"))
	if err != nil || string(asset) != "hello" {
		t.Fatalf("asset not copied: %s %v", asset, err)
	}
	if _, err := os.Stat(filepath.Join(stage, generationLockPath)); !os.IsNotExist(err) {
		t.Fatal("copied project lock")
	}
	writeE2EFile(t, "assets/message.txt", "other")
	after, err := snapshotUpdateProject(context.Background(), root, "")
	if err != nil || before == after {
		t.Fatalf("asset edit not protected: %v", err)
	}
	if err := os.Symlink("main.go", "linked.go"); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := snapshotUpdateProject(context.Background(), root, ""); err == nil || !strings.Contains(err.Error(), "symlinks") {
		t.Fatalf("symlink accepted: %v", err)
	}
}

func updateTestResult(t *testing.T, args ...string) (map[string]any, int) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := Run(append([]string{"update", "--json"}, args...), &out, &errOut)
	var envelope struct {
		OK     bool           `json:"ok"`
		Result map[string]any `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("invalid envelope: %v %s %s", err, out.String(), errOut.String())
	}
	if envelope.OK != (code == 0) {
		t.Fatalf("incorrect success state: %d %s", code, out.String())
	}
	return envelope.Result, code
}

func TestUpdateDryRunApplyAndStaleInputs(t *testing.T) {
	layeredProject(t)
	t.Setenv("GOWORK", "off")
	t.Setenv("GOFLAGS", " ")
	before, _ := os.ReadFile("go.mod")
	plan, code := updateTestResult(t, "--dry-run")
	if code != 0 || plan["base_hash"] == "" || plan["updated"] != false {
		t.Fatalf("plan: %d %+v", code, plan)
	}
	untouched, _ := os.ReadFile("go.mod")
	if !bytes.Equal(before, untouched) {
		t.Fatal("dry-run changed go.mod")
	}
	if _, err := os.Stat("go.sum"); !os.IsNotExist(err) {
		t.Fatal("dry-run created go.sum")
	}
	writeE2EFile(t, "reviewed-asset.txt", "new source input")
	if result, code := updateTestResult(t, "--if-match", plan["base_hash"].(string)); code != 4 || result["failed_stage"] != "publish" {
		t.Fatalf("stale plan accepted: %d %+v", code, result)
	}
	plan, code = updateTestResult(t, "--dry-run")
	if code != 0 {
		t.Fatalf("fresh plan: %d %+v", code, plan)
	}
	result, code := updateTestResult(t, "--if-match", plan["base_hash"].(string))
	if code != 0 || result["updated"] != true {
		t.Fatalf("apply: %d %+v", code, result)
	}
	goMod, _ := os.ReadFile("go.mod")
	goSum, _ := os.ReadFile("go.sum")
	info, _ := os.Stat("go.mod")
	result, code = updateTestResult(t)
	infoAfter, _ := os.Stat("go.mod")
	if code != 0 || result["updated"] != false || !info.ModTime().Equal(infoAfter.ModTime()) {
		t.Fatalf("repeat was not a no-op: %d %+v", code, result)
	}
	afterMod, _ := os.ReadFile("go.mod")
	afterSum, _ := os.ReadFile("go.sum")
	if !bytes.Equal(goMod, afterMod) || !bytes.Equal(goSum, afterSum) {
		t.Fatal("repeat changed dependency files")
	}
}

func TestUpdateRejectsBrokenContractWithoutPublishing(t *testing.T) {
	layeredProject(t)
	t.Setenv("GOWORK", "off")
	t.Setenv("GOFLAGS", " ")
	// 이전 커널 호출이 남아 있으면 소스를 자동 이동하지 않고 계약 오류로 중단합니다.
	source, _ := os.ReadFile("internal/application/application.go")
	source = bytes.ReplaceAll(source, []byte(`echoplatform "github.com/NARUBROWN/spine-echo-platform"`), []byte(`_ "github.com/NARUBROWN/spine-echo-platform"`))
	source = bytes.ReplaceAll(source, []byte("spine.New(echoplatform.Option())"), []byte("spine.New()"))
	writeE2EFile(t, "internal/application/application.go", string(source))
	before, _ := os.ReadFile("go.mod")
	result, code := updateTestResult(t)
	if code != 5 || result["failed_stage"] != "contract" || !strings.Contains(result["cause"].(string), "HTTP_ADAPTER_MISSING") {
		t.Fatalf("broken adapter published: %d %+v", code, result)
	}
	after, _ := os.ReadFile("go.mod")
	if !bytes.Equal(before, after) {
		t.Fatal("failed contract changed go.mod")
	}
	if _, err := os.Stat("go.sum"); !os.IsNotExist(err) {
		t.Fatal("failed contract created go.sum")
	}
}

func TestUpdateDiscoveryAndCompletions(t *testing.T) {
	found := false
	for _, command := range currentCommandSchema().Commands {
		if command.Name == "update" {
			found = command.SupportsJSON && command.SupportsDryRun && command.Effects.Network && command.Effects.WritesFiles
		}
	}
	if !found {
		t.Fatal("update effects missing from schema")
	}
	for _, shell := range []string{"bash", "zsh", "powershell"} {
		var out, errOut bytes.Buffer
		if code := runCompletion([]string{shell}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "update") {
			t.Fatalf("completion %s: %d %s", shell, code, out.String())
		}
	}
}

func TestUpdateUnsupportedInputsPreserveOriginalProject(t *testing.T) {
	for _, test := range []struct {
		name    string
		prepare func(*testing.T)
		code    int
		want    string
	}{
		{"workspace", func(t *testing.T) {
			root, _ := os.Getwd()
			path := filepath.Join(root, "go.work")
			writeE2EFile(t, path, "go 1.25.5\n")
			t.Setenv("GOWORK", path)
		}, 3, "active workspace"},
		{"flags", func(t *testing.T) { t.Setenv("GOFLAGS", "-trimpath") }, 3, "GOFLAGS"},
		{"vendor", func(t *testing.T) {
			if err := os.Mkdir("vendor", 0755); err != nil {
				t.Fatal(err)
			}
		}, 3, "vendored projects"},
		{"lock", func(t *testing.T) { writeE2EFile(t, generationLockPath, "another owner") }, 4, "generation is in progress"},
		{"local replacement", func(t *testing.T) {
			contents, _ := os.ReadFile("go.mod")
			writeE2EFile(t, "go.mod", string(contents)+"\nreplace example.com/local => ../local\n")
		}, 3, "local replace"},
		{"symlink", func(t *testing.T) {
			if runtime.GOOS == "windows" {
				t.Skip("symlink permissions vary")
			}
			if err := os.Symlink("main.go", "linked.go"); err != nil {
				t.Skip(err)
			}
		}, 3, "not a regular file"},
	} {
		t.Run(test.name, func(t *testing.T) {
			layeredProject(t)
			t.Setenv("GOWORK", "off")
			t.Setenv("GOFLAGS", " ")
			test.prepare(t)
			before, _ := os.ReadFile("go.mod")
			result, code := updateTestResult(t)
			if code != test.code || !strings.Contains(result["cause"].(string), test.want) {
				t.Fatalf("got %d %+v", code, result)
			}
			after, _ := os.ReadFile("go.mod")
			if !bytes.Equal(before, after) {
				t.Fatal("precondition failure changed go.mod")
			}
			if _, err := os.Stat("go.sum"); !os.IsNotExist(err) {
				t.Fatal("precondition failure created go.sum")
			}
			if test.name == "lock" {
				contents, _ := os.ReadFile(generationLockPath)
				if string(contents) != "another owner" {
					t.Fatal("replaced another owner's lock")
				}
			}
		})
	}
}

func TestUpdateLegacyAndRegistrationEnvironment(t *testing.T) {
	inTempDir(t)
	t.Setenv("GOWORK", "off")
	t.Setenv("GOFLAGS", " ")
	var out, errOut bytes.Buffer
	if code := runNew([]string{"legacy", "--module", "example.com/legacy", "--layout", "legacy", "--skip-install"}, &out, &errOut); code != 0 {
		t.Fatal(errOut.String())
	}
	if err := os.Chdir("legacy"); err != nil {
		t.Fatal(err)
	}
	writeE2EFile(t, "registration_input.go", "package main\nimport \"os\"\nvar mode = os.Getenv(\"SPINE_UPDATE_TEST_INPUT\")\n")
	t.Setenv("SPINE_UPDATE_TEST_INPUT", "before")
	plan, code := updateTestResult(t, "--dry-run")
	if code != 0 {
		t.Fatalf("plan %d %+v", code, plan)
	}
	t.Setenv("SPINE_UPDATE_TEST_INPUT", "after")
	if result, code := updateTestResult(t, "--if-match", plan["base_hash"].(string)); code != 4 {
		t.Fatalf("environment change accepted: %d %+v", code, result)
	}
	if result, code := updateTestResult(t); code != 0 {
		t.Fatalf("legacy apply: %d %+v", code, result)
	}
}

func TestUpdateRollbackRestoresDependencyFiles(t *testing.T) {
	inTempDir(t)
	writeE2EFile(t, "go.mod", "changed module")
	writeE2EFile(t, "go.sum", "new checksums")
	backups := map[string]fileBackup{"go.mod": {contents: []byte("original module"), mode: 0640, existed: true}, "go.sum": {existed: false}}
	if err := rollbackFileChanges([]string{"go.mod", "go.sum"}, backups); err != nil {
		t.Fatal(err)
	}
	contents, _ := os.ReadFile("go.mod")
	info, _ := os.Stat("go.mod")
	if string(contents) != "original module" || (runtime.GOOS != "windows" && info.Mode().Perm() != 0640) {
		t.Fatal("dependency backup was not restored")
	}
	if _, err := os.Stat("go.sum"); !os.IsNotExist(err) {
		t.Fatal("created go.sum was not rolled back")
	}
}

func TestUpdateWithoutRecipeKeepsStableEchoDependencies(t *testing.T) {
	layeredProject(t)
	t.Setenv("GOWORK", "off")
	t.Setenv("GOFLAGS", " ")
	config, _ := os.ReadFile("spine.toml")
	lines := []string{}
	for _, line := range strings.Split(string(config), "\n") {
		if !strings.HasPrefix(line, "recipe = ") {
			lines = append(lines, line)
		}
	}
	writeE2EFile(t, "spine.toml", strings.Join(lines, "\n"))
	writeE2EFile(t, "go.mod", "module example.com/todo\n\ngo 1.25.5\n\nrequire github.com/NARUBROWN/spine v0.5.2\n")
	before, _ := os.ReadFile("internal/application/application.go")
	plan, code := updateTestResult(t, "--dry-run")
	if code != 0 || plan["recipe"] != echoRecipe {
		t.Fatalf("existing app changed recipe: %d %+v", code, plan)
	}
	if result, code := updateTestResult(t, "--if-match", plan["base_hash"].(string)); code != 0 {
		t.Fatalf("stable update failed: %d %+v", code, result)
	}
	mod, _ := os.ReadFile("go.mod")
	if !strings.Contains(string(mod), spineImportPath+" "+platformSpineVersion) || strings.Contains(string(mod), assemblySpineVersion) {
		t.Fatalf("wrong stable version: %s", mod)
	}
	after, _ := os.ReadFile("internal/application/application.go")
	if !bytes.Equal(before, after) {
		t.Fatal("dependency update rewrote application")
	}
}
