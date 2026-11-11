package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSelectedModulesResolvesRelativeReplacementsFromOriginalProject(t *testing.T) {
	for _, mode := range []string{"current-directory", "relative-directory", "absolute-directory"} {
		t.Run(mode, func(t *testing.T) {
			base := t.TempDir()
			project := filepath.Join(base, "project")
			for _, directory := range []string{project, filepath.Join(project, "core"), filepath.Join(base, "shared")} {
				if err := os.MkdirAll(directory, 0755); err != nil {
					t.Fatal(err)
				}
			}
			t.Chdir(project)
			t.Setenv("GOWORK", "off")
			t.Setenv("GOFLAGS", "")
			module := []byte(`module example.com/body

go 1.25.5

require (
 github.com/NARUBROWN/spine v0.7.5-preview.1
 example.com/shared v0.0.0
)

replace github.com/NARUBROWN/spine => ./core
replace example.com/shared v0.0.0 => ../shared
`)
			writeE2EFile(t, "go.mod", string(module))
			writeE2EFile(t, "core/go.mod", "module github.com/NARUBROWN/spine\n\ngo 1.25.5\n")
			writeE2EFile(t, "../shared/go.mod", "module example.com/shared\n\ngo 1.25.5\n")
			root := "."
			if mode != "current-directory" {
				t.Chdir(base)
				root = "project"
				if mode == "absolute-directory" {
					root = project
				}
			}
			modules, err := selectedModules(context.Background(), root, true)
			if err != nil {
				t.Fatalf("valid local replacements were rejected: %v", err)
			}
			if len(modules) != 1 || modules[0].Path != spineImportPath || modules[0].Version != "v0.7.5-preview.1" || modules[0].Replace == nil || modules[0].Dir != filepath.Join(project, "core") || modules[0].Replace.Path != filepath.Join(project, "core") {
				t.Fatalf("replacement resolved against the temporary module instead of the project: %+v", modules)
			}
			after, err := os.ReadFile(filepath.Join(project, "go.mod"))
			if err != nil || !bytes.Equal(after, module) {
				t.Fatalf("module inspection changed the project: %v", err)
			}
			if _, err := os.Stat(filepath.Join(project, "go.sum")); !os.IsNotExist(err) {
				t.Fatalf("offline module inspection wrote go.sum: %v", err)
			}
		})
	}
}
