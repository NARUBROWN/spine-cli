package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
)

func runGenerate(args []string, out, errOut io.Writer) int {
	if len(args) < 2 {
		fmt.Fprintln(errOut, "usage: spine generate <resource|controller|service|repository> <name> [--register] [--dry-run] [--json] [--force]")
		return 1
	}
	name, err := parseResourceName(args[1])
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	kind := args[0]
	if kind != "resource" && kind != "controller" && kind != "service" && kind != "repository" {
		fmt.Fprintf(errOut, "unknown generator: %s\n", kind)
		return 1
	}
	options := writeOptions{}
	register, jsonOutput := kind == "resource", false
	for _, arg := range args[2:] {
		switch arg {
		case "--register":
			register = true
		case "--no-register":
			register = false
		case "--dry-run":
			options.DryRun = true
		case "--force":
			options.Force = true
		case "--json":
			jsonOutput = true
		default:
			fmt.Fprintf(errOut, "unknown option: %s\n", arg)
			return 1
		}
	}
	if register && kind != "resource" {
		fmt.Fprintln(errOut, "--register is only supported for resource generation")
		return 1
	}
	if register {
		if err := validateRegistration(name); err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
	}
	files := map[string]string{}
	if kind == "resource" {
		base := filepath.Join("src", name.Package)
		files[filepath.Join(base, "module.go")] = moduleTemplate(name)
		files[filepath.Join(base, name.Package+"_repository.go")] = repositoryTemplate(name)
		files[filepath.Join(base, name.Package+"_service.go")] = serviceTemplate(name)
		files[filepath.Join(base, name.Package+"_controller.go")] = controllerTemplate(name)
	} else {
		base := filepath.Join("src", kind)
		path := filepath.Join(base, name.Package+"_"+kind+".go")
		switch kind {
		case "controller":
			files[path] = standaloneControllerTemplate(name)
		case "service":
			files[path] = standaloneServiceTemplate(name)
		case "repository":
			files[path] = standaloneRepositoryTemplate(name)
		}
	}
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		if err := writeFile(path, files[path], options); err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
	}
	if register && !options.DryRun {
		if err := registerResource(name); err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
	}
	base := filepath.Join("src", kind)
	if kind == "resource" {
		base = filepath.Join("src", name.Package)
	}
	if jsonOutput {
		_ = json.NewEncoder(out).Encode(map[string]any{"kind": kind, "name": name.Type, "files": paths, "registered": register, "dry_run": options.DryRun})
		return 0
	}
	if options.DryRun {
		fmt.Fprintf(out, "Would generate %s %q in %s\n", kind, name.Type, base)
		return 0
	}
	fmt.Fprintf(out, "Generated %s %q in %s\n", kind, name.Type, base)
	if kind == "resource" {
		fmt.Fprintf(out, "Register the resource from main: %s.Register(app)\n", name.Package)
	} else if kind == "controller" {
		fmt.Fprintf(out, "Register the generated controller constructor and route in your application composition root.\n")
	}
	return 0
}

func moduleTemplate(n resourceName) string {
	return fmt.Sprintf(`package %s

import "github.com/NARUBROWN/spine"

// Register adds this resource to the application.
func Register(app spine.App) {
	app.Constructor(
		New%sRepository,
		New%sService,
		New%sController,
	)
	app.Route("GET", "/%s/:id", (*%sController).Get)
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

func standaloneRepositoryTemplate(n resourceName) string {
	return fmt.Sprintf(`package repository

type %sRepository struct{}

func New%sRepository() *%sRepository { return &%sRepository{} }
`, n.Type, n.Type, n.Type, n.Type)
}

func standaloneServiceTemplate(n resourceName) string {
	return fmt.Sprintf(`package service

type %sService struct{}

func New%sService() *%sService { return &%sService{} }
`, n.Type, n.Type, n.Type, n.Type)
}

func standaloneControllerTemplate(n resourceName) string {
	return fmt.Sprintf(`package controller

import (
	"github.com/NARUBROWN/spine/pkg/httpx"
	"github.com/NARUBROWN/spine/pkg/path"
)

type %sController struct{}

func New%sController() *%sController { return &%sController{} }

func (c *%sController) Get(id path.Int) httpx.Response[map[string]any] {
	return httpx.Response[map[string]any]{Body: map[string]any{"id": id.Value}}
}
`, n.Type, n.Type, n.Type, n.Type, n.Type)
}
