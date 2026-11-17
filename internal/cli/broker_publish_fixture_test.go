package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"
	"golang.org/x/tools/go/packages"
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
	// Inspect the resolved package so local source replacements with an existing
	// preview version use the actual DI contract, not the published version's API.
	loaded, err := packages.Load(&packages.Config{Dir: root, Env: mergeE2EEnvironment([]string{"GOWORK=off", "GOTOOLCHAIN=local"}), Mode: packages.NeedName | packages.NeedTypes | packages.NeedImports | packages.NeedDeps, BuildFlags: []string{"-mod=readonly"}}, eventPublishImportPath)
	if err != nil || len(loaded) != 1 || loaded[0].Types == nil || len(loaded[0].Errors) != 0 {
		t.Fatalf("load publisher fixture contract: %v %+v", err, loaded)
	}
	if applicationPublisherType(map[string]*packages.Package{eventPublishImportPath: loaded[0]}) != nil {
		return source
	}
	// Earlier supported cores either queue JSON fields or accept typed events.
	fieldsAPI := false
	for _, req := range file.Require {
		if req.Mod.Path == spineImportPath && semver.Compare(req.Mod.Version, "v0.7.6-preview.1") >= 0 {
			fieldsAPI = true
		}
	}
	source = strings.ReplaceAll(source, "publisher publish.Publisher;", "")
	source = strings.ReplaceAll(source, "func(publisher publish.Publisher)*brokerController{controller.publisher=publisher;return controller}", "func()*brokerController{return controller}")
	source = strings.ReplaceAll(source, "func(publisher publish.Publisher)*natsController{controller.publisher=publisher;return controller}", "func()*natsController{return controller}")
	if fieldsAPI {
		return strings.ReplaceAll(source, "c.publisher.Event(", "publish.Event(")
	}
	for _, replacement := range []struct{ call, event string }{
		{`c.publisher.Event(ctx,c.input,publish.Fields{"email":input.Email,"age":input.Age})`, `brokerEvent{brokerDTO:*input,topic:c.input}`},
		{`c.publisher.Event(ctx,c.output,publish.Fields{"email":input.Email,"age":input.Age})`, `brokerEvent{brokerDTO:input,topic:c.output}`},
		{`c.publisher.Event(ctx,"order.created",publish.Fields{"email":input.Email,"age":input.Age})`, `natsEvent{natsDTO:*input,topic:"order.created"}`},
		{`c.publisher.Event(ctx,"order.completed",publish.Fields{"email":input.Email,"age":input.Age})`, `natsEvent{natsDTO:input,topic:"order.completed"}`},
	} {
		source = strings.ReplaceAll(source, "if err:="+replacement.call+";err!=nil{return httpx.Response[string]{},err}", "publish.Event(ctx,"+replacement.event+")")
		event := strings.ReplaceAll(replacement.event, "brokerDTO:*input", "brokerDTO:input")
		source = strings.ReplaceAll(source, "if err:="+replacement.call+";err!=nil{return err}", "publish.Event(ctx,"+event+")")
	}
	return source
}
