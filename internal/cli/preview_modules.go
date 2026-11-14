package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

// 테스트는 브랜치 이동과 조회 실패를 외부 저장소 변경 없이 재현합니다.
var previewModuleResolver = resolvePreviewModules

const defaultDependencyChannel = "preview"

var releaseCandidateVersion = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)-rc\.[1-9][0-9]*$`)

func validateDependencyChannel(channel string) error {
	switch channel {
	case "preview", "rc", "main":
		return nil
	default:
		return fmt.Errorf("unsupported dependency channel: %s", channel)
	}
}

func dependencyStability(channel string) string {
	if channel == "main" {
		return "released"
	}
	return "prerelease"
}

func latestDependencyVersion(versions []string, channel string) (string, error) {
	if channel != "rc" && channel != "main" {
		return "", fmt.Errorf("unsupported published dependency channel: %s", channel)
	}
	latest := ""
	for _, version := range versions {
		if !semver.IsValid(version) || semver.Canonical(version) != version {
			continue
		}
		prerelease := semver.Prerelease(version)
		if channel == "main" && prerelease != "" {
			continue
		}
		if channel == "rc" && prerelease != "" && !releaseCandidateVersion.MatchString(version) {
			continue
		}
		if latest == "" || semver.Compare(version, latest) > 0 {
			latest = version
		}
	}
	if latest == "" {
		return "", fmt.Errorf("no published %s version", channel)
	}
	return latest, nil
}

func resolveDependencyModules(ctx context.Context, targets []selectedModule, channel string) ([]selectedModule, error) {
	if err := validateDependencyChannel(channel); err != nil {
		return nil, err
	}
	if channel == "preview" {
		return previewModuleResolver(ctx, targets)
	}
	resolved := append([]selectedModule(nil), targets...)
	root, err := os.MkdirTemp("", "spine-channel-modules-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(root)
	proxy, err := platformGo(ctx, root, false, "env", "GOPROXY")
	if err != nil {
		return nil, fmt.Errorf("resolve %s dependencies: %w", channel, err)
	}
	environment := previewQueryEnvironment(strings.TrimSpace(string(proxy)))
	for index, target := range targets {
		if target.Version != "preview" {
			continue
		}
		output, err := platformGoWithEnv(ctx, root, environment, "list", "-m", "-versions", "-json", target.Path+"@latest")
		if err != nil {
			return nil, fmt.Errorf("list published versions for %s: %w", target.Path, err)
		}
		var module struct {
			Path     string
			Versions []string
		}
		if err := json.Unmarshal(output, &module); err != nil || module.Path != target.Path {
			return nil, fmt.Errorf("invalid version listing for %s", target.Path)
		}
		version, err := latestDependencyVersion(module.Versions, channel)
		if err != nil {
			return nil, fmt.Errorf("resolve %s dependency %s: %w", channel, target.Path, err)
		}
		resolved[index].Version = version
	}
	return resolved, nil
}

// 목적지나 호출자의 go.mod를 바꾸지 않고 브랜치를 정규 모듈 버전으로 해석합니다.
func resolvePreviewModules(ctx context.Context, targets []selectedModule) ([]selectedModule, error) {
	resolved := append([]selectedModule(nil), targets...)
	args := []string{"list", "-m", "-json", "-e"}
	for _, target := range targets {
		if target.Version == "preview" {
			args = append(args, target.Path+"@preview")
		}
	}
	if len(args) == 4 {
		return resolved, nil
	}
	root, err := os.MkdirTemp("", "spine-preview-modules-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(root)
	proxy, err := platformGo(ctx, root, false, "env", "GOPROXY")
	if err != nil {
		return nil, fmt.Errorf("resolve Preview branches: %w", err)
	}
	environment := previewQueryEnvironment(strings.TrimSpace(string(proxy)))
	var output []byte
	// 여러 Go 프로세스가 Git 캐시의 shallow 정보를 갱신하면 조회가 일시적으로 실패할 수 있습니다.
	for attempt := 0; ; attempt++ {
		output, err = platformGoWithEnv(ctx, root, environment, args...)
		message := string(output)
		if err != nil {
			message += err.Error()
		}
		if attempt == 2 || !strings.Contains(message, "shallow file has changed since we read it") {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(attempt+1) * 200 * time.Millisecond):
		}
	}
	if err != nil {
		return nil, fmt.Errorf("resolve Preview branches: %w", err)
	}
	versions := map[string]string{}
	decoder := json.NewDecoder(bytes.NewReader(output))
	for {
		var module struct {
			Path, Version string
			Replace       *selectedModule
			Error         *struct{ Err string }
		}
		if err := decoder.Decode(&module); err == io.EOF {
			break
		} else if err != nil {
			return nil, fmt.Errorf("invalid Preview module response: %w", err)
		}
		if module.Error != nil {
			kind, _ := platformModuleIdentity(module.Path)
			if kind != "" {
				reason := "platform repository lookup failed"
				if kind == "frontend" {
					reason = "frontend provider repository lookup failed"
				}
				message := strings.ToLower(module.Error.Err)
				if strings.Contains(message, "not found") || strings.Contains(message, "404") || strings.Contains(message, "unknown revision") || strings.Contains(message, "terminal prompts disabled") || strings.Contains(message, "no such file") {
					reason = "platform repository or preview branch not found or inaccessible"
					if kind == "frontend" {
						reason = "frontend provider repository or preview branch not found or inaccessible"
					}
				}
				return nil, fmt.Errorf("%s: %s@preview: %s", reason, module.Path, module.Error.Err)
			}
			return nil, fmt.Errorf("resolve Preview branch %s: %s", module.Path, module.Error.Err)
		}
		if !semver.IsValid(module.Version) || module.Replace != nil || versions[module.Path] != "" {
			return nil, fmt.Errorf("invalid Preview version for %s: %s", module.Path, module.Version)
		}
		versions[module.Path] = module.Version
	}
	for index, target := range resolved {
		if target.Version != "preview" {
			continue
		}
		version := versions[target.Path]
		if version == "" {
			return nil, fmt.Errorf("Preview version was not returned for %s", target.Path)
		}
		resolved[index].Version = version
	}
	return resolved, nil
}

// Resolve moving branches against the source when using the default Go proxy.
// A proxy can cache a pre-tag pseudo-version while dependencies already require
// the new tag. Downloads still use the normal proxy and checksum database.
// Custom proxies, including offline test proxies, remain authoritative.
func previewQueryEnvironment(proxy string) []string {
	environment := goEnvironment(false)
	if proxy != "" && proxy != "https://proxy.golang.org,direct" {
		return environment
	}
	for index, entry := range environment {
		if strings.HasPrefix(entry, "GOPROXY=") {
			environment[index] = "GOPROXY=direct"
			return environment
		}
	}
	return append(environment, "GOPROXY=direct")
}
