package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGeneratorRecognizesInstalledApplicationProviders(t *testing.T) {
	for _, tc := range []struct {
		name, sender string
		want         bool
	}{
		{"legacy sender", `package ws;type Sender interface{Send(int,[]byte)error}`, false},
		{"application sender", `package ws;import "context";type Sender interface{Send(context.Context,string,int,[]byte)error}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inTempDir(t)
			core := filepath.Join(t.TempDir(), "core")
			if err := os.MkdirAll(filepath.Join(core, "pkg/ws"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(core, "pkg/event/publish"), 0755); err != nil {
				t.Fatal(err)
			}
			writeE2EFile(t, filepath.Join(core, "go.mod"), "module "+spineImportPath+"\ngo 1.25.0\n")
			writeE2EFile(t, filepath.Join(core, "pkg/ws/sender.go"), tc.sender)
			writeE2EFile(t, filepath.Join(core, "pkg/event/publish/publish.go"), synchronousPublisherContract)
			writeE2EFile(t, "go.mod", "module example.com/app\ngo 1.25.0\nrequire "+spineImportPath+" v0.7.6-preview.1\nreplace "+spineImportPath+" => "+filepath.ToSlash(core)+"\n")
			wsKey := spineImportPath + "/pkg/ws:Sender"
			publisherKey := eventPublishImportPath + ":Publisher"
			got := installedApplicationProviders(map[string]bool{wsKey: true, publisherKey: true, "example.com/fake:Sender": true})
			if got[wsKey] != tc.want || !got[publisherKey] || got["example.com/fake:Sender"] {
				t.Fatal(got)
			}
		})
	}
}
