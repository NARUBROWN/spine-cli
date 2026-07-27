package cli

import (
	"bufio"
	"bytes"
	"fmt"
	"go/format"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func runGo(args []string, production bool, out, errOut io.Writer) int {
	cmd := exec.Command("go", args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, out, errOut
	if production {
		cmd.Env = append(os.Environ(), "SPINE_ENV=production")
	}
	if err := cmd.Run(); err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			return exitError.ExitCode()
		}
		fmt.Fprintf(errOut, "could not run go: %v\n", err)
		return 1
	}
	return 0
}

func runProject(development bool, out, errOut io.Writer) int {
	config, err := loadProjectConfig()
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	cmd := exec.Command("go", "run", config.Entrypoint)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, out, errOut
	env := append(os.Environ(), fmt.Sprintf("SPINE_PORT=%d", config.Port))
	if !development {
		env = append(env, "SPINE_ENV=production")
	}
	cmd.Env = env
	if err := cmd.Run(); err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			return exitError.ExitCode()
		}
		fmt.Fprintf(errOut, "could not run application: %v\n", err)
		return 1
	}
	return 0
}

func runCheck(out, errOut io.Writer) int {
	unformatted, err := unformattedFiles(".")
	if err != nil {
		fmt.Fprintf(errOut, "could not check formatting: %v\n", err)
		return 1
	}
	if len(unformatted) > 0 {
		fmt.Fprintf(errOut, "files need gofmt:\n%s\n", strings.Join(unformatted, "\n"))
		return 1
	}
	if code := runGo([]string{"vet", "./..."}, false, out, errOut); code != 0 {
		return code
	}
	return runGo([]string{"test", "./..."}, false, out, errOut)
}

func unformattedFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && (entry.Name() == ".git" || entry.Name() == "bin" || entry.Name() == ".gocache") {
			return filepath.SkipDir
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" {
			return nil
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		formatted, err := format.Source(contents)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if !bytes.Equal(contents, formatted) {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

func runBuild(out, errOut io.Writer) int {
	name, err := projectName()
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	return runGo([]string{"build", "-o", filepath.Join("bin", name), "."}, false, out, errOut)
}

func projectName() (string, error) {
	f, err := os.Open("go.mod")
	if err != nil {
		return "", fmt.Errorf("spine build must run from a Go module: %w", err)
	}
	defer f.Close()
	for scanner := bufio.NewScanner(f); scanner.Scan(); {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 2 && fields[0] == "module" {
			return filepath.Base(fields[1]), nil
		}
	}
	return "", fmt.Errorf("could not find module name in go.mod")
}

func runCompletion(args []string, out, errOut io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(errOut, "usage: spine completion <powershell|bash|zsh>")
		return 1
	}
	commands := "new generate g dev run test check build completion help version"
	switch args[0] {
	case "powershell":
		fmt.Fprintf(out, "Register-ArgumentCompleter -Native -CommandName spine -ScriptBlock { param($wordToComplete) '%s'.Split(' ') | Where-Object { $_ -like \"$wordToComplete*\" } }\n", commands)
	case "bash":
		fmt.Fprintf(out, "complete -W '%s' spine\n", commands)
	case "zsh":
		fmt.Fprintf(out, "#compdef spine\n_arguments '1: :(%s)'\n", commands)
	default:
		fmt.Fprintf(errOut, "unsupported shell: %s\n", args[0])
		return 1
	}
	return 0
}
