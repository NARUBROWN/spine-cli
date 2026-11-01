package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestGenerationHelpDoesNotReadOrChangeAProject(t *testing.T) {
	inTempDir(t)
	invalidConfig := []byte("this is not a valid spine config\n")
	writeE2EFile(t, "spine.toml", string(invalidConfig))
	for _, command := range []string{"g", "generate"} {
		for _, kind := range []string{"", "domain", "dto", "repository", "service", "controller", "resource", "route", "consumer", "websocket", "slice"} {
			for _, flag := range []string{"--help", "-h"} {
				for _, named := range []bool{false, true} {
					if kind == "" && named {
						continue
					}
					args := []string{command}
					if kind != "" {
						args = append(args, kind)
					}
					if named {
						args = append(args, "example")
					}
					args = append(args, flag)
					var out, errOut bytes.Buffer
					if code := Run(args, &out, &errOut); code != 0 || errOut.Len() != 0 || !strings.Contains(out.String(), "Usage:") {
						t.Fatalf("help %v: code=%d, stdout=%s, stderr=%s", args, code, out.String(), errOut.String())
					}
					if kind == "route" && (!strings.Contains(out.String(), "--handler") || !strings.Contains(out.String(), "--method")) {
						t.Fatal("route help omitted required options")
					}
				}
			}
		}
	}
	for _, args := range [][]string{{"new", "--help"}, {"new", "example", "-h"}, {"g", "route", "--help", "--json"}, {"g", "route", "--help", "--root", "missing-project"}} {
		var out, errOut bytes.Buffer
		if code := Run(args, &out, &errOut); code != 0 || !strings.Contains(out.String(), "Usage:") {
			t.Fatalf("help %v: %d %s %s", args, code, out.String(), errOut.String())
		}
	}
	after, _ := os.ReadFile("spine.toml")
	files, _ := os.ReadDir(".")
	if !bytes.Equal(after, invalidConfig) || len(files) != 1 {
		t.Fatal("help changed the project")
	}
}

func TestGenerationHelpDoesNotConsumeOptionValues(t *testing.T) {
	for _, args := range [][]string{{"route", "sample", "--handler", "--help"}, {"dto", "sample", "--field", "-h"}} {
		var out, errOut bytes.Buffer
		if handled, _ := generationHelp("generate", args, &out, &errOut); handled {
			t.Fatalf("option value was interpreted as help: %v", args)
		}
	}
}

func dryRunTextHash(t *testing.T, text string) string {
	t.Helper()
	match := regexp.MustCompile(`(?m)^Base hash: ([0-9a-f]{64})$`).FindStringSubmatch(text)
	if len(match) != 2 || !strings.Contains(text, "--if-match "+match[1]) {
		t.Fatalf("dry-run omitted a usable apply hash: %s", text)
	}
	return match[1]
}

func TestTextDryRunHashAppliesAndRejectsStalePlans(t *testing.T) {
	for _, layout := range []string{layeredLayout, "legacy"} {
		t.Run(layout, func(t *testing.T) {
			inTempDir(t)
			args := []string{"new", "todo", "--module", "example.com/todo", "--layout", layout}
			var out, errOut bytes.Buffer
			if code := Run(append(append([]string{}, args...), "--dry-run"), &out, &errOut); code != 0 {
				t.Fatal(errOut.String())
			}
			hash := dryRunTextHash(t, out.String())
			if _, err := os.Stat("todo"); !os.IsNotExist(err) {
				t.Fatal("new dry-run created the project")
			}
			out.Reset()
			if code := Run(append(append([]string{}, args...), "--if-match", hash), &out, &errOut); code != 0 {
				t.Fatal(errOut.String())
			}
			if err := os.Chdir("todo"); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"billing", "stale"} {
				args = []string{"g", "service", name}
				before, _ := os.ReadFile("internal/application/application.go")
				out.Reset()
				if code := Run(append(append([]string{}, args...), "--dry-run"), &out, &errOut); code != 0 {
					t.Fatal(errOut.String())
				}
				hash = dryRunTextHash(t, out.String())
				after, _ := os.ReadFile("internal/application/application.go")
				if !bytes.Equal(before, after) {
					t.Fatal("component dry-run changed composition")
				}
				out.Reset()
				if code := Run(append(append([]string{}, args...), "--dry-run", "--json"), &out, &errOut); code != 0 {
					t.Fatal(errOut.String())
				}
				var result struct {
					Result struct {
						BaseHash string `json:"base_hash"`
					} `json:"result"`
				}
				if err := json.Unmarshal(out.Bytes(), &result); err != nil || result.Result.BaseHash != hash {
					t.Fatalf("plain/JSON plan mismatch: %v %s", err, out.String())
				}
				if name == "stale" {
					writeE2EFile(t, "internal/application/application.go", string(before)+"\n// 계획 이후 소스를 변경합니다.\n")
				}
				out.Reset()
				errOut.Reset()
				code := Run(append(append([]string{}, args...), "--if-match", hash), &out, &errOut)
				if name == "billing" && code != 0 {
					t.Fatalf("text plan did not apply: %s", errOut.String())
				}
				if name == "stale" {
					if code == 0 || !strings.Contains(errOut.String(), "changed since dry-run") {
						t.Fatalf("stale text plan was accepted: %d %s", code, errOut.String())
					}
					path := filepath.Join("src", "stale_service.go")
					if layout == layeredLayout {
						path = filepath.Join("src", "service", "stale_service.go")
					}
					if _, err := os.Stat(path); !os.IsNotExist(err) {
						t.Fatal("stale plan created a service")
					}
				}
			}
		})
	}
}
