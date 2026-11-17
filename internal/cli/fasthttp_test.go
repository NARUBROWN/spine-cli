package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestFastHTTPWizardSelection(t *testing.T) {
	for _, choice := range []string{"3", "fasthttp"} {
		t.Run(choice, func(t *testing.T) {
			options, err := parseNewOptions([]string{"fast-api"})
			if err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			options, err = promptNewProjectOptions([]string{"fast-api"}, options, strings.NewReader("\n"+choice+"\n\n\n"), &output)
			if err != nil {
				t.Fatal(err)
			}
			if options.Platform != "fasthttp" || options.WebSocketPlatform != "none" || options.Frontend != "none" {
				t.Fatalf("wizard selection: %+v", options)
			}
			if !strings.Contains(output.String(), "  3. fasthttp\n") || strings.Contains(output.String(), "Gorilla WebSocket") || strings.Contains(output.String(), "Enable a frontend?") {
				t.Fatalf("missing numbered choice or unsupported integrations offered: %s", output.String())
			}
		})
	}
}

func TestFastHTTPRejectsUnsupportedIntegrationsBeforeLookup(t *testing.T) {
	for _, args := range [][]string{{"--websocket-platform", "gorilla"}, {"--websocket-platform", "custom"}, {"--frontend", "react"}} {
		if _, err := parseNewOptions(append([]string{"fast-api", "--platform", "fasthttp"}, args...)); err == nil {
			t.Fatalf("unsupported integration accepted: %v", args)
		}
	}
}

func TestFastHTTPMetadataControlsGeneration(t *testing.T) {
	root := t.TempDir()
	descriptor := &platformDescriptor{
		SchemaVersion: platformDescriptorSchema, Kind: "http", Provider: "fasthttp",
		ContractVersion: "spine.http-adapter/v3", Package: "platform", Factory: "Option", MinimumGo: "1.26.0",
	}
	writeTestDescriptor(t, root, descriptor)
	descriptor, err := resolvePlatformDescriptor(context.Background(), "http", "fasthttp", selectedModule{Path: providerModule("fasthttp"), Version: "v0.1.0-preview.2", Dir: root}, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, layout := range []string{layeredLayout, "legacy"} {
		options, err := parseNewOptions([]string{"fast-api", "--platform", "fasthttp", "--layout", layout})
		if err != nil {
			t.Fatal(err)
		}
		options.HTTPDescriptor = descriptor
		options.Modules = []selectedModule{{Path: spineImportPath, Version: "v0.7.6-preview.1"}, {Path: providerModule("fasthttp"), Version: "v0.1.0-preview.2"}}
		files, err := newProjectFiles(options)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(files["go.mod"], "go 1.26.0") || !strings.Contains(files["go.mod"], providerModule("fasthttp")+" v0.1.0-preview.2") {
			t.Fatalf("minimum Go and platform pin: %s", files["go.mod"])
		}
		if !strings.Contains(files["internal/application/application.go"], "fasthttpplatform.Option()") || !strings.Contains(files["spine.toml"], `platform = "fasthttp"`) {
			t.Fatalf("fasthttp composition/config missing")
		}
		if selectedModuleVersion(currentAssemblyModules(options), providerModule("fasthttp")) != "preview" {
			t.Fatal("fasthttp does not follow the selected dependency channel")
		}
	}
}
