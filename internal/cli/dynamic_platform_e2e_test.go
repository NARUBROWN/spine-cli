package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// CLI 제품 코드에 등록하지 않은 이름과 생성자로 실제 전달 및 검증을 실행합니다.
func TestGeneratedDynamicPlatformE2E(t *testing.T) {
	if os.Getenv("SPINE_TEST_NATS_URL") == "" {
		t.Skip("set SPINE_TEST_NATS_URL to run dynamic event and validator E2E")
	}
	requireValidatorToolchain(t)
	natsRoot := os.Getenv("SPINE_TEST_NATS_MODULE_DIR")
	if natsRoot == "" {
		modules, err := resolvePreviewModules(context.Background(), []selectedModule{{Path: natsPlatformPath, Version: "preview"}})
		if err != nil {
			t.Fatal(err)
		}
		output, err := platformGo(context.Background(), t.TempDir(), false, "mod", "download", "-json", natsPlatformPath+"@"+modules[0].Version)
		if err != nil {
			t.Fatal(err)
		}
		var downloaded struct{ Dir string }
		if err := json.Unmarshal(output, &downloaded); err != nil || downloaded.Dir == "" {
			t.Fatalf("NATS module source unavailable: %v", err)
		}
		natsRoot = downloaded.Dir
	}
	eventRoot, validatorRoot := t.TempDir(), t.TempDir()
	const eventProvider, validatorProvider = "sample-bus", "custom-rules"
	eventPath, validationPath := providerModule(eventProvider), validatorModule(validatorProvider)
	for _, name := range []string{"factory.go", "publisher.go", "reader.go", "resource.go", "go.mod"} {
		data, err := os.ReadFile(filepath.Join(natsRoot, name))
		if err != nil {
			t.Fatal(err)
		}
		source := strings.ReplaceAll(string(data), natsPlatformPath, eventPath)
		source = regexp.MustCompile(`(?m)(^|[^.\w])Config\b`).ReplaceAllString(source, "${1}Settings")
		source = strings.ReplaceAll(source, "NewFactory", "Open")
		writeE2EFile(t, filepath.Join(eventRoot, name), source)
	}
	descriptor, err := readPlatformDescriptor(natsRoot, "event", "nats")
	if err != nil {
		t.Fatal(err)
	}
	descriptor.Provider, descriptor.ConfigType, descriptor.Factory = eventProvider, "Settings", "Open"
	writeTestDescriptor(t, eventRoot, descriptor)
	validatorDescriptor := &platformDescriptor{
		SchemaVersion: platformDescriptorSchema, Kind: "validator", Provider: validatorProvider,
		ContractVersion: "spine.validation/v1", Package: "rules", Factory: "Create", ConfigType: "Settings", MinimumGo: "1.26.0",
		Fields: []platformConfigField{
			{Field: "Rule", Type: "string", Env: "RULE", Required: true, Example: json.RawMessage(`"email"`)},
			{Field: "Enabled", Type: "bool", Env: "ENABLED", Default: json.RawMessage("true"), Example: json.RawMessage("true")},
			{Field: "MaxErrors", Type: "int", Env: "MAX_ERRORS", Default: json.RawMessage("2"), Example: json.RawMessage("2")},
			{Field: "Wait", Type: "duration", Env: "WAIT", Default: json.RawMessage(`"10ms"`), Example: json.RawMessage(`"10ms"`)},
			{Field: "Labels", Type: "strings", Env: "LABELS", Default: json.RawMessage(`["email"]`), Example: json.RawMessage(`["email"]`)},
		},
	}
	writeTestDescriptor(t, validatorRoot, validatorDescriptor)
	writeE2EFile(t, filepath.Join(validatorRoot, "go.mod"), "module "+validationPath+"\n\ngo 1.26.0\n\nrequire (\n github.com/NARUBROWN/spine "+currentAssemblySpineVersion+"\n "+validatorPlatformPath+" "+previewPlatformVersion+"\n)\n")
	writeE2EFile(t, filepath.Join(validatorRoot, "validator.go"), dynamicValidatorSource)
	isolatedDynamicPlatformProxy(t, moduleProxySource{eventPath, "v0.1.0", eventRoot}, moduleProxySource{validationPath, "v0.1.0", validatorRoot})
	t.Setenv(validatorEnvPrefix(validatorProvider)+"RULE", "email")
	binary := buildUpdateCLI(t)
	data, err := os.ReadFile("testdata/assembly_nats_test.go.txt")
	if err != nil {
		t.Fatal(err)
	}
	fixture := strings.ReplaceAll(string(data), natsPlatformPath, eventPath)
	fixture = strings.ReplaceAll(fixture, "loadNatsConfig", descriptorLoader(descriptor))
	fixture = strings.ReplaceAll(fixture, "natsplatform.NewFactory", "natsplatform.Open")
	for _, layout := range []string{layeredLayout, "legacy"} {
		for _, platform := range []string{"echo", "none"} {
			t.Run(layout+"/"+platform, func(t *testing.T) {
				workspace := t.TempDir()
				applyAssemblyPlan(t, binary, workspace, "new", "sample", "--module", "example.com/sample", "--layout", layout, "--platform", platform, "--event-platform", "events:"+eventProvider, "--validator", validatorProvider)
				project := filepath.Join(workspace, "sample")
				contract := runE2E(binary, project, nil, "contract", "check", "--warnings-as-errors", "--json")
				assertE2ESuccess(t, contract, "unregistered event and validator static contract")
				for _, wanted := range []string{eventPath + ".Open", validationPath + ".Create", `"provider": "custom-rules"`, "runtime-values-not-evaluated"} {
					if !strings.Contains(contract.stdout, wanted) {
						t.Fatalf("dynamic selection not recognized: %s", contract.stdout)
					}
				}
				before, _ := os.ReadFile(filepath.Join(project, "internal/application/application.go"))
				addGeneratedTest(t, project, "dynamic_runtime_test.go", brokerPublishFixture(t, project, fixture))
				generatedGo(t, project, "mod", "tidy")
				output := generatedGo(t, project, "test", "-race", "-v", "./...", "-count=1", "-timeout=2m")
				if strings.Contains(output, "--- SKIP") {
					t.Fatal("dynamic platform runtime fixture was skipped")
				}
				t.Log(output)
				applyAssemblyPlan(t, binary, project, "update")
				after, _ := os.ReadFile(filepath.Join(project, "internal/application/application.go"))
				if !bytes.Equal(before, after) {
					t.Fatal("dynamic platform update changed application source")
				}
				mod, _ := os.ReadFile(filepath.Join(project, "go.mod"))
				if strings.Contains(string(mod), "replace") || !strings.Contains(string(mod), eventPath) || !strings.Contains(string(mod), validationPath) {
					t.Fatal("dynamic module selection was lost")
				}
			})
		}
	}
}

const dynamicValidatorSource = `package rules
import (
 "fmt"
 "time"
 validation "github.com/NARUBROWN/spine/contract/validator"
 playground "github.com/NARUBROWN/spine-validator-playground"
)
type Settings struct { Rule string; Enabled bool; MaxErrors int; Wait time.Duration; Labels []string }
func Create(settings Settings) (validation.Validator,error) {
 if settings.Rule!="email" || !settings.Enabled || settings.MaxErrors!=2 || settings.Wait!=10*time.Millisecond || len(settings.Labels)!=1 || settings.Labels[0]!="email" { return nil,fmt.Errorf("invalid sample validator settings") }
 return playground.New()
}
`
