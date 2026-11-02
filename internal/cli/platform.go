package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	echoPlatformPath     = "github.com/NARUBROWN/spine-echo-platform"
	echoPlatformVersion  = "v0.1.0"
	platformSpineVersion = "v0.6.0"
	platformEchoVersion  = "v5.4.4"
	httpAdapterContract  = "spine.http-adapter/v2"
	echoRecipe           = "echo-v0.1.0-spine-v0.6.0"
)

type selectedModule struct {
	Path    string          `json:"path"`
	Version string          `json:"version,omitempty"`
	Dir     string          `json:"-"`
	Replace *selectedModule `json:"replace,omitempty"`
}

func goEnvironment(offline bool) []string {
	values := map[string]string{}
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			values[key] = value
		}
	}
	// 생성한 모듈 밖의 설정을 바꾸지 않도록 실행 환경을 고정합니다.
	// GOFLAGS를 공백으로 지정하면 빈 값과 달리 저장된 실행 옵션을 읽지 않습니다.
	values["GOFLAGS"], values["GOWORK"], values["GOTOOLCHAIN"] = " ", "off", "local"
	if offline {
		values["GOPROXY"], values["GOSUMDB"] = "off", "off"
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+values[key])
	}
	return result
}

func platformGo(ctx context.Context, root string, offline bool, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "go", args...)
	command.Dir, command.Env = root, goEnvironment(offline)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("go %s: %w\n%s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()+stdout.String()))
	}
	return stdout.Bytes(), nil
}

type activeGoSettings struct{ GOWORK, GOFLAGS string }

func readActiveGoSettings(root string) (activeGoSettings, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "env", "-json", "GOWORK", "GOFLAGS")
	command.Dir = root
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GOTOOLCHAIN=") {
			command.Env = append(command.Env, entry)
		}
	}
	command.Env = append(command.Env, "GOTOOLCHAIN=local")
	output, err := command.Output()
	if err != nil {
		return activeGoSettings{}, fmt.Errorf("could not inspect Go environment: %w", err)
	}
	var settings activeGoSettings
	if err := json.Unmarshal(output, &settings); err != nil {
		return settings, fmt.Errorf("invalid Go environment response: %w", err)
	}
	return settings, nil
}

func activeWorkspace(root string) (string, error) {
	settings, err := readActiveGoSettings(root)
	return settings.GOWORK, err
}

// selectedModules는 프로젝트를 실행하지 않고 Go가 선택한 모듈 버전을 읽습니다.
func selectedModules(ctx context.Context, root string, offline bool) ([]selectedModule, error) {
	queryRoot := root
	if offline {
		settings, err := readActiveGoSettings(root)
		workspace := settings.GOWORK
		if err != nil {
			return nil, err
		}
		if workspace != "" && workspace != "off" {
			return nil, fmt.Errorf("active workspace %s cannot be resolved by the offline module inspector", workspace)
		}
		if strings.Contains(settings.GOFLAGS, "-modfile") || strings.Contains(settings.GOFLAGS, "-overlay") || strings.Contains(settings.GOFLAGS, "-mod=vendor") {
			return nil, fmt.Errorf("GOFLAGS %q changes module selection outside the offline inspector; remove those flags before inspecting", settings.GOFLAGS)
		}
		temporary, err := os.MkdirTemp("", "spine-module-graph-")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(temporary)
		queryRoot = temporary
		for _, name := range []string{"go.mod", "go.sum"} {
			contents, err := os.ReadFile(filepath.Join(root, name))
			if os.IsNotExist(err) && name == "go.sum" {
				continue
			}
			if err != nil {
				return nil, err
			}
			if err := os.WriteFile(filepath.Join(temporary, name), contents, 0o600); err != nil {
				return nil, err
			}
		}
		// 상대 replace는 원래 모듈 위치를 기준으로 해석합니다.
		output, err := platformGo(ctx, temporary, true, "mod", "edit", "-json")
		if err != nil {
			return nil, err
		}
		var file struct {
			Replace []struct {
				Old, New struct{ Path, Version string }
			}
		}
		if err := json.Unmarshal(output, &file); err != nil {
			return nil, err
		}
		for _, replacement := range file.Replace {
			if replacement.New.Version == "" && !filepath.IsAbs(replacement.New.Path) {
				old := replacement.Old.Path
				if replacement.Old.Version != "" {
					old += "@" + replacement.Old.Version
				}
				if _, err := platformGo(ctx, temporary, true, "mod", "edit", "-replace="+old+"="+filepath.Join(root, replacement.New.Path)); err != nil {
					return nil, err
				}
			}
		}
	}
	output, err := platformGo(ctx, queryRoot, offline, "list", "-mod=mod", "-m", "-json", "all")
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	result := []selectedModule{}
	for {
		var module selectedModule
		err := decoder.Decode(&module)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if module.Path == spineImportPath || module.Path == echoPlatformPath || module.Path == "github.com/labstack/echo/v5" {
			result = append(result, module)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result, nil
}

func selectedModuleVersion(modules []selectedModule, path string) string {
	for _, module := range modules {
		if module.Path == path {
			return module.Version
		}
	}
	return ""
}

func validateEchoModules(modules []selectedModule) error {
	for path, version := range map[string]string{spineImportPath: platformSpineVersion, echoPlatformPath: echoPlatformVersion, "github.com/labstack/echo/v5": platformEchoVersion} {
		if actual := selectedModuleVersion(modules, path); actual != version {
			return fmt.Errorf("platform compatibility: selected %s %s, expected %s", path, actual, version)
		}
	}
	for _, module := range modules {
		if module.Replace != nil {
			return fmt.Errorf("platform compatibility: local or version replacement for %s is not part of the verified public recipe", module.Path)
		}
	}
	return nil
}
