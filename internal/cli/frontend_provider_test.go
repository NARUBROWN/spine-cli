package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewWizardPromptsInProviderOrderAndSupportsCustomFrontendRoot(t *testing.T) {
	options, err := parseNewOptions([]string{"shop"})
	if err != nil {
		t.Fatal(err)
	}
	input := strings.NewReader("\n2\n\n\n\ny\n\nweb\n")
	var output bytes.Buffer
	options, err = promptNewProjectOptions([]string{"shop"}, options, input, &output)
	if err != nil {
		t.Fatal(err)
	}
	if options.DependencyChannel != "preview" || options.Platform != "echo" || len(options.Events) != 0 || options.WebSocketPlatform != "none" || options.Validator != "none" || options.Frontend != "react" || options.FrontendRoot != "web" {
		t.Fatalf("unexpected wizard selections: %#v", options)
	}
	positions := []int{
		strings.Index(output.String(), "Select Dependency channel"),
		strings.Index(output.String(), "Select HTTP adapter"),
		strings.Index(output.String(), "Select Event adapter"),
		strings.Index(output.String(), "Select WebSocket"),
		strings.Index(output.String(), "Select Validator"),
		strings.Index(output.String(), "Enable a frontend?"),
		strings.Index(output.String(), "Select Frontend provider"),
		strings.Index(output.String(), "Frontend root path"),
	}
	for index, position := range positions {
		if position < 0 || (index > 0 && position <= positions[index-1]) {
			t.Fatalf("provider prompts are missing or out of order: %v\n%s", positions, output.String())
		}
	}
}

func TestNewWizardResolvesAnUnlistedFrontendByProviderName(t *testing.T) {
	var output bytes.Buffer
	provider, err := promptDynamicChoice(bufio.NewReader(strings.NewReader("solid\n")), &output, "Frontend", []promptChoice{
		{value: "none", label: "Disabled"},
		{value: "react", label: "React CSR"},
	}, "none", true, "Provider module contract")
	if err != nil {
		t.Fatal(err)
	}
	if provider != "solid" || frontendProviderModule(provider) != "github.com/NARUBROWN/spine-frontend-solid" {
		t.Fatalf("unlisted provider was not mapped to its module: %q %q", provider, frontendProviderModule(provider))
	}
}

func TestNewWizardSkipsFrontendForUnsupportedHTTPProviders(t *testing.T) {
	options, err := parseNewOptions([]string{"shop"})
	if err != nil {
		t.Fatal(err)
	}
	options.Platform = "custom"
	var output bytes.Buffer
	options, err = promptNewProjectOptions([]string{"shop", "--platform", "custom"}, options, strings.NewReader("\n\n\n"), &output)
	if err != nil {
		t.Fatal(err)
	}
	if options.Frontend != "none" || strings.Contains(output.String(), "Enable a frontend?") {
		t.Fatalf("unsupported HTTP provider offered frontend selection: options=%#v\n%s", options, output.String())
	}
	if !strings.Contains(output.String(), "Echo and net/http") {
		t.Fatalf("unsupported HTTP provider did not explain frontend serving support: %s", output.String())
	}
}

func TestFrontendSelectionRequiresSupportedHTTPProvider(t *testing.T) {
	for _, platform := range []string{"none", "custom", "gin"} {
		t.Run(platform, func(t *testing.T) {
			options, err := parseNewOptions([]string{"shop", "--platform", platform, "--frontend", "react"})
			if err == nil || !strings.Contains(err.Error(), "--frontend requires the echo or nethttp HTTP platform") {
				t.Fatalf("unsupported frontend platform was accepted: options=%#v error=%v", options, err)
			}
		})
	}
}

func TestReactScaffoldUsesConfiguredFrontendRootAndProviderManifest(t *testing.T) {
	options, err := parseNewOptions([]string{"shop", "--frontend", "react", "--frontend-root", "web", "--skip-install"})
	if err != nil {
		t.Fatal(err)
	}
	options.FrontendDescriptor = defaultReactFrontendDescriptor()
	files, err := newProjectFiles(options)
	if err != nil {
		t.Fatal(err)
	}
	generatedComposition, err := parser.ParseFile(token.NewFileSet(), "application.go", files["internal/application/application.go"], 0)
	if err != nil {
		t.Fatal(err)
	}
	if kind, _, found := findFrontendPlatformOption(generatedComposition); !found || kind != "echo" {
		t.Fatalf("generated composition does not expose its supported adapter: kind=%q found=%v", kind, found)
	}
	for _, name := range []string{"web/index.html", "web/src/main.tsx", "web/package.json", "web/spine-frontend.json"} {
		if _, ok := files[name]; !ok {
			t.Fatalf("scaffold omitted %s", name)
		}
	}
	if !strings.Contains(files[".gitignore"], "node_modules/") {
		t.Fatalf("frontend scaffold does not ignore installed npm dependencies:\n%s", files[".gitignore"])
	}
	if _, ok := files["frontend/index.html"]; ok {
		t.Fatal("scaffold wrote files under the default frontend root")
	}

	root := t.TempDir()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
	for name, contents := range files {
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	config, err := loadProjectConfig()
	if err != nil {
		t.Fatal(err)
	}
	settings, err := resolveFrontendSettings(root, config)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(settings.root) != "web" || filepath.Base(settings.entry) != "main.tsx" || settings.descriptor.Provider != "react" {
		t.Fatalf("frontend settings did not use generated provider metadata: %#v", settings)
	}
}

func TestGeneratedFrontendOptOutDisablesAutoDetection(t *testing.T) {
	options, err := parseNewOptions([]string{"api"})
	if err != nil {
		t.Fatal(err)
	}
	files, err := newProjectFiles(options)
	if err != nil {
		t.Fatal(err)
	}
	configContents := files["spine.toml"]
	if !strings.Contains(configContents, "frontend_enabled = false") {
		t.Fatalf("project without a frontend did not persist its opt-out:\n%s", configContents)
	}

	root := t.TempDir()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
	if err := os.WriteFile("spine.toml", []byte(configContents), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll("internal/application", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("internal/application/application.go", []byte(files["internal/application/application.go"]), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll("frontend/src", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("frontend/src/main.tsx", []byte("export {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	config, err := loadProjectConfig()
	if err != nil {
		t.Fatal(err)
	}
	settings, err := resolveFrontendSettings(root, config)
	if err != nil || settings != nil {
		t.Fatalf("explicit opt-out did not disable frontend detection: settings=%#v error=%v", settings, err)
	}

	legacyContents := strings.Replace(configContents, "frontend_enabled = false\n", "", 1)
	if err := os.WriteFile("spine.toml", []byte(legacyContents), 0o644); err != nil {
		t.Fatal(err)
	}
	config, err = loadProjectConfig()
	if err != nil {
		t.Fatal(err)
	}
	settings, err = resolveFrontendSettings(root, config)
	if err != nil || settings == nil {
		t.Fatalf("implicit detection for existing projects changed: settings=%#v error=%v", settings, err)
	}
}

func TestImplicitFrontendDetectionRequiresAUsableServingAdapter(t *testing.T) {
	root := t.TempDir()
	composition := filepath.Join(root, "internal", "application")
	if err := os.MkdirAll(composition, 0o755); err != nil {
		t.Fatal(err)
	}
	source := `package application
import echoplatform "github.com/NARUBROWN/spine-echo-platform"
func BuildApplication() { _ = echoplatform.Option() }
`
	if err := os.WriteFile(filepath.Join(composition, "application.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	legacy := projectConfig{Composition: "./internal/application"}
	if !projectHasFrontendServingAdapter(root, legacy) {
		t.Fatal("legacy config with a direct Echo call lost implicit detection")
	}
	if projectHasFrontendServingAdapter(root, projectConfig{Composition: legacy.Composition, Platform: "custom"}) {
		t.Fatal("unsupported HTTP provider enabled implicit frontend detection")
	}
	if projectHasFrontendServingAdapter(root, projectConfig{Composition: legacy.Composition, Platform: "nethttp"}) {
		t.Fatal("HTTP platform mismatch enabled implicit frontend detection")
	}
}

func TestFrontendOverlayFingerprintTracksGeneratedServingInputs(t *testing.T) {
	root := t.TempDir()
	composition := filepath.Join(root, "internal", "application")
	if err := os.MkdirAll(composition, 0o755); err != nil {
		t.Fatal(err)
	}
	source := `package application
import echoplatform "github.com/NARUBROWN/spine-echo-platform"
func BuildApplication() { _ = echoplatform.Option() }
`
	if err := os.WriteFile(filepath.Join(composition, "application.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	config := projectConfig{Composition: "./internal/application", Platform: "echo"}
	settings := &frontendSettings{output: filepath.Join(root, "bin", "frontend"), apiPrefix: "/api"}
	base := frontendOverlayFingerprint(root, config, settings)
	if !strings.Contains(base, "frontend-overlay-v2") || !strings.Contains(base, installedVersion()) {
		t.Fatalf("frontend overlay cache fingerprint omitted cache or CLI version: %q", base)
	}
	apiChanged := *settings
	apiChanged.apiPrefix = "/v2"
	if base == frontendOverlayFingerprint(root, config, &apiChanged) {
		t.Fatal("frontend overlay fingerprint ignored API prefix changes")
	}
	outputChanged := *settings
	outputChanged.output = filepath.Join(root, "bin", "assets")
	if base == frontendOverlayFingerprint(root, config, &outputChanged) {
		t.Fatal("frontend overlay fingerprint ignored output directory changes")
	}
}

func TestGeneratedFrontendFallbackSupportsStreamingResponseWriters(t *testing.T) {
	for _, kind := range []string{"echo", "nethttp"} {
		t.Run(kind, func(t *testing.T) {
			source, err := frontendOverlaySource(kind, "application", "frontend", "/api")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := parser.ParseFile(token.NewFileSet(), "frontend.go", source, parser.AllErrors); err != nil {
				t.Fatalf("generated %s frontend overlay has invalid Go syntax: %v", kind, err)
			}
			for _, method := range []string{"Flush()", "Hijack()", "Unwrap()"} {
				if !strings.Contains(string(source), method) {
					t.Fatalf("generated %s frontend overlay omitted %s", kind, method)
				}
			}
		})
	}
}

func TestFrontendProviderDescriptorRejectsExecutableOrUnsafeMetadata(t *testing.T) {
	descriptor := defaultReactFrontendDescriptor()
	descriptor.Engine = "custom-command"
	if err := validateFrontendProviderDescriptor(descriptor, "react", true); err == nil {
		t.Fatal("unsupported frontend build engine was accepted")
	}

	descriptor = defaultReactFrontendDescriptor()
	descriptor.StarterFiles[0].Path = "../outside.html"
	if err := validateFrontendProviderDescriptor(descriptor, "react", true); err == nil {
		t.Fatal("frontend starter path escaped its root")
	}

	descriptor = defaultReactFrontendDescriptor()
	projectDescriptor, err := frontendProjectDescriptor(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(projectDescriptor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readFrontendProviderDescriptor(bytes.NewReader(data), "react", false); err != nil {
		t.Fatalf("project provider manifest was rejected: %v", err)
	}
}
