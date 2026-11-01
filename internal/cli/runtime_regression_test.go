package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeLogsDoNotInvalidateBuildInputs(t *testing.T) {
	inTempDir(t)
	writeE2EFile(t, "go.mod", "module example.com/app\n\ngo 1.25\n")
	writeE2EFile(t, "main.go", "package main\nfunc main() {}\n")
	before, err := sourceState(".")
	if err != nil {
		t.Fatal(err)
	}
	writeE2EFile(t, "runtime.log", "runtime output\n")
	after, err := sourceState(".")
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("runtime log changed the build input hash")
	}
}

func TestRunRebuildsAfterAnExternalLocalReplacementChanges(t *testing.T) {
	inTempDir(t)
	if err := os.MkdirAll("shared", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll("app", 0o755); err != nil {
		t.Fatal(err)
	}
	writeE2EFile(t, "shared/go.mod", "module example.com/shared\n\ngo 1.25\n")
	dependency := filepath.Join("..", "shared", "value.go")
	writeE2EFile(t, "shared/value.go", "package shared\nfunc Value() string { return \"FIRST\" }\n")
	writeE2EFile(t, "app/go.mod", "module example.com/app\n\ngo 1.25\nrequire example.com/shared v0.0.0\nreplace example.com/shared => ../shared\n")
	writeE2EFile(t, "app/spine.toml", "module = \"example.com/app\"\nentrypoint = \".\"\ncomposition = \".\"\nport = 8080\n")
	writeE2EFile(t, "app/main.go", "package main\nimport (\"fmt\"; \"example.com/shared\")\nfunc main() { fmt.Print(shared.Value()) }\n")
	if err := os.Chdir("app"); err != nil {
		t.Fatal(err)
	}
	before, err := sourceState(".")
	if err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if code := runProject(false, &out, &errOut); code != 0 || !strings.HasSuffix(out.String(), "FIRST") {
		t.Fatalf("first run: %d %s %s", code, out.String(), errOut.String())
	}
	writeE2EFile(t, dependency, "package shared\nfunc Value() string { return \"SECOND\" }\n")
	after, err := sourceState(".")
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("local replacement change was not observed by dev")
	}
	out.Reset()
	errOut.Reset()
	if code := runProject(false, &out, &errOut); code != 0 || !strings.HasSuffix(out.String(), "SECOND") {
		t.Fatalf("local replacement change was ignored: %d %s %s", code, out.String(), errOut.String())
	}
}

func TestVendoredDependencyChangesRemainBuildInputs(t *testing.T) {
	inTempDir(t)
	t.Setenv("GOFLAGS", "-mod=vendor")
	if err := os.MkdirAll("vendor/example.com/shared", 0o755); err != nil {
		t.Fatal(err)
	}
	writeE2EFile(t, "go.mod", "module example.com/app\n\ngo 1.25\nrequire example.com/shared v0.0.0\n")
	writeE2EFile(t, "main.go", "package main\nimport \"example.com/shared\"\nfunc main() { _=shared.Value() }\n")
	writeE2EFile(t, "vendor/modules.txt", "# example.com/shared v0.0.0\n## explicit; go 1.25\nexample.com/shared\n")
	writeE2EFile(t, "vendor/example.com/shared/value.go", "package shared\nfunc Value() string { return \"FIRST\" }\n")
	before, err := sourceState(".")
	if err != nil {
		t.Fatal(err)
	}
	writeE2EFile(t, "vendor/example.com/shared/value.go", "package shared\nfunc Value() string { return \"SECOND\" }\n")
	after, err := sourceState(".")
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("vendored dependency change was ignored")
	}
	var out, errOut bytes.Buffer
	if code := runGo([]string{"build", "-o", filepath.Join(t.TempDir(), "app"), "."}, false, &out, &errOut); code != 0 {
		t.Fatalf("vendor fixture is not a valid Go build: %s", errOut.String())
	}
}

func TestBuildInputCacheFilteringPreservesMutablePackages(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	cache := filepath.Join(t.TempDir(), "cache")
	for _, test := range []struct {
		name      string
		pkg       buildInputPackage
		immutable bool
	}{
		{"versioned cache", buildInputPackage{Dir: filepath.Join(cache, "library@v1"), Module: &buildInputModule{Version: "v1"}}, true},
		{"vendor", buildInputPackage{Dir: filepath.Join(root, "vendor/library"), Module: &buildInputModule{Version: "v1"}}, false},
		{"main module", buildInputPackage{Dir: filepath.Join(cache, "working"), Module: &buildInputModule{Main: true}}, false},
		{"local replacement", buildInputPackage{Dir: filepath.Join(cache, "local"), Module: &buildInputModule{Replace: &buildInputModule{}}}, false},
		{"similar prefix", buildInputPackage{Dir: cache + "-other", Module: &buildInputModule{Version: "v1"}}, false},
	} {
		if actual := immutableModuleCachePackage(test.pkg, root, cache); actual != test.immutable {
			t.Errorf("%s: immutable=%t", test.name, actual)
		}
	}
}
