package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBrokerPublishFixtureUsesResolvedContract(t *testing.T) {
	for _, tc := range []struct {
		name, version, contract, call string
		di                            bool
	}{
		{"typed release", "v0.7.5-preview.1", `package publish
import("context";"time")
type DomainEvent interface{Name()string;OccurredAt()time.Time}
func Event(context.Context,...DomainEvent){}
`, "publish.Event(ctx,brokerEvent", false},
		{"queued fields release", "v0.7.6-preview.1", `package publish
import "context"
type Fields map[string]any
func Event(context.Context,string,Fields)error{return nil}
`, "publish.Event(ctx,c.input,publish.Fields", false},
		{"DI source with same preview version", "v0.7.6-preview.1", synchronousPublisherContract, "c.publisher.Event(ctx,c.input,publish.Fields", true},
		{"DI source replacing older release", "v0.7.5-preview.1", synchronousPublisherContract, "c.publisher.Event(ctx,c.input,publish.Fields", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, "core/pkg/event/publish"), 0755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("GOWORK", "off")
			writeE2EFile(t, filepath.Join(root, "go.mod"), "module example.com/publisher-fixture\n\ngo 1.25.5\nrequire github.com/NARUBROWN/spine "+tc.version+"\nreplace github.com/NARUBROWN/spine => ./core\n")
			writeE2EFile(t, filepath.Join(root, "core/go.mod"), "module github.com/NARUBROWN/spine\n\ngo 1.25.5\n")
			writeE2EFile(t, filepath.Join(root, "core/pkg/event/publish/publish.go"), tc.contract)
			if tc.name == "DI source with same preview version" {
				// Fixture compilation disables the CLI process's workspace. API
				// selection must use that same project module and replacement.
				workspace := t.TempDir()
				if err := os.MkdirAll(filepath.Join(workspace, "old-core/pkg/event/publish"), 0755); err != nil {
					t.Fatal(err)
				}
				writeE2EFile(t, filepath.Join(workspace, "old-core/go.mod"), "module github.com/NARUBROWN/spine\n\ngo 1.25.5\n")
				writeE2EFile(t, filepath.Join(workspace, "old-core/pkg/event/publish/publish.go"), "package publish\ntype Fields map[string]any\n")
				writeE2EFile(t, filepath.Join(workspace, "go.work"), "go 1.25.5\nuse ./old-core\n")
				t.Setenv("GOWORK", filepath.Join(workspace, "go.work"))
			}
			for _, file := range []string{"assembly_broker_test.go.txt", "assembly_nats_test.go.txt"} {
				source, err := os.ReadFile(filepath.Join("testdata", file))
				if err != nil {
					t.Fatal(err)
				}
				got := brokerPublishFixture(t, root, string(source))
				if strings.Contains(got, "publish.Publisher") != tc.di || strings.Contains(got, "c.publisher.Event") != tc.di {
					t.Fatalf("wrong dependency contract in %s: %s", file, got)
				}
				if !tc.di && strings.Contains(got, "controller.publisher") {
					t.Fatal("legacy fixture retained publisher injection")
				}
				if file == "assembly_broker_test.go.txt" && !strings.Contains(got, tc.call) {
					t.Fatalf("missing %s", tc.call)
				}
				if tc.version == "v0.7.5-preview.1" && !tc.di && strings.Contains(got, "publish.Fields") {
					t.Fatal("legacy typed fixture retained Fields")
				}
			}
		})
	}
}
