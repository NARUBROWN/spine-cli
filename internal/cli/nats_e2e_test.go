package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const natsPlatformPath = "github.com/NARUBROWN/spine-nats-platform"
const natsPlatformVersion = "v0.1.0-preview.1"

// 모듈 공개 전에는 명시한 로컬 소스를 별도 프록시와 캐시에 넣습니다.
// 생성 앱에 replace를 넣지 않으며 공개 설치 증거로 해석하지 않습니다.
func localNATSModuleProxy(t *testing.T) {
	t.Helper()
	root := os.Getenv("SPINE_TEST_NATS_MODULE_DIR")
	if root == "" {
		return
	}
	isolatedModuleProxy(t, moduleProxySource{natsPlatformPath, natsPlatformVersion, root})
	t.Log("using an isolated local NATS module proxy; this is not public installation evidence")
}

func TestGeneratedNATSAssemblyE2E(t *testing.T) {
	if os.Getenv("SPINE_TEST_NATS_URL") == "" {
		t.Skip("set SPINE_TEST_NATS_URL to run generated JetStream applications")
	}
	requireValidatorToolchain(t)
	localNATSModuleProxy(t)
	binary := buildUpdateCLI(t)
	fixture, err := os.ReadFile("testdata/assembly_nats_test.go.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, layout := range []string{layeredLayout, "legacy"} {
		for _, platform := range []string{"echo", "none"} {
			t.Run(layout+"/"+platform, func(t *testing.T) {
				workspace := t.TempDir()
				applyAssemblyPlan(t, binary, workspace, "new", "sample", "--module", "example.com/sample", "--layout", layout, "--platform", platform, "--event-platform", "events:nats", "--validator", "playground")
				project := filepath.Join(workspace, "sample")
				mod, err := os.ReadFile(filepath.Join(project, "go.mod"))
				if err != nil || strings.Contains(string(mod), "replace") || !strings.Contains(string(mod), natsPlatformPath) || !strings.Contains(string(mod), "go 1.26.0") {
					t.Fatalf("invalid NATS module selection: %s %v", mod, err)
				}
				// 정적 검사는 잘못된 런타임 환경에서도 helper를 실행하지 않습니다.
				staticEnv := []string{"SPINE_EVENTS_ALLOW_INSECURE=invalid"}
				assertE2ESuccess(t, runE2E(binary, project, staticEnv, "context", "--json"), "NATS context")
				contract := runE2E(binary, project, staticEnv, "contract", "check", "--warnings-as-errors", "--json")
				assertE2ESuccess(t, contract, "NATS static contract")
				if !strings.Contains(contract.stdout, `"provider": "nats"`) || !strings.Contains(contract.stdout, "runtime-values-not-evaluated") {
					t.Fatalf("NATS static selection missing: %s", contract.stdout)
				}
				probeEnv := []string{"SPINE_EVENTS_URLS=" + os.Getenv("SPINE_TEST_NATS_URL"), "SPINE_EVENTS_STREAM=PROBE", "SPINE_EVENTS_CONSUMER_PREFIX=probe", "SPINE_EVENTS_ALLOW_INSECURE=true"}
				assertE2ESuccess(t, runE2E(binary, project, probeEnv, "contract", "probe", "--json"), "NATS partial runtime probe")
				source, _ := os.ReadFile(filepath.Join(project, "internal/application/application.go"))
				addGeneratedTest(t, project, "nats_e2e_test.go", string(fixture))
				generatedGo(t, project, "mod", "tidy")
				assertE2ESuccess(t, runE2E(binary, project, nil, "verify", "--warnings-as-errors", "--json"), "NATS generated verify")
				output := generatedGo(t, project, "test", "-race", "-v", "./...", "-count=1", "-timeout=2m")
				if strings.Contains(output, "--- SKIP") {
					t.Fatalf("NATS fixture was skipped: %s", output)
				}
				t.Log(output)
				applyAssemblyPlan(t, binary, project, "update")
				after, _ := os.ReadFile(filepath.Join(project, "internal/application/application.go"))
				if !bytes.Equal(source, after) {
					t.Fatal("NATS E2E or update changed generated composition")
				}
				t.Logf("unchanged generated NATS BuildApplication sha256 %x", sha256.Sum256(source))
			})
		}
	}
	if go125 := os.Getenv("SPINE_TEST_GO125"); go125 != "" {
		t.Run("minimum-go", func(t *testing.T) {
			root := t.TempDir()
			env := []string{"PATH=" + filepath.Dir(go125) + string(os.PathListSeparator) + os.Getenv("PATH"), "GOTOOLCHAIN=local"}
			result := runE2E(binary, root, env, "new", "needs-nats", "--platform", "none", "--event-platform", "nats", "--json")
			if result.code != 3 || !strings.Contains(result.stdout, `"failed_stage": "dependencies"`) || !strings.Contains(result.stdout+result.stderr, "1.26") {
				t.Fatalf("NATS minimum toolchain was not enforced: %+v", result)
			}
			if _, err := os.Stat(filepath.Join(root, "needs-nats")); !os.IsNotExist(err) {
				t.Fatal("incompatible toolchain published a partial NATS project")
			}
		})
	}
}

func TestResolvePreviewModulesIncludesNATS(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module "+natsPlatformPath+"\n\ngo 1.26.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SPINE_TEST_NATS_MODULE_DIR", root)
	localNATSModuleProxy(t)
	resolved, err := resolvePreviewModules(context.Background(), []selectedModule{{Path: natsPlatformPath, Version: "preview"}})
	if err != nil || len(resolved) != 1 || resolved[0].Version != natsPlatformVersion {
		t.Fatalf("NATS branch was not resolved: %+v %v", resolved, err)
	}
	data, _ := json.Marshal(resolved)
	t.Log(string(data))
}
