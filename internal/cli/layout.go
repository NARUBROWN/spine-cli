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

const layeredLayout = "layered-v1"
const layeredRoutePath = "src/route/route.go"
const layeredApplicationPath = "internal/application"

type generationRole struct {
	Name        string `json:"name"`
	Root        string `json:"root"`
	Suffix      string `json:"suffix"`
	Constructor bool   `json:"constructor"`
}

// generationRoles는 생성기와 에이전트 안내가 공유하는 공식 배치 규칙입니다.
func generationRoles() []generationRole {
	return append(componentRoles(),
		generationRole{"route", layeredRoutePath, "", false},
		generationRole{"application", layeredApplicationPath, "", false},
	)
}

func componentRoles() []generationRole {
	return []generationRole{
		{"domain", "database/domain", "", false},
		{"dto", "src/dto", "", false},
		{"repository", "database/repository", "Repository", true},
		{"service", "src/service", "Service", true},
		{"controller", "src/controller", "Controller", true},
	}
}

type generationName struct {
	Name  resourceName
	Group string
}

func parseGenerationName(value string) (generationName, error) {
	if value == "" || strings.TrimSpace(value) != value || strings.Contains(value, "\\") || strings.HasPrefix(value, "/") {
		return generationName{}, fmt.Errorf("invalid generation path %q", value)
	}
	parts := strings.Split(value, "/")
	for index, part := range parts {
		if part == "" || part == "." || part == ".." {
			return generationName{}, fmt.Errorf("invalid generation path %q", value)
		}
		n, err := parseResourceName(part)
		if err != nil {
			return generationName{}, err
		}
		if !token.IsIdentifier(n.Package) || token.Lookup(n.Package).IsKeyword() || n.Package == "_" {
			return generationName{}, fmt.Errorf("invalid Go package name %q", part)
		}
		parts[index] = n.Package
	}
	name, err := parseResourceName(parts[len(parts)-1])
	if err == nil && !ast.IsExported(name.Type) {
		return generationName{}, fmt.Errorf("name must produce an exported Go type: %q", value)
	}
	return generationName{Name: name, Group: filepath.ToSlash(filepath.Join(parts[:len(parts)-1]...))}, err
}

func roleFor(kind string) (generationRole, bool) {
	for _, role := range componentRoles() {
		if role.Name == kind {
			return role, true
		}
	}
	return generationRole{}, false
}

func roleTarget(role generationRole, name generationName) (string, string, error) {
	directory := filepath.Join(filepath.FromSlash(role.Root), filepath.FromSlash(name.Group))
	if err := regularAncestors(directory); err != nil {
		return "", "", err
	}
	packageName := filepath.Base(directory)
	entries, err := os.ReadDir(directory)
	if err != nil && !os.IsNotExist(err) {
		return "", "", err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(directory, entry.Name()), nil, parser.PackageClauseOnly)
		if err != nil {
			return "", "", err
		}
		if file.Name.Name != packageName {
			return "", "", fmt.Errorf("package conflict in %s: expected %s, found %s", directory, packageName, file.Name.Name)
		}
	}
	return directory, packageName, nil
}

func regularAncestors(path string) error {
	if filepath.IsAbs(path) {
		return fmt.Errorf("generation path must be module-relative: %s", path)
	}
	for current := filepath.Clean(path); current != "."; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("generation path contains a symlink: %s", current)
		}
		if _, err := os.Lstat(filepath.Join(current, "go.mod")); err == nil {
			return fmt.Errorf("generation path enters a nested Go module: %s", current)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
