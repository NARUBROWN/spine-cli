package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"
)

// 기존 공개 채널과 Preview 모두에서 같은 브로커 동작을 검증합니다.
func brokerPublishFixture(t *testing.T, root, source string) string {
	t.Helper()
	path := filepath.Join(root, "go.mod")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	file, err := modfile.Parse(path, data, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, req := range file.Require {
		if req.Mod.Path == spineImportPath && semver.Compare(req.Mod.Version, "v0.7.6-preview.1") >= 0 {
			return strings.NewReplacer(
				"publish.Event(ctx,brokerEvent{brokerDTO:*input,topic:c.input})", "publish.Event(ctx,c.input,publish.Fields{\"email\":input.Email,\"age\":input.Age})",
				"publish.Event(ctx,brokerEvent{brokerDTO:input,topic:c.input})", "publish.Event(ctx,c.input,publish.Fields{\"email\":input.Email,\"age\":input.Age})",
				"publish.Event(ctx,brokerEvent{brokerDTO:input,topic:c.output})", "publish.Event(ctx,c.output,publish.Fields{\"email\":input.Email,\"age\":input.Age})",
				"publish.Event(ctx,natsEvent{natsDTO:*input,topic:\"order.created\"})", "publish.Event(ctx,\"order.created\",publish.Fields{\"email\":input.Email,\"age\":input.Age})",
				"publish.Event(ctx,natsEvent{natsDTO:input,topic:\"order.completed\"})", "publish.Event(ctx,\"order.completed\",publish.Fields{\"email\":input.Email,\"age\":input.Age})",
			).Replace(source)
		}
	}
	return source
}
