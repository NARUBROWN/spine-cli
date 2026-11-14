package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSelection(t *testing.T) {
	names := []string{"TestCoreBehavior"}
	for name := range optional {
		names = append(names, name)
	}
	got, err := selection(strings.Join(names, "\n"))
	if err != nil || got != "^(TestCoreBehavior)$" {
		t.Fatalf("selection = %q, %v", got, err)
	}
	if _, err := selection("TestCoreBehavior"); err == nil {
		t.Fatal("missing optional tests were accepted")
	}
}

func TestCheck(t *testing.T) {
	for _, core := range []bool{false, true} {
		var evidence strings.Builder
		for name := range required {
			top, _, _ := strings.Cut(name, "/")
			if _, excluded := optional[top]; core && excluded {
				continue
			}
			if err := json.NewEncoder(&evidence).Encode(map[string]string{"Action": "pass", "Test": name}); err != nil {
				t.Fatal(err)
			}
		}
		log := evidence.String()
		if err := check(strings.NewReader(log+"{\"Action\":\"skip\",\"Package\":\"empty\"}\n"), core); err != nil {
			t.Fatal(err)
		}
		if core && check(strings.NewReader(log), false) == nil {
			t.Fatal("core-only evidence accepted as full evidence")
		}
		start := strings.IndexByte(log, '\n') + 1
		if check(strings.NewReader(log[start:]), core) == nil {
			t.Fatal("missing test accepted")
		}
		if check(strings.NewReader(log+"{\"Action\":\"fail\",\"Package\":\"broken\"}\n"), core) == nil {
			t.Fatal("package failure accepted")
		}
		if !core && check(strings.NewReader(log+"{\"Action\":\"skip\",\"Test\":\"TestGeneratedAssemblyBrokerE2E/legacy\"}\n"), core) == nil {
			t.Fatal("skipped subtest accepted")
		}
	}
}
