//go:build linux

package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestRuntimeCredentialsAreBoundedAndReservedNamesRejected(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "spine-env")
	if err := os.WriteFile(p, []byte(`{"API_KEY":"example","EMPTY":""}`), 0600); err != nil {
		t.Fatal(err)
	}
	env, err := readRuntimeEnv(dir, "18081")
	if err != nil || !reflect.DeepEqual(env, []string{"PATH=/usr/bin:/bin", "SPINE_PORT=18081", "API_KEY=example", "EMPTY="}) {
		t.Fatalf("unexpected runtime environment: %v", err)
	}
	for _, payload := range []string{`{"SPINE_PORT":"1"}`, `{"LD_PRELOAD":"/tmp/evil"}`, `{"CREDENTIALS_DIRECTORY":"/tmp"}`, `{"BAD-NAME":"x"}`} {
		if err = os.WriteFile(p, []byte(payload), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = readRuntimeEnv(dir, "18081"); err == nil {
			t.Fatal("accepted reserved or invalid environment")
		}
	}
	if _, err = readRuntimeEnv("", "22"); err == nil {
		t.Fatal("accepted privileged port")
	}
	if err = startCloudRuntime("/tmp/app"); err == nil {
		t.Fatal("accepted unallocated executable")
	}
}

func TestRuntimeExecReplacesProcess(t *testing.T) {
	if os.Getenv("SPINE_RUNTIME_TEST_HELPER") == "1" {
		if err := execCloudRuntime(os.Getenv("SPINE_RUNTIME_TEST_BINARY"), []string{"PATH=/usr/bin:/bin", "SPINE_PORT=18081", "API_KEY=example"}); err != nil {
			os.Exit(7)
		}
		return
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "main.go")
	program := `package main
import ("os"; "fmt")
func main() { if os.Getenv("API_KEY") != "example" || os.Getenv("SPINE_PORT") != "18081" || os.Getenv("SPINE_RUNTIME_TEST_HELPER") != "" || len(os.Args) != 1 { os.Exit(1) }; fmt.Print("ok") }`
	if err := os.WriteFile(source, []byte(program), 0600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "app")
	build := exec.Command("go", "build", "-o", binary, source)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build dummy executable: %v %s", err, output)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestRuntimeExecReplacesProcess$")
	command.Env = append(os.Environ(), "SPINE_RUNTIME_TEST_HELPER=1", "SPINE_RUNTIME_TEST_BINARY="+binary)
	output, err := command.CombinedOutput()
	if err != nil || string(output) != "ok" {
		t.Fatalf("runtime exec failed: %v", err)
	}
}
