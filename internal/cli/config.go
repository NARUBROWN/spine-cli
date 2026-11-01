package cli

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// projectConfig is deliberately small. It is the single project-level contract
// used by Spine CLI; application-specific configuration remains in the app.
type projectConfig struct {
	Module      string
	Entrypoint  string
	Composition string
	Port        int
	Layout      string
}

func defaultProjectConfig(module string) projectConfig {
	return projectConfig{Module: module, Entrypoint: ".", Composition: "./internal/application", Port: 8080, Layout: layeredLayout}
}

func projectConfigTemplate(config projectConfig) string {
	result := fmt.Sprintf("module = %q\nentrypoint = %q\ncomposition = %q\nport = %d\n", config.Module, config.Entrypoint, config.Composition, config.Port)
	if config.Layout != "" {
		result += fmt.Sprintf("layout = %q\n", config.Layout)
	}
	return result
}

func loadProjectConfig() (projectConfig, error) {
	contents, err := os.ReadFile("spine.toml")
	if err != nil {
		return projectConfig{}, fmt.Errorf("spine.toml is required: %w", err)
	}
	config := projectConfig{Entrypoint: ".", Composition: ".", Port: 8080}
	for lineNumber, line := range strings.Split(string(contents), "\n") {
		line = strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			return projectConfig{}, fmt.Errorf("spine.toml:%d: expected key = value", lineNumber+1)
		}
		key, value := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		switch key {
		case "module", "entrypoint", "composition", "layout":
			unquoted, err := strconv.Unquote(value)
			if err != nil || unquoted == "" {
				return projectConfig{}, fmt.Errorf("spine.toml:%d: %s must be a non-empty quoted string", lineNumber+1, key)
			}
			if key == "module" {
				config.Module = unquoted
			} else if key == "entrypoint" {
				config.Entrypoint = unquoted
			} else if key == "composition" {
				config.Composition = unquoted
			} else {
				if unquoted != layeredLayout && unquoted != "legacy" {
					return projectConfig{}, fmt.Errorf("spine.toml:%d: unsupported layout %q", lineNumber+1, unquoted)
				}
				config.Layout = unquoted
			}
		case "port":
			port, err := strconv.Atoi(value)
			if err != nil || port < 1 || port > 65535 {
				return projectConfig{}, fmt.Errorf("spine.toml:%d: port must be between 1 and 65535", lineNumber+1)
			}
			config.Port = port
		default:
			return projectConfig{}, fmt.Errorf("spine.toml:%d: unsupported setting %q", lineNumber+1, key)
		}
	}
	if config.Module == "" {
		return projectConfig{}, fmt.Errorf("spine.toml: module is required")
	}
	return config, nil
}
