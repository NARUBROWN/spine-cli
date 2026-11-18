package cli

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// 브로커 실행 없이 생성기의 각 독립 경로를 공개 모듈로 빌드하고 설정 테스트를 실행한다.
func TestGeneratedAssemblySmoke(t *testing.T) {
	binary := buildUpdateCLI(t)
	for _, tc := range []struct {
		layout, name string
		args         []string
	}{
		{layeredLayout, "echo", nil},
		{"legacy", "echo", nil},
		{layeredLayout, "nethttp-validator-websocket", []string{"--platform", "nethttp", "--validator", "playground", "--websocket-platform", "gorilla"}},
		{layeredLayout, "fasthttp-validator", []string{"--platform", "fasthttp", "--validator", "playground", "--channel", "preview"}},
		{layeredLayout, "nats-worker", []string{"--platform", "none", "--event-platform", "events:nats"}},
		{layeredLayout, "multiple-events", []string{"--event-platform", "primary:kafka", "--event-platform", "secondary:kafka", "--event-platform", "jobs:rabbitmq"}},
	} {
		t.Run(tc.layout+"/"+tc.name, func(t *testing.T) {
			workspace := t.TempDir()
			args := append([]string{"new", "sample", "--module", "example.com/sample", "--layout", tc.layout}, tc.args...)
			applyAssemblyPlan(t, binary, workspace, args...)
			project := filepath.Join(workspace, "sample")
			// new가 정적 계약과 진입점을 검사한다. 여기서는 생성된 설정 및 앱 테스트를 한 번 실행한다.
			generatedGo(t, project, "test", "./...", "-count=1", "-timeout=90s")
		})
	}
}

// 두 layout의 코드 생성 분기는 설치와 런타임 조합을 늘리지 않고 검사한다.
func TestAssemblyScaffoldMatrix(t *testing.T) {
	for _, layout := range []string{layeredLayout, "legacy"} {
		for _, tc := range []struct {
			name string
			args []string
			want []string
		}{
			{"echo", nil, []string{"echoplatform.Option()"}},
			{"nethttp", []string{"--platform", "nethttp"}, []string{"nethttpplatform.Option()"}},
			{"fasthttp", []string{"--platform", "fasthttp"}, []string{"fasthttpplatform.Option()"}},
			{"nats-worker", []string{"--platform", "none", "--event-platform", "events:nats"}, []string{"natsplatform.NewFactory"}},
			{"websocket", []string{"--websocket-platform", "gorilla"}, []string{"gorillaplatform.Option()"}},
			{"validator", []string{"--validator", "playground"}, []string{"playground.New", "spine.WithValidator"}},
			{"kafka", []string{"--event-platform", "kafka"}, []string{"kafkaplatform.NewFactory"}},
			{"rabbitmq", []string{"--event-platform", "rabbitmq"}, []string{"rabbitmqplatform.NewFactory"}},
			{"worker", []string{"--platform", "none", "--event-platform", "jobs:kafka"}, []string{`spine.WithEventAdapter("jobs"`}},
			{"multiple", []string{"--event-platform", "first:kafka", "--event-platform", "second:kafka", "--event-platform", "jobs:rabbitmq"}, []string{`spine.WithEventAdapter("first"`, `spine.WithEventAdapter("second"`, `spine.WithEventAdapter("jobs"`}},
		} {
			t.Run(layout+"/"+tc.name, func(t *testing.T) {
				options, err := parseNewOptions(append([]string{"sample", "--module", "example.com/sample", "--layout", layout}, tc.args...))
				if err != nil {
					t.Fatal(err)
				}
				if options.Platform == "fasthttp" {
					options.HTTPDescriptor = &platformDescriptor{SchemaVersion: platformDescriptorSchema, Kind: "http", Provider: "fasthttp", ContractVersion: "spine.http-adapter/v3", Package: "platform", Factory: "Option", MinimumGo: "1.26.0"}
				}
				for i := range options.Events {
					if options.Events[i].Provider == "nats" {
						options.Events[i].Descriptor, err = testNATSDescriptor()
						if err != nil {
							t.Fatal(err)
						}
					}
				}
				files, err := newProjectFiles(options)
				if err != nil {
					t.Fatal(err)
				}
				for _, want := range tc.want {
					if !strings.Contains(files["internal/application/application.go"], want) {
						t.Fatalf("구성 누락 %s:\n%s", want, files["internal/application/application.go"])
					}
				}
				for path, source := range files {
					if strings.HasSuffix(path, ".go") {
						if _, err := parser.ParseFile(token.NewFileSet(), path, source, parser.AllErrors); err != nil {
							t.Fatal(err)
						}
					}
				}
				for _, selected := range currentAssemblyModules(options) {
					if strings.HasPrefix(selected.Path, "github.com/NARUBROWN/") && !strings.Contains(files["go.mod"], selected.Path) {
						t.Fatalf("모듈 누락: %s", selected.Path)
					}
				}
				if strings.Contains(files["go.mod"], "replace ") {
					t.Fatal("공개 생성 모듈에 replace가 있습니다")
				}
			})
		}
	}
}
