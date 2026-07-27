package cli

import (
	"fmt"
	"strings"
	"unicode"
)

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
