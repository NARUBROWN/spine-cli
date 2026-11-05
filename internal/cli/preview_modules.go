package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"golang.org/x/mod/semver"
)

// 테스트는 브랜치 이동과 조회 실패를 외부 저장소 변경 없이 재현합니다.
var previewModuleResolver = resolvePreviewModules

// 목적지나 호출자의 go.mod를 바꾸지 않고 브랜치를 정규 모듈 버전으로 해석합니다.
func resolvePreviewModules(ctx context.Context, targets []selectedModule) ([]selectedModule, error) {
	resolved := append([]selectedModule(nil), targets...)
	args := []string{"list", "-m", "-json"}
	for _, target := range targets {
		if target.Version == "preview" {
			args = append(args, target.Path+"@preview")
		}
	}
	if len(args) == 3 {
		return resolved, nil
	}
	root, err := os.MkdirTemp("", "spine-preview-modules-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(root)
	output, err := platformGo(ctx, root, false, args...)
	if err != nil {
		return nil, fmt.Errorf("resolve Preview branches: %w", err)
	}
	versions := map[string]string{}
	decoder := json.NewDecoder(bytes.NewReader(output))
	for {
		var module selectedModule
		if err := decoder.Decode(&module); err == io.EOF {
			break
		} else if err != nil {
			return nil, fmt.Errorf("invalid Preview module response: %w", err)
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
