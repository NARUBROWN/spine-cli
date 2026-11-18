package main

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

func TestSelection(t *testing.T) {
	names := []string{"TestCoreBehavior"}
	for name := range optional {
		names = append(names, name)
	}
	got, err := selectTests(strings.Join(names, "\n"), false)
	if err != nil || got != "^(TestCoreBehavior)$" {
		t.Fatalf("selection = %q, %v", got, err)
	}
	if _, err := selectTests("TestCoreBehavior", false); err == nil {
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

// Every listed CLI test must run in exactly one suite, including newly added tests.
func TestSuitePartition(t *testing.T) {
	names := []string{"TestCoreBehavior", "TestNewCoreBehavior"}
	for name := range optional {
		names = append(names, name)
	}
	output := strings.Join(names, "\n")
	core, err := selectTests(output, false)
	if err != nil {
		t.Fatal(err)
	}
	extended, err := selectTests(output, true)
	if err != nil {
		t.Fatal(err)
	}
	corePattern, extendedPattern := regexp.MustCompile(core), regexp.MustCompile(extended)
	for _, name := range names {
		if corePattern.MatchString(name) == extendedPattern.MatchString(name) {
			t.Fatalf("test must belong to exactly one suite: %s", name)
		}
		_, wantExtended := optional[name]
		if extendedPattern.MatchString(name) != wantExtended {
			t.Fatalf("wrong suite for %s", name)
		}
		if extendedPattern.MatchString(name + "Extra") {
			t.Fatalf("extended pattern also selected unrelated test %sExtra", name)
		}
	}
}

func TestExtendedEvidence(t *testing.T) {
	var evidence strings.Builder
	for name := range required {
		top, _, _ := strings.Cut(name, "/")
		if _, included := optional[top]; included {
			if err := json.NewEncoder(&evidence).Encode(map[string]string{"Action": "pass", "Test": name}); err != nil {
				t.Fatal(err)
			}
		}
	}
	log := evidence.String()
	if err := checkScope(strings.NewReader(log), false, true); err != nil {
		t.Fatal(err)
	}
	if err := check(strings.NewReader(log), false); err == nil {
		t.Fatal("extended-only evidence accepted as full evidence")
	}
	if err := checkScope(strings.NewReader(log[strings.IndexByte(log, '\n')+1:]), false, true); err == nil {
		t.Fatal("missing extended evidence accepted")
	}
	for _, event := range []string{
		`{"Action":"fail","Package":"broken"}`,
		`{"Action":"skip","Test":"TestGeneratedAssemblyBrokerE2E/legacy"}`,
	} {
		if err := checkScope(strings.NewReader(log+event+"\n"), false, true); err == nil {
			t.Fatalf("invalid extended evidence accepted: %s", event)
		}
	}
}
