package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type installationFailure struct {
	Code  int
	Stage string
	Cause error
}

// 임시 프로젝트에서 의존성과 계약 및 컴파일만 검사하며 목적지는 게시하지 않습니다.
func installEchoProject(ctx context.Context, staging string, files map[string]string, stages []installationStage) ([]selectedModule, *installationFailure) {
	modules := []selectedModule{}
	failed := func(code int, stage string, cause error) ([]selectedModule, *installationFailure) {
		return modules, &installationFailure{code, stage, cause}
	}
	for path, contents := range files {
		target := filepath.Join(staging, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return failed(3, "scaffold", err)
		}
		if err := os.WriteFile(target, []byte(contents), 0o644); err != nil {
			return failed(3, "scaffold", err)
		}
	}
	stages[0].Status = "passed"
	modules, failure := verifyEchoProject(ctx, staging, ".", stages)
	if failure != nil {
		return modules, failure
	}
	for _, path := range []string{"go.mod", "go.sum"} {
		contents, err := os.ReadFile(filepath.Join(staging, path))
		if err != nil {
			return failed(10, "dependencies", err)
		}
		files[path] = string(contents)
	}
	return modules, nil
}

// new와 update가 같은 의존성 조합과 정적 계약 및 빌드 기준을 사용합니다.
func verifyEchoProject(ctx context.Context, staging, entrypoint string, stages []installationStage) ([]selectedModule, *installationFailure) {
	modules := []selectedModule{}
	failed := func(code int, stage string, cause error) ([]selectedModule, *installationFailure) {
		return modules, &installationFailure{code, stage, cause}
	}
	if _, err := platformGo(ctx, staging, false, "mod", "tidy"); err != nil {
		return failed(3, "dependencies", err)
	}
	stages[1].Status = "passed"
	var err error
	modules, err = selectedModules(ctx, staging, false)
	if err != nil {
		return failed(3, "modules", err)
	}
	if err := validateEchoModules(modules); err != nil {
		return failed(5, "modules", err)
	}
	stages[2].Status = "passed"
	contract, err := inspectStagedProject(staging)
	if err != nil {
		return failed(5, "contract", err)
	}
	if contractHasErrors(contract) {
		diagnostics, _ := json.Marshal(contract.Diagnostics)
		return failed(5, "contract", fmt.Errorf("static contract validation failed: %s", diagnostics))
	}
	stages[3].Status = "passed"
	if _, err := platformGo(ctx, staging, false, "build", "-o", filepath.Join(filepath.Dir(staging), "application.bin"), entrypoint); err != nil {
		return failed(5, "build", err)
	}
	stages[4].Status = "passed"
	return modules, nil
}
