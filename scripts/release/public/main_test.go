package main

import (
	"encoding/json"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestModuleGraphAndDirectRequirements(t *testing.T) {
	graph, err := moduleGraph("{\"Path\":\"example.com/a\",\"Version\":\"v1.0.0\"}\n{\"Path\":\"example.com/b\",\"Version\":\"v2.0.0\",\"Indirect\":true}")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"example.com/a": "v1.0.0"}
	if got := requiredModules(graph, true); !reflect.DeepEqual(got, want) {
		t.Fatal(got)
	}
	if err := assertModules(graph, want); err != nil {
		t.Fatal(err)
	}
	if err := assertModules(graph, map[string]string{"example.com/a": "v1.0.1"}); err == nil {
		t.Fatal("wrong version accepted")
	}
	graph[1].Replace = json.RawMessage(`{"Dir":"../local"}`)
	if err := assertModules(graph, want); err == nil {
		t.Fatal("replacement accepted")
	}
}

func TestEnvelopeRejectsFailedResultAndWrongSchema(t *testing.T) {
	for _, payload := range []string{`{"ok":false,"schema_version":"spine.cli-result/v1"}`, `{"ok":true,"schema_version":"wrong"}`} {
		r := runner{override: func(string, time.Duration, ...string) (string, error) { return payload, nil }}
		if _, err := r.envelope("spine", "schema"); err == nil {
			t.Fatal(payload)
		}
	}
}

func TestTagInstallReportChecksBothEntrypoints(t *testing.T) {
	const version = "v0.4.5-preview.3"
	var installed []string
	r := runner{override: func(_ string, _ time.Duration, args ...string) (string, error) {
		switch {
		case args[0] == "go" && args[1] == "list":
			return `{"Version":"` + version + `"}`, nil
		case args[0] == "go" && args[1] == "install":
			installed = append(installed, args[2])
			return "", nil
		case args[0] == "go" && len(args) == 2:
			return "go version go1.26.0", nil
		case args[0] == "go" && args[2] == "-m":
			return "mod " + module + " " + version, nil
		case len(args) == 2 && args[1] == "version":
			return "spine-cli " + strings.TrimPrefix(version, "v"), nil
		default:
			t.Fatalf("unexpected command %v", args)
			return "", nil
		}
	}}
	p := plan{SHA: strings.Repeat("a", 40), Version: version, Scope: "tag-install", Source: json.RawMessage(`{"source_sha":"abc"}`), Components: json.RawMessage(`{"recipe":{}}`)}
	report, err := r.verify(p, version)
	if err != nil || report["scope"] != "tag-install" || !reflect.DeepEqual(installed, []string{module + "/cmd/spine@" + version, module + "@" + version}) {
		t.Fatalf("%v %v %v", report, installed, err)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"result": "passed", "source_sha": p.SHA, "query": version, "resolved_cli_version": version,
		"components": p.Components, "os": runtime.GOOS, "go_version": "go version go1.26.0",
		"entrypoints": []string{"spine", "spine-cli"}, "checks": []string{"public installation and build provenance"}, "scope": "tag-install",
	}
	expected, err := json.Marshal(want)
	if err != nil || string(encoded) != string(expected) {
		t.Fatalf("report %s, want %s, error %v", encoded, expected, err)
	}

	p.Source = nil
	if _, err := r.verify(p, version); err == nil {
		t.Fatal("missing promotion evidence accepted")
	}
}

func TestExpectedDependenciesUseSelectedPreviewCommit(t *testing.T) {
	echo := platformPrefix + "echo-platform"
	nethttp := platformPrefix + "nethttp-platform"
	const selected = "v0.2.1-0.20261114000000-aaaaaaaaaaaa"
	p := plan{Upstream: map[string]json.RawMessage{
		echo:    json.RawMessage(`{"version":"v0.2.0-preview.1","selected_version":"` + selected + `","source_sha":"abc"}`),
		nethttp: json.RawMessage(`{"version":"v0.1.0-preview.1"}`),
	}}
	rec := recipe{Modules: map[string]string{echo: "v0.2.0-preview.1"}}
	allowed, err := expectedDependencies(map[string]string{echo: selected}, rec, p, "echo")
	if err != nil || allowed[echo] != selected {
		t.Fatalf("selected Preview commit: %v %v", allowed, err)
	}
	for _, wrong := range []string{"v0.2.0-preview.1", "v0.2.1-0.20261114000000-bbbbbbbbbbbb"} {
		if _, err := expectedDependencies(map[string]string{echo: wrong}, rec, p, ""); err == nil {
			t.Fatalf("wrong version accepted: %s", wrong)
		}
	}
	if _, err := expectedDependencies(map[string]string{}, rec, p, "echo"); err == nil {
		t.Fatal("incomplete platform combination accepted")
	}
}

func TestFullPublicReportPreservesCompositionMatrix(t *testing.T) {
	const version = "v0.4.5-rc.1"
	for _, full := range []bool{false, true} {
		p := plan{Version: version, SHA: strings.Repeat("a", 40), Components: json.RawMessage(`{"recipe":{"id":"fixed"}}`)}
		if full {
			p.Upstream = map[string]json.RawMessage{"github.com/NARUBROWN/spine": json.RawMessage(`{"version":"v0.7.5-preview.1"}`)}
			p.RC = map[string]string{"github.com/NARUBROWN/spine": "v0.7.5-rc.1"}
		}
		r := runner{override: func(_ string, _ time.Duration, args ...string) (string, error) {
			switch {
			case args[0] == "go" && args[1] == "list":
				return `{"Version":"` + version + `"}`, nil
			case args[0] == "go" && args[1] == "install":
				return "", nil
			case args[0] == "go" && len(args) == 2:
				return "go version fixture", nil
			case args[0] == "go" && args[2] == "-m":
				return "mod " + module + " " + version, nil
			case args[1] == "version":
				return "spine-cli " + strings.TrimPrefix(version, "v"), nil
			case args[1] == "schema" || args[1] == "capabilities":
				return `{"schema_version":"spine.cli-result/v1","ok":true,"result":{}}`, nil
			default:
				t.Fatalf("unexpected command %v", args)
				return "", nil
			}
		}}
		var calls []string
		got, err := r.verifyWithComposition(p, version, func(_ string, layout, provider string, rc bool, rec recipe, _ plan) error {
			if rec.ID != "fixed" {
				t.Fatal(rec)
			}
			if rc {
				layout = "rc-" + layout
			}
			calls = append(calls, layout+":"+provider)
			return nil
		})
		wantCalls := []string{"legacy:", "layered-v1:"}
		want := map[string]any{
			"result": "passed", "source_sha": p.SHA, "query": version, "resolved_cli_version": version,
			"components": p.Components, "os": runtime.GOOS, "go_version": "go version fixture", "entrypoints": []string{"spine", "spine-cli"},
			"checks": []string{"public installation and build provenance", "two layouts and default HTTP composition", "default recipe and selected public modules",
				"dry-run/if-match and source-preserving update", "static contracts and generated tests", "HTTP health and listener release"},
		}
		if full {
			wantCalls = []string{"legacy:", "legacy:echo", "legacy:nethttp", "layered-v1:", "layered-v1:echo", "layered-v1:nethttp",
				"rc-legacy:echo", "rc-legacy:nethttp", "rc-layered-v1:echo", "rc-layered-v1:nethttp"}
			want["upstream_evidence"], want["rc_cohort"] = p.Upstream, p.RC
			want["combination_checks"] = []string{"echo-full", "nethttp-full"}
			want["rc_combination_checks"] = []string{"echo-full", "nethttp-full"}
		}
		if err != nil || !reflect.DeepEqual(got, want) || !reflect.DeepEqual(calls, wantCalls) {
			t.Fatalf("full=%t: report %v, checks %v, error %v", full, got, calls, err)
		}
	}
}
