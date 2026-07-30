package cli

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// currentPackageName returns the package already used at the command location.
// An empty directory falls back to its directory name, which keeps generated files
// immediately usable after creating a feature directory.
func currentPackageName(fallback string) (string, error) {
	entries, err := os.ReadDir(".")
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(token.NewFileSet(), entry.Name(), nil, parser.PackageClauseOnly)
		if parseErr != nil {
			return "", fmt.Errorf("could not determine package from %s: %w", entry.Name(), parseErr)
		}
		return file.Name.Name, nil
	}
	if fallback == "" {
		return "", fmt.Errorf("could not determine package at the current directory")
	}
	return fallback, nil
}

type resourceName struct {
	Package string
	Type    string
	Route   string
}

func parseResourceName(value string) (resourceName, error) {
	value = strings.Trim(strings.TrimSpace(value), "/")
	if value == "" {
		return resourceName{}, fmt.Errorf("name is required")
	}
	for _, r := range value {
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_') {
			return resourceName{}, fmt.Errorf("%q may only contain letters, digits, hyphens, and underscores", value)
		}
	}

	words := strings.FieldsFunc(value, func(r rune) bool { return r == '-' || r == '_' })
	if len(words) == 0 {
		return resourceName{}, fmt.Errorf("invalid name %q", value)
	}
	for _, word := range words {
		if word == "" || unicode.IsDigit([]rune(word)[0]) {
			return resourceName{}, fmt.Errorf("invalid name %q", value)
		}
	}
	var title strings.Builder
	for _, word := range words {
		runes := []rune(strings.ToLower(word))
		title.WriteRune(unicode.ToUpper(runes[0]))
		title.WriteString(string(runes[1:]))
	}
	packageName := strings.Join(words, "_")
	return resourceName{Package: strings.ToLower(packageName), Type: title.String(), Route: strings.ToLower(strings.ReplaceAll(value, "_", "-"))}, nil
}
