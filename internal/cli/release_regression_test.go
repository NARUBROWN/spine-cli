package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestExternalRegistrationNeverReportsACompleteContract(t *testing.T) {
	for _, layout := range []string{"legacy", layeredLayout} {
		t.Run(layout, func(t *testing.T) {
			inTempDir(t)
			var out, errOut bytes.Buffer
			if code := runNew([]string{"todo", "--module", "example.com/todo", "--layout", layout, "--channel", "preview", "--skip-install"}, &out, &errOut); code != 0 {
				t.Fatal(errOut.String())
			}
			if err := os.Chdir("todo"); err != nil {
				t.Fatal(err)
			}
			source, _ := os.ReadFile("internal/application/application.go")
			source = bytes.Replace(source, []byte("import ("), []byte("import (\nplugin \"example.net/plugin\""), 1)
			source = bytes.Replace(source, []byte("app := spine.New(echoplatform.Option())"), []byte("app := spine.New(echoplatform.Option())\nif err := plugin.Register(app); err != nil { return nil, boot.Options{}, err }"), 1)
			writeE2EFile(t, "internal/application/application.go", string(source))
			contract, err := inspectApplication()
			if err != nil {
				t.Fatal(err)
			}
			if contract.Complete || !contractHasErrors(contract) {
				t.Fatalf("external registration was silently accepted: %#v", contract)
			}
			out.Reset()
			errOut.Reset()
			if code := Run([]string{"contract", "check", "--json"}, &out, &errOut); code != 5 || !strings.Contains(out.String(), `"analysis_complete": false`) {
				t.Fatalf("contract gate accepted external registration: %d %s", code, out.String())
			}
		})
	}
}

func TestExecutionCommandsValidateArgumentsBeforeRunning(t *testing.T) {
	inTempDir(t)
	for _, command := range []string{"run", "test", "check", "build"} {
		for _, help := range []string{"--help", "-h"} {
			var out, errOut bytes.Buffer
			if code := Run([]string{command, help}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "Usage:") {
				t.Errorf("%s %s did not return help: %d %s %s", command, help, code, out.String(), errOut.String())
			}
		}
		var out, errOut bytes.Buffer
		if code := Run([]string{command, "--definitely-invalid"}, &out, &errOut); code == 0 || !strings.Contains(errOut.String(), "unknown option") {
			t.Errorf("%s ignored an invalid option: %d %s", command, code, errOut.String())
		}
	}
	var out, errOut bytes.Buffer
	if code := Run([]string{"test", "-run", "TestDoesNotExist"}, &out, &errOut); code == 0 || !strings.Contains(errOut.String(), "unknown option") {
		t.Fatalf("test silently ignored its filter: %d %s", code, errOut.String())
	}
}
