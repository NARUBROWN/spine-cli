package cli

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

type componentDependency struct{ Field, Type string }

// findLocalComponent resolves dependencies only from the current package.
func findLocalComponent(directory, fileName string) (componentDependency, error) {
	fileName = strings.TrimSuffix(strings.TrimSpace(fileName), ".go")
	if fileName == "" || filepath.Base(fileName) != fileName {
		return componentDependency{}, fmt.Errorf("--depends must be a file name in the current directory, without .go")
	}
	if strings.HasSuffix(fileName, "_test") {
		return componentDependency{}, fmt.Errorf("--depends cannot reference a _test.go file")
	}
	path := filepath.Join(directory, fileName+".go")
	if _, err := os.Stat(path); err != nil {
		return componentDependency{}, fmt.Errorf("dependency component %s was not found in the current directory", path)
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		return componentDependency{}, fmt.Errorf("could not parse dependency component %s: %w", path, err)
	}
	packageName, err := packageNameForDirectory(directory)
	if err != nil {
		return componentDependency{}, err
	}
	if file.Name.Name != packageName {
		return componentDependency{}, fmt.Errorf("dependency component %s uses package %s, expected %s", path, file.Name.Name, packageName)
	}
	for _, decl := range file.Decls {
		general, ok := decl.(*ast.GenDecl)
		if !ok || general.Tok.String() != "type" {
			continue
		}
		for _, spec := range general.Specs {
			if typeSpec, ok := spec.(*ast.TypeSpec); ok && strings.HasSuffix(typeSpec.Name.Name, "Controller") || ok && strings.HasSuffix(typeSpec.Name.Name, "Service") || ok && strings.HasSuffix(typeSpec.Name.Name, "Repository") {
				field := strings.ToLower(typeSpec.Name.Name[:1]) + typeSpec.Name.Name[1:]
				return componentDependency{Field: field, Type: typeSpec.Name.Name}, nil
			}
		}
	}
	return componentDependency{}, fmt.Errorf("dependency component %s does not declare a Controller, Service, or Repository type", path)
}
