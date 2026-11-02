package cli

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"
)

type updateOptions struct {
	JSON  bool
	Write writeOptions
}

func updateHelp(args []string, out io.Writer) bool {
	for i, arg := range args {
		if (arg == "--help" || arg == "-h") && (i == 0 || args[i-1] != "--if-match") {
			fmt.Fprintln(out, "Usage: spine update [--dry-run] [--if-match <hash>] [--json] [--root <directory>]")
			fmt.Fprintln(out, "\n기존 프로젝트의 Spine과 Echo 플랫폼 의존성을 이 CLI의 검증된 레시피로 갱신합니다.\n임시 복사본에서 의존성 해석, 정적 계약 검사와 빌드를 완료한 뒤 go.mod와 go.sum을 반영합니다.\n--dry-run도 네트워크와 Go 캐시를 사용할 수 있지만 프로젝트 파일은 바꾸지 않습니다.\n현재 레시피보다 새로운 버전은 낮추지 않습니다. CLI 실행 파일과 업무 소스는 갱신하지 않습니다.\n\n예:\n  spine update\n  spine update --dry-run\n  spine update --root ./shop-api --dry-run --json")
			return true
		}
	}
	return false
}

func parseUpdateOptions(args []string) (updateOptions, error) {
	var options updateOptions
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			options.JSON = true
		case "--dry-run":
			options.Write.DryRun = true
		case "--if-match":
			if i+1 == len(args) || strings.HasPrefix(args[i+1], "-") {
				return options, fmt.Errorf("--if-match requires a hash")
			}
			if options.Write.IfMatch != "" {
				return options, fmt.Errorf("--if-match may only be specified once")
			}
			i++
			options.Write.IfMatch = args[i]
		default:
			return options, fmt.Errorf("unknown option or argument for update: %s", args[i])
		}
	}
	return options, nil
}

func echoRecipeModules() []selectedModule {
	return []selectedModule{
		{Path: spineImportPath, Version: platformSpineVersion},
		{Path: echoPlatformPath, Version: echoPlatformVersion},
		{Path: "github.com/labstack/echo/v5", Version: platformEchoVersion},
	}
}

// updateModuleRequirements는 레시피 외의 require와 replace 및 주석을 보존합니다.
func updateModuleRequirements(contents []byte, module string) ([]byte, []selectedModule, error) {
	file, err := modfile.Parse("go.mod", contents, nil)
	if err != nil {
		return nil, nil, err
	}
	if file.Module == nil || file.Module.Mod.Path != module {
		return nil, nil, fmt.Errorf("spine.toml module %q must match go.mod", module)
	}
	targets := echoRecipeModules()
	for _, replacement := range file.Replace {
		if replacement.New.Version == "" {
			return nil, nil, fmt.Errorf("local replace for %s is outside the single-module update recipe; use a published module version", replacement.Old.Path)
		}
		for _, target := range targets {
			if replacement.Old.Path == target.Path {
				return nil, nil, fmt.Errorf("replace for %s is outside the verified public recipe; remove it before updating", target.Path)
			}
		}
	}
	previous := []selectedModule{}
	hasSpine := false
	for _, requirement := range file.Require {
		for _, target := range targets {
			if requirement.Mod.Path != target.Path {
				continue
			}
			previous = append(previous, selectedModule{Path: requirement.Mod.Path, Version: requirement.Mod.Version})
			hasSpine = hasSpine || target.Path == spineImportPath
			if semver.Compare(requirement.Mod.Version, target.Version) > 0 {
				return nil, nil, fmt.Errorf("%s %s is newer than recipe %s (%s); install a newer CLI with a compatible recipe instead of downgrading", target.Path, requirement.Mod.Version, echoRecipe, target.Version)
			}
		}
	}
	if !hasSpine {
		return nil, nil, fmt.Errorf("go.mod must already require %s; update applies to an existing Spine project", spineImportPath)
	}
	for _, target := range targets {
		if err := file.AddRequire(target.Path, target.Version); err != nil {
			return nil, nil, err
		}
	}
	file.Cleanup()
	updated, err := file.Format()
	return updated, previous, err
}

func runUpdate(args []string, out, errOut io.Writer) int {
	if updateHelp(args, out) {
		return 0
	}
	options, err := parseUpdateOptions(args)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	stages := []installationStage{{"snapshot", "pending"}, {"dependencies", "pending"}, {"modules", "pending"}, {"contract", "pending"}, {"build", "pending"}, {"publish", "pending"}}
	result := map[string]any{"recipe": echoRecipe, "platform": "echo", "stability": "development", "dry_run": options.Write.DryRun, "updated": false, "target_modules": echoRecipeModules(), "execution_boundary": "dependency resolution, static inspection and compilation in a temporary copy only; no application initialization, tests or listeners"}
	finish := func(code int, stage string, cause error) int {
		if cause != nil && ctx.Err() != nil {
			code, cause = 3, ctx.Err()
		}
		if cause != nil {
			for i := range stages {
				if stages[i].Name == stage {
					stages[i].Status = "failed"
				}
			}
			result["failed_stage"], result["cause"] = stage, cause.Error()
			fmt.Fprintf(errOut, "%s: %v\n", stage, cause)
		}
		result["stages"] = stages
		if options.JSON {
			if err := json.NewEncoder(out).Encode(result); err != nil {
				fmt.Fprintln(errOut, err)
				return 10
			}
		}
		return code
	}
	root, err := projectRoot()
	if err != nil {
		return finish(3, "snapshot", fmt.Errorf("spine update must run inside an existing Go module: %w", err))
	}
	previousDirectory, err := os.Getwd()
	if err != nil {
		return finish(3, "snapshot", err)
	}
	if err := os.Chdir(root); err != nil {
		return finish(3, "snapshot", err)
	}
	defer os.Chdir(previousDirectory)
	result["project_root"] = root
	config, err := loadProjectConfig()
	if err != nil {
		return finish(3, "snapshot", err)
	}
	if config.Platform != "" && config.Platform != "echo" {
		return finish(3, "snapshot", fmt.Errorf("unsupported platform: %s", config.Platform))
	}
	if !filepath.IsLocal(config.Entrypoint) {
		return finish(3, "snapshot", fmt.Errorf("entrypoint must stay inside the project: %s", config.Entrypoint))
	}
	settings, err := readActiveGoSettings(root)
	if err != nil {
		return finish(3, "snapshot", err)
	}
	if settings.GOWORK != "" && settings.GOWORK != "off" {
		return finish(3, "snapshot", fmt.Errorf("active workspace %s is outside the single-module update recipe; run with GOWORK=off", settings.GOWORK))
	}
	if strings.TrimSpace(settings.GOFLAGS) != "" {
		return finish(3, "snapshot", fmt.Errorf("GOFLAGS %q is outside the default update build; clear GOFLAGS explicitly before updating", settings.GOFLAGS))
	}
	if _, err := os.Lstat("vendor"); err == nil {
		return finish(3, "snapshot", fmt.Errorf("vendored projects are outside the update recipe; remove vendor and use module mode before updating"))
	} else if !os.IsNotExist(err) {
		return finish(3, "snapshot", err)
	}
	lock, err := acquireProjectLock(generationLockPath)
	if err != nil {
		return finish(4, "snapshot", err)
	}
	defer lock.release()
	work, err := os.MkdirTemp("", "spine-update-*")
	if err != nil {
		return finish(3, "snapshot", err)
	}
	defer os.RemoveAll(work)
	staging := filepath.Join(work, "project")
	inputHash, err := snapshotUpdateProject(ctx, root, staging)
	if err != nil {
		return finish(3, "snapshot", err)
	}
	contents, err := os.ReadFile(filepath.Join(staging, "go.mod"))
	if err != nil {
		return finish(3, "snapshot", err)
	}
	updated, requirements, err := updateModuleRequirements(contents, config.Module)
	if err != nil {
		return finish(3, "snapshot", err)
	}
	result["previous_requirements"] = requirements
	if err := os.WriteFile(filepath.Join(staging, "go.mod"), updated, 0o644); err != nil {
		return finish(3, "snapshot", err)
	}
	stages[0].Status = "passed"
	modules, failure := verifyEchoProject(ctx, staging, config.Entrypoint, stages)
	result["selected_modules"] = modules
	if failure != nil {
		if failure.Stage == "modules" && selectedModuleVersion(modules, echoPlatformPath) == "" {
			failure.Cause = fmt.Errorf("%w; the Echo recipe requires an application import of %s and platform.New() or platform.Option(); update does not migrate application source", failure.Cause, echoPlatformPath)
		}
		return finish(failure.Code, failure.Stage, failure.Cause)
	}
	changes := []fileChange{}
	for _, name := range []string{"go.mod", "go.sum"} {
		contents, err := os.ReadFile(filepath.Join(staging, name))
		if err != nil {
			return finish(3, "dependencies", err)
		}
		changes = append(changes, fileChange{Path: name, Contents: string(contents), AllowOverwrite: true})
	}
	currentHash, err := snapshotUpdateProject(ctx, root, "")
	if err != nil {
		return finish(4, "publish", err)
	}
	if currentHash != inputHash {
		return finish(4, "publish", fmt.Errorf("project inputs changed during update; create a new dry-run"))
	}
	baseHash, err := updatePlanHash(changes, inputHash)
	if err != nil {
		return finish(3, "publish", err)
	}
	result["base_hash"], result["inputs_hash"] = baseHash, inputHash
	if options.Write.IfMatch != "" && options.Write.IfMatch != baseHash {
		return finish(4, "publish", fmt.Errorf("update inputs or recipe changed since dry-run: expected %s, current %s", options.Write.IfMatch, baseHash))
	}
	plan, err := planFileChanges(changes)
	if err != nil {
		return finish(3, "publish", err)
	}
	diff, err := renderFileChanges(changes)
	if err != nil {
		return finish(3, "publish", err)
	}
	result["changes"], result["diff"] = plan, diff
	mutations := []fileChange{}
	for i, item := range plan {
		if item.Operation != "unchanged" {
			mutations = append(mutations, changes[i])
		}
	}
	result["update_available"] = len(mutations) > 0
	if options.Write.DryRun {
		stages[5].Status = "planned"
	} else {
		if ctx.Err() != nil {
			return finish(3, "publish", ctx.Err())
		}
		if err := applyFileChanges(mutations, false); err != nil {
			return finish(4, "publish", err)
		}
		result["updated"] = len(mutations) > 0
		stages[5].Status = "passed"
	}
	if !options.JSON {
		if options.Write.DryRun {
			if len(mutations) == 0 {
				fmt.Fprintf(out, "Spine dependencies already match Echo recipe %s\n", echoRecipe)
			} else {
				fmt.Fprintf(out, "Would update Spine dependencies to Echo recipe %s\n%s", echoRecipe, diff)
			}
			printDryRunHash(out, baseHash)
		} else if len(mutations) == 0 {
			fmt.Fprintf(out, "Spine dependencies already match Echo recipe %s\n", echoRecipe)
		} else {
			fmt.Fprintf(out, "Updated Spine dependencies to Echo recipe %s\n", echoRecipe)
		}
		for _, target := range modules {
			before := selectedModuleVersion(requirements, target.Path)
			if before == "" {
				before = "not required"
			}
			fmt.Fprintf(out, "  %s: %s -> %s\n", target.Path, before, target.Version)
		}
		for _, item := range plan {
			fmt.Fprintf(out, "  %s %s\n", item.Operation, item.Path)
		}
		fmt.Fprintln(out, "Dependencies, static contract and build verified in a temporary copy.")
		if !options.Write.DryRun {
			fmt.Fprintln(out, "Next steps:\n  spine verify\n  spine dev")
		}
	}
	return finish(0, "", nil)
}

func updatePlanHash(changes []fileChange, inputHash string) (string, error) {
	state, err := fileChangesBaseHash(changes)
	if err != nil {
		return "", err
	}
	digest := sha256.New()
	fmt.Fprintf(digest, "spine.update/v1\n%s\n%s\n%s\n", echoRecipe, inputHash, state)
	for _, change := range changes {
		fmt.Fprintf(digest, "%d:%s%d:%s", len(change.Path), change.Path, len(change.Contents), change.Contents)
	}
	return fmt.Sprintf("%x", digest.Sum(nil)), nil
}
