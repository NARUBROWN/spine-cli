package cli

import (
	"fmt"
	"go/format"
	"os"
	"strings"
)

func validateRegistration(name resourceName) error {
	if _, err := os.ReadFile("main.go"); err != nil {
		return fmt.Errorf("--register requires main.go in the current directory: %w", err)
	}
	if _, err := modulePath(); err != nil {
		return err
	}
	return nil
}

func modulePath() (string, error) {
	config, err := loadProjectConfig()
	if err == nil {
		return config.Module, nil
	}
	return modulePathFromGoMod()
}

func modulePathFromGoMod() (string, error) {
	contents, err := os.ReadFile("go.mod")
	if err != nil {
		return "", fmt.Errorf("--register requires go.mod in the current directory: %w", err)
	}
	for _, line := range strings.Split(string(contents), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "module" {
			return fields[1], nil
		}
	}
	return "", fmt.Errorf("could not find module name in go.mod")
}

func registerResource(name resourceName) error {
	module, err := modulePath()
	if err != nil {
		return err
	}
	contents, err := os.ReadFile("main.go")
	if err != nil {
		return err
	}
	source := string(contents)
	call := name.Package + ".Register(app)"
	if strings.Contains(source, call) {
		return nil
	}
	anchor := "app := spine.New()"
	if !strings.Contains(source, anchor) {
		return fmt.Errorf("could not find %q in main.go", anchor)
	}
	path := module + "/src/" + name.Package
	importLine := "\t" + name.Package + " \"" + path + "\""
	if !strings.Contains(source, "\""+path+"\"") {
		importAnchor := "import ("
		if !strings.Contains(source, importAnchor) {
			return fmt.Errorf("main.go must use a parenthesized import block for --register")
		}
		source = strings.Replace(source, importAnchor, importAnchor+"\n"+importLine, 1)
	}
	source = strings.Replace(source, anchor, anchor+"\n\t"+call, 1)
	formatted, err := format.Source([]byte(source))
	if err != nil {
		return fmt.Errorf("could not format updated main.go: %w", err)
	}
	return os.WriteFile("main.go", formatted, 0o644)
}
