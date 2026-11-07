package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
)

var rcTag = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)-rc\.[1-9][0-9]*$`)

type channelUnavailable struct{ path, channel string }

func (e *channelUnavailable) Error() string {
	return fmt.Sprintf("%s: no published %s version", e.path, e.channel)
}

// RC는 최신 RC와 정식 태그 중 더 높은 버전을 선택합니다.
func latestChannelTag(versions []string, channel string) (string, error) {
	if channel != "rc" && channel != "stable" {
		return "", fmt.Errorf("unsupported channel %q", channel)
	}
	latest := ""
	for _, version := range versions {
		if !semver.IsValid(version) || semver.Canonical(version) != version {
			continue
		}
		if channel == "stable" && semver.Prerelease(version) != "" {
			continue
		}
		if channel == "rc" && semver.Prerelease(version) != "" && !rcTag.MatchString(version) {
			continue
		}
		if latest == "" || semver.Compare(version, latest) > 0 {
			latest = version
		}
	}
	if latest == "" {
		return "", fmt.Errorf("no published %s tag", channel)
	}
	return latest, nil
}

func resolveChannelTestModules(ctx context.Context, targets []selectedModule, channel string) ([]selectedModule, error) {
	if channel == "preview" {
		resolved, err := resolvePreviewModules(ctx, targets)
		if err != nil && len(targets) == 1 && strings.Contains(err.Error(), "unknown revision preview") {
			return nil, &channelUnavailable{path: targets[0].Path, channel: channel}
		}
		return resolved, err
	}
	resolved := append([]selectedModule(nil), targets...)
	root, err := os.MkdirTemp("", "spine-channel-versions-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(root)
	proxy, err := platformGo(ctx, root, false, "env", "GOPROXY")
	if err != nil {
		return nil, fmt.Errorf("read Go proxy: %w", err)
	}
	for i, target := range targets {
		if target.Version != "preview" {
			continue
		}
		output, err := platformGoWithEnv(ctx, root, previewQueryEnvironment(strings.TrimSpace(string(proxy))), "list", "-m", "-versions", "-json", target.Path+"@latest")
		if err != nil {
			return nil, fmt.Errorf("list versions for %s: %w: %s", target.Path, err, output)
		}
		var info struct {
			Path     string
			Versions []string
		}
		if err := json.Unmarshal(output, &info); err != nil || info.Path != target.Path {
			return nil, fmt.Errorf("invalid version listing for %s: %s", target.Path, output)
		}
		version, err := latestChannelTag(info.Versions, channel)
		if err != nil {
			return nil, &channelUnavailable{path: target.Path, channel: channel}
		}
		resolved[i].Version = version
	}
	return resolved, nil
}

func TestLatestChannelTag(t *testing.T) {
	versions := []string{"v0.9.0", "v0.10.0", "v1.0.0-preview.3", "v1.0.0-rc.2", "v1.0.0-rc.10", "v1.0.0-rc.0", "v1.1.0-rc.1", "v2.0.0-0.20260101000000-abcdefabcdef"}
	for _, tc := range []struct{ channel, want string }{{"stable", "v0.10.0"}, {"rc", "v1.1.0-rc.1"}} {
		got, err := latestChannelTag(versions, tc.channel)
		if err != nil || got != tc.want {
			t.Fatalf("%s: got %q, %v; want %q", tc.channel, got, err, tc.want)
		}
	}
	if got, err := latestChannelTag([]string{"v1.0.0-preview.1", "v1.0.0"}, "rc"); err != nil || got != "v1.0.0" {
		t.Fatalf("RC should accept a current stable version: %s %v", got, err)
	}
	if got, err := latestChannelTag([]string{"v1.0.0-rc.2", "v1.0.0"}, "rc"); err != nil || got != "v1.0.0" {
		t.Fatalf("RC should not select an older candidate than stable: %s %v", got, err)
	}
	if _, err := latestChannelTag([]string{"v1.0.0-preview.1"}, "stable"); err == nil {
		t.Fatal("missing stable silently fell back to a prerelease")
	}
}

func TestResolveChannelTestModulesWithIsolatedProxy(t *testing.T) {
	root := t.TempDir()
	path := "example.com/channel-module"
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module "+path+"\n\ngo 1.25.5\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	proxy := isolatedModuleProxy(t, moduleProxySource{path, "v0.9.0", root}, moduleProxySource{path, "v1.0.0-rc.1", root})
	parsed, err := url.Parse(proxy)
	if err != nil {
		t.Fatal(err)
	}
	escaped, err := module.EscapePath(path)
	if err != nil {
		t.Fatal(err)
	}
	proxyRoot := filepath.FromSlash(parsed.Path)
	if len(parsed.Path) > 1 && filepath.VolumeName(filepath.FromSlash(parsed.Path[1:])) != "" {
		proxyRoot = filepath.FromSlash(parsed.Path[1:])
	}
	list := filepath.Join(proxyRoot, filepath.FromSlash(escaped), "@v", "list")
	if err := os.WriteFile(list, []byte("v0.9.0\nv1.0.0-rc.1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOPROXY", proxy)
	t.Setenv("GOSUMDB", "off")
	for _, tc := range []struct{ channel, want string }{{"stable", "v0.9.0"}, {"rc", "v1.0.0-rc.1"}} {
		got, err := resolveChannelTestModules(context.Background(), []selectedModule{{Path: path, Version: "preview"}}, tc.channel)
		if err != nil || len(got) != 1 || got[0].Version != tc.want {
			t.Fatalf("%s: got %+v, %v; want %s", tc.channel, got, err, tc.want)
		}
	}
}

// 이 검사는 릴리스 대상 CLI의 생성 로직을 사용하고 채널 조회만 교체합니다.
func TestChannelComposition(t *testing.T) {
	channel := os.Getenv("SPINE_COMPOSITION_CHANNEL")
	if channel == "" {
		t.Skip("SPINE_COMPOSITION_CHANNEL enables the live channel gate")
	}
	if channel != "preview" && channel != "rc" && channel != "stable" {
		t.Fatalf("unknown channel %q", channel)
	}
	layout := os.Getenv("SPINE_COMPOSITION_LAYOUT")
	if layout != "legacy" && layout != "layered-v1" {
		t.Fatalf("unknown layout %q", layout)
	}
	cases := map[string][]string{
		"echo":            nil,
		"nethttp":         {"--platform", "nethttp"},
		"websocket":       {"--websocket-platform", "gorilla"},
		"validator":       {"--validator", "playground"},
		"kafka":           {"--event-platform", "kafka"},
		"rabbitmq":        {"--event-platform", "rabbitmq"},
		"nats":            {"--event-platform", "nats"},
		"worker-kafka":    {"--platform", "none", "--event-platform", "kafka"},
		"worker-rabbitmq": {"--platform", "none", "--event-platform", "rabbitmq"},
		"worker-nats":     {"--platform", "none", "--event-platform", "nats"},
	}
	name := os.Getenv("SPINE_COMPOSITION_CASE")
	options, ok := cases[name]
	if !ok {
		t.Fatalf("unknown composition %q", name)
	}
	t.Setenv("GOWORK", "off")
	optional := map[string]string{"nethttp": nethttpPlatformPath, "websocket": gorillaPlatformPath, "validator": validatorPlatformPath, "kafka": kafkaPlatformPath, "rabbitmq": rabbitPlatformPath, "nats": natsPlatformPath, "worker-kafka": kafkaPlatformPath, "worker-rabbitmq": rabbitPlatformPath, "worker-nats": natsPlatformPath}
	if path := optional[name]; path != "" {
		_, err := resolveChannelTestModules(context.Background(), []selectedModule{{Path: path, Version: "preview"}}, channel)
		var unavailable *channelUnavailable
		if errors.As(err, &unavailable) {
			t.Skipf("선택형 구성 검사 생략: %s", err)
		}
		if err != nil {
			t.Fatalf("선택형 구성 조회 실패: %v", err)
		}
	}
	binary := buildUpdateCLI(t)
	inTempDir(t)
	original := previewModuleResolver
	previewModuleResolver = func(ctx context.Context, targets []selectedModule) ([]selectedModule, error) {
		resolved, err := resolveChannelTestModules(ctx, targets, channel)
		if err == nil {
			for _, module := range resolved {
				t.Logf("%s: %s@%s", channel, module.Path, module.Version)
			}
		}
		return resolved, err
	}
	t.Cleanup(func() { previewModuleResolver = original })
	args := append([]string{"new", "sample", "--module", "example.com/sample", "--layout", layout}, options...)
	invoke := func(extra ...string) map[string]any {
		var out, errors bytes.Buffer
		command := append(append(append([]string{}, args...), extra...), "--json")
		if code := Run(command, &out, &errors); code != 0 {
			t.Fatalf("%s %s %s: exit %d\n%s\n%s", channel, layout, name, code, out.String(), errors.String())
		}
		var envelope struct {
			OK     bool           `json:"ok"`
			Result map[string]any `json:"result"`
		}
		if err := json.Unmarshal(out.Bytes(), &envelope); err != nil || !envelope.OK {
			t.Fatalf("invalid CLI response: %v %s", err, out.String())
		}
		return envelope.Result
	}
	plan := invoke("--dry-run")
	hash, ok := plan["base_hash"].(string)
	if !ok || hash == "" || plan["diff"] == "" {
		t.Fatal("generation plan did not contain a diff and base hash")
	}
	invoke("--if-match", hash)
	project, err := filepath.Abs("sample")
	if err != nil {
		t.Fatal(err)
	}
	t.Log(generatedGo(t, project, "list", "-m", "all"))
	for _, command := range [][]string{{"contract", "check", "--json"}, {"verify", "--json"}} {
		result := runE2E(binary, project, nil, command...)
		assertE2ESuccess(t, result, "channel composition verification")
		assertProtocolOK(t, result.stdout)
		t.Log(result.stdout)
	}
	generatedGo(t, project, "build", "./...")
}
