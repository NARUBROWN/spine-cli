package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/format"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

type installationStage struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

type newProjectOptions struct {
	Name                     resourceName
	Module, Layout, Platform string
	Events                   []eventPlatformSpec
	Validator                string
	SkipInstall, JSON        bool
	Write                    writeOptions
}

func parseNewOptions(args []string) (newProjectOptions, error) {
	options := newProjectOptions{Layout: layeredLayout, Platform: "echo", Validator: "none"}
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return options, fmt.Errorf("project name is required")
	}
	name, err := parseResourceName(args[0])
	if err != nil {
		return options, err
	}
	options.Name, options.Module = name, "github.com/your-org/"+name.Route
	for index := 1; index < len(args); index++ {
		switch args[index] {
		case "--module", "--layout", "--platform", "--event-platform", "--validator", "--if-match":
			key := args[index]
			if index+1 == len(args) {
				return options, fmt.Errorf("%s requires a value", key)
			}
			index++
			switch key {
			case "--module":
				options.Module = args[index]
			case "--layout":
				options.Layout = args[index]
			case "--platform":
				options.Platform = args[index]
			case "--event-platform":
				spec, err := parseEventPlatform(args[index])
				if err != nil {
					return options, err
				}
				options.Events = append(options.Events, spec)
			case "--validator":
				options.Validator = args[index]
			case "--if-match":
				options.Write.IfMatch = args[index]
			}
		case "--skip-install":
			options.SkipInstall = true
		case "--dry-run":
			options.Write.DryRun = true
		case "--force":
			options.Write.Force = true
		case "--json":
			options.JSON = true
		default:
			return options, fmt.Errorf("unknown option: %s", args[index])
		}
	}
	if options.Layout != layeredLayout && options.Layout != "legacy" {
		return options, fmt.Errorf("unsupported layout: %s", options.Layout)
	}
	if options.Platform != "echo" && options.Platform != "none" {
		return options, fmt.Errorf("unsupported platform: %s", options.Platform)
	}
	if err := validateAssemblySelection(options.Platform, options.Events, options.Validator); err != nil {
		return options, err
	}
	sort.Slice(options.Events, func(i, j int) bool { return options.Events[i].Name < options.Events[j].Name })
	if options.Write.Force && !options.SkipInstall {
		return options, fmt.Errorf("--force requires --skip-install; installed projects must use a fresh destination")
	}
	if options.Module == "" || strings.ContainsAny(options.Module, " \t\r\n\"`\\") || strings.HasPrefix(options.Module, "-") {
		return options, fmt.Errorf("invalid module path: %q", options.Module)
	}
	return options, nil
}

func newProjectFiles(options newProjectOptions) (map[string]string, error) {
	config := defaultProjectConfig(options.Module)
	config.Layout, config.Platform = options.Layout, options.Platform
	config.Recipe, config.Events, config.Validator = assemblyRecipe, options.Events, options.Validator
	application, err := assemblyApplication(options)
	if err != nil {
		return nil, err
	}
	files := map[string]string{
		"go.mod":                              assemblyGoMod(options),
		"main.go":                             strings.ReplaceAll(projectMain, "{{MODULE}}", options.Module),
		"internal/application/application.go": application,
		"internal/application/application_test.go": projectApplicationTest,
		"README.md":  assemblyReadme(options),
		"spine.toml": projectConfigTemplate(config),
		".gitignore": ".env\nbin/\ncoverage.out\n",
	}
	if options.Layout == layeredLayout && options.Platform == "echo" {
		files["src/controller/health_controller.go"] = layeredHealthController
		files["src/route/route.go"] = strings.ReplaceAll(layeredHealthRoute, "{{MODULE}}", options.Module)
	}
	if len(options.Events) > 0 {
		setup := ""
		for _, spec := range options.Events {
			prefix := eventEnvPrefix(spec.Name)
			if spec.Provider == "kafka" {
				setup += fmt.Sprintf("t.Setenv(%q, %q)\nt.Setenv(%q, %q)\n", prefix+"BROKERS", "127.0.0.1:9092", prefix+"GROUP_ID", "generated-test")
			} else {
				setup += fmt.Sprintf("t.Setenv(%q, %q)\nt.Setenv(%q, %q)\nt.Setenv(%q, %q)\n", prefix+"URL", "amqp://guest:guest@127.0.0.1:5672/", prefix+"EXCHANGE", "generated-test", prefix+"ALLOW_INSECURE", "true")
			}
		}
		files["internal/application/application_test.go"] = strings.Replace(projectApplicationTest, "app, options, err := BuildApplication()", setup+"app, options, err := BuildApplication()", 1)
		files["internal/application/platform_config.go"] = assemblyConfig(options)
		files["internal/application/platform_config_test.go"] = assemblyConfigTest(options)
	}
	for path, source := range files {
		if strings.HasSuffix(path, ".go") {
			formatted, err := format.Source([]byte(source))
			if err != nil {
				return nil, err
			}
			files[path] = string(formatted)
		}
	}
	return files, nil
}

func inspectStagedProject(root string) (applicationContract, error) {
	previous, err := os.Getwd()
	if err != nil {
		return applicationContract{}, err
	}
	if err := os.Chdir(root); err != nil {
		return applicationContract{}, err
	}
	defer os.Chdir(previous)
	return inspectApplication()
}

func runNew(args []string, out, errOut io.Writer) int {
	if handled, code := generationHelp("new", args, out, errOut); handled {
		return code
	}
	options, err := parseNewOptions(args)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}
	stages := []installationStage{{"scaffold", "pending"}, {"dependencies", "skipped"}, {"modules", "skipped"}, {"contract", "skipped"}, {"build", "skipped"}, {"publish", "pending"}}
	modules := []selectedModule{}
	dir := options.Name.Route
	result := map[string]any{"project": dir, "module": options.Module, "layout": options.Layout, "directory": dir, "platform": options.Platform, "recipe": assemblyRecipe, "stability": "prerelease", "event_platforms": options.Events, "validator": options.Validator, "minimum_go": assemblyGoVersion(options.Validator), "target_modules": assemblyModules(options.Platform, options.Events, options.Validator), "install_skipped": options.SkipInstall, "execution_boundary": "dependency resolution, static inspection and compilation only; no application initialization, tests or listeners"}
	finish := func(code int, stage string, err error) int {
		if err != nil {
			for index := range stages {
				if stages[index].Name == stage {
					stages[index].Status = "failed"
				}
			}
			result["failed_stage"] = stage
			result["cause"] = err.Error()
			fmt.Fprintf(errOut, "%s: %v\n", stage, err)
		}
		result["stages"], result["selected_modules"] = stages, modules
		if options.JSON {
			if err := json.NewEncoder(out).Encode(result); err != nil {
				fmt.Fprintln(errOut, err)
				return 10
			}
		}
		return code
	}
	if info, err := os.Lstat(dir); err == nil {
		if !options.Write.Force || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return finish(4, "publish", fmt.Errorf("destination already exists: %s", dir))
		}
	} else if !os.IsNotExist(err) {
		return finish(3, "publish", err)
	}
	files, err := newProjectFiles(options)
	if err != nil {
		return finish(10, "scaffold", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	staging := ""
	if !options.SkipInstall {
		workspace, err := activeWorkspace(".")
		if err != nil {
			return finish(3, "dependencies", err)
		}
		if workspace != "" && workspace != "off" {
			return finish(3, "dependencies", fmt.Errorf("active workspace %s is outside the verified single-module recipe; use GOWORK=off or --skip-install", workspace))
		}
		work, err := os.MkdirTemp(".", ".spine-new-*")
		if err != nil {
			return finish(3, "scaffold", err)
		}
		defer os.RemoveAll(work)
		staging, err = filepath.Abs(filepath.Join(work, "project"))
		if err != nil {
			return finish(10, "scaffold", err)
		}
		var failure *installationFailure
		modules, failure = installAssemblyProject(ctx, staging, files, stages, assemblyModules(options.Platform, options.Events, options.Validator))
		if failure != nil {
			return finish(failure.Code, failure.Stage, failure.Cause)
		}

	} else {
		stages[0].Status = "passed"
	}
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	changes := make([]fileChange, 0, len(paths))
	for _, path := range paths {
		changes = append(changes, fileChange{Path: filepath.Join(dir, filepath.FromSlash(path)), Contents: files[path], AllowOverwrite: options.Write.Force})
	}
	state, err := fileChangesBaseHash(changes)
	if err != nil {
		return finish(3, "publish", err)
	}
	// 생성 모드와 최종 내용도 해시에 넣어 다른 레시피의 계획을 재사용하지 못하게 합니다.
	digest := sha256.New()
	fmt.Fprintf(digest, "%s\n%s\n%s\n%t\n%t\n", state, assemblyRecipe, options.Platform, options.SkipInstall, options.Write.Force)
	for _, path := range paths {
		fmt.Fprintf(digest, "%d:%s%d:%s", len(path), path, len(files[path]), files[path])
	}
	baseHash := hex.EncodeToString(digest.Sum(nil))
	result["base_hash"], result["files"], result["dry_run"] = baseHash, paths, options.Write.DryRun
	if options.Write.IfMatch != "" && options.Write.IfMatch != baseHash {
		return finish(4, "publish", fmt.Errorf("generation targets or recipe changed since dry-run: expected base hash %s, current %s", options.Write.IfMatch, baseHash))
	}
	plan, err := planFileChanges(changes)
	if err != nil {
		return finish(4, "publish", err)
	}
	result["changes"] = plan
	result["diff"] = ""
	if options.Write.DryRun {
		diff, err := renderFileChanges(changes)
		if err != nil {
			return finish(3, "publish", err)
		}
		result["diff"] = diff
		stages[5].Status = "planned"
	} else if options.SkipInstall {
		if err := applyFileChanges(changes, false); err != nil {
			return finish(4, "publish", err)
		}
		stages[5].Status = "passed"
	} else {
		if ctx.Err() != nil {
			return finish(3, "publish", ctx.Err())
		}
		lockPath := ".spine-new-" + dir + ".lock"
		lock, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return finish(4, "publish", err)
		}
		lock.Close()
		defer os.Remove(lockPath)
		if _, err := os.Lstat(dir); err == nil {
			return finish(4, "publish", fmt.Errorf("destination already exists: %s", dir))
		} else if !os.IsNotExist(err) {
			return finish(3, "publish", err)
		}
		if err := os.Rename(staging, dir); err != nil {
			return finish(3, "publish", err)
		}
		stages[5].Status = "passed"
	}
	if !options.JSON {
		if options.Write.DryRun {
			if options.SkipInstall {
				fmt.Fprintln(out, "Installation and build verification: skipped")
			} else {
				fmt.Fprintln(out, "Dependencies, static contract and build verified in a temporary project")
			}
			fmt.Fprintf(out, "Would create Spine application in %s\n%s", dir, result["diff"])
			printDryRunHash(out, baseHash)
		} else {
			fmt.Fprintf(out, "Created Spine application in %s (%s, prerelease recipe %s)\n\nNext steps:\n  cd %s\n", dir, options.Platform, assemblyRecipe, dir)
			if options.SkipInstall {
				fmt.Fprintln(out, "  go mod tidy\n  spine dev\n\nDependency installation and build verification were skipped.")
			} else {
				fmt.Fprintln(out, "  spine dev\n\nDependencies, static contract and build verified.")
				for _, module := range modules {
					fmt.Fprintf(out, "  %s %s\n", module.Path, module.Version)
				}
			}
		}
	}
	return finish(0, "", nil)
}
