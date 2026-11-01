package cli

import (
	"encoding/json"
	"fmt"
	"go/format"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func runGenerate(args []string, out, errOut io.Writer) int {
	root, rootErr := projectRoot()
	workingDirectory := "src"
	if rootErr == nil {
		var workingErr error
		workingDirectory, workingErr = generationDirectory(root)
		if workingErr != nil {
			fmt.Fprintln(errOut, workingErr)
			return 1
		}
		previous, changeErr := os.Getwd()
		if changeErr != nil || os.Chdir(root) != nil {
			fmt.Fprintln(errOut, "could not enter project root")
			return 1
		}
		defer os.Chdir(previous)
	}
	if config, err := loadProjectConfig(); err == nil && config.Layout == layeredLayout {
		return runLayeredGenerate(args, config, out, errOut)
	} else if err != nil {
		if _, statErr := os.Stat("spine.toml"); statErr == nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
	}
	if len(args) < 2 {
		fmt.Fprintln(errOut, "usage: spine generate <resource|slice|consumer|websocket|controller|service|repository> <name> [options]")
		return 1
	}
	name, err := parseResourceName(args[1])
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	kind := args[0]
	if kind != "resource" && kind != "slice" && kind != "consumer" && kind != "websocket" && kind != "controller" && kind != "service" && kind != "repository" {
		fmt.Fprintf(errOut, "unknown generator: %s\n", kind)
		return 1
	}
	options := writeOptions{}
	register, jsonOutput := true, false
	storage := ""
	topic, socketPath := "", ""
	var dependencies []componentDependency
	dependencyFields := map[string]bool{}
	var sliceFields []sliceField
	for index := 2; index < len(args); index++ {
		switch args[index] {
		case "--register":
			register = true
		case "--no-register":
			register = false
		case "--dry-run":
			options.DryRun = true
		case "--force":
			options.Force = true
		case "--if-match":
			if index+1 >= len(args) {
				fmt.Fprintln(errOut, "--if-match requires a base hash")
				return 1
			}
			index++
			options.IfMatch = args[index]
		case "--json":
			jsonOutput = true
		case "--storage":
			if index+1 >= len(args) {
				fmt.Fprintln(errOut, "--storage requires a value")
				return 1
			}
			index++
			storage = args[index]
		case "--field":
			if index+1 >= len(args) {
				fmt.Fprintln(errOut, "--field requires name:type")
				return 1
			}
			index++
			field, parseErr := parseSliceField(args[index])
			if parseErr != nil {
				fmt.Fprintln(errOut, parseErr)
				return 1
			}
			sliceFields = append(sliceFields, field)
		case "--topic":
			if index+1 >= len(args) {
				fmt.Fprintln(errOut, "--topic requires a value")
				return 1
			}
			index++
			topic = args[index]
		case "--path":
			if index+1 >= len(args) {
				fmt.Fprintln(errOut, "--path requires a value")
				return 1
			}
			index++
			socketPath = args[index]
		case "--depends":
			if kind != "controller" && kind != "service" && kind != "repository" {
				fmt.Fprintln(errOut, "--depends is only supported for controller, service, and repository generation")
				return 1
			}
			dependencyCount := 0
			for index+1 < len(args) && !strings.HasPrefix(args[index+1], "--") {
				index++
				dependency, dependencyErr := findLocalComponent(workingDirectory, args[index])
				if dependencyErr != nil {
					fmt.Fprintln(errOut, dependencyErr)
					return 1
				}
				if dependencyFields[dependency.Field] {
					fmt.Fprintf(errOut, "duplicate dependency %s from %s\n", dependency.Type, args[index])
					return 1
				}
				dependencyFields[dependency.Field] = true
				dependencies = append(dependencies, dependency)
				dependencyCount++
			}
			if dependencyCount == 0 {
				fmt.Fprintln(errOut, "--depends requires at least one file name")
				return 1
			}
		default:
			fmt.Fprintf(errOut, "unknown option: %s\n", args[index])
			return 1
		}
	}
	if kind == "slice" {
		if topic != "" || socketPath != "" {
			fmt.Fprintln(errOut, "slice generation does not support --topic or --path")
			return 1
		}
		if storage != "memory" {
			fmt.Fprintln(errOut, "slice generation currently requires explicit --storage memory; no production persistence recipe is implemented yet")
			return 1
		}
		if err := validateSliceFields(sliceFields); err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
	} else if kind == "consumer" {
		if storage != "" || socketPath != "" {
			fmt.Fprintln(errOut, "consumer generation supports --topic and --field, not --storage or --path")
			return 1
		}
		if strings.TrimSpace(topic) == "" {
			fmt.Fprintln(errOut, "consumer generation requires --topic <topic>")
			return 1
		}
		if err := validateMessageFields(sliceFields); err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
	} else if kind == "websocket" {
		if storage != "" || topic != "" {
			fmt.Fprintln(errOut, "websocket generation supports --path and --field, not --storage or --topic")
			return 1
		}
		if !strings.HasPrefix(socketPath, "/") {
			fmt.Fprintln(errOut, "websocket generation requires --path beginning with /")
			return 1
		}
		if err := validateMessageFields(sliceFields); err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
	} else if storage != "" || len(sliceFields) > 0 || topic != "" || socketPath != "" {
		fmt.Fprintln(errOut, "the selected generator does not support --storage, --field, --topic, or --path")
		return 1
	}
	lock, err := acquireProjectLock(generationLockPath)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	defer lock.release()
	files := map[string]string{}
	if kind == "resource" {
		base := filepath.Join("src", name.Package)
		files[filepath.Join(base, name.Package+"_route.go")] = resourceRouteTemplate(name)
		files[filepath.Join(base, name.Package+"_repository.go")] = repositoryTemplate(name)
		files[filepath.Join(base, name.Package+"_service.go")] = serviceTemplate(name)
		files[filepath.Join(base, name.Package+"_controller.go")] = controllerTemplate(name)
	} else if kind == "slice" {
		files = sliceFiles(name, sliceFields)
	} else if kind == "consumer" || kind == "websocket" {
		registrationName := topic
		if kind == "websocket" {
			registrationName = socketPath
		}
		files = messageModuleFiles(kind, name, registrationName, sliceFields)
	} else {
		packageName, packageErr := packageNameForDirectory(workingDirectory)
		if packageErr != nil {
			fmt.Fprintln(errOut, packageErr)
			return 1
		}
		path := filepath.Join(workingDirectory, name.Package+"_"+kind+".go")
		switch kind {
		case "controller":
			files[path] = standaloneControllerTemplate(name, packageName, dependencies)
		case "service":
			files[path] = standaloneServiceTemplate(name, packageName, dependencies)
		case "repository":
			files[path] = standaloneRepositoryTemplate(name, packageName, dependencies)
		}
	}
	for path, contents := range files {
		if filepath.Ext(path) != ".go" {
			continue
		}
		formatted, formatErr := format.Source([]byte(contents))
		if formatErr != nil {
			fmt.Fprintf(errOut, "generated template %s is invalid: %v\n", path, formatErr)
			return 1
		}
		files[path] = string(formatted)
	}
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	changes := make([]fileChange, 0, len(paths)+1)
	for _, path := range paths {
		changes = append(changes, fileChange{Path: path, Contents: files[path], AllowOverwrite: options.Force})
	}
	if register {
		var registrations []fileChange
		if kind == "consumer" || kind == "websocket" {
			module, moduleErr := modulePath()
			if moduleErr != nil {
				fmt.Fprintln(errOut, moduleErr)
				return 1
			}
			alias := name.Package + kind
			var registration fileChange
			registration, err = prepareModuleRegistration(alias, module+"/src/"+kind+"/"+name.Package)
			registrations = []fileChange{registration}
		} else if kind == "resource" {
			module, moduleErr := modulePath()
			if moduleErr != nil {
				fmt.Fprintln(errOut, moduleErr)
				return 1
			}
			var registration fileChange
			registration, err = prepareResourceModuleRegistration(name, module)
			registrations = []fileChange{registration}
		} else if kind == "controller" || kind == "service" || kind == "repository" {
			packageName, packageErr := packageNameForDirectory(workingDirectory)
			if packageErr != nil {
				fmt.Fprintln(errOut, packageErr)
				return 1
			}
			registrations, err = prepareComponentWiring(kind, name, workingDirectory, packageName)
		} else {
			module, moduleErr := modulePath()
			if moduleErr != nil {
				fmt.Fprintln(errOut, moduleErr)
				return 1
			}
			var registration fileChange
			registration, err = prepareModuleRegistration(name.Package, module+"/src/"+name.Package)
			registrations = []fileChange{registration}
		}
		if err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
		changes = append(changes, registrations...)
	}
	diff := ""
	baseHash, err := fileChangesBaseHash(changes)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	if options.IfMatch != "" && options.IfMatch != baseHash {
		fmt.Fprintf(errOut, "generation targets changed since dry-run: expected base hash %s, current %s\n", options.IfMatch, baseHash)
		return 1
	}
	plan, err := planFileChanges(changes)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	if options.DryRun {
		diff, err = renderFileChanges(changes)
		if err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
	}
	if err := applyFileChanges(changes, options.DryRun); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	base := workingDirectory
	if kind == "resource" || kind == "slice" {
		base = filepath.Join("src", name.Package)
	} else if kind == "consumer" || kind == "websocket" {
		base = filepath.Join("src", kind, name.Package)
	}
	if jsonOutput {
		_ = json.NewEncoder(out).Encode(map[string]any{"kind": kind, "name": name.Type, "files": paths, "registered": register, "storage": storage, "dry_run": options.DryRun, "base_hash": baseHash, "changes": plan, "diff": diff})
		return 0
	}
	if options.DryRun {
		fmt.Fprintf(out, "Would generate %s %q in %s\n%s", kind, name.Type, base, diff)
		return 0
	}
	fmt.Fprintf(out, "Generated %s %q in %s\n", kind, name.Type, base)
	if kind == "resource" && register {
		fmt.Fprintf(out, "Registered the resource route module in the application composition root: %s.Register(app)\n", name.Package)
	} else if kind == "resource" {
		fmt.Fprintf(out, "Register the resource route module from the application composition root: %s.Register(app)\n", name.Package)
	} else if kind == "slice" && register {
		fmt.Fprintf(out, "Registered the vertical slice in the application composition root: %s.Register(app)\n", name.Package)
		fmt.Fprintln(out, "The generated memory repository is development-only and loses data when the process stops.")
	} else if kind == "slice" {
		fmt.Fprintf(out, "Register the vertical slice from the application composition root: %s.Register(app)\n", name.Package)
		fmt.Fprintln(out, "The generated memory repository is development-only and loses data when the process stops.")
	} else if (kind == "consumer" || kind == "websocket") && register {
		registrationName := topic
		if kind == "websocket" {
			registrationName = socketPath
		}
		fmt.Fprintf(out, "Registered the %s contract %q in the application composition root.\n", kind, registrationName)
	} else if kind == "consumer" || kind == "websocket" {
		fmt.Fprintf(out, "Register the generated %s module from the application composition root.\n", kind)
	} else if kind == "controller" && register {
		fmt.Fprintf(out, "Registered New%sController and collected GET /%s/:id in src/route/route.go.\n", name.Type, name.Route)
	} else if register {
		fmt.Fprintf(out, "Registered New%s%s in the application composition root.\n", name.Type, strings.Title(kind))
	}
	return 0
}

func resourceRouteTemplate(n resourceName) string {
	return fmt.Sprintf(`package %s

import "github.com/NARUBROWN/spine"

// Register는 이 resource를 애플리케이션에 등록합니다.
func Register(app spine.App) error {
	app.Constructor(
		New%sRepository,
		New%sService,
		New%sController,
	)
	app.Route("GET", "/%s/:id", (*%sController).Get)
	return nil
}
`, n.Package, n.Type, n.Type, n.Type, n.Route, n.Type)
}

func repositoryTemplate(n resourceName) string {
	return fmt.Sprintf(`package %s

type %sRepository struct{}

func New%sRepository() *%sRepository { return &%sRepository{} }
`, n.Package, n.Type, n.Type, n.Type, n.Type)
}

func serviceTemplate(n resourceName) string {
	return fmt.Sprintf(`package %s

type %sService struct { repository *%sRepository }

func New%sService(repository *%sRepository) *%sService {
	return &%sService{repository: repository}
}
`, n.Package, n.Type, n.Type, n.Type, n.Type, n.Type, n.Type)
}

func controllerTemplate(n resourceName) string {
	return fmt.Sprintf(`package %s

import (
	"github.com/NARUBROWN/spine/pkg/httpx"
	"github.com/NARUBROWN/spine/pkg/path"
)

type %sController struct { service *%sService }

func New%sController(service *%sService) *%sController {
	return &%sController{service: service}
}

func (c *%sController) Get(id path.Int) httpx.Response[map[string]any] {
	return httpx.Response[map[string]any]{Body: map[string]any{"id": id.Value}}
}
`, n.Package, n.Type, n.Type, n.Type, n.Type, n.Type, n.Type, n.Type)
}

func standaloneRepositoryTemplate(n resourceName, packageName string, dependencies []componentDependency) string {
	return standaloneComponentTemplate("repository", n, packageName, dependencies)
}

func standaloneServiceTemplate(n resourceName, packageName string, dependencies []componentDependency) string {
	return standaloneComponentTemplate("service", n, packageName, dependencies)
}

func standaloneControllerTemplate(n resourceName, packageName string, dependencies []componentDependency) string {
	return standaloneComponentTemplate("controller", n, packageName, dependencies)
}

func standaloneComponentTemplate(kind string, n resourceName, packageName string, dependencies []componentDependency) string {
	suffix := strings.ToUpper(kind[:1]) + kind[1:]
	fields, parameters, values := "", "", ""
	for index, dependency := range dependencies {
		if index > 0 {
			fields += "\n"
			parameters += ", "
			values += "\n"
		}
		fields += "\t" + dependency.Field + " *" + dependency.Type
		parameters += dependency.Field + " *" + dependency.Type
		values += "\t\t" + dependency.Field + ": " + dependency.Field + ","
	}
	if fields != "" {
		fields = "\n" + fields + "\n"
	}
	if values != "" {
		values = "{\n" + values + "\n\t}"
	} else {
		values = "{}"
	}
	header := fmt.Sprintf("package %s\n\n", packageName)
	methods := ""
	if kind == "controller" {
		header += `import (
	"github.com/NARUBROWN/spine/pkg/httpx"
	"github.com/NARUBROWN/spine/pkg/path"
)

`
		methods = fmt.Sprintf(`
func (c *%[1]sController) Get(id path.Int) httpx.Response[map[string]any] {
	return httpx.Response[map[string]any]{Body: map[string]any{"id": id.Value}}
}
`, n.Type)
	}
	return header + fmt.Sprintf(`type %[1]s%[2]s struct{%[3]s}

func New%[1]s%[2]s(%[4]s) *%[1]s%[2]s {
	return &%[1]s%[2]s%[5]s
}
`, n.Type, suffix, fields, parameters, values) + methods
}
