package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAssemblyArgumentsAndPlanIsolation(t *testing.T) {
	inTempDir(t)
	t.Setenv("PATH", t.TempDir())
	for _, options := range [][]string{
		{"--platform", "none"}, {"--event-platform", "unknown"}, {"--event-platform", "events:kafka", "--event-platform", "events:rabbitmq"},
		{"--event-platform", "bad-name:kafka"}, {"--validator", "unknown"}, {"--event-platform"}, {"--validator"},
	} {
		var out, errOut bytes.Buffer
		if code := Run(append([]string{"new", "sample", "--skip-install", "--json"}, options...), &out, &errOut); code != 2 {
			t.Fatalf("%v: %d %s %s", options, code, out.String(), errOut.String())
		}
	}
	for _, args := range [][]string{
		{"new", "sample", "--skip-install", "--event-platform", "kafka", "--validator", "playground"},
		{"new", "sample", "--skip-install", "--platform", "none", "--event-platform", "rabbitmq"},
		{"new", "sample", "--skip-install", "--platform", "none", "--event-platform", "events:nats"},
	} {
		var out, errOut bytes.Buffer
		if code := Run(append(append([]string{}, args...), "--dry-run", "--json"), &out, &errOut); code != 0 {
			t.Fatalf("resolved scaffold plan: %d %s", code, errOut.String())
		}
		var plan struct {
			Schema string `json:"schema_version"`
			OK     bool   `json:"ok"`
			Result struct {
				Hash, Diff string
				BaseHash   string `json:"base_hash"`
			} `json:"result"`
		}
		if err := json.Unmarshal(out.Bytes(), &plan); err != nil || !plan.OK || plan.Schema != protocolSchemaVersion || plan.Result.BaseHash == "" || plan.Result.Diff == "" {
			t.Fatalf("invalid plan: %v %s", err, out.String())
		}
		changed := append(append([]string{}, args...), "--validator", "none", "--event-platform", "extra:kafka", "--if-match", plan.Result.BaseHash, "--json")
		out.Reset()
		errOut.Reset()
		if code := Run(changed, &out, &errOut); code != 4 {
			t.Fatalf("cross-recipe plan reused: %d %s", code, out.String())
		}
	}
	entries, _ := os.ReadDir(".")
	if len(entries) != 0 {
		t.Fatalf("failed plans wrote destination files: %v", entries)
	}
}

func TestAssemblyRecipeRejectsMismatches(t *testing.T) {
	targets := assemblyModules("echo", []eventPlatformSpec{{Name: "events", Provider: "kafka"}}, "none")
	if err := validateRecipeModules(targets, targets); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func([]selectedModule){
		func(m []selectedModule) { m[0].Version = "v0.6.0" },
		func(m []selectedModule) { m[0].Replace = &selectedModule{Path: "../spine"} },
	} {
		actual := append([]selectedModule{}, targets...)
		mutate(actual)
		if err := validateRecipeModules(actual, targets); err == nil {
			t.Fatalf("incompatible graph accepted: %+v", actual)
		}
	}
}

func TestAssemblyStaticDiagnosticsAndSemanticDrift(t *testing.T) {
	if err := prepareTestModuleCache(); err != nil {
		t.Fatal(err)
	}
	inTempDir(t)
	var out, errOut bytes.Buffer
	if code := runNew([]string{"sample", "--module", "example.com/sample", "--skip-install", "--event-platform", "kafka", "--event-platform", "rabbitmq"}, &out, &errOut); code != 0 {
		t.Fatal(errOut.String())
	}
	if err := os.Chdir("sample"); err != nil {
		t.Fatal(err)
	}
	source, _ := os.ReadFile("internal/application/application.go")
	baseline, err := inspectApplication()
	if err != nil || !baseline.Complete || len(baseline.EventAdapters) != 2 {
		t.Fatalf("static baseline: %v %+v", err, baseline)
	}
	// 환경 변수가 잘못되어도 정적 검사에서는 factory와 설정 helper를 실행하지 않습니다.
	for _, spec := range []eventPlatformSpec{{Name: "kafka", Provider: "kafka"}, {Name: "rabbitmq", Provider: "rabbitmq"}} {
		t.Setenv(eventEnvPrefix(spec.Name)+"ALLOW_INSECURE", "invalid")
	}
	for _, scenario := range []struct{ before, after, code string }{
		{`spine.WithEventAdapter("rabbitmq", factory1)`, `spine.WithEventAdapter("kafka", factory1)`, "EVENT_ADAPTER_DUPLICATE"},
		{`spine.WithEventAdapter("kafka", factory0)`, `spine.WithEventAdapter(dynamicName(), factory0)`, "EVENT_ADAPTER_UNRESOLVED"},
		{`factory0, err := kafkaplatform.NewFactory(config0)`, `factory0, err := customFactory(config0)`, "EVENT_ADAPTER_UNRESOLVED"},
		{`spine.WithEventAdapter("kafka", factory0)`, `spine.WithEventAdapter("kafka", factory0, customOption())`, "EVENT_ADAPTER_UNRESOLVED"},
		{`app := spine.New(`, "factory0 = arbitraryFactory()\napp := spine.New(", "EVENT_ADAPTER_UNRESOLVED"},
		{`spine.WithEventAdapter("kafka", factory0)`, `spine.WithEventAdapter("kafka", factory1)`, "EVENT_PLATFORM_MISMATCH"},
	} {
		updated := strings.Replace(string(source), scenario.before, scenario.after, 1)
		if updated == string(source) {
			t.Fatalf("fixture replacement missing: %s", scenario.before)
		}
		writeE2EFile(t, "internal/application/application.go", updated)
		contract, err := inspectApplication()
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, d := range contract.Diagnostics {
			found = found || d.Code == scenario.code
		}
		if !found || contract.Complete {
			t.Fatalf("missing %s: %+v", scenario.code, contract.Diagnostics)
		}
	}
	writeE2EFile(t, "internal/application/application.go", "\n\n"+string(source))
	shifted, err := inspectApplication()
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(semanticView(baseline))
	after, _ := json.Marshal(semanticView(shifted))
	if !bytes.Equal(before, after) {
		t.Fatalf("line shifts changed assembly semantics: %s %s", before, after)
	}
	changed := baseline
	changed.EventAdapters = append([]eventAdapterSelection{}, baseline.EventAdapters...)
	changed.EventAdapters[0].Name = "other"
	other, _ := json.Marshal(semanticView(changed))
	if bytes.Equal(before, other) {
		t.Fatal("event selection did not participate in semantic snapshots")
	}
	replaced := string(source)
	start := strings.Index(replaced, "app := spine.New(")
	end := strings.Index(replaced[start:], "\n\t)") + start + len("\n\t)")
	writeE2EFile(t, "internal/application/application.go", replaced[:start]+"app := echoplatform.New()"+replaced[end:])
	missing, err := inspectApplication()
	if err != nil || missing.Complete {
		t.Fatalf("removed assembly was accepted: %v %+v", err, missing.Diagnostics)
	}
	writeE2EFile(t, "internal/application/application.go", string(source))
	config, err := loadProjectConfig()
	if err != nil || config.Recipe != currentAssemblyRecipe || len(config.Events) != 2 {
		t.Fatalf("config lost selection: %+v %v", config, err)
	}
	if _, err := os.Stat(filepath.Join("internal/application", "platform_config.go")); err != nil {
		t.Fatal(err)
	}
}
