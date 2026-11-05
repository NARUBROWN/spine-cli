package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestDescriptor(t *testing.T, root string, d *platformDescriptor) {
	t.Helper()
	data, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, platformDescriptorFile), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDynamicPlatformDescriptorValidation(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		mutate func(*platformDescriptor)
	}{
		{"schema", func(d *platformDescriptor) { d.SchemaVersion = "unknown" }},
		{"kind", func(d *platformDescriptor) { d.Kind = "validator" }},
		{"provider", func(d *platformDescriptor) { d.Provider = "other" }},
		{"contract", func(d *platformDescriptor) { d.ContractVersion = "spine.event-adapter/v99" }},
		{"factory expression", func(d *platformDescriptor) { d.Factory = "NewFactory(); panic(1)" }},
		{"invalid go", func(d *platformDescriptor) { d.MinimumGo = "latest" }},
		{"duplicate field", func(d *platformDescriptor) { d.Fields = append(d.Fields, d.Fields[0]) }},
		{"duplicate env", func(d *platformDescriptor) { d.Fields[1].Env = d.Fields[0].Env }},
		{"env expression", func(d *platformDescriptor) { d.Fields[0].Env = "URLS\nINJECTED" }},
		{"unknown type", func(d *platformDescriptor) { d.Fields[0].Type = "func()" }},
		{"missing example", func(d *platformDescriptor) { d.Fields[0].Example = nil }},
		{"null default", func(d *platformDescriptor) { d.Fields[0].Default = json.RawMessage("null") }},
		{"wrong default", func(d *platformDescriptor) { d.Fields[0].Default = json.RawMessage("42") }},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			d, err := testNATSDescriptor()
			if err != nil {
				t.Fatal(err)
			}
			scenario.mutate(d)
			root := t.TempDir()
			writeTestDescriptor(t, root, d)
			if _, err := readPlatformDescriptor(root, "event", "nats"); err == nil {
				t.Fatal("invalid descriptor was accepted")
			}
		})
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, platformDescriptorFile), []byte(`{"unexpected":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readPlatformDescriptor(root, "event", "kafka"); err == nil {
		t.Fatal("invalid metadata silently selected a compatibility profile")
	}
}

func TestDynamicPlatformMissingRepositoryAndMetadata(t *testing.T) {
	core, kafka, noMetadata := t.TempDir(), t.TempDir(), t.TempDir()
	for root, path := range map[string]string{core: spineImportPath, kafka: kafkaPlatformPath, noMetadata: providerModule("emptybus")} {
		if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module "+path+"\n\ngo 1.25.5\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	proxy := isolatedModuleProxy(t, moduleProxySource{spineImportPath, currentAssemblySpineVersion, core}, moduleProxySource{kafkaPlatformPath, previewPlatformVersion, kafka}, moduleProxySource{providerModule("emptybus"), "v0.1.0", noMetadata})
	t.Setenv("GOPROXY", proxy)
	t.Setenv("GOSUMDB", "off")
	inTempDir(t)
	previewModuleResolver, platformDescriptorResolver = resolvePreviewModules, resolvePlatformDescriptor
	for _, scenario := range []struct {
		args []string
		want string
	}{
		{[]string{"--platform", "none", "--event-platform", "missingbus"}, providerModule("missingbus") + "@preview"},
		{[]string{"--platform", "none", "--event-platform", "kafka", "--validator", "missingrules"}, validatorModule("missingrules") + "@preview"},
		{[]string{"--platform", "none", "--event-platform", "emptybus"}, "does not provide spine-platform.json"},
	} {
		var out, errOut bytes.Buffer
		args := append([]string{"new", "sample", "--skip-install", "--json"}, scenario.args...)
		if code := Run(args, &out, &errOut); code != 3 || !strings.Contains(out.String()+errOut.String(), scenario.want) {
			t.Fatalf("missing repository/metadata error: %d %s %s", code, out.String(), errOut.String())
		}
		if strings.Contains(out.String()+errOut.String(), "unsupported event platform") || strings.Contains(out.String()+errOut.String(), "unsupported validator") {
			t.Fatal("provider was rejected before repository lookup")
		}
		if _, err := os.Stat("sample"); !os.IsNotExist(err) {
			t.Fatal("failed lookup published project files")
		}
	}
}

func TestDynamicPlatformMetadataInvalidatesPlan(t *testing.T) {
	inTempDir(t)
	d, _ := testNATSDescriptor()
	d.Provider, d.SHA256 = "newbus", "first-descriptor"
	platformDescriptorResolver = func(context.Context, string, string, selectedModule, bool) (*platformDescriptor, error) {
		return d, nil
	}
	args := []string{"sample", "--platform", "none", "--event-platform", "newbus", "--skip-install", "--json"}
	var out, errOut bytes.Buffer
	if code := runNew(append(append([]string{}, args...), "--dry-run"), &out, &errOut); code != 0 {
		t.Fatal(errOut.String())
	}
	var plan map[string]any
	if err := json.Unmarshal(out.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	d.SHA256 = "second-descriptor"
	out.Reset()
	errOut.Reset()
	if code := runNew(append(args, "--if-match", plan["base_hash"].(string)), &out, &errOut); code != 4 {
		t.Fatalf("metadata change reused a reviewed plan: %d %s", code, out.String())
	}
	if _, err := os.Stat("sample"); !os.IsNotExist(err) {
		t.Fatal("stale descriptor plan wrote project files")
	}
}
