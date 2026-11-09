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
	Name                       resourceName
	Module, Layout, Platform   string
	DependencyChannel          string
	Frontend, FrontendRoot     string
	Events                     []eventPlatformSpec
	Validator                  string
	WebSocketPlatform          string
	Modules                    []selectedModule
	ValidatorDescriptor        *platformDescriptor
	HTTPDescriptor             *platformDescriptor
	WebSocketDescriptor        *platformDescriptor
	FrontendDescriptor         *frontendProviderDescriptor
	SkipInstall, JSON          bool
	Interactive, NoInteractive bool
	Write                      writeOptions
}

func parseNewOptions(args []string) (newProjectOptions, error) {
	options := newProjectOptions{Layout: layeredLayout, Platform: "echo", DependencyChannel: defaultDependencyChannel, Validator: "none", WebSocketPlatform: "none", Frontend: "none", FrontendRoot: "frontend"}
	frontendSet, frontendRootSet := false, false
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
		case "--module", "--layout", "--channel", "--platform", "--event-platform", "--validator", "--websocket-platform", "--frontend", "--frontend-root", "--if-match":
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
			case "--channel":
				options.DependencyChannel = strings.ToLower(args[index])
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
			case "--websocket-platform":
				options.WebSocketPlatform = args[index]
			case "--frontend":
				options.Frontend = args[index]
				frontendSet = true
			case "--frontend-root":
				options.FrontendRoot = args[index]
				frontendRootSet = true
			case "--if-match":
				options.Write.IfMatch = args[index]
			}
		case "--interactive":
			options.Interactive = true
		case "--no-interactive":
			options.NoInteractive = true
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
	if frontendRootSet && !frontendSet {
		options.Frontend = "react"
	}
	return validateNewProjectOptions(options)
}

func validateNewProjectOptions(options newProjectOptions) (newProjectOptions, error) {
	if err := validateDependencyChannel(options.DependencyChannel); err != nil {
		return options, err
	}
	if options.Layout != layeredLayout && options.Layout != "legacy" {
		return options, fmt.Errorf("unsupported layout: %s", options.Layout)
	}
	if options.Platform != "none" && !validProviderName(options.Platform) {
		return options, fmt.Errorf("unsupported platform: %s", options.Platform)
	}
	if options.WebSocketPlatform != "none" && !validProviderName(options.WebSocketPlatform) {
		return options, fmt.Errorf("unsupported WebSocket platform: %s", options.WebSocketPlatform)
	}
	if options.WebSocketPlatform != "none" && options.Platform == "none" {
		return options, fmt.Errorf("--websocket-platform requires an HTTP platform")
	}
	if options.WebSocketPlatform != "none" && options.WebSocketPlatform == options.Platform {
		return options, fmt.Errorf("HTTP and WebSocket providers must use different platform modules")
	}
	if options.Frontend != "none" && !frontendProviderModuleName.MatchString(options.Frontend) {
		return options, fmt.Errorf("invalid frontend provider: %s", options.Frontend)
	}
	if options.Frontend != "none" && !supportsFrontendServingPlatform(options.Platform) {
		return options, fmt.Errorf("--frontend requires the echo or nethttp HTTP platform")
	}
	if options.FrontendRoot == "" || validateProjectRelativePath(options.FrontendRoot) != nil {
		return options, fmt.Errorf("--frontend-root must be a project-relative path")
	}
	if options.Frontend != "none" && (filepath.Clean(filepath.FromSlash(options.FrontendRoot)) == "." || projectPathContains(options.FrontendRoot, "bin/frontend") || projectPathContains("bin/frontend", options.FrontendRoot)) {
		return options, fmt.Errorf("--frontend-root must not overlap bin/frontend")
	}
	if options.Interactive && options.NoInteractive {
		return options, fmt.Errorf("--interactive and --no-interactive cannot be used together")
	}
	if options.Interactive && options.JSON {
		return options, fmt.Errorf("--interactive cannot be used with --json")
	}
	for _, event := range options.Events {
		if (options.Platform != "none" && event.Provider == options.Platform) || (options.WebSocketPlatform != "none" && event.Provider == options.WebSocketPlatform) {
			return options, fmt.Errorf("platform module %s cannot represent multiple adapter kinds", event.Provider)
		}
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
	config.Recipe, config.Events, config.Validator = currentAssemblyRecipe, options.Events, options.Validator
	config.DependencyChannel = options.DependencyChannel
	config.WebSocketPlatform = options.WebSocketPlatform
	if options.Frontend != "" && options.Frontend != "none" {
		config.FrontendConfigured = true
		config.FrontendProvider = options.Frontend
		config.FrontendRoot = options.FrontendRoot
	} else {
		config.FrontendDisabled = true
	}
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
	if options.Layout == layeredLayout && options.Platform != "none" {
		if controllerRoutesScaffold(options) {
			files["src/controller/health_controller.go"] = registeredHealthController
		} else {
			files["src/controller/health_controller.go"] = layeredHealthController
			files["src/route/route.go"] = strings.ReplaceAll(layeredHealthRoute, "{{MODULE}}", options.Module)
		}
	}
	if len(platformBindings(options)) > 0 {
		setup := descriptorTestSetup(options)
		files["internal/application/application_test.go"] = strings.Replace(projectApplicationTest, "app, options, err := BuildApplication()", setup+"app, options, err := BuildApplication()", 1)
		files["internal/application/platform_config.go"] = assemblyConfig(options)
		files["internal/application/platform_config_test.go"] = assemblyConfigTest(options)
	}
	if options.Frontend != "" && options.Frontend != "none" {
		descriptor := options.FrontendDescriptor
		if descriptor == nil && options.Frontend == "react" {
			descriptor = defaultReactFrontendDescriptor()
		}
		if descriptor == nil {
			return nil, fmt.Errorf("frontend metadata was not resolved: %s", options.Frontend)
		}
		if err := validateFrontendProviderDescriptor(descriptor, options.Frontend, true); err != nil {
			return nil, err
		}
		projectDescriptor, err := frontendProjectDescriptor(descriptor)
		if err != nil {
			return nil, err
		}
		manifest, err := json.MarshalIndent(projectDescriptor, "", "  ")
		if err != nil {
			return nil, err
		}
		root := filepath.ToSlash(filepath.Clean(filepath.FromSlash(options.FrontendRoot)))
		files[filepath.ToSlash(filepath.Join(root, frontendProviderManifest))] = string(manifest) + "\n"
		for _, starter := range descriptor.StarterFiles {
			path := filepath.ToSlash(filepath.Join(root, filepath.FromSlash(starter.Path)))
			if _, exists := files[path]; exists {
				return nil, fmt.Errorf("frontend starter file %q conflicts with another generated file", path)
			}
			files[path] = starter.Contents
		}
		packagePath := filepath.ToSlash(filepath.Join(root, "package.json"))
		if _, exists := files[packagePath]; exists {
			return nil, fmt.Errorf("frontend package manifest %q conflicts with another generated file", packagePath)
		}
		packageContents, err := renderFrontendPackageJSON(options.Name.Route+"-frontend", descriptor.NPMDependencies)
		if err != nil {
			return nil, err
		}
		files[packagePath] = packageContents
		if !strings.Contains(files[".gitignore"], "node_modules/") {
			if files[".gitignore"] != "" && !strings.HasSuffix(files[".gitignore"], "\n") {
				files[".gitignore"] += "\n"
			}
			files[".gitignore"] += "node_modules/\n"
		}
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

func renderFrontendPackageJSON(name string, dependencies map[string]string) (string, error) {
	manifest := struct {
		Name         string            `json:"name"`
		Version      string            `json:"version"`
		Private      bool              `json:"private"`
		Type         string            `json:"type"`
		Dependencies map[string]string `json:"dependencies,omitempty"`
	}{Name: name, Version: "0.1.0", Private: true, Type: "module", Dependencies: dependencies}
	contents, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", err
	}
	return string(contents) + "\n", nil
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
	return runNewWithInput(args, os.Stdin, out, errOut)
}

func runNewWithInput(args []string, in io.Reader, out, errOut io.Writer) int {
	if handled, code := generationHelp("new", args, out, errOut); handled {
		return code
	}
	options, err := parseNewOptions(args)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}
	if shouldRunNewWizard(in, options) {
		options, err = promptNewProjectOptions(args, options, in, out)
		if err != nil {
			fmt.Fprintln(errOut, err)
			return 2
		}
		options, err = validateNewProjectOptions(options)
		if err != nil {
			fmt.Fprintln(errOut, err)
			return 2
		}
	}
	stages := []installationStage{{"scaffold", "pending"}, {"dependencies", "skipped"}, {"modules", "skipped"}, {"contract", "skipped"}, {"build", "skipped"}, {"publish", "pending"}}
	modules := []selectedModule{}
	dir := options.Name.Route
	result := map[string]any{"project": dir, "module": options.Module, "layout": options.Layout, "directory": dir, "platform": options.Platform, "frontend": options.Frontend, "frontend_root": options.FrontendRoot, "recipe": currentAssemblyRecipe, "dependency_channel": options.DependencyChannel, "stability": dependencyStability(options.DependencyChannel), "event_platforms": options.Events, "validator": options.Validator, "websocket_platform": options.WebSocketPlatform, "minimum_go": assemblyGoVersion(options.Validator, options.Events...), "target_modules": currentAssemblyModules(options), "install_skipped": options.SkipInstall, "execution_boundary": "dependency resolution, static inspection and compilation only; no application initialization, tests or listeners"}
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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	queries := currentAssemblyModules(options)
	result["module_queries"] = queries
	options.Modules, err = resolveDependencyModules(ctx, queries, options.DependencyChannel)
	if err != nil {
		return finish(3, "dependencies", err)
	}
	if err := resolveProjectDescriptors(ctx, &options); err != nil {
		return finish(3, "dependencies", err)
	}
	if options.Frontend != "" && options.Frontend != "none" {
		options.FrontendDescriptor, err = resolveFrontendProvider(ctx, options.Frontend, options.DependencyChannel)
		if err != nil {
			return finish(3, "dependencies", err)
		}
		result["frontend_provider"] = options.FrontendDescriptor.Framework
		if options.FrontendDescriptor.Module.Version != "" {
			result["frontend_provider_module"] = options.FrontendDescriptor.Module
		}
	}
	result["minimum_go"] = projectGoVersion(options)
	result["platform_descriptors"] = descriptorFingerprints(options)
	result["target_modules"] = options.Modules
	files, err := newProjectFiles(options)
	if err != nil {
		return finish(10, "scaffold", err)
	}
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
		modules, failure = installAssemblyProject(ctx, staging, files, stages, options.Modules)
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
	fmt.Fprintf(digest, "%s\n%s\n%s\n%s\n%t\n%t\n", state, currentAssemblyRecipe, options.DependencyChannel, options.Platform, options.SkipInstall, options.Write.Force)
	metadata, _ := json.Marshal(descriptorFingerprints(options))
	digest.Write(metadata)
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
			fmt.Fprintf(out, "Created Spine application in %s (%s, %s channel, recipe %s)\n\nNext steps:\n  cd %s\n", dir, options.Platform, options.DependencyChannel, currentAssemblyRecipe, dir)
			if options.FrontendDescriptor != nil {
				fmt.Fprintf(out, "  npm --prefix %s install\n", options.FrontendRoot)
			}
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
