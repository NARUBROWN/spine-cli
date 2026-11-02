package cli

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

func packageNameForDirectory(directory string) (string, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		if !os.IsNotExist(err) {
			return "", err
		}
		base := filepath.Base(directory)
		if directory == "src" {
			base = "src"
		}
		name, parseErr := parseResourceName(base)
		if parseErr != nil {
			return "", parseErr
		}
		return name.Package, nil
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(token.NewFileSet(), filepath.Join(directory, entry.Name()), nil, parser.PackageClauseOnly)
		if parseErr != nil {
			return "", parseErr
		}
		return file.Name.Name, nil
	}
	base := filepath.Base(directory)
	name, parseErr := parseResourceName(base)
	if parseErr != nil {
		return "", parseErr
	}
	return name.Package, nil
}

func projectRoot() (string, error) {
	directory, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil {
			return directory, nil
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return "", fmt.Errorf("spine generate must run inside a Go module")
		}
		directory = parent
	}
}

func generationDirectory(root string) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(root, cwd)
	if err != nil {
		return "", err
	}
	source := "src"
	if relative == "src" || len(relative) > len("src") && relative[:len("src")+1] == "src"+string(filepath.Separator) {
		source = relative
	}
	return source, nil
}
