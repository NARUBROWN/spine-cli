package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func runNew(args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(errOut, "project name is required")
		return 1
	}
	name, err := parseResourceName(args[0])
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	module := "github.com/your-org/" + name.Route
	options := writeOptions{}
	jsonOutput := false
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--module":
			if i+1 >= len(args) {
				fmt.Fprintln(errOut, "--module requires a module path")
				return 1
			}
			i++
			module = args[i]
		case "--dry-run":
			options.DryRun = true
		case "--force":
			options.Force = true
		case "--if-match":
			if i+1 >= len(args) {
				fmt.Fprintln(errOut, "--if-match requires a base hash")
				return 1
			}
			i++
			options.IfMatch = args[i]
		case "--json":
			jsonOutput = true
		default:
			fmt.Fprintf(errOut, "unknown option: %s\n", args[i])
			return 1
		}
	}
	dir := filepath.Join(".", name.Route)
	if _, err := os.Stat(dir); err == nil && !options.Force {
		fmt.Fprintf(errOut, "destination already exists: %s\n", dir)
		return 1
	} else if err != nil && !os.IsNotExist(err) {
		fmt.Fprintln(errOut, err)
		return 1
	}

	files := map[string]string{
		"go.mod":  fmt.Sprintf("module %s\n\ngo 1.27.3\n\nrequire github.com/NARUBROWN/spine v0.5.1\n", module),
		"main.go": strings.ReplaceAll(projectMain, "{{MODULE}}", module),
		filepath.Join("internal", "application", "application.go"):      projectApplication,
		filepath.Join("internal", "application", "application_test.go"): projectApplicationTest,
		"README.md":  strings.ReplaceAll(projectReadme, "{{MODULE}}", module),
		"spine.toml": projectConfigTemplate(defaultProjectConfig(module)),
		".gitignore": ".env\nbin/\ncoverage.out\n",
	}
	paths := []string{"go.mod", "main.go", filepath.Join("internal", "application", "application.go"), filepath.Join("internal", "application", "application_test.go"), "README.md", "spine.toml", ".gitignore"}
	changes := make([]fileChange, 0, len(paths))
	for _, path := range paths {
		changes = append(changes, fileChange{Path: filepath.Join(dir, path), Contents: files[path], AllowOverwrite: options.Force})
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
	if jsonOutput {
		_ = json.NewEncoder(out).Encode(map[string]any{"project": name.Route, "module": module, "directory": dir, "files": paths, "dry_run": options.DryRun, "base_hash": baseHash, "changes": plan, "diff": diff})
		return 0
	}
	if options.DryRun {
		fmt.Fprintf(out, "Would create Spine application in %s\n%s", dir, diff)
		return 0
	}
	fmt.Fprintf(out, "Created Spine application in %s\n\nNext steps:\n  cd %s\n  go mod tidy\n  go run .\n", dir, name.Route)
	return 0
}

const projectMain = `package main

import (
	"log"

	"{{MODULE}}/internal/application"
)

func main() {
	app, options, err := application.BuildApplication()
	if err != nil {
		log.Fatal(err)
	}
	if err := app.Validate(options); err != nil {
		log.Fatal(err)
	}
	if err := app.Run(options); err != nil {
		log.Fatal(err)
	}
}
`

const projectApplication = `package application

import (
	"os"

	"github.com/NARUBROWN/spine"
	"github.com/NARUBROWN/spine/pkg/boot"
	"github.com/NARUBROWN/spine/pkg/httpx"
)

type HealthController struct{}

func NewHealthController() *HealthController { return &HealthController{} }

func (c *HealthController) Get() httpx.Response[map[string]string] {
	return httpx.Response[map[string]string]{Body: map[string]string{"status": "ok"}}
}

// BuildApplication은 서버 실행, 계약 검사와 오프라인 검증이 함께 사용하는 composition root입니다.
func BuildApplication() (spine.App, boot.Options, error) {
	app := spine.New()
	app.Constructor(NewHealthController)
	app.Route("GET", "/health", (*HealthController).Get)

	address := ":8080"
	if port := os.Getenv("SPINE_PORT"); port != "" {
		address = ":" + port
	}
	return app, boot.Options{Address: address, HTTP: &boot.HTTPOptions{}}, nil
}
`

const projectApplicationTest = `package application

import "testing"

func TestBuildApplicationIsValid(t *testing.T) {
	app, options, err := BuildApplication()
	if err != nil {
		t.Fatalf("generated application registration is invalid: %v", err)
	}
	if err := app.Validate(options); err != nil {
		t.Fatalf("generated application is invalid: %v", err)
	}
}
`

const projectReadme = `# Spine application

Generated with Spine CLI.

Requires Go 1.27.3 or later.

## Run

    go mod tidy
    spine dev
    curl http://localhost:8080/health

## Generate a resource

    spine g resource users # main.go에 자동 등록

Module: {{MODULE}}
`
